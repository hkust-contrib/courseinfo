package main

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"unicode"

	"github.com/labstack/echo/v4"
)

func extractDepartment(code string) string {
	for i, r := range code {
		if unicode.IsDigit(r) {
			return code[:i]
		}
	}
	return code
}

func (a *app) HandleIntrospection(c echo.Context) error {
	m, err := a.manifest.MarshalJSON()
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{
			Status:  "error",
			Message: err.Error(),
		})
		return nil
	}
	c.JSONBlob(http.StatusOK, m)
	return nil
}

func (a *app) HandleHealthCheck(c echo.Context) error {
	c.JSON(http.StatusOK, healthzResponse{
		Status: "ok",
	})
	return nil
}

func (a *app) HandleGetSemester(c echo.Context) error {
	a.logger.Info("GET /v1/semesters", "semester", c.Param("semester"))
	if c.Param("semester") != "current" {
		s, err := parseSemester(c.Param("semester"))
		if err != nil {
			if errors.Is(err, ErrInvalidSemesterCode) {
				c.JSON(http.StatusBadRequest, errorResponse{
					Status:  "error",
					Message: err.Error(),
				})
			} else {
				c.JSON(http.StatusInternalServerError, errorResponse{
					Status:  "error",
					Message: err.Error(),
				})
			}
			return nil
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
		return nil
	}
	s, err := parseSemester(currentSemester)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{
			Status:  "error",
			Message: err.Error(),
		})
		return nil
	}
	c.JSON(http.StatusOK, s)
	return nil
}

func (a *app) HandleGetCourse(c echo.Context) error {
	a.logger.Info("GET /v1/courses/", "course", c.Param("course"))
	courseCode := strings.ToUpper(c.Param("course"))
	department := extractDepartment(courseCode)
	if department == "" || department == courseCode {
		c.JSON(http.StatusBadRequest, errorResponse{
			Status:  "error",
			Message: "course code must have an alphabetic department prefix followed by a number",
		})
		return nil
	}

	a.mu.RLock()
	if val, ok := a.cache[courseCode]; ok {
		a.mu.RUnlock()
		c.JSON(http.StatusOK, val)
		return nil
	}
	a.mu.RUnlock()

	a.GetCourse(department)

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
		return nil
	}
	a.setEndpoint(fmt.Sprintf("%s/%s", a.config.BaseURL, semester))
	a.PreCacheCurrentSemesterCourses()

	a.mu.RLock()
	courses := slices.Collect(maps.Values(a.cache))
	a.mu.RUnlock()
	c.JSON(http.StatusOK, courses)
	return nil
}
