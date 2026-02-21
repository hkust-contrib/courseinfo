package main

import (
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"github.com/gocolly/colly/v2"
)

func ParseCourse(e *colly.HTMLElement, logger *slog.Logger) (*CourseParsingResult, error) {
	courseCode, courseTitle, _ := strings.Cut(e.ChildText("div.courseinfo > div.courseattrContainer > div.subject"), " - ")
	logger.Info("Parsing for", "courseCode", courseCode)
	code := strings.ReplaceAll(courseCode, " ", "")

	openParen := strings.LastIndex(courseTitle, "(")
	closeParen := strings.LastIndex(courseTitle, ")")
	if openParen < 0 || closeParen < 0 || closeParen <= openParen {
		return nil, fmt.Errorf("course parsing: malformed title %q: missing parenthesized credits", courseTitle)
	}
	unitString := courseTitle[openParen+1 : closeParen]
	unit, err := strconv.ParseFloat(strings.Split(unitString, " ")[0], 32)
	if err != nil {
		return nil, fmt.Errorf("course parsing: error converting course credits unit: %w", err)
	}

	titleEnd := strings.Index(courseTitle, " (")
	if titleEnd < 0 {
		titleEnd = len(courseTitle)
	}

	course := &Course{
		Code:        code,
		Title:       courseTitle[:titleEnd],
		Credits:     unit,
		Instructors: make(map[string][]string),
	}
	e.ForEach(".newsect", func(i int, e *colly.HTMLElement) {
		var sectionCode string
		for _, section := range e.ChildTexts("td:nth-child(1)") {
			if section != "" {
				if before, _, found := strings.Cut(section, " ("); found {
					sectionCode = before
				} else {
					sectionCode = section
				}
			}
		}
		course.Sections = append(course.Sections, sectionCode)
		taTexts := e.ChildTexts("td:nth-child(5) > div.taListContainer > div.taList > a")
		isTutorial := len(taTexts) > 0 && taTexts[0] != ""
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

func (a *app) GetCourse(department string) {
	collector := colly.NewCollector()
	collector.OnHTML("div[class=course]", func(e *colly.HTMLElement) {
		result, err := ParseCourse(e, a.logger)
		if err != nil {
			a.logger.Error("error while parsing course", slog.String("error", err.Error()))
			return
		}
		a.remember(result)
	})
	collector.Visit(fmt.Sprintf("%s/subject/%s", a.getEndpoint(), department))
}

func (a *app) PreCacheCurrentSemesterCourses() {
	collector := colly.NewCollector()
	collector.OnHTML("div[class=course]", func(e *colly.HTMLElement) {
		result, err := ParseCourse(e, a.logger)
		if err != nil {
			a.logger.Error("error while parsing course", slog.String("error", err.Error()))
			return
		}
		a.remember(result)
	})
	collector.OnHTML("a[class=ug]", func(e *colly.HTMLElement) {
		department := e.Text
		a.mu.RLock()
		found := slices.Contains(a.departmentCache, department)
		a.mu.RUnlock()
		if !found {
			a.logger.Info("Traversing courses for", "department", department)
			a.mu.Lock()
			a.departmentCache = append(a.departmentCache, department)
			a.mu.Unlock()
			collector.Visit(fmt.Sprintf("%s/subject/%s", a.getEndpoint(), department))
		}
	})
	collector.OnHTML("a[class=pg]", func(e *colly.HTMLElement) {
		department := e.Text
		a.mu.RLock()
		found := slices.Contains(a.departmentCache, department)
		a.mu.RUnlock()
		if !found {
			a.logger.Info("Traversing courses for", "department", department)
			a.mu.Lock()
			a.departmentCache = append(a.departmentCache, department)
			a.mu.Unlock()
			collector.Visit(fmt.Sprintf("%s/subject/%s", a.getEndpoint(), department))
		}
	})
	err := collector.Visit(fmt.Sprintf("%s/subject/COMP", a.getEndpoint()))
	if err != nil {
		a.logger.Error("error while visting page", slog.String("error", err.Error()))
	}
}
