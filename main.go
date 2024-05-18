package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/gocolly/colly/v2"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

var (
	baseURL = "https://w5.ab.ust.hk/wcq/cgi-bin"
)

type Course struct {
	Code        string              `json:"code"`
	Title       string              `json:"title"`
	Credits     float64             `json:"credits"`
	Instructors map[string][]string `json:"instructors"`
	Sections    []string            `json:"sections"`
}

type CourseParsingResult struct {
	Code   string
	Course *Course
}

type buildInfo struct {
	Name        string
	Runtime     string
	Hostname    string
	Platform    string
	Version     string
	BuildCommit string
	BuildDate   string
	StartTime   time.Time
}

type app struct {
	endpoint        string
	cache           map[string]*Course
	departmentCache []string
	server          *echo.Echo
	logger          *slog.Logger
	manifest        *buildInfo
}

func (b *buildInfo) Uptime() string {
	return fmt.Sprintf("%.2f", time.Since(b.StartTime).Seconds())
}

func (b *buildInfo) String() string {
	return fmt.Sprintf("%20s: %s\n%20s: %s\n%20s: %s\n%20s: %s\n%20s: %s\n%20s: %s\n", "Application", b.Name, "Runtime", b.Runtime, "Platform", b.Platform, "Version", b.Version, "Commit", b.BuildCommit, "Build Date", b.BuildDate)
}

func (b *buildInfo) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Runtime     string `json:"runtime"`
		Hostname    string `json:"hostname"`
		Platform    string `json:"platform"`
		BuildCommit string `json:"buildCommit"`
		BuildDate   string `json:"buildDate"`
		Uptime      string `json:"uptime"`
	}{
		Runtime:     b.Runtime,
		Hostname:    b.Hostname,
		Platform:    b.Platform,
		BuildCommit: b.BuildCommit,
		BuildDate:   b.BuildDate,
		Uptime:      b.Uptime(),
	})
}

func Manifest() *buildInfo {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "localhost"
	}
	return &buildInfo{
		Name:     "Course Catalogue",
		Platform: fmt.Sprintf("%v %v", runtime.GOOS, runtime.GOARCH),
		Runtime:  runtime.Version(),
		Hostname: hostname,
		Version:  "0.0.1",
		BuildCommit: func() string {
			if info, ok := debug.ReadBuildInfo(); ok {
				for _, setting := range info.Settings {
					if setting.Key == "vcs.revision" {
						return setting.Value
					}
				}
			}

			return "n/a"
		}(),
		BuildDate: func() string {
			if info, ok := debug.ReadBuildInfo(); ok {
				for _, setting := range info.Settings {
					if setting.Key == "vcs.time" {
						return setting.Value
					}
				}
			}

			return "n/a"
		}(),
		StartTime: time.Now(),
	}
}

func NewApp(logger *slog.Logger) *app {
	manifest := Manifest()
	fmt.Print(manifest.String())
	logger.Info("Initializing application...")
	e := echo.New()
	e.HideBanner = true
	e.Pre(middleware.RemoveTrailingSlash())
	currentSemester, err := getCurrentSemesterCode()
	if err != nil {
		logger.Error("error while getting current semester code", err)
		os.Exit(1)
	}
	return &app{
		endpoint:        fmt.Sprintf("%s/%s", baseURL, currentSemester),
		server:          e,
		cache:           make(map[string]*Course),
		departmentCache: []string{},
		logger:          logger,
		manifest:        manifest,
	}
}

func (a *app) remember(r *CourseParsingResult) {
	a.cache[r.Code] = r.Course
	a.logger.Info("In-memory cache updated for", "courseCode", r.Code)
}

func handleInterrupt(logger *slog.Logger, a *app) {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)
	<-quit
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.server.Shutdown(ctx); err != nil {
		logger.Error("Server Shutdown: ", err)
	}
	logger.Info("Shutting down...")
	os.Exit(0)
}

func GetCourse(department string, a *app) {
	collector := colly.NewCollector()
	collector.OnHTML("div[class=course]", func(e *colly.HTMLElement) {
		result, err := ParseCourse(e, a.logger)
		if err != nil {
			a.logger.Error("error while parsing course", err)
			return
		}
		a.remember(result)
	})
	collector.Visit(fmt.Sprintf("%s/subject/%s", a.endpoint, department))
}

func PreCacheCurrentSemesterCourses(a *app, logger *slog.Logger) {
	collector := colly.NewCollector()
	collector.OnHTML("div[class=course]", func(e *colly.HTMLElement) {
		result, err := ParseCourse(e, logger)
		if err != nil {
			logger.Error("error while parsing course", err)
			return
		}
		a.remember(result)
	})
	collector.OnHTML("a[class=ug]", func(e *colly.HTMLElement) {
		department := e.Text
		if !slices.Contains(a.departmentCache, department) {
			logger.Info("Traversing courses for", "department", department)
			collector.Visit(fmt.Sprintf("%s/subject/%s", a.endpoint, department))
			a.departmentCache = append(a.departmentCache, department)
		}
	})
	collector.OnHTML("a[class=pg]", func(e *colly.HTMLElement) {
		department := e.Text
		if !slices.Contains(a.departmentCache, department) {
			logger.Info("Traversing courses for", "department", department)
			collector.Visit(fmt.Sprintf("%s/subject/%s", a.endpoint, department))
			a.departmentCache = append(a.departmentCache, department)
		}
	})
	collector.Visit(fmt.Sprintf("%s/subject/COMP", a.endpoint))
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	a := NewApp(logger)
	a.routes()
	PreCacheCurrentSemesterCourses(a, logger)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		handleInterrupt(logger, a)
	}()
	if err := a.server.Start(":8080"); err != http.ErrServerClosed {
		logger.Error("An unexpected error has occured", err)
	}
	wg.Wait()
}
