package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func setupHandlerTest(method, path string, _ *app) (echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	return c, rec
}

func TestHandleHealthCheck(t *testing.T) {
	a := testApp()
	c, rec := setupHandlerTest(http.MethodGet, "/healthz", a)

	err := a.HandleHealthCheck(c)
	if err != nil {
		t.Fatalf("HandleHealthCheck() error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp healthzResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("status = %q, want %q", resp.Status, "ok")
	}
}

func TestHandleIntrospection(t *testing.T) {
	a := testApp()
	a.manifest = Manifest()
	c, rec := setupHandlerTest(http.MethodGet, "/v1", a)

	err := a.HandleIntrospection(c)
	if err != nil {
		t.Fatalf("HandleIntrospection() error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	for _, key := range []string{"runtime", "uptime"} {
		if _, ok := m[key]; !ok {
			t.Errorf("response missing key %q", key)
		}
	}
}

func TestHandleGetSemester_Current(t *testing.T) {
	a := testApp()
	c, rec := setupHandlerTest(http.MethodGet, "/v1/semesters/current", a)
	c.SetParamNames("semester")
	c.SetParamValues("current")

	err := a.HandleGetSemester(c)
	if err != nil {
		t.Fatalf("HandleGetSemester(current) error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHandleGetSemester_ValidCode(t *testing.T) {
	a := testApp()
	c, rec := setupHandlerTest(http.MethodGet, "/v1/semesters/2510", a)
	c.SetParamNames("semester")
	c.SetParamValues("2510")

	err := a.HandleGetSemester(c)
	if err != nil {
		t.Fatalf("HandleGetSemester(2510) error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var s semester
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if s.Code != "2510" {
		t.Errorf("code = %q, want %q", s.Code, "2510")
	}
}

func TestHandleGetSemester_InvalidCode(t *testing.T) {
	a := testApp()
	c, rec := setupHandlerTest(http.MethodGet, "/v1/semesters/2550", a)
	c.SetParamNames("semester")
	c.SetParamValues("2550")

	err := a.HandleGetSemester(c)
	if err != nil {
		t.Errorf("HandleGetSemester() should return nil after writing error response, got %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleGetSemester_NonNumeric(t *testing.T) {
	a := testApp()
	c, rec := setupHandlerTest(http.MethodGet, "/v1/semesters/XX10", a)
	c.SetParamNames("semester")
	c.SetParamValues("XX10")

	err := a.HandleGetSemester(c)
	if err != nil {
		t.Errorf("HandleGetSemester() should return nil after writing error response, got %v", err)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestHandleGetCourse_CacheHit(t *testing.T) {
	a := testApp()
	a.cache["COMP1021"] = &Course{
		Code:    "COMP1021",
		Title:   "Introduction to Computer Science",
		Credits: 3.0,
	}

	c, rec := setupHandlerTest(http.MethodGet, "/v1/courses/COMP1021", a)
	c.SetParamNames("course")
	c.SetParamValues("COMP1021")

	err := a.HandleGetCourse(c)
	if err != nil {
		t.Fatalf("HandleGetCourse() error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var course Course
	if err := json.Unmarshal(rec.Body.Bytes(), &course); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if course.Code != "COMP1021" {
		t.Errorf("code = %q, want %q", course.Code, "COMP1021")
	}
}

func TestHandleGetCourse_TooShortCode(t *testing.T) {
	a := testApp()
	c, rec := setupHandlerTest(http.MethodGet, "/v1/courses/AB", a)
	c.SetParamNames("course")
	c.SetParamValues("AB")

	err := a.HandleGetCourse(c)
	if err != nil {
		t.Errorf("HandleGetCourse() should return nil after writing error response, got %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleGetCourse_CacheMiss(t *testing.T) {
	a := testApp()
	// Set a non-routable endpoint to avoid making real HTTP requests
	a.endpoint = "http://127.0.0.1:1/invalid"

	c, rec := setupHandlerTest(http.MethodGet, "/v1/courses/COMP9999", a)
	c.SetParamNames("course")
	c.SetParamValues("COMP9999")

	err := a.HandleGetCourse(c)
	if err != nil {
		t.Fatalf("HandleGetCourse() error: %v", err)
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleGetCourses_Empty(t *testing.T) {
	a := testApp()
	c, rec := setupHandlerTest(http.MethodGet, "/v1/courses", a)

	err := a.HandleGetCourses(c)
	if err != nil {
		t.Fatalf("HandleGetCourses() error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var courses []Course
	if err := json.Unmarshal(rec.Body.Bytes(), &courses); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if len(courses) != 0 {
		t.Errorf("len(courses) = %d, want 0", len(courses))
	}
}

func TestHandleGetCourses_Populated(t *testing.T) {
	a := testApp()
	a.cache["COMP1021"] = &Course{Code: "COMP1021", Title: "Intro to CS", Credits: 3.0}
	a.cache["COMP2011"] = &Course{Code: "COMP2011", Title: "Data Structures", Credits: 4.0}

	c, rec := setupHandlerTest(http.MethodGet, "/v1/courses", a)

	err := a.HandleGetCourses(c)
	if err != nil {
		t.Fatalf("HandleGetCourses() error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var courses []*Course
	if err := json.Unmarshal(rec.Body.Bytes(), &courses); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if len(courses) != 2 {
		t.Errorf("len(courses) = %d, want 2", len(courses))
	}
}

func TestExtractDepartment(t *testing.T) {
	tests := []struct {
		code string
		want string
	}{
		{"COMP1021", "COMP"},
		{"MATH2111", "MATH"},
		{"ISOM3230", "ISOM"},
		{"BIEN1010", "BIEN"},
		{"AB123", "AB"},
		// No digits → returns full string (treated as invalid by caller)
		{"COMP", "COMP"},
		// Empty → returns empty
		{"", ""},
		// Starts with digit → returns empty
		{"1234", ""},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			got := extractDepartment(tt.code)
			if got != tt.want {
				t.Errorf("extractDepartment(%q) = %q, want %q", tt.code, got, tt.want)
			}
		})
	}
}

func TestParseCourse(t *testing.T) {
	t.Skip("requires refactoring: ParseCourse depends on colly.HTMLElement which is hard to construct in tests")
}
