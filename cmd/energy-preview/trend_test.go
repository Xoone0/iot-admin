package main

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func readTrendFixture(t *testing.T, grain, mode, scenario string) f08Report {
	t.Helper()
	result := httptest.NewRecorder()
	query := "?building=office-a&energy=electricity&granularity=" + grain + "&mode=" + mode + "&scenario=" + scenario
	previewHandler().ServeHTTP(result, httptest.NewRequest(http.MethodGet, trendPath+query, nil))
	var payload struct {
		Code int       `json:"code"`
		Data f08Report `json:"data"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &payload); err != nil || result.Code != http.StatusOK || payload.Code != 0 {
		t.Fatalf("response %d (%v): %s", result.Code, err, result.Body)
	}
	return payload.Data
}

func TestF08SampleStatisticsMatchHandCalculation(t *testing.T) {
	for _, tc := range []struct {
		grain, mode              string
		buckets                  int
		hours, current, mom, yoy float64
	}{
		{"hour", "complete", 24, 1, 64, 48, 40},
		{"day", "complete", 14, 24, 1536, 1152, 960},
		{"day", "to-date", 14, 12, 768, 576, 480},
		{"month", "to-date", 12, 348, 14208, 12432, 8880},
	} {
		t.Run(tc.grain+"/"+tc.mode, func(t *testing.T) {
			report := readTrendFixture(t, tc.grain, tc.mode, "standard")
			if !report.Sample || report.Unit != "kWh" || report.Timezone != "Asia/Shanghai" ||
				report.Current.Hours != tc.hours || report.Partial != (tc.mode == "to-date") || len(report.Trend) != tc.buckets {
				t.Fatalf("invalid report periods or units: %+v", report)
			}
			if report.RuleVersion != "f08-v1" || report.Current.MeterRuleVersion != "m1-a-v1" ||
				report.Current.ClassificationRuleVersion != "m1-c-v1" || report.Current.CatalogVersion == "" || report.Current.TopologyVersion == "" ||
				report.AsOf.Before(report.Current.To) {
				t.Fatalf("calculation provenance missing: %+v", report)
			}
			assertF06Usage(t, report.Current.Usage, tc.current)
			for i := 1; i < len(report.Trend); i++ {
				if !report.Trend[i-1].To.Equal(report.Trend[i].From) {
					t.Fatalf("bucket boundary drift at %d: %+v", i, report.Trend)
				}
			}
			latest := report.Trend[len(report.Trend)-1]
			assertF06Usage(t, latest.Usage, tc.current)
			if !latest.To.Equal(report.Current.To) || latest.Coverage.Expected != tc.hours*3 || latest.Coverage.Valid != latest.Coverage.Expected {
				t.Fatalf("trend and report coverage differ: %+v", latest)
			}
			for _, baseline := range []struct {
				value float64
				got   f08Comparison
			}{{tc.mom, report.Previous}, {tc.yoy, report.PriorYear}} {
				result := baseline.got
				assertF06Usage(t, result.Period.Usage, baseline.value)
				assertF06Usage(t, result.Delta, tc.current-baseline.value)
				rate := (tc.current - baseline.value) / baseline.value
				if result.Status != "comparable" || result.ChangeRate == nil || math.Abs(*result.ChangeRate-rate) > 1e-12 ||
					result.Period.Hours != tc.hours || result.Period.Coverage.Valid != result.Period.Coverage.Expected {
					t.Fatalf("wrong comparison: %+v, expected rate %v and duration %v", result, rate, tc.hours)
				}
			}
		})
	}
}

func TestF08ComparisonStatesStayDistinct(t *testing.T) {
	for _, tc := range []struct{ scenario, status string }{
		{"missing", "missing_data"},
		{"suspect", "insufficient_quality"},
		{"zero", "zero_baseline"},
		{"scope", "inconsistent_scope"},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			report := readTrendFixture(t, "day", "complete", tc.scenario)
			got := report.Previous
			if string(got.Status) != tc.status || got.ChangeRate != nil || report.PriorYear.Status != "comparable" {
				t.Fatalf("expected %q with no rate: %+v", tc.status, got)
			}
			switch tc.scenario {
			case "missing":
				if got.Period.Usage != nil || got.Period.Coverage.Missing != 72 || got.Delta != nil {
					t.Fatalf("missing values lost: %+v", got)
				}
			case "suspect":
				if got.Period.Coverage.Suspect != 3 || got.Delta != nil {
					t.Fatalf("suspect coverage lost: %+v", got)
				}
			case "zero":
				assertF06Usage(t, got.Period.Usage, 0)
				assertF06Usage(t, got.Delta, 1536)
			case "scope":
				if got.Period.CatalogVersion == report.Current.CatalogVersion || got.Delta != nil {
					t.Fatalf("changed catalog is not explained: %+v", got)
				}
			}
		})
	}
	monthly := readTrendFixture(t, "month", "complete", "standard")
	if monthly.Current.Hours != 744 || monthly.Previous.Period.Hours != 720 ||
		monthly.Previous.Status != "unequal_periods" || monthly.Previous.ChangeRate != nil ||
		monthly.PriorYear.Status != "insufficient_quality" || monthly.Current.Coverage.Missing != 72 || monthly.Current.Coverage.Suspect != 72 {
		t.Fatalf("full month duration or coverage mismatch: %+v", monthly)
	}
	assertF06Usage(t, monthly.Current.Usage, 26880)
}

func TestF08PreviewRejectsUnknownOrDuplicateSelectors(t *testing.T) {
	for _, suffix := range []string{"&from=%zz", "&point=foreign", "&energy=water", "&mode=to-date"} {
		t.Run(suffix, func(t *testing.T) {
			res := httptest.NewRecorder()
			url := trendPath + "?building=office-a&energy=electricity&granularity=day&mode=complete&scenario=standard" + suffix
			previewHandler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, url, nil))
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400", res.Code)
			}
		})
	}
	res := httptest.NewRecorder()
	url := trendPath + "?building=office-a&energy=water&granularity=hour&mode=to-date&scenario=standard"
	previewHandler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, url, nil))
	if res.Code != http.StatusBadRequest {
		t.Fatalf("partial hour accepted: %d", res.Code)
	}
}
