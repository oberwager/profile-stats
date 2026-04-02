package api

import "time"

type StatsResponse struct {
	CachedAt time.Time        `json:"cached_at"`
	Cluster  ClusterResponse  `json:"cluster"`
	Pipeline PipelineResponse `json:"pipeline"`
	Services ServicesResponse `json:"services"`
}

type ClusterResponse struct {
	CPUUsagePct                float64      `json:"cpu_usage_pct"`
	MemUsedBytes               int64        `json:"mem_used_bytes"`
	MemTotalBytes              int64        `json:"mem_total_bytes"`
	DiskUsedPct                float64      `json:"disk_used_pct"`
	Load1                      float64      `json:"load1"`
	NodeUptimeSeconds          int64        `json:"node_uptime_seconds"`
	NodeUptimeDisplay          string       `json:"node_uptime_display"`
	PodsRunning                int          `json:"pods_running"`
	PodsUnhealthy              int          `json:"pods_unhealthy"`
	NamespaceCount             int          `json:"namespace_count"`
	RestartCount24h            int          `json:"restart_count_24h"`
	CertsHealthy               int          `json:"certs_healthy"`
	MinCertExpiryDays          int          `json:"min_cert_expiry_days"`
	ActiveAlerts               AlertSummary `json:"active_alerts"`
	PrometheusScrapeLagSeconds float64      `json:"prometheus_scrape_lag_seconds"`
	TargetsUp                  int          `json:"targets_up"`
	TargetsDown                int          `json:"targets_down"`
}

type AlertSummary struct {
	Critical int `json:"critical"`
	Warning  int `json:"warning"`
	Total    int `json:"total"`
}

type PipelineResponse struct {
	AvgDeploySeconds     int          `json:"avg_deploy_seconds"`
	AvgDeployDisplay     string       `json:"avg_deploy_display"`
	DeploysLast30Days    int          `json:"deploys_last_30_days"`
	DeploySuccessRatePct float64      `json:"deploy_success_rate_pct"`
	Repos                []RepoSummary `json:"repos"`
	LastDeployAt         time.Time    `json:"last_deploy_at"`
	LastDeployRepo       string       `json:"last_deploy_repo"`
}

type RepoSummary struct {
	Name              string    `json:"name"`
	URL               string    `json:"url"`
	CurrentVersion    string    `json:"current_version"`
	ImageDigest       string    `json:"image_digest"`
	ImageSizeBytes    int64     `json:"image_size_bytes"`
	ImageSizeDisplay  string    `json:"image_size_display"`
	CosignVerified    bool      `json:"cosign_verified"`
	LastRunConclusion string    `json:"last_run_conclusion"`
	LastRunSeconds    int       `json:"last_run_seconds"`
	LastRunAt         time.Time `json:"last_run_at"`
	DeploysLast30Days int       `json:"deploys_last_30_days"`
}

type ServicesResponse struct {
	Uptime30DayPct  float64         `json:"uptime_30day_pct"`
	Uptime24HourPct float64         `json:"uptime_24hour_pct"`
	ServicesUp      int             `json:"services_up"`
	ServicesDown    int             `json:"services_down"`
	Services        []ServiceStatus `json:"services"`
}

type ServiceStatus struct {
	Name          string    `json:"name"`
	MonitorID     int       `json:"monitor_id"`
	Uptime24h     float64   `json:"uptime_24h"`
	Uptime30d     float64   `json:"uptime_30d"`
	Up            bool      `json:"up"`
	LastStatus    int       `json:"last_status"`
	LastCheckedAt time.Time `json:"last_checked_at"`
	AvgPingMs     int       `json:"avg_ping_ms"`
}

type HealthResponse struct {
	Status    string         `json:"status"`
	Version   string         `json:"version"`
	StartedAt time.Time      `json:"started_at"`
	Upstreams UpstreamHealth `json:"upstreams"`
}

type UpstreamHealth struct {
	Prometheus   bool `json:"prometheus"`
	Alertmanager bool `json:"alertmanager"`
	UptimeKuma   bool `json:"uptime_kuma"`
	GitHub       bool `json:"github"`
}
