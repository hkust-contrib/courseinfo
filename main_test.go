package main

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLoadConfig_Defaults(t *testing.T) {
	cfg := loadConfig()
	if cfg.Port != ":8080" {
		t.Errorf("Port = %q, want %q", cfg.Port, ":8080")
	}
	if cfg.MetricsPort != ":2112" {
		t.Errorf("MetricsPort = %q, want %q", cfg.MetricsPort, ":2112")
	}
	if cfg.BaseURL != "https://w5.ab.ust.hk/wcq/cgi-bin" {
		t.Errorf("BaseURL = %q, want default", cfg.BaseURL)
	}
	if cfg.RefreshInterval != 7*24*time.Hour {
		t.Errorf("RefreshInterval = %v, want %v", cfg.RefreshInterval, 7*24*time.Hour)
	}
}

func TestLoadConfig_FromEnv(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("METRICS_PORT", "3000")
	t.Setenv("BASE_URL", "https://example.com")
	t.Setenv("REFRESH_INTERVAL", "1h")

	cfg := loadConfig()
	if cfg.Port != ":9090" {
		t.Errorf("Port = %q, want %q", cfg.Port, ":9090")
	}
	if cfg.MetricsPort != ":3000" {
		t.Errorf("MetricsPort = %q, want %q", cfg.MetricsPort, ":3000")
	}
	if cfg.BaseURL != "https://example.com" {
		t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, "https://example.com")
	}
	if cfg.RefreshInterval != time.Hour {
		t.Errorf("RefreshInterval = %v, want %v", cfg.RefreshInterval, time.Hour)
	}
}

func TestRemember(t *testing.T) {
	a := testApp()

	r := &CourseParsingResult{
		Code: "COMP1021",
		Course: &Course{
			Code:    "COMP1021",
			Title:   "Introduction to Computer Science",
			Credits: 3.0,
		},
	}
	a.remember(r)

	a.mu.RLock()
	got, ok := a.cache["COMP1021"]
	a.mu.RUnlock()

	if !ok {
		t.Fatal("remember() did not add course to cache")
	}
	if got.Title != "Introduction to Computer Science" {
		t.Errorf("cache[COMP1021].Title = %q, want %q", got.Title, "Introduction to Computer Science")
	}

	// Overwrite
	r2 := &CourseParsingResult{
		Code: "COMP1021",
		Course: &Course{
			Code:    "COMP1021",
			Title:   "Intro to CS (Updated)",
			Credits: 3.0,
		},
	}
	a.remember(r2)

	a.mu.RLock()
	got2 := a.cache["COMP1021"]
	a.mu.RUnlock()

	if got2.Title != "Intro to CS (Updated)" {
		t.Errorf("cache[COMP1021].Title = %q after overwrite, want %q", got2.Title, "Intro to CS (Updated)")
	}
}

func TestRemember_ConcurrentSafe(t *testing.T) {
	a := testApp()
	var wg sync.WaitGroup

	for i := range 100 {
		wg.Go(func() {
			code := "COMP" + strings.Repeat("0", 4-len(string(rune('0'+i%10)))) + string(rune('0'+i%10))
			a.remember(&CourseParsingResult{
				Code: code,
				Course: &Course{
					Code:    code,
					Title:   "Test Course",
					Credits: 3.0,
				},
			})
		})
	}
	wg.Wait()
}

func TestManifest(t *testing.T) {
	m := Manifest()

	if m.Name == "" {
		t.Error("Manifest().Name should not be empty")
	}
	if m.Runtime == "" {
		t.Error("Manifest().Runtime should not be empty")
	}
	if m.Platform == "" {
		t.Error("Manifest().Platform should not be empty")
	}
	if m.Hostname == "" {
		t.Error("Manifest().Hostname should not be empty")
	}
	if m.Version == "" {
		t.Error("Manifest().Version should not be empty")
	}
	if m.StartTime.IsZero() {
		t.Error("Manifest().StartTime should not be zero")
	}
	if time.Since(m.StartTime) > 5*time.Second {
		t.Error("Manifest().StartTime should be recent")
	}
}

func TestBuildInfo_Uptime(t *testing.T) {
	b := &buildInfo{
		StartTime: time.Now().Add(-10 * time.Second),
	}
	uptime := b.Uptime()
	if uptime == "" {
		t.Error("Uptime() should not be empty")
	}
	// Should be roughly "10.xx"
	if !strings.HasPrefix(uptime, "1") {
		t.Errorf("Uptime() = %q, expected to start with '1' (around 10 seconds)", uptime)
	}
}

func TestBuildInfo_MarshalJSON(t *testing.T) {
	b := &buildInfo{
		Name:        "Test",
		Runtime:     "go1.21",
		Hostname:    "test-host",
		Platform:    "linux amd64",
		Version:     "0.0.1",
		BuildCommit: "abc123",
		BuildDate:   "2024-01-01",
		StartTime:   time.Now(),
	}

	data, err := b.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() error: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	expectedKeys := []string{"runtime", "hostname", "platform", "build_commit", "build_date", "uptime"}
	for _, key := range expectedKeys {
		if _, ok := m[key]; !ok {
			t.Errorf("MarshalJSON() missing key %q", key)
		}
	}

	if m["runtime"] != "go1.21" {
		t.Errorf("runtime = %v, want go1.21", m["runtime"])
	}
	if m["hostname"] != "test-host" {
		t.Errorf("hostname = %v, want test-host", m["hostname"])
	}
}

func TestBuildInfo_String(t *testing.T) {
	b := &buildInfo{
		Name:        "Test App",
		Runtime:     "go1.21",
		Platform:    "linux amd64",
		Version:     "0.0.1",
		BuildCommit: "abc123",
		BuildDate:   "2024-01-01",
	}

	s := b.String()
	for _, want := range []string{"Application", "Runtime", "Platform", "Version", "Commit", "Build Date"} {
		if !strings.Contains(s, want) {
			t.Errorf("String() = %q, want to contain %q", s, want)
		}
	}
	for _, want := range []string{"Test App", "go1.21", "linux amd64", "0.0.1", "abc123", "2024-01-01"} {
		if !strings.Contains(s, want) {
			t.Errorf("String() = %q, want to contain %q", s, want)
		}
	}
}
