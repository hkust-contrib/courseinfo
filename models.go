package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"time"
)

type Course struct {
	Code        string              `json:"code"`
	Title       string              `json:"title"`
	Credits     float64             `json:"credits"`
	Instructors map[string][]string `json:"instructors"`
	Sections    []string            `json:"sections"`
}

type CourseParsingResult struct {
	Code   string
	Course *Course
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

type healthzResponse struct {
	Status string `json:"status"`
}

type errorResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
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
		BuildCommit string `json:"build_commit"`
		BuildDate   string `json:"build_date"`
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
