package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
	"github.com/gorilla/mux"
)

var (
	baseURL = "https://w5.ab.ust.hk/wcq/cgi-bin"
)

type Course struct {
	Code  string `json:"code"`
	Title string `json:"title"`
}

type app struct {
	startedAt time.Time
	endpoint  string
	cache     map[string]*Course
	router    *mux.Router
	crawler   *colly.Collector
	logger    *slog.Logger
}

func NewApp(logger *slog.Logger) *app {
	logger.Info("Initializing application...")
	currentSemester, err := getCurrentSemesterCode(logger)
	if err != nil {
		logger.Error("error while getting current semester code", err)
		os.Exit(1)
	}
	router := mux.NewRouter()
	return &app{
		startedAt: time.Now(),
		endpoint:  fmt.Sprintf("%s/%s", baseURL, currentSemester),
		router:    router,
		cache:     make(map[string]*Course),
		crawler:   colly.NewCollector(),
		logger:    logger,
	}
}

func (a *app) defineCrawlingRules() {
	a.logger.Info("Setting up crawler...")
	a.crawler.OnHTML("div[class=course]", func(e *colly.HTMLElement) {
		courseCode, courseTitle, _ := strings.Cut(e.ChildText("h2"), " - ")
		a.logger.Info("Caching result for", "courseCode", courseCode, "courseTitle", courseTitle)
		code := strings.ReplaceAll(courseCode, " ", "")
		course := &Course{
			Code:  code,
			Title: courseTitle[0:strings.Index(courseTitle, " (")],
		}
		a.remember(code, course)
	})
	a.crawler.OnHTML("a[class=ug]", func(e *colly.HTMLElement) {
		department := e.Text
		a.crawler.Visit(fmt.Sprintf("%s/subject/%s", a.endpoint, department))
	})
	a.crawler.OnHTML("a[class=pg]", func(e *colly.HTMLElement) {
		department := e.Text
		a.crawler.Visit(fmt.Sprintf("%s/subject/%s", a.endpoint, department))
	})
}

func (a *app) remember(courseCode string, c *Course) {
	a.logger.Info("Caching value for", "courseCode", courseCode)
	a.cache[courseCode] = c
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	a := NewApp(logger)
	a.routes()
	a.defineCrawlingRules()
	// a.crawler.Visit(fmt.Sprintf("%s/subject/COMP", a.endpoint))
	server := &http.Server{
		Addr:    ":8080",
		Handler: a.router,
	}
	logger.Error("An unexpected error has occured", server.ListenAndServe())
}
