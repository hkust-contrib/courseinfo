package main

import (
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/gocolly/colly/v2"
	"github.com/labstack/echo/v4"
)

type healthzResponse struct {
	Status string `json:"status"`
}

type errorResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

func (a *app) HandleIntrospection(c echo.Context) error {
	m, err := a.manifest.MarshalJSON()
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{
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
				c.JSON(http.StatusBadRequest, errorResponse{
					Status:  "error",
					Message: err.Error(),
				})
				return err
			} else {
				c.JSON(http.StatusInternalServerError, errorResponse{
					Status:  "error",
					Message: err.Error(),
				})
			}
			return err
		}
		c.JSON(http.StatusOK, s)
		return nil
	}
	currentSemester, err := getCurrentSemesterCode()
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{
			Status:  "error",
			Message: err.Error(),
		})
		return err
	}
	s, err := a.parseSemester(currentSemester)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{
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
	courseCode := strings.ToUpper(c.Param("course"))
	if len(courseCode) < 4 {
		c.JSON(http.StatusBadRequest, errorResponse{
			Status:  "error",
			Message: "course code must be at least 4 characters",
		})
		return fmt.Errorf("invalid course code: %s", courseCode)
	}
	department := courseCode[0:4]

	a.mu.RLock()
	if val, ok := a.cache[courseCode]; ok {
		a.mu.RUnlock()
		c.JSON(http.StatusOK, val)
		return nil
	}
	a.mu.RUnlock()

	GetCourse(department, a)

	a.mu.RLock()
	val, ok := a.cache[courseCode]
	a.mu.RUnlock()
	if !ok {
		c.JSON(http.StatusNotFound, errorResponse{
			Status:  "error",
			Message: fmt.Sprintf("course %s not found", courseCode),
		})
		return nil
	}
	c.JSON(http.StatusOK, val)
	return nil
}

func (a *app) HandleGetCourses(c echo.Context) error {
	a.logger.Info("GET /v1/courses")
	a.mu.RLock()
	courses := slices.Collect(maps.Values(a.cache))
	a.mu.RUnlock()
	c.JSON(http.StatusOK, courses)
	return nil
}

func (a *app) HandleRefreshCourses(c echo.Context) error {
	a.logger.Info("PATCH /v1/courses")
	semester, err := getCurrentSemesterCode()
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{
			Status:  "error",
			Message: err.Error(),
		})
		return fmt.Errorf("route handler: error getting current semester code: %w", err)
	}
	a.endpoint = fmt.Sprintf("%s/%s", baseURL, semester)
	PreCacheCurrentSemesterCourses(a, a.logger)

	a.mu.RLock()
	cache := a.cache
	a.mu.RUnlock()
	c.JSON(http.StatusOK, cache)
	return nil
}

func ParseCourse(e *colly.HTMLElement, logger *slog.Logger) (*CourseParsingResult, error) {
	courseCode, courseTitle, _ := strings.Cut(e.ChildText("div.courseinfo > div.courseattrContainer > div.subject"), " - ")
	logger.Info("Parsing for", "courseCode", courseCode)
	code := strings.ReplaceAll(courseCode, " ", "")
	unitString := courseTitle[strings.LastIndex(courseTitle, "(")+1 : strings.LastIndex(courseTitle, ")")]
	unit, err := strconv.ParseFloat(strings.Split(unitString, " ")[0], 32)
	if err != nil {
		return nil, fmt.Errorf("course parsing: error converting course credits unit: %w", err)
	}
	course := &Course{
		Code:        code,
		Title:       courseTitle[0:strings.Index(courseTitle, " (")],
		Credits:     unit,
		Instructors: make(map[string][]string),
	}
	e.ForEach(".newsect", func(i int, e *colly.HTMLElement) {
		var sectionCode string
		for _, section := range e.ChildTexts("td:nth-child(1)") {
			if section != "" {
				sectionCode = section[0:strings.Index(section, " (")]
			}
		}
		course.Sections = append(course.Sections, sectionCode)
		isTutorial := len(e.ChildTexts("td:nth-child(5) > div.taListContainer > div.taList > a")) > 0 && e.ChildTexts("td:nth-child(5) > div.taListContainer > div.taList > a")[0] != ""
		querySelector := "td:nth-child(4) > div.instructorList > a"
		if isTutorial {
			querySelector = "td:nth-child(5) > div.taListContainer > div.taList > a"
		}
		for _, name := range e.ChildTexts(querySelector) {
			course.Instructors[name] = append(course.Instructors[name], sectionCode)
		}
	})
	return &CourseParsingResult{
		Code:   code,
		Course: course,
	}, nil
}
