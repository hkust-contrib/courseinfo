package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type config struct {
	Port            string
	MetricsPort     string
	BaseURL         string
	RefreshInterval time.Duration
}

func loadConfig() config {
	cfg := config{
		Port:            ":8080",
		MetricsPort:     ":2112",
		BaseURL:         "https://w5.ab.ust.hk/wcq/cgi-bin",
		RefreshInterval: 7 * 24 * time.Hour,
	}
	if v := os.Getenv("PORT"); v != "" {
		cfg.Port = ":" + v
	}
	if v := os.Getenv("METRICS_PORT"); v != "" {
		cfg.MetricsPort = ":" + v
	}
	if v := os.Getenv("BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv("REFRESH_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.RefreshInterval = d
		}
	}
	return cfg
}

type app struct {
	config          config
	endpoint        string
	cache           map[string]*Course
	departmentCache []string
	mu              sync.RWMutex
	server          *echo.Echo
	metricsServer   *http.Server
	logger          *slog.Logger
	manifest        *buildInfo
}

func NewApp(logger *slog.Logger) *app {
	cfg := loadConfig()
	manifest := Manifest()
	fmt.Print(manifest.String())
	logger.Info("Initializing application...")
	e := echo.New()
	e.HideBanner = true
	e.Pre(middleware.RemoveTrailingSlash())
	e.Use(middleware.Logger())
	currentSemester, err := getCurrentSemesterCode()
	if err != nil {
		logger.Error("error while getting current semester code", slog.String("error", err.Error()))
		os.Exit(1)
	}

	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())

	return &app{
		config:          cfg,
		endpoint:        fmt.Sprintf("%s/%s", cfg.BaseURL, currentSemester),
		server:          e,
		cache:           make(map[string]*Course),
		departmentCache: []string{},
		metricsServer: &http.Server{
			Addr:    cfg.MetricsPort,
			Handler: metricsMux,
		},
		logger:   logger,
		manifest: manifest,
	}
}

func (a *app) getEndpoint() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.endpoint
}

func (a *app) setEndpoint(endpoint string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.endpoint = endpoint
}

func (a *app) remember(r *CourseParsingResult) {
	a.mu.Lock()
	a.cache[r.Code] = r.Course
	a.mu.Unlock()
	a.logger.Info("In-memory cache updated for", "courseCode", r.Code)
}

func (a *app) Start() error {
	go func() {
		if err := a.metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			a.logger.Error("Metrics server error", slog.String("error", err.Error()))
		}
	}()
	err := a.server.Start(a.config.Port)
	if err != nil {
		return err
	}
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	var precache bool
	flag.BoolVar(&precache, "precache", false, "Pre-cache current semester courses")
	flag.Parse()
	a := NewApp(logger)
	a.routes()
	if precache {
		a.PreCacheCurrentSemesterCourses()
	}
	go func() {
		ticker := time.NewTicker(a.config.RefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				logger.Info("Refreshing course cache (weekly)")
				a.mu.Lock()
				a.cache = make(map[string]*Course)
				a.departmentCache = []string{}
				a.mu.Unlock()
				a.PreCacheCurrentSemesterCourses()
			}
		}
	}()

	go func() {
		if err := a.Start(); err != http.ErrServerClosed {
			logger.Error("An unexpected error has occurred", slog.String("error", err.Error()))
		}
	}()

	<-ctx.Done()
	logger.Info("Shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("Metrics server shutdown error", slog.String("error", err.Error()))
	}
	if err := a.server.Shutdown(shutdownCtx); err != nil {
		logger.Error("Server shutdown error", slog.String("error", err.Error()))
	}
}
