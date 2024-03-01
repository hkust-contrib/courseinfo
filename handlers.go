package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/gocolly/colly/v2"
	"github.com/labstack/echo/v4"
	"golang.org/x/exp/maps"
)

type healthzResponse struct {
	Status string `json:"status"`
}

func (a *app) HandleIntrospection(c echo.Context) error {
	m, err := a.manifest.MarshalJSON()
	if err != nil {
		c.JSON(http.StatusInternalServerError, struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		}{
			Status:  "error",
			Message: err.Error(),
		})
		return err
	}
	c.JSONBlob(http.StatusOK, m)
	return nil
}

func (a *app) HandleHealthCheck(c echo.Context) error {
	a.logger.Info("GET /healthz")
	c.JSON(http.StatusOK, healthzResponse{
		Status: "ok",
	})
	return nil
}

func (a *app) HandleGetSemester(c echo.Context) error {
	a.logger.Info("GET /v1/semesters", "semester", c.Param("semester"))
	if c.Param("semester") != "current" {
		s, err := a.parseSemester(c.Param("semester"))
		if err != nil {
			if err.Error() == "invalid semester code" {
				c.JSON(http.StatusBadRequest, struct {
					Status  string `json:"status"`
					Message string `json:"message"`
				}{
					Status:  "error",
					Message: err.Error(),
				})
				return err
			} else {
				c.JSON(http.StatusInternalServerError, struct {
					Status  string `json:"status"`
					Message string `json:"message"`
				}{
					Status:  "error",
					Message: err.Error(),
				})
			}
			return err
		}
		c.JSON(http.StatusOK, s)
		return nil
	}
	currentSemester, err := getCurrentSemesterCode(a.logger)
	if err != nil {
		c.JSON(http.StatusInternalServerError, struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		}{
			Status:  "error",
			Message: err.Error(),
		})
		return err
	}
	s, err := a.parseSemester(currentSemester)
	if err != nil {
		c.JSON(http.StatusInternalServerError, struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		}{
			Status:  "error",
			Message: err.Error(),
		})
		return err
	}
	c.JSON(http.StatusOK, s)
	return nil
}

func (a *app) HandleGetCourse(c echo.Context) error {
	a.logger.Info("GET /v1/courses/", "course", c.Param("course"))
	courseCode := c.Param("course")
	department := courseCode[0:4]
	if val, ok := a.cache[courseCode]; ok {
		c.JSON(http.StatusOK, val)
		return nil
	}
	GetCourse(department, a)
	c.JSON(http.StatusOK, a.cache[courseCode])
	return nil
}

func (a *app) HandleGetCourses(c echo.Context) error {
	a.logger.Info("GET /v1/courses")
	c.JSON(http.StatusOK, maps.Values(a.cache))
	return nil
}

func (a *app) HandleRefreshCourses(c echo.Context) error {
	a.logger.Info("PATCH /v1/courses")
	semester, err := getCurrentSemesterCode(a.logger)
	if err != nil {
		c.JSON(http.StatusInternalServerError, struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		}{
			Status:  "error",
			Message: err.Error(),
		})
		return err
	}
	a.endpoint = fmt.Sprintf("%s/%s", baseURL, semester)
	PreCacheCurrentSemesterCourses(a, a.logger)
	c.JSON(http.StatusOK, a.cache)
	return nil
}

func ParseCourse(e *colly.HTMLElement, logger *slog.Logger) (string, *Course) {
	courseCode, courseTitle, _ := strings.Cut(e.ChildText("h2"), " - ")
	logger.Info("Parsing for", "courseCode", courseCode)
	code := strings.ReplaceAll(courseCode, " ", "")
	unitString := courseTitle[strings.Index(courseTitle, "(")+1:strings.Index(courseTitle, ")")]
	unit := strconv.Atoi(strings.Split(unitString, " ")[0])
	course := &Course{
		Code:        code,
		Title:       courseTitle[0:strings.Index(courseTitle, " (")],
		Credits:	 unit,
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
