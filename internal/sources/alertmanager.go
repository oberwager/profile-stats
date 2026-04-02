package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type AlertmanagerClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewAlertmanagerClient(baseURL string) *AlertmanagerClient {
	return &AlertmanagerClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type alertmanagerAlert struct {
	Labels map[string]string `json:"labels"`
}

// AlertSummary holds grouped alert counts by severity.
type AlertSummary struct {
	Critical int
	Warning  int
	Total    int
}

// ActiveAlerts fetches GET /api/v2/alerts?silenced=false&inhibited=false
// and returns grouped counts by severity label.
func (c *AlertmanagerClient) ActiveAlerts(ctx context.Context) (AlertSummary, error) {
	base, err := safeURL(c.baseURL, "api/v2/alerts")
	if err != nil {
		return AlertSummary{}, fmt.Errorf("alertmanager URL: %w", err)
	}
	u := base + "?silenced=false&inhibited=false"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return AlertSummary{}, fmt.Errorf("alertmanager request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return AlertSummary{}, fmt.Errorf("alertmanager fetch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return AlertSummary{}, fmt.Errorf("alertmanager read body: %w", err)
	}

	var alerts []alertmanagerAlert
	if err := json.Unmarshal(body, &alerts); err != nil {
		return AlertSummary{}, fmt.Errorf("alertmanager parse: %w", err)
	}

	var summary AlertSummary
	for _, a := range alerts {
		severity := a.Labels["severity"]
		switch severity {
		case "critical":
			summary.Critical++
		case "warning":
			summary.Warning++
		}
		summary.Total++
	}

	return summary, nil
}
