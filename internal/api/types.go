package api

import "time"

type StatsResponse struct {
	CachedAt     time.Time        `json:"cached_at"`
	Cluster      ClusterResponse  `json:"cluster"`
	Pipeline     PipelineResponse `json:"pipeline"`
	Services     ServicesResponse `json:"services"`
	LastWorkedOn LastWorkedOn     `json:"last_worked_on"`
}

type LastWorkedOn struct {
	Repo      string    `json:"repo"`
	RepoURL   string    `json:"repo_url"`
	PushedAt  time.Time `json:"pushed_at"`
}

type ClusterResponse struct {
	CPUUsagePct       float64      `json:"cpu_usage_pct"`
	MemUsedBytes      int64        `json:"mem_used_bytes"`
	MemTotalBytes     int64        `json:"mem_total_bytes"`
	DiskUsedPct       float64      `json:"disk_used_pct"`
	Load1             float64      `json:"load1"`
	NodeUptimeDisplay string       `json:"node_uptime_display"`
	PodsRunning       int          `json:"pods_running"`
	PodsUnhealthy     int          `json:"pods_unhealthy"`
	NamespaceCount    int          `json:"namespace_count"`
	RestartCount24h   int          `json:"restart_count_24h"`
	CertsHealthy      int          `json:"certs_healthy"`
	MinCertExpiryDays int          `json:"min_cert_expiry_days"`
	ActiveAlerts      AlertSummary `json:"active_alerts"`
	TargetsUp         int          `json:"targets_up"`
	TargetsDown       int          `json:"targets_down"`
}

type AlertSummary struct {
	Critical int `json:"critical"`
	Warning  int `json:"warning"`
	Total    int `json:"total"`
}

type PipelineResponse struct {
	AvgDeployDisplay     string        `json:"avg_deploy_display"`
	DeploysLast30Days    int           `json:"deploys_last_30_days"`
	DeploySuccessRatePct float64       `json:"deploy_success_rate_pct"`
	Repos                []RepoSummary `json:"repos"`
}

type RepoSummary struct {
	Name              string    `json:"name"`
	RepoURL           string    `json:"repo_url"`
	CurrentVersion    string    `json:"current_version"`
	ImageSizeDisplay  string    `json:"image_size_display"`
	CosignVerified    bool      `json:"cosign_verified"`
	LastRunConclusion string    `json:"last_run_conclusion"`
	LastRunSeconds    int       `json:"last_run_seconds"`
	LastRunAt         time.Time `json:"last_run_at"`
}

type ServicesResponse struct {
	Uptime90DayPct  float64         `json:"uptime_90day_pct"`
	Uptime24HourPct float64         `json:"uptime_24hour_pct"`
	ServicesUp      int             `json:"services_up"`
	ServicesDown    int             `json:"services_down"`
	Services        []ServiceStatus `json:"services"`
}

type ServiceStatus struct {
	Name      string  `json:"name"`
	Uptime24h float64 `json:"uptime_24h"`
	Up        bool    `json:"up"`
	AvgPingMs int     `json:"avg_ping_ms"`
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
