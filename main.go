package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gocolly/colly/v2"
	"github.com/gorilla/mux"
)

var (
	baseURL = "https://w5.ab.ust.hk/wcq/cgi-bin"
)

type healthzResponse struct {
	Status string `json:"status"`
}

type semester struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Year   string `json:"year"`
	Cohort string `json:"cohort"`
}

type Course struct {
	Code  string `json:"code"`
	Title string `json:"title"`
}

type buildInfo struct {
	Release        string    `json:"release"`
	Runtime        string    `json:"runtime"`
	RuntimeVersion string    `json:"runtimeVersion"`
	Hostname       string    `json:"hostname"`
	Platform       string    `json:"platform"`
	BuildCommit    string    `json:"buildCommit"`
	BuildDate      string    `json:"buildDate"`
	Uptime         time.Time `json:"uptime"`
}

type app struct {
	startedAt time.Time
	endpoint  string
	cache     map[string]*Course
	router    *mux.Router
	crawler   *colly.Collector
	logger    *slog.Logger
}

func writeResponseBody(w http.ResponseWriter, status int, v any) error {
	w.WriteHeader(status)
	w.Header().Add("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(v)
}

func NewApp(logger *slog.Logger) *app {
	logger.Info("Initializing application...")
	currentSemester := getCurrentSemesterCode()
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

func (a *app) routes() {
	a.logger.Info("Setting up route handlers")
	a.router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/v1", http.StatusMovedPermanently)
	})
	a.router.HandleFunc("/v1", func(w http.ResponseWriter, r *http.Request) {
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "localhost"
		}
		writeResponseBody(w, http.StatusOK, &buildInfo{
			Platform: fmt.Sprintf("%v %v", runtime.GOOS, runtime.GOARCH),
			Runtime:  runtime.Version(),
			Hostname: hostname,
		})
	})
	a.router.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		a.logger.Info("GET /healthz")
		writeResponseBody(w, http.StatusOK, healthzResponse{
			Status: "ok",
		})
	}).Methods("GET")
	a.router.HandleFunc("/v1/semesters/{semester}", func(w http.ResponseWriter, r *http.Request) {
		a.logger.Info("GET /v1/semesters")
		vars := mux.Vars(r)
		if vars["semester"] != "current" {
			s := parseSemester(vars["semester"])
			writeResponseBody(w, http.StatusOK, s)
			return
		}
		currentSemester := getCurrentSemesterCode()
		writeResponseBody(w, http.StatusOK, parseSemester(currentSemester))
	}).Methods("GET")
	a.router.HandleFunc("/v1/courses/{course}", func(w http.ResponseWriter, r *http.Request) {
		a.logger.Info("GET /v1/courses")
		vars := mux.Vars(r)
		courseCode := vars["course"]
		department := courseCode[0:4]
		if val, ok := a.cache[courseCode]; ok {
			writeResponseBody(w, http.StatusOK, val)
			return
		}
		a.crawler.Visit(fmt.Sprintf("%s/subject/%s", a.endpoint, department))
		writeResponseBody(w, http.StatusOK, a.cache[courseCode])
	}).Methods("GET")
}

func (a *app) defineCrawlingRules() {
	a.logger.Info("Setting up crawler...")
	a.crawler.OnHTML("div[class=course]", func(e *colly.HTMLElement) {
		segments := strings.Split(e.ChildText("h2"), " - ")
		a.logger.Info("Received parsing request for ", "payload", segments)
		code := strings.ReplaceAll(segments[0], " ", "")
		course := &Course{
			Code:  code,
			Title: segments[1][0:strings.Index(segments[1], " (")],
		}
		a.remember(code, course)
	})
}

func (a *app) remember(courseCode string, c *Course) {
	a.logger.Info("Caching value for", "courseCode", courseCode)
	a.cache[courseCode] = c
}

func parseSemester(code string) semester {
	semesterNames := map[string]string{
		"10": "Fall",
		"20": "Winter",
		"30": "Spring",
		"40": "Summer",
	}
	currentYear := fmt.Sprintf("%d", time.Now().Year())
	currentYearPrefix := currentYear[0 : len(currentYear)-2]
	seasonIndicator := code[len(code)-2:]
	inputSemesterPrefix := code[0 : len(code)-2]
	inputYear, err := strconv.Atoi(inputSemesterPrefix)
	if err != nil {
		fmt.Println("error while parsing semester code", err)
	}
	inputSeason, err := strconv.Atoi(seasonIndicator)
	if err != nil {
		fmt.Println("error while parsing semester code", err)
	}
	cohort := fmt.Sprintf("%s%s - %s%d", currentYearPrefix, inputSemesterPrefix, currentYearPrefix, inputYear+1)
	var year string
	if inputSeason > 20 {
		year = fmt.Sprintf("%s%d", currentYearPrefix, inputYear+1)
	} else {
		year = fmt.Sprintf("%s%s", currentYearPrefix, inputSemesterPrefix)
	}
	return semester{
		Code:   code,
		Name:   fmt.Sprintf("%s %s", cohort, semesterNames[seasonIndicator]),
		Year:   year,
		Cohort: cohort,
	}
}

func getCurrentSemesterCode() string {
	req, err := http.NewRequest(http.MethodGet, baseURL, nil)
	if err != nil {
		fmt.Printf("error while forming http request %s\n", err)
		os.Exit(1)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("error making http request: %s\n", err)
		os.Exit(1)
	}
	redirect := strings.Split(res.Request.URL.String(), "/")
	return redirect[len(redirect)-2]
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	a := NewApp(logger)
	a.routes()
	a.defineCrawlingRules()
	server := &http.Server{
		Addr:    ":8080",
		Handler: a.router,
	}
	logger.Error("An unexpected error has occured", server.ListenAndServe())
}
