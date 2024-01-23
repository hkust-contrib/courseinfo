package main

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

func (a *app) routes() {
	a.logger.Info("Setting up route handlers")
	a.server.GET("/healthz", a.HandleHealthCheck)
	a.server.GET("/", func(c echo.Context) error {
		return c.Redirect(http.StatusMovedPermanently, "/v1")
	})
	group := a.server.Group("/v1")
	group.GET("/", a.HandleIntrospection)
	group.GET("/semesters/:semester", a.HandleGetSemester)
	group.GET("/courses/:course", a.HandleGetCourse)
	group.GET("/courses", a.HandleGetCourses)
	group.PATCH("/courses", a.HandleRefreshCourses)
}
