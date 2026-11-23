package main

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestF11SampleMatchesF06AndHandCalculatedIntensity(t *testing.T) {
	for _, tt := range []struct {
		name, query, unit          string
		numerator, area, headcount float64
	}{
		{"electricity", "building=office-a&energy=electricity&period=day", "kWh", 1536, 1.28, 16},
		{"water", "building=office-a&energy=water&period=day", "m³", 384, .32, 4},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var data struct {
				Sample                                                       bool
				Unit, From, To, AsOf, Quality, RuleVersion, MeterRuleVersion string
				Numerator                                                    f06Value
				Sources                                                      []f06Source
				Metrics                                                      []struct {
					ID, Unit, Status string
					Value            *float64
					Denominator      *struct {
						Value                           float64
						Unit, Source, Version, From, To string
					}
				}
			}
			getSamplePage(t, "/efficiency?"+tt.query, &data)
			var classification f06Report
			getSamplePage(t, "/classification?"+tt.query, &classification)
			assertF06Usage(t, data.Numerator.Usage, tt.numerator)
			assertF06Usage(t, classification.Total.Usage, tt.numerator)
			if !data.Sample || data.Unit != tt.unit || data.Quality != "valid" || data.From != "2026-10-15T00:00:00+08:00" || data.To != "2026-10-16T00:00:00+08:00" || data.AsOf != data.To || len(data.Sources) != 3 ||
				data.RuleVersion == "" || data.MeterRuleVersion != classification.RuleVersion || len(data.Metrics) != 2 {
				t.Fatalf("intensity report lacks same-scope metadata: %+v", data)
			}
			for i, expected := range []struct {
				id, unit           string
				value, denominator float64
			}{{"per-area", "m²", tt.area, 1200}, {"per-person", "人", tt.headcount, 96}} {
				metric := data.Metrics[i]
				assertF06Usage(t, metric.Value, expected.value)
				if metric.ID != expected.id || metric.Unit != tt.unit+"/"+expected.unit || metric.Status != "ready" ||
					metric.Denominator == nil || metric.Denominator.Unit != expected.unit || metric.Denominator.Value != expected.denominator ||
					metric.Denominator.Version == "" || metric.Denominator.Source == "" || metric.Denominator.From == "" || metric.Denominator.To == "" {
					t.Fatalf("metric lacks denominator scope: %+v", metric)
				}
			}
		})
	}
}

func TestF11MissingDenominatorAndPartialUsageSuppressIntensity(t *testing.T) {
	for _, tt := range []struct{ name, query, quality, areaStatus, personStatus string }{
		{"missing headcount", "building=office-b&energy=electricity&period=day", "valid", "ready", "missing_denominator"},
		{"missing and suspect intervals", "building=office-a&energy=electricity&period=7", "partial", "insufficient_quality", "insufficient_quality"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var data struct {
				Quality   string
				Numerator f06Value
				Metrics   []struct {
					Status string
					Value  *float64
				}
			}
			getSamplePage(t, "/efficiency?"+tt.query, &data)
			if data.Quality != tt.quality || data.Numerator.Usage == nil || len(data.Metrics) != 2 {
				t.Fatalf("measured numerator or quality missing: %+v", data)
			}
			for i, status := range []string{tt.areaStatus, tt.personStatus} {
				metric := data.Metrics[i]
				if metric.Status != status || (metric.Value != nil) != (status == "ready") {
					t.Fatalf("got %+v for %s", metric, status)
				}
			}
		})
	}
}

func TestF11IntensityDistinguishesZeroInvalidAndCrossPeriodInputs(t *testing.T) {
	from := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	zero := 0.0
	for _, tt := range []struct {
		name, status string
		change       func(*f06Report, *metricDenominator)
	}{
		{"valid zero", "ready", func(*f06Report, *metricDenominator) {}},
		{"zero denominator", "invalid_denominator", func(_ *f06Report, d *metricDenominator) { d.Value = 0 }},
		{"negative denominator", "invalid_denominator", func(_ *f06Report, d *metricDenominator) { d.Value = -1 }},
		{"non-finite denominator", "invalid_denominator", func(_ *f06Report, d *metricDenominator) { d.Value = math.Inf(1) }},
		{"wrong unit", "invalid_denominator", func(_ *f06Report, d *metricDenominator) { d.Unit = "人" }},
		{"cross-period denominator", "denominator_out_of_period", func(_ *f06Report, d *metricDenominator) { d.To = to.Add(-time.Hour) }},
		{"missing usage", "missing_usage", func(r *f06Report, _ *metricDenominator) { r.Total.Usage = nil }},
		{"partial zero", "insufficient_quality", func(r *f06Report, _ *metricDenominator) { r.Total.Coverage.Valid-- }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			usage := f06Report{Unit: "kWh", From: from, To: to,
				Total: f06Value{Usage: &zero, Coverage: f06Coverage{Expected: 3, Valid: 3}}}
			denominator := &metricDenominator{Value: 1200, Unit: "m²", Source: "sample", Version: "v1", From: from, To: to}
			tt.change(&usage, denominator)
			metric := calculateIntensity("per-area", "m²", usage, denominator)
			if metric.Status != tt.status {
				t.Fatalf("expected %s, got %+v", tt.status, metric)
			}
			if tt.status == "ready" {
				assertF06Usage(t, metric.Value, 0)
			} else if metric.Value != nil {
				t.Fatal("unavailable metric returned a number")
			}
		})
	}
}

func getSamplePage(t *testing.T, route string, data any) {
	t.Helper()
	response := httptest.NewRecorder()
	previewHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, overviewPath+route, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("%s returned %d: %s", route, response.Code, response.Body)
	}
	var payload struct{ Data json.RawMessage }
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payload.Data, data); err != nil {
		t.Fatal(err)
	}
}
