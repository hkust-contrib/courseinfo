package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/gocolly/colly/v2"
	"github.com/gorilla/mux"
)

type healthzResponse struct {
	Status string `json:"status"`
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
	m, err := a.manifest.MarshalJSON()
	if err != nil {
		writeResponseBody(w, http.StatusInternalServerError, healthzResponse{
			Status: "error",
		})
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Header().Add("Content-Type", "application/json")
	w.Write(m)
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
			if err.Error() == "invalid semester code" {
				writeResponseBody(w, http.StatusBadRequest, healthzResponse{
					Status: "error",
				})
				return
			} else {
				writeResponseBody(w, http.StatusInternalServerError, healthzResponse{
					Status: "error",
				})
			}
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
	GetCourse(department, a)
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
	PreCacheCurrentSemesterCourses(a, a.logger)
	writeResponseBody(w, http.StatusOK, a.cache)
}

func ParseCourse(e *colly.HTMLElement, logger *slog.Logger) (string, *Course) {
	courseCode, courseTitle, _ := strings.Cut(e.ChildText("h2"), " - ")
	logger.Info("Parsing for", "courseCode", courseCode)
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
	return code, course
}
