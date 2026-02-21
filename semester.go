package main

import (
	"fmt"
	"strconv"
	"time"
)

type semester struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Year   string `json:"year"`
	Cohort string `json:"cohort"`
}

const centuryPrefix = "20"

func parseSemester(code string) (semester, error) {
	semesterNames := map[string]string{
		"10": "Fall",
		"20": "Winter",
		"30": "Spring",
		"40": "Summer",
	}
	if len(code) < 3 {
		return semester{}, ErrInvalidSemesterCode
	}
	seasonIndicator := code[len(code)-2:]
	if _, ok := semesterNames[seasonIndicator]; !ok {
		return semester{}, ErrInvalidSemesterCode
	}
	inputSemesterPrefix := code[:len(code)-2]
	inputYear, err := strconv.Atoi(inputSemesterPrefix)
	if err != nil {
		return semester{}, fmt.Errorf("semester code integer conversion for year: %w", err)
	}
	inputSeason, err := strconv.Atoi(seasonIndicator)
	if err != nil {
		return semester{}, fmt.Errorf("semester code integer conversion for season: %w", err)
	}
	cohort := fmt.Sprintf("%s%s - %s%d", centuryPrefix, inputSemesterPrefix, centuryPrefix, inputYear+1)
	var year string
	if inputSeason > 20 {
		year = fmt.Sprintf("%s%d", centuryPrefix, inputYear+1)
	} else {
		year = fmt.Sprintf("%s%s", centuryPrefix, inputSemesterPrefix)
	}
	return semester{
		Code:   code,
		Name:   fmt.Sprintf("%s %s", cohort, semesterNames[seasonIndicator]),
		Year:   year,
		Cohort: cohort,
	}, nil
}

func getSemesterCodeForTime(t time.Time) (string, error) {
	year := t.Year()
	month := t.Month()
	if month < time.September {
		prefix := fmt.Sprintf("%02d", (year-1)%100)
		if month >= time.February {
			if month >= time.June {
				return prefix + "40", nil
			}
			return prefix + "30", nil
		}
		return prefix + "20", nil
	}
	prefix := fmt.Sprintf("%02d", year%100)
	return prefix + "10", nil
}

func getCurrentSemesterCode() (string, error) {
	return getSemesterCodeForTime(time.Now())
}
