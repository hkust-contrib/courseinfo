package main

import (
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"golang.org/x/exp/maps"
)

type semester struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Year   string `json:"year"`
	Cohort string `json:"cohort"`
}

func (a *app) parseSemester(code string) (semester, error) {
	semesterNames := map[string]string{
		"10": "Fall",
		"20": "Winter",
		"30": "Spring",
		"40": "Summer",
	}
	currentYear := fmt.Sprintf("%d", time.Now().Year())
	currentYearPrefix := currentYear[0 : len(currentYear)-2]
	seasonIndicator := code[len(code)-2:]
	if !slices.Contains(maps.Keys(semesterNames), seasonIndicator) {
		a.logger.Error("invalid semester code", "code", code)
		return semester{}, fmt.Errorf("invalid semester code")
	}
	inputSemesterPrefix := code[0 : len(code)-2]
	inputYear, err := strconv.Atoi(inputSemesterPrefix)
	if err != nil {
		a.logger.Error("error while parsing semester code", slog.String("error", err.Error()))
		return semester{}, fmt.Errorf("semester code integer conversion for year: %w", err)
	}
	inputSeason, err := strconv.Atoi(seasonIndicator)
	if err != nil {
		a.logger.Error("error while parsing semester code", slog.String("error", err.Error()))
		return semester{}, fmt.Errorf("semester code integer conversion for season: %w", err)
	}
	cohort := fmt.Sprintf("%s%s - %s%d", currentYearPrefix, inputSemesterPrefix, currentYearPrefix, inputYear+1)
	var year string
	if inputSeason > 20 {
		year = fmt.Sprintf("%s%d", currentYearPrefix, inputYear+1)
	} else {
		year = fmt.Sprintf("%s%s", currentYearPrefix, inputSemesterPrefix)
	}
	return semester{
		Code:   code,
		Name:   fmt.Sprintf("%s %s", cohort, semesterNames[seasonIndicator]),
		Year:   year,
		Cohort: cohort,
	}, nil
}

func getCurrentSemesterCode() (string, error) {
	t := time.Now()
	year := t.Year()
	month := t.Month()
	if month < time.September {
		prefix := fmt.Sprintf("%d", year-1)
		prefix = prefix[len(prefix)-2:]
		if month > time.February {
			if month > time.June {
				return fmt.Sprintf("%s40", prefix), nil
			}
			return fmt.Sprintf("%s30", prefix), nil
		}
		return fmt.Sprintf("%s20", prefix), nil
	} else {
		prefix := fmt.Sprintf("%d", year)
		prefix = prefix[len(prefix)-2:]
		return fmt.Sprintf("%s10", prefix), nil
	}
}
