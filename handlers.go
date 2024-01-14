package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/gorilla/mux"
)

type healthzResponse struct {
	Status string `json:"status"`
}

type buildInfo struct {
	Runtime     string `json:"runtime"`
	Hostname    string `json:"hostname"`
	Platform    string `json:"platform"`
	BuildCommit string `json:"buildCommit"`
	BuildDate   string `json:"buildDate"`
	Uptime      string `json:"uptime"`
}

func writeResponseBody(w http.ResponseWriter, status int, v any) error {
	w.WriteHeader(status)
	w.Header().Add("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(v)
}

func (a *app) HandleRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/v1", http.StatusMovedPermanently)
}

func (a *app) HandleIntrospection(w http.ResponseWriter, r *http.Request) {
	{
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "localhost"
		}
		writeResponseBody(w, http.StatusOK, &buildInfo{
			Platform: fmt.Sprintf("%v %v", runtime.GOOS, runtime.GOARCH),
			Runtime:  runtime.Version(),
			Hostname: hostname,
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
			Uptime: fmt.Sprintf("%.2f", time.Since(a.startedAt).Seconds()),
		})
	}
}

func (a *app) HandleHealthCheck(w http.ResponseWriter, r *http.Request) {
	a.logger.Info("GET /healthz")
	writeResponseBody(w, http.StatusOK, healthzResponse{
		Status: "ok",
	})
}

func (a *app) HandleGetSemester(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	a.logger.Info("GET /v1/semesters", "semester", vars["semester"])
	if vars["semester"] != "current" {
		s, err := a.parseSemester(vars["semester"])
		if err != nil {
			writeResponseBody(w, http.StatusInternalServerError, healthzResponse{
				Status: "error",
			})
			return
		}
		writeResponseBody(w, http.StatusOK, s)
		return
	}
	currentSemester, err := getCurrentSemesterCode(a.logger)
	if err != nil {
		writeResponseBody(w, http.StatusInternalServerError, healthzResponse{
			Status: "error",
		})
		return
	}
	s, err := a.parseSemester(currentSemester)
	if err != nil {
		writeResponseBody(w, http.StatusInternalServerError, healthzResponse{
			Status: "error",
		})
		return
	}
	writeResponseBody(w, http.StatusOK, s)
}

func (a *app) HandleGetCourse(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	a.logger.Info("GET /v1/courses/", "course", vars["course"])
	courseCode := vars["course"]
	department := courseCode[0:4]
	if val, ok := a.cache[courseCode]; ok {
		writeResponseBody(w, http.StatusOK, val)
		return
	}
	a.crawler.Visit(fmt.Sprintf("%s/subject/%s", a.endpoint, department))
	writeResponseBody(w, http.StatusOK, a.cache[courseCode])
}

func (a *app) HandleGetCourses(w http.ResponseWriter, r *http.Request) {
	a.logger.Info("GET /v1/courses")
	writeResponseBody(w, http.StatusOK, a.cache)
}

func (a *app) HandleRefreshCourses(w http.ResponseWriter, r *http.Request) {
	a.logger.Info("PATCH /v1/courses")
	semester, err := getCurrentSemesterCode(a.logger)
	if err != nil {
		writeResponseBody(w, http.StatusInternalServerError, healthzResponse{
			Status: "error",
		})
		return
	}
	a.endpoint = fmt.Sprintf("%s/%s", baseURL, semester)
	a.crawler.Visit(a.endpoint)
	writeResponseBody(w, http.StatusOK, a.cache)
}
