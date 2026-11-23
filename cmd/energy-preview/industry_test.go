package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type industryTestMetric struct {
	ID, Name, Definition, Unit, Status string
	Value                              *float64
	Template                           struct {
		Version, Source, From, To, DenominatorKind, DenominatorUnit string
	}
	Denominator *metricDenominator
	Benchmark   struct {
		Status       string
		Limit, Delta *float64
		Standard     *struct {
			ID, Name, Publisher, Version, Source, Kind, Region, BuildingType, Unit, From, To, Condition string
		}
	}
}

type industryTestReport struct {
	businessUsageScope
	BuildingName, BuildingType, Region, Scenario, Quality, RuleVersion string
	Numerator                                                          f06Value
	Metrics                                                            []industryTestMetric
}

func industrySampleReport(t *testing.T, building, energy, scenario string) industryTestReport {
	t.Helper()
	var result industryTestReport
	getSamplePage(t, "/industry?building="+building+"&energy="+energy+"&scenario="+scenario, &result)
	return result
}

func TestF12F13OfficeDayReusesF11(t *testing.T) {
	for _, energy := range []string{"electricity", "water"} {
		t.Run(energy, func(t *testing.T) {
			got := industrySampleReport(t, "office-a", energy, "standard")
			var f11 efficiencyReport
			getSamplePage(t, "/efficiency?building=office-a&energy="+energy+"&period=day", &f11)
			if !got.Sample || got.BuildingType != "office" || got.RuleVersion == "" ||
				!reflect.DeepEqual(got.businessUsageScope, f11.businessUsageScope) ||
				!reflect.DeepEqual(got.Numerator, f11.Numerator) || got.Quality != string(f11.Quality) ||
				len(got.Metrics) != len(f11.Metrics) {
				t.Fatalf("F11 evidence or scope differs: %+v", got)
			}
			for i, metric := range got.Metrics {
				original := f11.Metrics[i]
				if metric.ID != original.ID || metric.Unit != original.Unit || metric.Status != original.Status ||
					!reflect.DeepEqual(metric.Value, original.Value) || !reflect.DeepEqual(metric.Denominator, original.Denominator) {
					t.Fatalf("office metric differs from F11: %+v vs %+v", metric, original)
				}
			}
		})
	}
}

func TestF12F13IndustryCohortsUseVersionedScopedEvidence(t *testing.T) {
	for _, tt := range []struct {
		building, kind, unit, benchmark   string
		numerator, denominator, intensity float64
	}{
		{"hospital-a", "hospital", "kWh/床日", "within_threshold", 3072, 128, 24},
		{"school-a", "school", "kWh/人", "exceeds_threshold", 768, 240, 3.2},
		{"hotel-a", "hotel", "kWh/间夜", "within_threshold", 1152, 72, 16},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			result := industrySampleReport(t, tt.building, "electricity", "standard")
			if !result.Sample || result.Project != "f12-industry-demo" || result.Building != tt.building ||
				result.BuildingType != tt.kind || !strings.Contains(result.BuildingName, "合成") ||
				result.Numerator.Coverage.Valid != 72 || len(result.Sources) != 3 || len(result.Metrics) == 0 {
				t.Fatalf("industry evidence outside selected synthetic scope: %+v", result)
			}
			for _, source := range result.Sources {
				if !strings.HasPrefix(source.Source, tt.building+"-electricity-") || source.BindingVersion == "" {
					t.Fatalf("unscoped or unversioned source: %+v", source)
				}
			}
			assertF06Usage(t, result.Numerator.Usage, tt.numerator)
			metric := result.Metrics[0]
			assertF06Usage(t, metric.Value, tt.intensity)
			if metric.Status != "ready" || metric.Unit != tt.unit || metric.Template.Version == "" ||
				metric.Template.Source == "" || metric.Template.From == "" || metric.Template.To == "" ||
				metric.Definition == "" || metric.Denominator == nil || metric.Denominator.Value != tt.denominator ||
				metric.Denominator.Source == "" || metric.Denominator.Version == "" {
				t.Fatalf("metric or denominator evidence missing: %+v", metric)
			}
			benchmark := metric.Benchmark
			if benchmark.Status != tt.benchmark || benchmark.Delta == nil || benchmark.Limit == nil || benchmark.Standard == nil ||
				benchmark.Standard.Kind != "demo" || !strings.Contains(benchmark.Standard.Name, "演示") ||
				benchmark.Standard.Publisher == "" || benchmark.Standard.Source == "" || benchmark.Standard.Region != result.Region ||
				benchmark.Standard.BuildingType != tt.kind || benchmark.Standard.Unit != tt.unit ||
				benchmark.Standard.From == "" || benchmark.Standard.To == "" || benchmark.Standard.Condition == "" {
				t.Fatalf("missing demonstration standard provenance: %+v", benchmark)
			}
			if tt.kind == "hospital" {
				revenue := result.Metrics[1]
				assertF06Usage(t, revenue.Value, 128)
				if revenue.Unit != "kWh/万元" || revenue.Denominator.Unit != "元" || revenue.Denominator.Value != 240_000 ||
					revenue.Benchmark.Status != "no_standard" || revenue.Benchmark.Standard != nil {
					t.Fatalf("revenue must retain raw unit and no standard: %+v", revenue)
				}
			}
		})
	}
}

func TestF12F13SuppressesIneligibleMetricsAndBenchmarks(t *testing.T) {
	for _, tt := range []struct {
		scenario, metric, benchmark string
	}{
		{"missing-denominator", "missing_denominator", "insufficient_data"},
		{"invalid-denominator", "invalid_denominator", "insufficient_data"},
		{"out-of-period", "denominator_out_of_period", "insufficient_data"},
		{"partial", "insufficient_quality", "insufficient_data"},
		{"zero", "ready", "within_threshold"},
		{"no-standard", "ready", "no_standard"},
		{"not-applicable", "ready", "inapplicable"},
		{"incomparable", "ready", "not_comparable"},
	} {
		t.Run(tt.scenario, func(t *testing.T) {
			got := industrySampleReport(t, "hotel-a", "electricity", tt.scenario)
			metric := got.Metrics[0]
			if got.Scenario != tt.scenario || metric.Status != tt.metric || metric.Benchmark.Status != tt.benchmark ||
				(metric.Value != nil) != (tt.metric == "ready") ||
				(metric.Benchmark.Delta != nil) != (tt.scenario == "zero") {
				t.Fatalf("unsafe metrics or comparison: %+v", metric)
			}
			if tt.scenario == "zero" {
				assertF06Usage(t, metric.Value, 0)
				if got.Quality != "valid_zero" || got.Numerator.Coverage.Valid != got.Numerator.Coverage.Expected {
					t.Fatalf("zero must come from valid zero readings: %+v", got)
				}
			}
			if tt.scenario == "no-standard" && (metric.Benchmark.Standard != nil || metric.Benchmark.Limit != nil) {
				t.Fatal("no-standard manufactured a standard")
			}
			if tt.scenario == "partial" && (got.Quality != "partial" || got.Numerator.Coverage.Missing == 0) {
				t.Fatal("partial must come from missing metering intervals")
			}
		})
	}
}

func TestF12F13PreviewOnlyAcceptsExactReadOnlySampleScope(t *testing.T) {
	root := overviewPath + "/industry"
	valid := "?building=hospital-a&energy=electricity&scenario=standard"
	for _, tt := range []struct {
		name, method, query string
		want                int
	}{
		{"read", http.MethodGet, valid, http.StatusOK},
		{"write", http.MethodPost, valid, http.StatusMethodNotAllowed},
		{"unknown building", http.MethodGet, strings.Replace(valid, "hospital-a", "production-building", 1), http.StatusBadRequest},
		{"unknown scenario", http.MethodGet, strings.Replace(valid, "standard", "custom", 1), http.StatusBadRequest},
		{"wrong energy", http.MethodGet, strings.Replace(valid, "electricity", "gas", 1), http.StatusBadRequest},
		{"repeated key", http.MethodGet, valid + "&building=school-a", http.StatusBadRequest},
		{"unsupported period", http.MethodGet, valid + "&period=30", http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			previewHandler().ServeHTTP(w, httptest.NewRequest(tt.method, root+tt.query, nil))
			if w.Code != tt.want {
				t.Fatalf("response %d, want %d: %s", w.Code, tt.want, w.Body)
			}
		})
	}
}
