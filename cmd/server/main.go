package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/oberwager/profile-stats/internal/api"
)

var Version = "dev"

type Config = api.Config

func loadConfig() Config {
	repos := []string{"d2-live", "cloudflare-ddns", "trivio", "photobooth-api", "ecobux-api", "neu-course-checker", "profile-stats"}
	if r := os.Getenv("MANAGED_REPOS"); r != "" {
		repos = strings.Split(r, ",")
	}
	return Config{
		Port:            getEnv("PORT", "8080"),
		AllowedOrigin:   getEnv("ALLOWED_ORIGIN", "https://lucas.tools"),
		PrometheusURL:   getEnv("PROMETHEUS_URL", "http://prometheus.monitoring.svc:9090"),
		AlertmanagerURL: getEnv("ALERTMANAGER_URL", "http://alertmanager.monitoring.svc:9093"),
		UptimeKumaURL:   getEnv("UPTIME_KUMA_URL", "http://uptime-kuma.monitoring.svc:3001"),
		UptimeKumaKey:   os.Getenv("UPTIME_KUMA_API_KEY"),
		GitHubPAT:       os.Getenv("GITHUB_PAT"),
		GitHubOwner:     getEnv("GITHUB_OWNER", "oberwager"),
		ManagedRepos:    repos,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func validateConfig(cfg Config) error {
	// Validate AllowedOrigin: must be a valid http/https URL with no path.
	ou, err := url.Parse(cfg.AllowedOrigin)
	if err != nil || (ou.Scheme != "http" && ou.Scheme != "https") || ou.Host == "" {
		return fmt.Errorf("ALLOWED_ORIGIN must be a valid http/https URL, got %q", cfg.AllowedOrigin)
	}
	if ou.Path != "" && ou.Path != "/" {
		return fmt.Errorf("ALLOWED_ORIGIN must not contain a path, got %q", cfg.AllowedOrigin)
	}

	// Validate internal service URLs.
	serviceURLs := map[string]string{
		"PROMETHEUS_URL":   cfg.PrometheusURL,
		"ALERTMANAGER_URL": cfg.AlertmanagerURL,
		"UPTIME_KUMA_URL":  cfg.UptimeKumaURL,
	}
	for name, raw := range serviceURLs {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("%s must be a valid http/https URL, got %q", name, raw)
		}
	}
	return nil
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg := loadConfig()
	if err := validateConfig(cfg); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	srv := api.NewServer(cfg, Version)

	httpSrv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      srv.Routes(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		slog.Info("server starting", "port", cfg.Port, "version", Version)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpSrv.Shutdown(ctx)
}
