package main

import (
	"errors"
	"io"
	"log/slog"
	"regexp"
	"sync"
	"testing"
	"time"
)

// testApp returns an *app with a discarding logger suitable for tests.
func testApp() *app {
	return &app{
		cache:           make(map[string]*Course),
		departmentCache: []string{},
		logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestGetCurrentSemesterCode(t *testing.T) {
	code, err := getCurrentSemesterCode()
	if err != nil {
		t.Fatalf("getCurrentSemesterCode() returned error: %v", err)
	}
	pattern := regexp.MustCompile(`^\d{2}(10|20|30|40)$`)
	if !pattern.MatchString(code) {
		t.Errorf("getCurrentSemesterCode() = %q, want match for %s", code, pattern)
	}
}

func TestParseSemester(t *testing.T) {
	tests := []struct {
		name       string
		code       string
		wantName   string
		wantErr    bool
		errSubstr  string
	}{
		{
			name:     "Fall semester",
			code:     "2510",
			wantName: "Fall",
			wantErr:  false,
		},
		{
			name:     "Winter semester",
			code:     "2520",
			wantName: "Winter",
			wantErr:  false,
		},
		{
			name:     "Spring semester",
			code:     "2530",
			wantName: "Spring",
			wantErr:  false,
		},
		{
			name:     "Summer semester",
			code:     "2540",
			wantName: "Summer",
			wantErr:  false,
		},
		{
			name:      "Invalid season 50",
			code:      "2550",
			wantErr:   true,
			errSubstr: "invalid semester code",
		},
		{
			name:      "Invalid season 00",
			code:      "2500",
			wantErr:   true,
			errSubstr: "invalid semester code",
		},
		{
			name:      "Non-numeric prefix",
			code:      "XX10",
			wantErr:   true,
			errSubstr: "integer conversion",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := parseSemester(tt.code)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseSemester(%q) expected error, got nil", tt.code)
				}
				if tt.errSubstr != "" && !containsSubstring(err.Error(), tt.errSubstr) {
					t.Errorf("parseSemester(%q) error = %q, want substring %q", tt.code, err.Error(), tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSemester(%q) unexpected error: %v", tt.code, err)
			}
			if s.Code != tt.code {
				t.Errorf("parseSemester(%q).Code = %q, want %q", tt.code, s.Code, tt.code)
			}
			if !containsSubstring(s.Name, tt.wantName) {
				t.Errorf("parseSemester(%q).Name = %q, want to contain %q", tt.code, s.Name, tt.wantName)
			}
		})
	}
}

func TestParseSemester_SentinelError(t *testing.T) {
	_, err := parseSemester("2550")
	if err == nil {
		t.Fatal("expected error for invalid code 2550")
	}
	if !errors.Is(err, ErrInvalidSemesterCode) {
		t.Errorf("error = %v, want ErrInvalidSemesterCode", err)
	}
}

func TestParseSemester_Fields(t *testing.T) {
	s, err := parseSemester("2510")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if s.Code != "2510" {
		t.Errorf("Code = %q, want %q", s.Code, "2510")
	}
	if s.Cohort == "" {
		t.Error("Cohort should not be empty")
	}
	if s.Year == "" {
		t.Error("Year should not be empty")
	}
	if s.Name == "" {
		t.Error("Name should not be empty")
	}
}

func TestParseSemester_ConcurrentSafe(t *testing.T) {
	var wg sync.WaitGroup
	codes := []string{"2510", "2520", "2530", "2540"}
	for _, code := range codes {
		wg.Go(func() {
			_, _ = parseSemester(code)
		})
	}
	wg.Wait()
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && contains(s, substr))
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestGetSemesterCodeForTime(t *testing.T) {
	tests := []struct {
		name string
		time time.Time
		want string
	}{
		// Fall: Sep-Dec → current year prefix + "10"
		{"September 2025", time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC), "2510"},
		{"October 2025", time.Date(2025, 10, 15, 0, 0, 0, 0, time.UTC), "2510"},
		{"December 2025", time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), "2510"},

		// Winter: Jan → (year-1) prefix + "20"
		{"January 2026", time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), "2520"},

		// Spring: Feb-May → (year-1) prefix + "30"
		{"February 2026", time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), "2530"},
		{"May 2026", time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC), "2530"},

		// Summer: Jun-Aug → (year-1) prefix + "40"
		{"June 2026", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), "2540"},
		{"August 2026", time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC), "2540"},

		// Boundary: last day of August → still Summer
		{"Aug 31 boundary", time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC), "2540"},
		// Boundary: first day of September → Fall of new academic year
		{"Sep 1 boundary", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), "2610"},

		// Century edge: year 2000
		{"Year 2000 Sep", time.Date(2000, 9, 1, 0, 0, 0, 0, time.UTC), "0010"},
		{"Year 2001 Jan", time.Date(2001, 1, 15, 0, 0, 0, 0, time.UTC), "0020"},

		// Year 2099/2100 boundary
		{"Year 2099 Sep", time.Date(2099, 9, 1, 0, 0, 0, 0, time.UTC), "9910"},
		{"Year 2100 Jan", time.Date(2100, 1, 15, 0, 0, 0, 0, time.UTC), "9920"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getSemesterCodeForTime(tt.time)
			if err != nil {
				t.Fatalf("getSemesterCodeForTime() error: %v", err)
			}
			if got != tt.want {
				t.Errorf("getSemesterCodeForTime(%v) = %q, want %q", tt.time, got, tt.want)
			}
		})
	}
}
