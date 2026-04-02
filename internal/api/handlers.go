package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/oberwager/profile-stats/internal/cache"
	"github.com/oberwager/profile-stats/internal/sources"
)

const (
	ttlStats    = 5 * time.Minute
	ttlCluster  = 2 * time.Minute
	ttlPipeline = 5 * time.Minute
	ttlServices = 1 * time.Minute
)

// Config holds all server configuration.
type Config struct {
	Port            string
	AllowedOrigin   string
	PrometheusURL   string
	AlertmanagerURL string
	UptimeKumaURL   string
	UptimeKumaKey   string
	GitHubPAT       string
	GitHubOwner     string
	ManagedRepos    []string
}

// Server holds all clients, cache, config, and state.
type Server struct {
	cache         *cache.Cache
	prometheus    *sources.PrometheusClient
	alertmanager  *sources.AlertmanagerClient
	uptimeKuma    *sources.UptimeKumaClient
	github        *sources.GitHubClient
	managedRepos  []string
	allowedOrigin string
	version       string
	startedAt     time.Time
	upstreamMu    sync.Mutex
	upstreams     UpstreamHealth
}

func NewServer(cfg Config, version string) *Server {
	return &Server{
		cache:         cache.New(),
		prometheus:    sources.NewPrometheusClient(cfg.PrometheusURL),
		alertmanager:  sources.NewAlertmanagerClient(cfg.AlertmanagerURL),
		uptimeKuma:    sources.NewUptimeKumaClient(cfg.UptimeKumaURL, cfg.UptimeKumaKey),
		github:        sources.NewGitHubClient(cfg.GitHubPAT, cfg.GitHubOwner),
		managedRepos:  cfg.ManagedRepos,
		allowedOrigin: cfg.AllowedOrigin,
		version:       version,
		startedAt:     time.Now(),
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /v1/stats", s.handleStats)
	mux.HandleFunc("GET /v1/cluster", s.handleCluster)
	mux.HandleFunc("GET /v1/pipeline", s.handlePipeline)
	mux.HandleFunc("GET /v1/services", s.handleServices)
	return CORSMiddleware(s.allowedOrigin)(RateLimitMiddleware(mux))
}

func writeJSON(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

func formatUptime(seconds int64) string {
	days := seconds / 86400
	hours := (seconds % 86400) / 3600
	if days > 0 {
		return fmt.Sprintf("%d days %d hours", days, hours)
	}
	return fmt.Sprintf("%d hours", hours)
}

func formatBytes(b int64) string {
	const mb = 1024 * 1024
	const kb = 1024
	if b >= mb {
		return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
	}
	if b >= kb {
		return fmt.Sprintf("%d KB", b/kb)
	}
	return fmt.Sprintf("%d B", b)
}

func formatDuration(seconds int) string {
	if seconds >= 60 {
		return fmt.Sprintf("%dm %ds", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%ds", seconds)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.upstreamMu.Lock()
	ups := s.upstreams
	s.upstreamMu.Unlock()

	hr := HealthResponse{
		Status:    "ok",
		Version:   s.version,
		StartedAt: s.startedAt,
		Upstreams: ups,
	}

	data, err := json.Marshal(hr)
	if err != nil {
		slog.Warn("health marshal error", "error", err)
		data = []byte(`{"status":"ok"}`)
	}
	writeJSON(w, data)
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if data, ok := s.cache.Get("stats"); ok {
		writeJSON(w, data)
		return
	}

	ctx := r.Context()

	var (
		clusterData  []byte
		pipelineData []byte
		servicesData []byte
	)

	eg, ctx2 := errgroup.WithContext(ctx)

	eg.Go(func() error {
		cr := s.fetchCluster(ctx2)
		b, err := json.Marshal(cr)
		if err != nil {
			return err
		}
		clusterData = b
		return nil
	})
	eg.Go(func() error {
		pr := s.fetchPipeline(ctx2)
		b, err := json.Marshal(pr)
		if err != nil {
			return err
		}
		pipelineData = b
		return nil
	})
	eg.Go(func() error {
		sr := s.fetchServices(ctx2)
		b, err := json.Marshal(sr)
		if err != nil {
			return err
		}
		servicesData = b
		return nil
	})

	if err := eg.Wait(); err != nil {
		slog.Warn("stats aggregation partial error", "error", err)
	}

	var cluster ClusterResponse
	var pipeline PipelineResponse
	var services ServicesResponse

	if clusterData != nil {
		_ = json.Unmarshal(clusterData, &cluster)
	}
	if pipelineData != nil {
		_ = json.Unmarshal(pipelineData, &pipeline)
	}
	if servicesData != nil {
		_ = json.Unmarshal(servicesData, &services)
	}

	stats := StatsResponse{
		CachedAt: time.Now(),
		Cluster:  cluster,
		Pipeline: pipeline,
		Services: services,
	}

	data, err := json.Marshal(stats)
	if err != nil {
		slog.Warn("stats marshal error", "error", err)
		data = []byte(`{}`)
	}

	s.cache.Set("stats", data, ttlStats)
	writeJSON(w, data)
}

func (s *Server) handleCluster(w http.ResponseWriter, r *http.Request) {
	if data, ok := s.cache.Get("cluster"); ok {
		writeJSON(w, data)
		return
	}

	cr := s.fetchCluster(r.Context())

	data, err := json.Marshal(cr)
	if err != nil {
		slog.Warn("cluster marshal error", "error", err)
		data = []byte(`{}`)
	}

	s.cache.Set("cluster", data, ttlCluster)
	writeJSON(w, data)
}

func (s *Server) fetchCluster(ctx context.Context) ClusterResponse {
	var (
		cpuUsage        float64
		memUsed         float64
		memTotal        float64
		diskUsed        float64
		load1           float64
		nodeUptime      float64
		podsRunning     float64
		podsUnhealthy   float64
		nsCount         float64
		restarts        float64
		certsHealthy    float64
		minCertExpiry   float64
		targetsUp       float64
		targetsDown     float64
		scrapeLag       float64
		alertSummaryRaw sources.AlertSummary
	)

	eg, egCtx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		v, err := s.prometheus.CPUUsage(egCtx)
		if err != nil {
			slog.Warn("prometheus cpu usage", "error", err)
			return nil
		}
		cpuUsage = v
		return nil
	})
	eg.Go(func() error {
		v, err := s.prometheus.MemUsed(egCtx)
		if err != nil {
			slog.Warn("prometheus mem used", "error", err)
			return nil
		}
		memUsed = v
		return nil
	})
	eg.Go(func() error {
		v, err := s.prometheus.MemTotal(egCtx)
		if err != nil {
			slog.Warn("prometheus mem total", "error", err)
			return nil
		}
		memTotal = v
		return nil
	})
	eg.Go(func() error {
		v, err := s.prometheus.DiskUsed(egCtx)
		if err != nil {
			slog.Warn("prometheus disk used", "error", err)
			return nil
		}
		diskUsed = v
		return nil
	})
	eg.Go(func() error {
		v, err := s.prometheus.Load1(egCtx)
		if err != nil {
			slog.Warn("prometheus load1", "error", err)
			return nil
		}
		load1 = v
		return nil
	})
	eg.Go(func() error {
		v, err := s.prometheus.NodeUptimeSeconds(egCtx)
		if err != nil {
			slog.Warn("prometheus node uptime", "error", err)
			return nil
		}
		nodeUptime = v
		return nil
	})
	eg.Go(func() error {
		v, err := s.prometheus.PodsRunning(egCtx)
		if err != nil {
			slog.Warn("prometheus pods running", "error", err)
			return nil
		}
		podsRunning = v
		return nil
	})
	eg.Go(func() error {
		v, err := s.prometheus.PodsUnhealthy(egCtx)
		if err != nil {
			slog.Warn("prometheus pods unhealthy", "error", err)
			return nil
		}
		podsUnhealthy = v
		return nil
	})
	eg.Go(func() error {
		v, err := s.prometheus.NamespaceCount(egCtx)
		if err != nil {
			slog.Warn("prometheus namespace count", "error", err)
			return nil
		}
		nsCount = v
		return nil
	})
	eg.Go(func() error {
		v, err := s.prometheus.RestartCount24h(egCtx)
		if err != nil {
			slog.Warn("prometheus restart count", "error", err)
			return nil
		}
		restarts = v
		return nil
	})
	eg.Go(func() error {
		v, err := s.prometheus.CertsHealthy(egCtx)
		if err != nil {
			slog.Warn("prometheus certs healthy", "error", err)
			return nil
		}
		certsHealthy = v
		return nil
	})
	eg.Go(func() error {
		v, err := s.prometheus.MinCertExpiryDays(egCtx)
		if err != nil {
			slog.Warn("prometheus min cert expiry", "error", err)
			return nil
		}
		minCertExpiry = v
		return nil
	})
	eg.Go(func() error {
		v, err := s.prometheus.TargetsUp(egCtx)
		if err != nil {
			slog.Warn("prometheus targets up", "error", err)
			return nil
		}
		targetsUp = v
		return nil
	})
	eg.Go(func() error {
		v, err := s.prometheus.TargetsDown(egCtx)
		if err != nil {
			slog.Warn("prometheus targets down", "error", err)
			return nil
		}
		targetsDown = v
		return nil
	})
	eg.Go(func() error {
		v, err := s.prometheus.PrometheusScrapeLag(egCtx)
		if err != nil {
			slog.Warn("prometheus scrape lag", "error", err)
			return nil
		}
		scrapeLag = v
		return nil
	})
	eg.Go(func() error {
		summary, err := s.alertmanager.ActiveAlerts(egCtx)
		if err != nil {
			slog.Warn("alertmanager active alerts", "error", err)
			s.setUpstreamHealth("alertmanager", false)
			return nil
		}
		alertSummaryRaw = summary
		s.setUpstreamHealth("alertmanager", true)
		return nil
	})

	_ = eg.Wait()

	uptimeSecs := int64(nodeUptime)
	s.setUpstreamHealth("prometheus", cpuUsage != 0 || memTotal != 0)

	return ClusterResponse{
		CPUUsagePct:                cpuUsage,
		MemUsedBytes:               int64(memUsed),
		MemTotalBytes:              int64(memTotal),
		DiskUsedPct:                diskUsed,
		Load1:                      load1,
		NodeUptimeSeconds:          uptimeSecs,
		NodeUptimeDisplay:          formatUptime(uptimeSecs),
		PodsRunning:                int(podsRunning),
		PodsUnhealthy:              int(podsUnhealthy),
		NamespaceCount:             int(nsCount),
		RestartCount24h:            int(restarts),
		CertsHealthy:               int(certsHealthy),
		MinCertExpiryDays:          int(minCertExpiry),
		ActiveAlerts:               AlertSummary{Critical: alertSummaryRaw.Critical, Warning: alertSummaryRaw.Warning, Total: alertSummaryRaw.Total},
		PrometheusScrapeLagSeconds: scrapeLag,
		TargetsUp:                  int(targetsUp),
		TargetsDown:                int(targetsDown),
	}
}

func (s *Server) handlePipeline(w http.ResponseWriter, r *http.Request) {
	if data, ok := s.cache.Get("pipeline"); ok {
		writeJSON(w, data)
		return
	}

	pr := s.fetchPipeline(r.Context())

	data, err := json.Marshal(pr)
	if err != nil {
		slog.Warn("pipeline marshal error", "error", err)
		data = []byte(`{}`)
	}

	s.cache.Set("pipeline", data, ttlPipeline)
	writeJSON(w, data)
}

func (s *Server) fetchPipeline(ctx context.Context) PipelineResponse {
	type repoRuns struct {
		repo string
		runs []sources.WorkflowRun
	}
	type repoImage struct {
		repo      string
		digest    string
		sizeBytes int64
	}

	runsCh := make([]repoRuns, len(s.managedRepos))
	imagesCh := make([]repoImage, len(s.managedRepos))

	eg1, eg1Ctx := errgroup.WithContext(ctx)
	for i, repo := range s.managedRepos {
		i, repo := i, repo
		eg1.Go(func() error {
			runs, err := s.github.WorkflowRuns(eg1Ctx, repo)
			if err != nil {
				slog.Warn("github workflow runs", "repo", repo, "error", err)
				runsCh[i] = repoRuns{repo: repo, runs: nil}
				return nil
			}
			runsCh[i] = repoRuns{repo: repo, runs: runs}
			return nil
		})
	}
	_ = eg1.Wait()

	eg2, eg2Ctx := errgroup.WithContext(ctx)
	for i, repo := range s.managedRepos {
		i, repo := i, repo
		eg2.Go(func() error {
			digest, sizeBytes, err := s.github.ImageManifest(eg2Ctx, repo)
			if err != nil {
				slog.Warn("github image manifest", "repo", repo, "error", err)
				imagesCh[i] = repoImage{repo: repo}
				return nil
			}
			imagesCh[i] = repoImage{repo: repo, digest: digest, sizeBytes: sizeBytes}
			return nil
		})
	}
	_ = eg2.Wait()

	// Map repo -> digest for version lookup
	digestMap := make(map[string]string, len(s.managedRepos))
	sizeMap := make(map[string]int64, len(s.managedRepos))
	for _, img := range imagesCh {
		digestMap[img.repo] = img.digest
		sizeMap[img.repo] = img.sizeBytes
	}

	// Version lookup: try for each repo
	versionMap := make(map[string]string, len(s.managedRepos))
	eg3, eg3Ctx := errgroup.WithContext(ctx)
	for _, repo := range s.managedRepos {
		repo := repo
		eg3.Go(func() error {
			digest := digestMap[repo]
			if digest == "" {
				return nil
			}
			ver, err := s.github.CurrentVersion(eg3Ctx, repo, digest)
			if err != nil {
				slog.Warn("github current version", "repo", repo, "error", err)
				return nil
			}
			versionMap[repo] = ver
			return nil
		})
	}
	_ = eg3.Wait()

	// Cosign signature existence check (one HEAD request per repo per digest)
	cosignChecked := make([]bool, len(s.managedRepos))
	eg4, eg4Ctx := errgroup.WithContext(ctx)
	for i, repo := range s.managedRepos {
		i, repo := i, repo
		eg4.Go(func() error {
			digest := digestMap[repo]
			if digest == "" {
				return nil
			}
			signed, err := s.github.IsCosignSigned(eg4Ctx, repo, digest)
			if err != nil {
				slog.Warn("github cosign check", "repo", repo, "error", err)
				return nil
			}
			cosignChecked[i] = signed
			return nil
		})
	}
	_ = eg4.Wait()

	cosignMap := make(map[string]bool, len(s.managedRepos))
	for i, repo := range s.managedRepos {
		cosignMap[repo] = cosignChecked[i]
	}

	now := time.Now()
	cutoff := now.AddDate(0, 0, -30)

	var (
		totalDeploySeconds int
		totalSuccessful    int
		totalCompleted     int
		totalDeploys30d    int
		lastDeployAt       time.Time
		lastDeployRepo     string
		repos              []RepoSummary
	)

	// Build per-repo maps
	runsMap := make(map[string][]sources.WorkflowRun, len(s.managedRepos))
	for _, rr := range runsCh {
		runsMap[rr.repo] = rr.runs
	}

	for _, repo := range s.managedRepos {
		runs := runsMap[repo]

		var lastRunConclusion string
		var lastRunSeconds int
		var lastRunAt time.Time
		var deploys30d int

		for i, run := range runs {
			if i == 0 {
				lastRunConclusion = run.Conclusion
				lastRunAt = run.CreatedAt
				if run.Status == "completed" {
					lastRunSeconds = int(run.UpdatedAt.Sub(run.CreatedAt).Seconds())
				}
			}

			if run.CreatedAt.After(lastDeployAt) {
				lastDeployAt = run.CreatedAt
				lastDeployRepo = repo
			}

			if run.Status == "completed" {
				totalCompleted++
				if run.Conclusion == "success" {
					totalSuccessful++
					dur := int(run.UpdatedAt.Sub(run.CreatedAt).Seconds())
					if dur > 0 {
						totalDeploySeconds += dur
					}
				}
			}

			if run.CreatedAt.After(cutoff) && run.Conclusion == "success" {
				deploys30d++
				totalDeploys30d++
			}
		}

		sizeBytes := sizeMap[repo]
		repos = append(repos, RepoSummary{
			Name:              repo,
			URL:               fmt.Sprintf("https://github.com/oberwager/%s", repo),
			CurrentVersion:    versionMap[repo],
			ImageDigest:       digestMap[repo],
			ImageSizeBytes:    sizeBytes,
			ImageSizeDisplay:  formatBytes(sizeBytes),
			CosignVerified:    cosignMap[repo],
			LastRunConclusion: lastRunConclusion,
			LastRunSeconds:    lastRunSeconds,
			LastRunAt:         lastRunAt,
			DeploysLast30Days: deploys30d,
		})
	}

	var avgDeploySeconds int
	if totalSuccessful > 0 {
		avgDeploySeconds = totalDeploySeconds / totalSuccessful
	}

	var successRatePct float64
	if totalCompleted > 0 {
		successRatePct = float64(totalSuccessful) / float64(totalCompleted) * 100
	}

	s.setUpstreamHealth("github", len(allWorkflowRuns(runsMap)) > 0)

	return PipelineResponse{
		AvgDeploySeconds:     avgDeploySeconds,
		AvgDeployDisplay:     formatDuration(avgDeploySeconds),
		DeploysLast30Days:    totalDeploys30d,
		DeploySuccessRatePct: successRatePct,
		Repos:                repos,
		LastDeployAt:         lastDeployAt,
		LastDeployRepo:       lastDeployRepo,
	}
}

// allWorkflowRuns returns all WorkflowRun values across all repos (helper for health check).
func allWorkflowRuns(m map[string][]sources.WorkflowRun) []sources.WorkflowRun {
	var all []sources.WorkflowRun
	for _, v := range m {
		all = append(all, v...)
	}
	return all
}

func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	if data, ok := s.cache.Get("services"); ok {
		writeJSON(w, data)
		return
	}

	sr := s.fetchServices(r.Context())

	data, err := json.Marshal(sr)
	if err != nil {
		slog.Warn("services marshal error", "error", err)
		data = []byte(`{}`)
	}

	s.cache.Set("services", data, ttlServices)
	writeJSON(w, data)
}

func (s *Server) fetchServices(ctx context.Context) ServicesResponse {
	var (
		monitors  []sources.MonitorInfo
		heartbeat sources.UptimeKumaHeartbeat
	)

	eg, egCtx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		m, err := s.uptimeKuma.StatusPage(egCtx, "default")
		if err != nil {
			slog.Warn("uptime kuma status page", "error", err)
			s.setUpstreamHealth("uptime_kuma", false)
			return nil
		}
		monitors = m
		s.setUpstreamHealth("uptime_kuma", true)
		return nil
	})
	eg.Go(func() error {
		hb, err := s.uptimeKuma.Heartbeat(egCtx, "default")
		if err != nil {
			slog.Warn("uptime kuma heartbeat", "error", err)
			return nil
		}
		heartbeat = hb
		return nil
	})
	_ = eg.Wait()

	var (
		serviceStatuses []ServiceStatus
		totalUptime30d  float64
		totalUptime24h  float64
		servicesUp      int
		servicesDown    int
	)

	slog.Info("uptime kuma uptime keys", "keys", heartbeat.UptimeList)
	for _, mon := range monitors {
		idStr := strconv.Itoa(mon.ID)
		uptime24h := heartbeat.UptimeList[idStr+"_24"]
		uptime30d := heartbeat.UptimeList[idStr+"_720"]

		up := uptime24h > 0.95

		var lastStatus int
		var lastCheckedAt time.Time
		var avgPingMs int

		entries := heartbeat.HeartbeatList[idStr]
		if len(entries) > 0 {
			last := entries[len(entries)-1]
			lastStatus = last.Status
			if t, err := time.Parse("2006-01-02 15:04:05", last.Time); err == nil {
				lastCheckedAt = t
			}

			// Average ping of last 10 entries
			start := len(entries) - 10
			if start < 0 {
				start = 0
			}
			var pingSum, pingCount int
			for _, e := range entries[start:] {
				if e.Ping > 0 {
					pingSum += e.Ping
					pingCount++
				}
			}
			if pingCount > 0 {
				avgPingMs = pingSum / pingCount
			}
		}

		ss := ServiceStatus{
			Name:          mon.Name,
			MonitorID:     mon.ID,
			Uptime24h:     uptime24h,
			Uptime30d:     uptime30d,
			Up:            up,
			LastStatus:    lastStatus,
			LastCheckedAt: lastCheckedAt,
			AvgPingMs:     avgPingMs,
		}
		serviceStatuses = append(serviceStatuses, ss)
		totalUptime30d += uptime30d
		totalUptime24h += uptime24h
		if up {
			servicesUp++
		} else {
			servicesDown++
		}
	}

	var uptime30DayPct, uptime24HourPct float64
	if len(serviceStatuses) > 0 {
		uptime30DayPct = (totalUptime30d / float64(len(serviceStatuses))) * 100
		uptime24HourPct = (totalUptime24h / float64(len(serviceStatuses))) * 100
	}

	if serviceStatuses == nil {
		serviceStatuses = []ServiceStatus{}
	}

	return ServicesResponse{
		Uptime30DayPct:  uptime30DayPct,
		Uptime24HourPct: uptime24HourPct,
		ServicesUp:      servicesUp,
		ServicesDown:    servicesDown,
		Services:        serviceStatuses,
	}
}

func (s *Server) setUpstreamHealth(name string, healthy bool) {
	s.upstreamMu.Lock()
	defer s.upstreamMu.Unlock()
	switch name {
	case "prometheus":
		s.upstreams.Prometheus = healthy
	case "alertmanager":
		s.upstreams.Alertmanager = healthy
	case "uptime_kuma":
		s.upstreams.UptimeKuma = healthy
	case "github":
		s.upstreams.GitHub = healthy
	}
}
