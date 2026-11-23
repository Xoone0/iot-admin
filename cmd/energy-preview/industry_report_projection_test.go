package main

import (
	"context"
	"encoding/csv"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

var industryReportTestTime = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

func measuredIndustryReport(t *testing.T, building, scenario string) industryReport {
	t.Helper()
	preset := industryBuildings[building]
	fixture, err := newIndustryFixture(building, preset, buildingenergy.Electricity)
	if err != nil {
		t.Fatal(err)
	}
	report, err := calculateIndustrySample(context.Background(), fixture, preset, scenario)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func assertIndustryReportCSV(t *testing.T, report industryReportSnapshot) {
	t.Helper()
	if !strings.HasPrefix(report.CSV, "\uFEFF") || !strings.HasSuffix(report.CSV, "\r\n") || !strings.Contains(report.CSV, "演示规则，非权威标准") {
		t.Fatalf("invalid report encoding or missing notice: %q", report.CSV)
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(report.CSV, "\uFEFF"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	headers := make([]string, len(report.Columns))
	for i, column := range report.Columns {
		headers[i] = column.Label
	}
	if !reflect.DeepEqual(rows, append([][]string{headers}, report.Rows...)) {
		t.Fatalf("CSV differs from projected snapshot: records = %#v, rows = %#v", rows, report.Rows)
	}
}

func industryReportCell(t *testing.T, report industryReportSnapshot, row int, id string) string {
	t.Helper()
	for i, column := range report.Columns {
		if string(column.ID) == id {
			return report.Rows[row][i]
		}
	}
	t.Fatalf("column %s is missing", id)
	return ""
}

func TestF22ProjectsMeasuredInputsAndReportsBenchmarksSeparately(t *testing.T) {
	report := measuredIndustryReport(t, "hospital-a", "standard")
	snapshot, err := projectIndustryReport(report, industryReportOptions{Kind: "f22"}, industryReportTestTime, "synthetic-operator")
	if err != nil {
		t.Fatal(err)
	}
	assertIndustryReportCSV(t, snapshot)
	for row, metric := range report.Metrics {
		for id, want := range map[string]string{
			"metric": metric.Name, "numerator": reportOptionalNumber(report.Numerator.Usage),
			"denominator": reportNumber(metric.Denominator.Value), "value": reportOptionalNumber(metric.Value),
			"unit": metric.Unit, "notice": "演示规则，非权威标准",
		} {
			if got := industryReportCell(t, snapshot, row, id); got != want {
				t.Errorf("row %d %s = %q, want %q", row, id, got, want)
			}
		}
	}
	for id, wantParts := range map[string][]string{
		"scope":            {report.BuildingName, report.Building, report.Project, "电"},
		"period":           {report.From.Format(time.RFC3339), report.To.Format(time.RFC3339), report.Timezone},
		"benchmarkRule":    {report.Metrics[0].Benchmark.Standard.Source, report.Metrics[0].Benchmark.Standard.Version, report.Metrics[0].Benchmark.Standard.Condition},
		"internalTarget":   {"F14"},
		"externalStandard": {"未接入权威标准"},
	} {
		for _, wanted := range wantParts {
			if got := industryReportCell(t, snapshot, 0, id); !strings.Contains(got, wanted) {
				t.Errorf("%s = %q, did not include %q", id, got, wanted)
			}
		}
	}
	if snapshot.Project != report.Project || snapshot.RuleVersion == "" || snapshot.MetricRuleVersion != report.RuleVersion ||
		snapshot.ReportID == "" || snapshot.Operator != "synthetic-operator" || !snapshot.GeneratedAt.Equal(industryReportTestTime) ||
		!reflect.DeepEqual(snapshot.Sources, report.Sources) {
		t.Fatalf("snapshot dropped scope, versions or evidence: %+v", snapshot)
	}
}

func TestF22KeepsReasonsAndEffectiveZeros(t *testing.T) {
	for _, test := range []struct {
		scenario, reason, quality           string
		metricAvailable, benchmarkAvailable bool
	}{
		{scenario: "missing-denominator", reason: "缺少有效分母", quality: "正常"},
		{scenario: "invalid-denominator", reason: "分母数值", quality: "正常"},
		{scenario: "out-of-period", reason: "分母记录与当前统计期不匹配", quality: "正常"},
		{scenario: "partial", reason: "覆盖率或数据质量不足", quality: "部分覆盖"},
		{scenario: "no-standard", reason: "无演示规则", quality: "正常", metricAvailable: true},
		{scenario: "not-applicable", reason: "不适用", quality: "正常", metricAvailable: true},
		{scenario: "incomparable", reason: "不可比较", quality: "正常", metricAvailable: true},
		{scenario: "zero", reason: "未超过演示阈值", quality: "有效零", metricAvailable: true, benchmarkAvailable: true},
	} {
		t.Run(test.scenario, func(t *testing.T) {
			report := measuredIndustryReport(t, "hospital-a", test.scenario)
			snapshot, err := projectIndustryReport(report, industryReportOptions{Kind: "f22"}, industryReportTestTime, "synthetic-operator")
			if err != nil {
				t.Fatal(err)
			}
			assertIndustryReportCSV(t, snapshot)
			status := industryReportCell(t, snapshot, 0, "status")
			quality := industryReportCell(t, snapshot, 0, "quality")
			value := industryReportCell(t, snapshot, 0, "value")
			delta := industryReportCell(t, snapshot, 0, "benchmarkDelta")
			if !strings.Contains(status, test.reason) || !strings.Contains(quality, test.quality) ||
				(value != "") != test.metricAvailable || (delta != "") != test.benchmarkAvailable ||
				strings.Contains(status, "达标") {
				t.Fatalf("status = %q, quality = %q, metric=%q, benchmark delta=%q", status, quality, value, delta)
			}
			if test.scenario == "zero" && (value != "0" || delta != "-25") {
				t.Fatalf("effective zero or signed numeric delta was altered: value=%q delta=%q", value, delta)
			}
			if test.scenario == "missing-denominator" && industryReportCell(t, snapshot, 0, "denominator") != "" {
				t.Fatal("missing denominator must remain blank")
			}
		})
	}
}

func TestF25ProjectsOnlySelectedFieldsWithMandatoryEvidence(t *testing.T) {
	report := measuredIndustryReport(t, "hospital-a", "missing-denominator")
	snapshot, err := projectIndustryReport(report, industryReportOptions{
		Kind: "f25", Fields: []string{"denominator", "value"}, Sort: "value", Direction: "desc",
	}, industryReportTestTime, "synthetic-operator")
	if err != nil {
		t.Fatal(err)
	}
	assertIndustryReportCSV(t, snapshot)
	var ids []string
	for _, column := range snapshot.Columns {
		ids = append(ids, string(column.ID))
	}
	if !reflect.DeepEqual(ids, []string{"metric", "denominator", "value", "scope", "period", "unit", "quality", "status", "versions", "notice"}) ||
		snapshot.Rows[0][0] != "万元业务收入用量" || snapshot.Rows[1][0] != "床日用量" ||
		!strings.Contains(industryReportCell(t, snapshot, 1, "status"), "缺少有效分母") ||
		!strings.Contains(industryReportCell(t, snapshot, 1, "quality"), "缺失率") ||
		!strings.Contains(industryReportCell(t, snapshot, 1, "versions"), snapshot.ReportID) {
		t.Fatalf("projected fields or null-last sorting wrong: columns=%v, rows=%v", ids, snapshot.Rows)
	}
	if report.Metrics[0].ID != "bed-days" {
		t.Fatal("projection modified its input")
	}
}

func TestF25SortsNumericallyWithinUnitWithMissingValuesLast(t *testing.T) {
	for _, test := range []struct {
		direction string
		want      []string
	}{
		{direction: "asc", want: []string{"revenue", "zero", "low", "high", "missing"}},
		{direction: "desc", want: []string{"revenue", "high", "low", "zero", "missing"}},
	} {
		t.Run(test.direction, func(t *testing.T) {
			report := measuredIndustryReport(t, "hospital-a", "standard")
			zero, low, high := 0., 2., 11.
			report.Metrics = []industryMetricView{
				{ID: "high", Name: "high", Unit: "kWh/床日", Value: &high},
				{ID: "missing", Name: "missing", Unit: "kWh/万元"},
				{ID: "low", Name: "low", Unit: "kWh/床日", Value: &low},
				{ID: "revenue", Name: "revenue", Unit: "kWh/万元", Value: &high},
				{ID: "zero", Name: "zero", Unit: "kWh/床日", Value: &zero},
			}
			snapshot, err := projectIndustryReport(report, industryReportOptions{Kind: "f25", Sort: "value", Direction: test.direction}, industryReportTestTime, "synthetic-operator")
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for i := range snapshot.Rows {
				got = append(got, industryReportCell(t, snapshot, i, "metric"))
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("sort got %v, want %v", got, test.want)
			}
			assertIndustryReportCSV(t, snapshot)
		})
	}
}

func TestF25NoApplicableTemplateAndClosedConfiguration(t *testing.T) {
	report := measuredIndustryReport(t, "office-a", "standard")
	report.Metrics = nil
	snapshot, err := projectIndustryReport(report, industryReportOptions{Kind: "f25", Fields: []string{}}, industryReportTestTime, "synthetic-operator")
	if err != nil {
		t.Fatal(err)
	}
	assertIndustryReportCSV(t, snapshot)
	if snapshot.Status != "no_applicable_template" || snapshot.StatusMessage != "无适用模板" || snapshot.Rows == nil || len(snapshot.Rows) != 0 {
		t.Fatalf("unexpected no-template result: %+v", snapshot)
	}
	if len(snapshot.Columns) != 8 {
		t.Fatalf("essential fields differ: %v", snapshot.Columns)
	}
	for _, options := range []industryReportOptions{
		{Kind: "unknown"}, {Kind: "f22", Fields: []string{"value"}},
		{Kind: "f22", Sort: "metric", Direction: "asc"}, {Kind: "f25", Fields: []string{"raw_meter_reading"}},
		{Kind: "f25", Fields: []string{"value", "value"}}, {Kind: "f25", Fields: []string{"notice"}}, {Kind: "f22", Fields: []string{}},
		{Kind: "f25", Sort: "value + coverage", Direction: "asc"}, {Kind: "f25", Sort: "coverage", Direction: "descending"},
	} {
		if err := checkIndustryReportOptions(options); !errors.Is(err, buildingenergy.ErrInvalidScope) {
			t.Errorf("unauthorized configuration %+v returned %v", options, err)
		}
	}
}

func TestIndustryReportTreatsTextAsTextInCSV(t *testing.T) {
	report := measuredIndustryReport(t, "office-a", "zero")
	report.Metrics[0].Name = "  =1+1"
	report.Metrics[0].Definition = "\u00a0＠示例"
	snapshot, err := projectIndustryReport(report, industryReportOptions{Kind: "f22"}, industryReportTestTime, "synthetic-operator")
	if err != nil {
		t.Fatal(err)
	}
	assertIndustryReportCSV(t, snapshot)
	for id, want := range map[string]string{"metric": "'  =1+1", "definition": "'\u00a0＠示例", "value": "0"} {
		if got := industryReportCell(t, snapshot, 0, id); got != want {
			t.Errorf("%s = %q, want %q", id, got, want)
		}
	}
}

func TestIndustryReportRejectsNonDemoRulesAndInvalidScope(t *testing.T) {
	for _, kind := range []string{"external", "official", ""} {
		t.Run("rule_kind/"+kind, func(t *testing.T) {
			report := measuredIndustryReport(t, "hospital-a", "standard")
			report.Metrics[0].Benchmark.Standard.Kind = kind
			snapshot, err := projectIndustryReport(report, industryReportOptions{Kind: "f22"}, industryReportTestTime, "synthetic-operator")
			if !errors.Is(err, buildingenergy.ErrInvalidScope) || snapshot.CSV != "" || len(snapshot.Rows) != 0 {
				t.Fatalf("non-demo rule kind %q was accepted: err=%v, rows=%d", kind, err, len(snapshot.Rows))
			}
		})
	}

	for _, tc := range []struct {
		name   string
		mutate func(*industryReport)
	}{
		{"non-sample", func(r *industryReport) { r.Sample = false }},
		{"empty-project", func(r *industryReport) { r.Project = "" }},
		{"empty-building", func(r *industryReport) { r.Building = "" }},
		{"empty-period", func(r *industryReport) { r.To = r.From }},
		{"invalid-timezone", func(r *industryReport) { r.Timezone = "Invalid/Zone" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := measuredIndustryReport(t, "hospital-a", "standard")
			tc.mutate(&report)
			snapshot, err := projectIndustryReport(report, industryReportOptions{Kind: "f22"}, industryReportTestTime, "synthetic-operator")
			if !errors.Is(err, buildingenergy.ErrInvalidScope) || snapshot.CSV != "" || len(snapshot.Rows) != 0 {
				t.Fatalf("invalid report scope %s was accepted: err=%v, rows=%d", tc.name, err, len(snapshot.Rows))
			}
		})
	}
}

func TestIndustryReportHandlesInvalidRuleLowCoverageMissingUsageAndEmptyF22(t *testing.T) {
	t.Run("invalid-benchmark-rule", func(t *testing.T) {
		loc, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			t.Fatal(err)
		}
		from := time.Date(2026, time.October, 15, 0, 0, 0, 0, loc)
		window := buildingenergy.Window{
			Range:       buildingenergy.Range{From: from, To: from.AddDate(0, 0, 1)},
			Timezone:    loc.String(),
			Granularity: buildingenergy.Day,
			AsOf:        from.AddDate(0, 0, 1),
		}
		project := buildingenergy.ProjectKey{TenantID: "preview", ProjectID: "f12-industry-demo"}
		object := buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: "hospital-a"}
		meta := industryMeta("v1", "test-source", buildingenergy.Range{From: from.AddDate(0, 0, -5), To: from.AddDate(0, 0, 5)})
		usage := 3072.0
		classified := buildingenergy.ClassifiedUsageReport{
			Query:          buildingenergy.ClassifiedUsageQuery{Project: project, Object: object, Window: window},
			RuleVersion:    "meter-classification-v1",
			CatalogVersion: meta,
			TopologyVersion: meta,
			Energy: []buildingenergy.EnergyCategorySummary{{
				Energy: buildingenergy.Electricity,
				Unit:   "kWh",
				Total:  buildingenergy.CategoryValue{Usage: &usage, Coverage: buildingenergy.WeightedCoverage{Expected: 72, Valid: 72}},
			}},
		}
		scope := buildingenergy.IndustryContext{Project: project, Object: object, BuildingType: buildingenergy.BuildingHospital, Region: industryDemoRegion}
		template := buildingenergy.IndustryTemplate{
			ID: "bed-days", Name: "床日用量", Meta: meta,
			BuildingType: buildingenergy.BuildingHospital, ObjectKind: buildingenergy.Building,
			Energy: buildingenergy.Electricity, Denominator: buildingenergy.DenominatorBedDays,
		}
		denom := buildingenergy.BusinessDenominator{
			Project: project, Object: object, Kind: buildingenergy.DenominatorBedDays,
			Value: 128, Unit: "床日", Period: window.Range, Meta: meta,
		}
		metric := buildingenergy.EvaluateIndustryMetric(scope, template, classified, &denom)

		invalidRules := []struct {
			name string
			rule *buildingenergy.BenchmarkRule
		}{
			{"negative-maximum", &buildingenergy.BenchmarkRule{
				ID: "bad-limit", Name: "无效上限", Publisher: "test", Region: industryDemoRegion,
				MetricID: "bed-days", TemplateVersion: "v1", Unit: "kWh/床日", Kind: buildingenergy.BenchmarkDemo,
				BuildingType: buildingenergy.BuildingHospital, ObjectKind: buildingenergy.Building,
				Period: buildingenergy.Day, Meta: meta, Maximum: -1,
			}},
			{"missing-publisher-version", &buildingenergy.BenchmarkRule{
				ID: "bad-meta", Name: "缺版本", Publisher: "", Region: industryDemoRegion,
				MetricID: "bed-days", TemplateVersion: "", Unit: "kWh/床日", Kind: buildingenergy.BenchmarkDemo,
				BuildingType: buildingenergy.BuildingHospital, ObjectKind: buildingenergy.Building,
				Period: buildingenergy.Day, Meta: buildingenergy.VersionMeta{}, Maximum: 25,
			}},
		}
		for _, tc := range invalidRules {
			t.Run(tc.name, func(t *testing.T) {
				evaluated := buildingenergy.EvaluateBenchmark(metric, tc.rule)
				report := measuredIndustryReport(t, "hospital-a", "standard")
				report.Metrics[0].Benchmark = industryBenchmarkProjection(evaluated)
				for _, opts := range []industryReportOptions{
					{Kind: "f22"},
					{Kind: "f25", Fields: []string{"value", "comparison", "benchmarkRule", "benchmarkThreshold", "benchmarkDelta"}},
				} {
					snapshot, err := projectIndustryReport(report, opts, industryReportTestTime, "synthetic-operator")
					if err != nil {
						t.Fatal(err)
					}
					assertIndustryReportCSV(t, snapshot)
					row := 0
					for idx := range snapshot.Rows {
						if industryReportCell(t, snapshot, idx, "metric") == "床日用量" {
							row = idx
							break
						}
					}
					if industryReportCell(t, snapshot, row, "benchmarkThreshold") != "" ||
						industryReportCell(t, snapshot, row, "benchmarkDelta") != "" ||
						industryReportCell(t, snapshot, row, "benchmarkRule") != "" {
						t.Fatalf("invalid rule exposed threshold/delta/rule: %+v", snapshot.Rows[row])
					}
					status := industryReportCell(t, snapshot, row, "status")
					if !strings.Contains(status, "不可比较") || strings.Contains(status, "达标") || strings.Contains(status, "合规") {
						t.Fatalf("unexpected status for invalid rule: %q", status)
					}
				}
			})
		}
	})

	t.Run("at04-low-coverage-70pct", func(t *testing.T) {
		report := measuredIndustryReport(t, "hospital-a", "partial")
		rate := 0.70
		report.Numerator.Coverage = f06Coverage{Expected: 72, Valid: 50.4, Missing: 21.6, Suspect: 0, Rate: &rate}
		report.Quality = usagePartial
		for _, opts := range []industryReportOptions{
			{Kind: "f22"},
			{Kind: "f25", Fields: []string{"value", "coverage", "benchmarkThreshold", "benchmarkDelta"}},
		} {
			snapshot, err := projectIndustryReport(report, opts, industryReportTestTime, "synthetic-operator")
			if err != nil {
				t.Fatal(err)
			}
			assertIndustryReportCSV(t, snapshot)
			if industryReportCell(t, snapshot, 0, "value") != "" ||
				industryReportCell(t, snapshot, 0, "benchmarkThreshold") != "" ||
				industryReportCell(t, snapshot, 0, "benchmarkDelta") != "" {
				t.Fatalf("70%% low coverage must withhold metric and delta: %+v", snapshot.Rows[0])
			}
			if got := industryReportCell(t, snapshot, 0, "coverage"); got != "70.0%" {
				t.Fatalf("coverage = %q, want 70.0%%", got)
			}
			quality := industryReportCell(t, snapshot, 0, "quality")
			status := industryReportCell(t, snapshot, 0, "status")
			if !strings.Contains(quality, "部分覆盖") || !strings.Contains(quality, "缺失率 30.0%") ||
				!strings.Contains(status, "覆盖率或数据质量不足") || strings.Contains(status, "达标") || strings.Contains(status, "合规") {
				t.Fatalf("AT-04 low coverage quality/status wrong: quality=%q status=%q", quality, status)
			}
		}
	})

	t.Run("missing-usage-100pct", func(t *testing.T) {
		report := measuredIndustryReport(t, "hospital-a", "standard")
		zeroRate := 0.0
		report.Numerator = f06Value{Usage: nil, Coverage: f06Coverage{Expected: 72, Valid: 0, Missing: 72, Suspect: 0, Rate: &zeroRate}}
		report.Quality = usageMissing
		for i := range report.Metrics {
			report.Metrics[i].Status = string(buildingenergy.IndustryMissingUsage)
			report.Metrics[i].Value = nil
			report.Metrics[i].Benchmark.Status = string(buildingenergy.BenchmarkInsufficientData)
			report.Metrics[i].Benchmark.Limit = nil
			report.Metrics[i].Benchmark.Delta = nil
		}
		for _, opts := range []industryReportOptions{
			{Kind: "f22"},
			{Kind: "f25", Fields: []string{"value", "numerator", "coverage"}},
		} {
			snapshot, err := projectIndustryReport(report, opts, industryReportTestTime, "synthetic-operator")
			if err != nil {
				t.Fatal(err)
			}
			assertIndustryReportCSV(t, snapshot)
			if industryReportCell(t, snapshot, 0, "numerator") != "" || industryReportCell(t, snapshot, 0, "value") != "" {
				t.Fatalf("100%% missing usage must remain blank (not 0): %+v", snapshot.Rows[0])
			}
			quality := industryReportCell(t, snapshot, 0, "quality")
			status := industryReportCell(t, snapshot, 0, "status")
			if !strings.Contains(quality, "缺测") || !strings.Contains(quality, "缺失率 100.0%") ||
				!strings.Contains(status, "缺少有效用量") {
				t.Fatalf("missing usage quality/status wrong: quality=%q status=%q", quality, status)
			}
		}
	})

	t.Run("empty-f22-metrics", func(t *testing.T) {
		report := measuredIndustryReport(t, "office-a", "standard")
		report.Metrics = nil
		snapshot, err := projectIndustryReport(report, industryReportOptions{Kind: "f22"}, industryReportTestTime, "synthetic-operator")
		if err != nil {
			t.Fatal(err)
		}
		assertIndustryReportCSV(t, snapshot)
		if snapshot.Status != "no_applicable_template" || snapshot.StatusMessage != "无适用模板" || snapshot.Rows == nil || len(snapshot.Rows) != 0 {
			t.Fatalf("empty F22 snapshot wrong: %+v", snapshot)
		}
	})
}
