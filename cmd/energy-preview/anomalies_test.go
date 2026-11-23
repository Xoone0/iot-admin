package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

func TestF10F17AnomalyPreviewIsReadOnly(t *testing.T) {
	path := overviewPath + "/anomalies?building=office-a&energy=electricity&scenario=standard"
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
			if item.method == http.MethodGet && !json.Valid(w.Body.Bytes()) {
				t.Fatalf("expected a JSON preview envelope: %s", w.Body.String())
			}
		})
	}
}

func readAnomalyFixture(t *testing.T, building, energy, scenario string) anomalyPreview {
	t.Helper()
	w := httptest.NewRecorder()
	query := "?building=" + building + "&energy=" + energy + "&scenario=" + scenario
	previewHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, anomaliesPath+query, nil))
	var payload struct {
		Code int            `json:"code"`
		Data anomalyPreview `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil || w.Code != http.StatusOK || payload.Code != 0 {
		t.Fatalf("response %d (%v): %s", w.Code, err, w.Body.String())
	}
	return payload.Data
}

func TestF10F17BaselineAndOpportunitiesReuseExistingGoResults(t *testing.T) {
	got := readAnomalyFixture(t, "office-a", "electricity", "standard")
	if !got.Sample || got.Project != "f06-demo" || got.Timezone != "Asia/Shanghai" || got.Unit != "kWh" ||
		got.Rule == nil || got.Rule.Version != "sample-increase-office-a-electricity-v1" ||
		got.Rule.MinHistory != 3 || got.Rule.MinChangeRate != .6 || got.Rule.MinIncrease != 120 ||
		got.Current.From.Before(got.Rule.From) || got.Current.To.After(got.Rule.To) ||
		got.Finding.Status != "usage_candidate" || got.Finding.UsageStatus != "usage_candidate" {
		t.Fatalf("bad standard sample: %+v", got)
	}
	trend := readTrendFixture(t, "day", "complete", "standard")
	if !reflect.DeepEqual(got.Current.f08Period, trend.Current) || !got.AsOf.Equal(trend.AsOf) {
		t.Fatalf("F10 changed the F08 same-window usage or provenance: %+v vs %+v", got.Current.f08Period, trend.Current)
	}
	w := httptest.NewRecorder()
	previewHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet,
		classificationPath+"?building=office-a&energy=electricity&period=day", nil))
	var classification struct{ Data f06Report }
	if err := json.Unmarshal(w.Body.Bytes(), &classification); err != nil || w.Code != http.StatusOK ||
		!reflect.DeepEqual(classification.Data.Total, got.Current.f06Value) {
		t.Fatalf("F06 day usage did not reconcile: %s", w.Body)
	}
	if len(got.Current.Sources) != len(classification.Data.Sources) || len(got.Current.Sources) != 3 {
		t.Fatalf("F10 did not keep the F06 source set: %+v", got.Current.Sources)
	}
	for i, source := range got.Current.Sources {
		f06 := classification.Data.Sources[i]
		if source.Source != f06.Source || source.ProfileVersion != f06.ProfileVersion ||
			source.BindingVersion != f06.BindingVersion || source.CategoryBindingVersion != f06.CategoryBindingVersion ||
			!reflect.DeepEqual(source.Usage, f06.Usage) || source.Valid != f06.Valid {
			t.Fatalf("F10 did not keep the projected F06 source: %+v vs %+v", source, f06)
		}
	}
	if len(got.History) != 3 || got.Finding.BaselineCount != 3 {
		t.Fatalf("wrong baseline period count: %+v", got.Finding)
	}
	for i, want := range []float64{336, 768, 384} {
		history := got.History[i]
		assertF06Usage(t, history.Usage, want)
		if !history.From.Equal(got.Current.From.AddDate(0, 0, -7*(3-i))) ||
			history.From.Weekday() != time.Thursday || history.Hours != 24 ||
			history.Coverage.Valid != 72 || len(history.Sources) != 3 {
			t.Fatalf("incomparable history sample: %+v", history)
		}
	}
	assertF06Usage(t, got.Current.Usage, 1536)
	assertF06Usage(t, got.Finding.Baseline, 384)
	assertF06Usage(t, got.Finding.Delta, 1152)
	assertF06Usage(t, got.Finding.ChangeRate, 3)

	// F16 projects the authorized F07 Space ranking shown below.
	ranked := readDimensions(t, "space", "office-a", "day", "standard")
	if len(got.Opportunities) != 2 || len(ranked.Rows) != 2 {
		t.Fatalf("missing anomaly or high-use review clues: %+v", got.Opportunities)
	}
	for _, clue := range got.Opportunities {
		if clue.Status != "pending_manual_review" || clue.Evidence == "" ||
			len(clue.Limitations) == 0 || len(clue.Confirm) == 0 || len(clue.Sources) == 0 {
			t.Fatalf("review clue lost its evidence boundary: %+v", clue)
		}
		switch clue.Kind {
		case "baseline_increase":
			if clue.Object != "office-a" || !reflect.DeepEqual(clue.Sources, got.Current.Sources) {
				t.Fatalf("trend clue did not retain its scope: %+v", clue)
			}
			assertF06Usage(t, clue.Usage, 1536)
			assertF06Usage(t, clue.Baseline, 384)
		case "high_usage_observation":
			top := ranked.Rows[0]
			if clue.Object != top.ID || clue.Name != top.Name || clue.ComparablePeers != 2 || len(clue.Sources) != len(top.Sources) {
				t.Fatalf("high-use clue did not match F16: %+v vs %+v", clue, top)
			}
			if clue.Measurement == nil || clue.Measurement.RuleVersion != ranked.RuleVersion ||
				clue.Measurement.MeterRuleVersion != top.Sources[0].MeterRuleVersion || clue.Measurement.CatalogVersion != ranked.CatalogVersion {
				t.Fatalf("high-use clue lost the F16 evidence versions: %+v", clue.Measurement)
			}
			for i, source := range clue.Sources {
				if source.Source != top.Sources[i].Channel || source.BindingVersion != top.Sources[i].ObjectBindingVersion ||
					!reflect.DeepEqual(source.Usage, top.Sources[i].Usage) {
					t.Fatalf("high-use clue returned an unscoped source: %+v", source)
				}
			}
			assertF06Usage(t, clue.Usage, *top.Usage)
		default:
			t.Fatalf("unknown opportunity kind: %s", clue.Kind)
		}
	}
}

func TestF10F17QualityAndComparisonStatesKeepFindingsApart(t *testing.T) {
	for _, test := range []struct {
		scenario, status, reason string
		missing, suspect         float64
	}{
		{"normal", "no_candidate", "", 0, 0},
		{"missing", "metering_anomaly", "missing_usage", 72, 0},
		{"suspect", "metering_anomaly", "suspect_readings", 0, 3},
		{"partial", "metering_anomaly", "partial_coverage", 3, 0},
		{"zero", "no_candidate", "", 0, 0},
		{"incomparable", "insufficient_evidence", "incompatible_scope", 0, 0},
		{"baseline-zero", "insufficient_evidence", "zero_baseline", 0, 0},
		{"no-rule", "insufficient_evidence", "missing_rule", 0, 0},
	} {
		t.Run(test.scenario, func(t *testing.T) {
			got := readAnomalyFixture(t, "office-a", "electricity", test.scenario)
			finding := got.Finding
			wantUsage := test.status
			if test.status == "metering_anomaly" {
				wantUsage = "insufficient_evidence"
			}
			wantClues := 0
			if test.scenario == "no-rule" {
				wantClues = 1
			}
			if string(finding.Status) != test.status || string(finding.UsageStatus) != wantUsage || string(finding.Reason) != test.reason ||
				got.Current.Coverage.Expected != 72 || got.Current.Coverage.Missing != test.missing || got.Current.Coverage.Suspect != test.suspect ||
				len(got.Opportunities) != wantClues {
				t.Fatalf("wrong decision for %q: %+v, coverage %+v, clues %+v", test.scenario, finding, got.Current.Coverage, got.Opportunities)
			}
			if wantUsage == "insufficient_evidence" && (finding.Delta != nil || finding.ChangeRate != nil) {
				t.Fatalf("unsubstantiated usage comparison: %+v", finding)
			}
			switch test.scenario {
			case "missing":
				if got.Current.Usage != nil {
					t.Fatalf("missing sample was turned into a number: %+v", got.Current)
				}
			case "normal":
				assertF06Usage(t, got.Current.Usage, 384)
			case "zero":
				assertF06Usage(t, got.Current.Usage, 0)
				if got.Current.Coverage.Valid != 72 {
					t.Fatalf("complete zero was not distinguished from missing: %+v", got.Current)
				}
			case "no-rule":
				if got.Rule != nil || got.Opportunities[0].Kind != "high_usage_observation" {
					t.Fatal("no-rule sample must retain only the independent high-use review clue")
				}
			}
		})
	}
}

func TestF10F17KeepEnergyAndSourceScope(t *testing.T) {
	got := readAnomalyFixture(t, "office-b", "water", "standard")
	if got.Unit != "m³" || got.Energy != buildingenergy.Water || got.Rule == nil ||
		got.Rule.MinChangeRate != .4 || got.Rule.MinIncrease != 60 {
		t.Fatalf("the scope retained another energy's rules: %+v", got)
	}
	assertF06Usage(t, got.Current.Usage, 768)
	assertF06Usage(t, got.Finding.Baseline, 192)
	check := func(sources []anomalySource) {
		t.Helper()
		for _, source := range sources {
			if !strings.HasPrefix(source.Source, "office-b-water-") || source.ProfileVersion == "" ||
				source.BindingVersion == "" || source.MappingGeneration != 1 {
				t.Fatalf("unexpected source disclosure: %+v", source)
			}
		}
	}
	check(got.Current.Sources)
	for _, history := range got.History {
		check(history.Sources)
	}
	for _, clue := range got.Opportunities {
		check(clue.Sources)
	}
}

func TestF10F17AnomalyPreviewRejectsInvalidSelectors(t *testing.T) {
	for _, query := range []string{
		"building=all&energy=electricity&scenario=standard",
		"building=office-a&energy=gas&scenario=standard",
		"building=office-a&energy=electricity&scenario=confirmed",
		"building=office-a&energy=electricity",
		"building=office-a&energy=electricity&scenario=standard&scenario=normal",
		"building=office-a&energy=electricity&scenario=standard&threshold=0",
	} {
		t.Run(query, func(t *testing.T) {
			w := httptest.NewRecorder()
			previewHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, anomaliesPath+"?"+query, nil))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("invalid selector got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}
