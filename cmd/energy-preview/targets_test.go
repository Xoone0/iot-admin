package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestF05F14TargetPreviewIsReadOnly(t *testing.T) {
	path := overviewPath + "/targets?building=office-a&energy=electricity&year=2026"
	for _, item := range []struct {
		method string
		want   int
	}{{http.MethodGet, http.StatusOK}, {http.MethodPost, http.StatusMethodNotAllowed}} {
		t.Run(item.method, func(t *testing.T) {
			w := httptest.NewRecorder()
			previewHandler().ServeHTTP(w, httptest.NewRequest(item.method, path, nil))
			if w.Code != item.want {
				t.Fatalf("response %d, want %d: %s", w.Code, item.want, w.Body.String())
			}
		})
	}
}

func readTargetFixture(t *testing.T, building, energy, year string) targetPreview {
	t.Helper()
	w := httptest.NewRecorder()
	query := "?building=" + building + "&energy=" + energy + "&year=" + year
	previewHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, targetsPath+query, nil))
	var payload struct {
		Code int           `json:"code"`
		Data targetPreview `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil || w.Code != http.StatusOK || payload.Code != 0 {
		t.Fatalf("response %d (%v): %s", w.Code, err, w.Body.String())
	}
	return payload.Data
}

func TestF05F14HistoricalTargetUsesFullNaturalYear(t *testing.T) {
	got := readTargetFixture(t, "office-a", "electricity", "2025")
	if !got.Sample || got.Timezone != "Asia/Shanghai" || got.Unit != "kWh" || got.Project != "f06-demo" ||
		got.RuleVersion != "f05-f14-v1" || got.Target == nil || got.Target.Method != "linear" ||
		got.Target.Version != "sample-target-2025-electricity-v1" || got.Target.WeightsPPM != nil {
		t.Fatalf("invalid target scope or version: %+v, target %+v", got, got.Target)
	}
	if got.Annual.From.Format(time.RFC3339) != "2025-01-01T00:00:00+08:00" ||
		got.Annual.To.Format(time.RFC3339) != "2026-01-01T00:00:00+08:00" ||
		!got.TargetSelectedAt.Equal(got.Annual.To.Add(-time.Nanosecond)) ||
		!got.Target.From.Equal(got.Annual.From) || !got.Target.To.Equal(got.Annual.To) ||
		got.AsOf.Year() != 2026 || got.Annual.Status != "comparable" ||
		got.Annual.Coverage == nil || got.Annual.Coverage.Expected != 26_280 || got.Annual.Coverage.Valid != 26_280 {
		t.Fatalf("wrong closed-year boundary, quality or selection cutoff: %+v", got.Annual)
	}
	// 7 thirty-one-day months contribute 79 day weights each, the 4
	// thirty-day months 75 apiece, and February 70. Each weight is 240 kWh.
	assertF06Usage(t, got.Annual.Usage, 221_520)
	assertF06Usage(t, got.Annual.Target, 240_000)
	assertF06Usage(t, got.Annual.Ratio, .923)
	assertF06Usage(t, got.Annual.Delta, -18_480)
	actual := 0.0
	for i, month := range got.Months {
		assertF06Usage(t, month.Target, 20_000)
		if month.Index != i+1 || month.Status != "comparable" || month.Usage == nil ||
			i > 0 && !got.Months[i-1].To.Equal(month.From) {
			t.Fatalf("invalid month at %d: %+v", i, month)
		}
		actual += *month.Usage
	}
	if actual != *got.Annual.Usage {
		t.Fatalf("annual and monthly readings diverged: %g != %g", actual, *got.Annual.Usage)
	}
}

func TestF05F14SeasonalTargetsRespectCutoffAndQuality(t *testing.T) {
	got := readTargetFixture(t, "office-a", "electricity", "2026")
	if got.Target == nil || got.Target.Method != "seasonal" || len(got.Target.WeightsPPM) != 12 ||
		!got.TargetSelectedAt.Equal(got.AsOf) || got.Annual.Status != "incomplete_period" ||
		got.Annual.Usage != nil || got.Annual.Ratio != nil || got.Annual.Delta != nil || got.Annual.Coverage != nil {
		t.Fatalf("open-year ratio or invalid seasonal plan: %+v", got)
	}
	assertF06Usage(t, got.Annual.Target, 360_000)
	var total float64
	for _, month := range got.Months {
		if month.Target == nil {
			t.Fatalf("missing phased target: %+v", month)
		}
		total += *month.Target
	}
	if total != *got.Annual.Target {
		t.Fatalf("seasonal targets total %g, annual target %g", total, *got.Annual.Target)
	}

	sep, oct, nov := got.Months[8], got.Months[9], got.Months[10]
	assertF06Usage(t, sep.Target, 32_400)
	assertF06Usage(t, sep.Usage, 25_200)
	assertF06Usage(t, sep.Ratio, 25_200.0/32_400)
	assertF06Usage(t, oct.Target, 28_800)
	assertF06Usage(t, oct.Usage, 26_880)
	if sep.Status != "comparable" || oct.Status != "partial_coverage" ||
		oct.Ratio != nil || oct.Delta != nil || oct.Coverage == nil || oct.Coverage.Expected != 2_232 ||
		oct.Coverage.Valid != 2_088 || oct.Coverage.Missing != 72 || oct.Coverage.Suspect != 72 ||
		nov.Status != "incomplete_period" || nov.Usage != nil || nov.Ratio != nil || nov.Coverage != nil {
		t.Fatalf("incorrect comparison state: Sep %+v; Oct %+v; Nov %+v", sep, oct, nov)
	}
	trend := readTrendFixture(t, "month", "complete", "standard")
	for _, pair := range []struct {
		got  targetPeriodView
		want f08Period
	}{{oct, trend.Current}, {sep, trend.Previous.Period}} {
		if !pair.got.From.Equal(pair.want.From) || !pair.got.To.Equal(pair.want.To) ||
			!reflect.DeepEqual(pair.got.Usage, pair.want.Usage) || !reflect.DeepEqual(pair.got.Coverage, &pair.want.Coverage) {
			t.Fatalf("target measurement diverged from F08: %+v vs %+v", pair.got, pair.want)
		}
	}
	if got.Measurement == nil || len(got.Measurement.Sources) != 3 ||
		got.Measurement.MeterRuleVersion != trend.Current.MeterRuleVersion ||
		got.Measurement.ClassificationRuleVersion != trend.Current.ClassificationRuleVersion ||
		got.Measurement.CatalogVersion != trend.Current.CatalogVersion ||
		got.Measurement.TopologyVersion != trend.Current.TopologyVersion {
		t.Fatalf("missing measurement versions or source points: %+v", got.Measurement)
	}
}

func TestF05F14TargetFixtureRespectsBuildingAndEnergy(t *testing.T) {
	unconfigured := readTargetFixture(t, "office-b", "electricity", "2026")
	sep := unconfigured.Months[8]
	if unconfigured.Target != nil || unconfigured.Annual.Target != nil || unconfigured.Annual.Status != "unconfigured" ||
		sep.Status != "unconfigured" || sep.Target != nil || sep.Ratio != nil || sep.Delta != nil || sep.Coverage == nil {
		t.Fatalf("unconfigured scope synthesized a target or dropped observed quality: %+v; %+v", unconfigured.Annual, sep)
	}
	assertF06Usage(t, sep.Usage, 50_400)
	for _, source := range unconfigured.Measurement.Sources {
		if len(source.Source) < len("office-b-") || source.Source[:len("office-b-")] != "office-b-" {
			t.Fatalf("foreign meter in target scope: %+v", source)
		}
	}
	water := readTargetFixture(t, "office-a", "water", "2026")
	if water.Unit != "m³" || water.Target == nil || water.Target.Method != "linear" || water.Target.WeightsPPM != nil {
		t.Fatalf("incorrect unit or method: %+v", water)
	}
	assertF06Usage(t, water.Annual.Target, 90_000)
	assertF06Usage(t, water.Months[8].Target, 7_500)
	assertF06Usage(t, water.Months[8].Usage, 6_300)
	assertF06Usage(t, water.Months[8].Ratio, .84)
}

func TestF05F14TargetPreviewRejectsInvalidScope(t *testing.T) {
	for _, query := range []string{
		"building=office-c&energy=electricity&year=2026",
		"building=office-a&energy=gas&year=2026",
		"building=office-a&energy=electricity&year=2027",
		"building=office-a&energy=electricity",
		"building=office-a&energy=electricity&year=2025&year=2026",
		"building=office-a&energy=electricity&year=2026&project=f06-demo",
	} {
		t.Run(query, func(t *testing.T) {
			w := httptest.NewRecorder()
			previewHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, targetsPath+"?"+query, nil))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("invalid selector got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}
