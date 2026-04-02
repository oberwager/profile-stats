package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type PrometheusClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewPrometheusClient(baseURL string) *PrometheusClient {
	return &PrometheusClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// prometheusRawData uses json.RawMessage so we can decode result as either
// a vector ([]vectorResult) or a scalar ([timestamp, "value"]) depending on resultType.
type prometheusRawData struct {
	ResultType string          `json:"resultType"`
	Result     json.RawMessage `json:"result"`
}

type prometheusResponse struct {
	Status string            `json:"status"`
	Data   prometheusRawData `json:"data"`
}

type prometheusVectorResult struct {
	Metric map[string]string `json:"metric"`
	Value  []interface{}     `json:"value"`
}

// Query executes an instant PromQL query and returns the first scalar result as float64.
// Returns 0, nil if the result set is empty.
// Returns 0, err only on HTTP or parse failure.
func (c *PrometheusClient) Query(ctx context.Context, promql string) (float64, error) {
	base, err := safeURL(c.baseURL, "/api/v1/query")
	if err != nil {
		return 0, fmt.Errorf("prometheus query URL: %w", err)
	}
	u := base + "?" + url.Values{"query": {promql}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, fmt.Errorf("prometheus query request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("prometheus query: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return 0, fmt.Errorf("prometheus read body: %w", err)
	}

	var pr prometheusResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return 0, fmt.Errorf("prometheus parse response: %w", err)
	}

	if pr.Status != "success" {
		return 0, fmt.Errorf("prometheus non-success status: %s", pr.Status)
	}

	switch pr.Data.ResultType {
	case "scalar":
		// scalar result: [timestamp, "value"]
		var scalarResult []interface{}
		if err := json.Unmarshal(pr.Data.Result, &scalarResult); err != nil {
			return 0, fmt.Errorf("prometheus scalar parse: %w", err)
		}
		if len(scalarResult) < 2 {
			return 0, nil
		}
		valStr, ok := scalarResult[1].(string)
		if !ok {
			return 0, fmt.Errorf("prometheus scalar value not a string")
		}
		return strconv.ParseFloat(valStr, 64)

	default:
		// vector result: [{metric:{}, value:[timestamp,"value"]}, ...]
		var vectorResults []prometheusVectorResult
		if err := json.Unmarshal(pr.Data.Result, &vectorResults); err != nil {
			return 0, fmt.Errorf("prometheus vector parse: %w", err)
		}
		if len(vectorResults) == 0 {
			return 0, nil
		}
		vals := vectorResults[0].Value
		if len(vals) < 2 {
			return 0, nil
		}
		valStr, ok := vals[1].(string)
		if !ok {
			return 0, fmt.Errorf("prometheus vector value not a string")
		}
		return strconv.ParseFloat(valStr, 64)
	}
}

func (c *PrometheusClient) CPUUsage(ctx context.Context) (float64, error) {
	return c.Query(ctx, `100 - (avg(rate(node_cpu_seconds_total{mode="idle"}[1m])) * 100)`)
}

func (c *PrometheusClient) MemUsed(ctx context.Context) (float64, error) {
	return c.Query(ctx, `node_memory_MemTotal_bytes - node_memory_MemAvailable_bytes`)
}

func (c *PrometheusClient) MemTotal(ctx context.Context) (float64, error) {
	return c.Query(ctx, `node_memory_MemTotal_bytes`)
}

func (c *PrometheusClient) DiskUsed(ctx context.Context) (float64, error) {
	return c.Query(ctx, `(1 - node_filesystem_avail_bytes{mountpoint="/var"} / node_filesystem_size_bytes{mountpoint="/var"}) * 100`)
}

func (c *PrometheusClient) Load1(ctx context.Context) (float64, error) {
	return c.Query(ctx, `node_load1`)
}

func (c *PrometheusClient) NodeUptimeSeconds(ctx context.Context) (float64, error) {
	return c.Query(ctx, `time() - node_boot_time_seconds`)
}

func (c *PrometheusClient) PodsRunning(ctx context.Context) (float64, error) {
	return c.Query(ctx, `count(kube_pod_status_phase{phase="Running"})`)
}

func (c *PrometheusClient) PodsUnhealthy(ctx context.Context) (float64, error) {
	return c.Query(ctx, `count(kube_pod_status_phase{phase=~"Pending|Failed|Unknown"} == 1) or vector(0)`)
}

func (c *PrometheusClient) NamespaceCount(ctx context.Context) (float64, error) {
	return c.Query(ctx, `count(kube_namespace_status_phase{phase="Active"}) or vector(0)`)
}

func (c *PrometheusClient) RestartCount24h(ctx context.Context) (float64, error) {
	return c.Query(ctx, `sum(increase(kube_pod_container_status_restarts_total[24h]))`)
}

func (c *PrometheusClient) CertsHealthy(ctx context.Context) (float64, error) {
	return c.Query(ctx, `count(certmanager_certificate_ready_status{condition="True"} == 1) or vector(0)`)
}

func (c *PrometheusClient) MinCertExpiryDays(ctx context.Context) (float64, error) {
	return c.Query(ctx, `min((certmanager_certificate_expiration_timestamp_seconds - time()) / 86400)`)
}

func (c *PrometheusClient) TargetsUp(ctx context.Context) (float64, error) {
	return c.Query(ctx, `count(up == 1)`)
}

func (c *PrometheusClient) TargetsDown(ctx context.Context) (float64, error) {
	return c.Query(ctx, `count(up == 0) or vector(0)`)
}
