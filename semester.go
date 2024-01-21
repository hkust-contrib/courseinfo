package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
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
		a.logger.Error("error while parsing semester code", err)
		return semester{}, err
	}
	inputSeason, err := strconv.Atoi(seasonIndicator)
	if err != nil {
		a.logger.Error("error while parsing semester code", err)
		return semester{}, err
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

func getCurrentSemesterCode(logger *slog.Logger) (string, error) {
	req, err := http.NewRequest(http.MethodGet, baseURL, nil)
	if err != nil {
		logger.Error("error while forming http request %s\n", err)
		return "", err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.Error("error making http request: %s\n", err)
		return "", err
	}
	redirect := strings.Split(res.Request.URL.String(), "/")
	return redirect[len(redirect)-2], nil
}
