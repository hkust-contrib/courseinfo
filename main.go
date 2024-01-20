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
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
	"github.com/gorilla/mux"
)

var (
	baseURL = "https://w5.ab.ust.hk/wcq/cgi-bin"
)

type Course struct {
	Code        string   `json:"code"`
	Title       string   `json:"title"`
	Instructors []string `json:"instructors"`
	Sections    []string `json:"sections"`
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
	endpoint string
	cache    map[string]*Course
	router   *mux.Router
	crawler  *colly.Collector
	logger   *slog.Logger
	manifest *buildInfo
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
	currentSemester, err := getCurrentSemesterCode(logger)
	if err != nil {
		logger.Error("error while getting current semester code", err)
		os.Exit(1)
	}
	router := mux.NewRouter()
	return &app{
		endpoint: fmt.Sprintf("%s/%s", baseURL, currentSemester),
		router:   router,
		cache:    make(map[string]*Course),
		crawler:  colly.NewCollector(),
		logger:   logger,
		manifest: manifest,
	}
}

func (a *app) defineCrawlingRules() {
	a.crawler.OnHTML("div[class=course]", func(e *colly.HTMLElement) {
		courseCode, courseTitle, _ := strings.Cut(e.ChildText("h2"), " - ")
		a.logger.Info("Parsing for", "courseCode", courseCode)
		code := strings.ReplaceAll(courseCode, " ", "")
		course := &Course{
			Code:        code,
			Title:       courseTitle[0:strings.Index(courseTitle, " (")],
			Instructors: []string{},
		}
		for _, name := range e.ChildTexts("a") {
			if !slices.Contains(course.Instructors, name) && name != "" {
				course.Instructors = append(course.Instructors, name)
			}
		}
		for _, section := range e.ChildTexts(".newsect > td:nth-child(1)") {
			if !slices.Contains(course.Sections, section) && section != "" {
				course.Sections = append(course.Sections, section[0:strings.Index(section, " (")])
			}
		}
		a.remember(code, course)
	})
	a.crawler.OnHTML("a[class=ug]", func(e *colly.HTMLElement) {
		department := e.Text
		a.logger.Info("Traversing courses for", "department", department)
		a.crawler.Visit(fmt.Sprintf("%s/subject/%s", a.endpoint, department))
	})
	a.crawler.OnHTML("a[class=pg]", func(e *colly.HTMLElement) {
		department := e.Text
		a.logger.Info("Traversing courses for", "department", department)
		a.crawler.Visit(fmt.Sprintf("%s/subject/%s", a.endpoint, department))
	})
	a.logger.Info("Crawler parsing and traversing rules established")
}

func (a *app) remember(courseCode string, c *Course) {
	a.cache[courseCode] = c
	a.logger.Info("In-memory cache updated for", "courseCode", courseCode)
}

func handleInterrupt(logger *slog.Logger, server *http.Server) {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)
	<-quit
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("Server Shutdown: ", err)
	}
	logger.Info("Shutting down...")
	os.Exit(0)
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	a := NewApp(logger)
	a.routes()
	a.defineCrawlingRules()
	a.crawler.Visit(fmt.Sprintf("%s/subject/COMP", a.endpoint))
	server := &http.Server{
		Addr:    ":8080",
		Handler: a.router,
	}
	go handleInterrupt(logger, server)
	logger.Error("An unexpected error has occured", server.ListenAndServe())
}
