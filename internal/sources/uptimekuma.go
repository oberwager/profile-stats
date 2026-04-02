package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type UptimeKumaClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewUptimeKumaClient(baseURL, apiKey string) *UptimeKumaClient {
	return &UptimeKumaClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type MonitorInfo struct {
	ID   int
	Name string
}

type UptimeKumaHeartbeat struct {
	UptimeList    map[string]float64
	HeartbeatList map[string][]HeartbeatEntry
}

type HeartbeatEntry struct {
	Status int
	Time   string
	Ping   int
}

type statusPageResponse struct {
	PublicGroupList []struct {
		Name        string `json:"name"`
		MonitorList []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"monitorList"`
	} `json:"publicGroupList"`
}

type heartbeatResponse struct {
	UptimeList    map[string]float64       `json:"uptimeList"`
	HeartbeatList map[string][]heartbeatRaw `json:"heartbeatList"`
}

type heartbeatRaw struct {
	Status int    `json:"status"`
	Time   string `json:"time"`
	Ping   int    `json:"ping"`
}

func (c *UptimeKumaClient) newRequest(ctx context.Context, method, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, err
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// StatusPage fetches /api/status-page/{slug} and returns the monitor list
// for the group named "External Routes".
func (c *UptimeKumaClient) StatusPage(ctx context.Context, slug string) ([]MonitorInfo, error) {
	if err := validateSlug(slug); err != nil {
		return nil, fmt.Errorf("uptime kuma status page: %w", err)
	}
	u, err := safeURL(c.baseURL, "api/status-page", slug)
	if err != nil {
		return nil, fmt.Errorf("uptime kuma status page URL: %w", err)
	}
	req, err := c.newRequest(ctx, http.MethodGet, u)
	if err != nil {
		return nil, fmt.Errorf("uptime kuma status page request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("uptime kuma status page fetch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("uptime kuma status page read: %w", err)
	}

	var page statusPageResponse
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("uptime kuma status page parse: %w", err)
	}

	var monitors []MonitorInfo
	for _, group := range page.PublicGroupList {
		if group.Name == "External Routes" {
			for _, m := range group.MonitorList {
				monitors = append(monitors, MonitorInfo{ID: m.ID, Name: m.Name})
			}
			break
		}
	}

	return monitors, nil
}

// Heartbeat fetches /api/status-page/heartbeat/{slug}.
func (c *UptimeKumaClient) Heartbeat(ctx context.Context, slug string) (UptimeKumaHeartbeat, error) {
	if err := validateSlug(slug); err != nil {
		return UptimeKumaHeartbeat{}, fmt.Errorf("uptime kuma heartbeat: %w", err)
	}
	u, err := safeURL(c.baseURL, "api/status-page/heartbeat", slug)
	if err != nil {
		return UptimeKumaHeartbeat{}, fmt.Errorf("uptime kuma heartbeat URL: %w", err)
	}
	req, err := c.newRequest(ctx, http.MethodGet, u)
	if err != nil {
		return UptimeKumaHeartbeat{}, fmt.Errorf("uptime kuma heartbeat request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return UptimeKumaHeartbeat{}, fmt.Errorf("uptime kuma heartbeat fetch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return UptimeKumaHeartbeat{}, fmt.Errorf("uptime kuma heartbeat read: %w", err)
	}

	var raw heartbeatResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return UptimeKumaHeartbeat{}, fmt.Errorf("uptime kuma heartbeat parse: %w", err)
	}

	hb := UptimeKumaHeartbeat{
		UptimeList:    raw.UptimeList,
		HeartbeatList: make(map[string][]HeartbeatEntry),
	}
	if hb.UptimeList == nil {
		hb.UptimeList = make(map[string]float64)
	}

	for k, entries := range raw.HeartbeatList {
		out := make([]HeartbeatEntry, len(entries))
		for i, e := range entries {
			out[i] = HeartbeatEntry{Status: e.Status, Time: e.Time, Ping: e.Ping}
		}
		hb.HeartbeatList[k] = out
	}

	return hb, nil
}
