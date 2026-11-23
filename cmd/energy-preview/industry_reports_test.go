package main

import (
	"context"
	"encoding/csv"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

func TestIndustryReportEndpointPreviewsAndDownloadsTheSameSnapshot(t *testing.T) {
	const notice = "演示规则，非权威标准"
	for _, tt := range []struct {
		name, suffix, building, project string
	}{
		{"F22 benchmark", "kind=f22&building=office-a&energy=electricity&scenario=standard&project=f06-demo", "office-a", "f06-demo"},
		{"F25 restricted columns", "kind=f25&building=hospital-a&energy=electricity&scenario=standard&template=hospital&fields=value,denominator,coverage&sort=value&direction=desc", "hospital-a", "f12-industry-demo"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got struct {
				businessUsageScope
				Kind, Notice, CSV, Filename, ReportID, Operator, RuleVersion string
				Columns                                                      []struct{ ID, Label string }
				Rows                                                         [][]string
			}
			getSamplePage(t, "/industry-reports?"+tt.suffix, &got)
			if !got.Sample || got.Building != tt.building || got.Project != tt.project || got.Unit != "kWh" ||
				got.Timezone != "Asia/Shanghai" || got.ReportID == "" || got.Operator != "local-viewer" ||
				got.RuleVersion == "" || len(got.Sources) != 3 || len(got.Rows) != 2 ||
				got.Notice != notice || !strings.Contains(got.CSV, notice) || !strings.HasSuffix(got.Filename, ".csv") ||
				!strings.HasPrefix(got.CSV, "\uFEFF") || !strings.Contains(got.CSV, "\r\n") {
				t.Fatalf("report scope, source evidence, or disclaimer missing: %+v", got)
			}
			for _, source := range got.Sources {
				if !strings.HasPrefix(source.Source, tt.building+"-electricity-") {
					t.Fatalf("unexpected point in report: %s", source.Source)
				}
			}
			records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(got.CSV, "\uFEFF"))).ReadAll()
			if err != nil || len(records) != len(got.Rows)+1 {
				t.Fatalf("expected a CSV header and %d rows, got %d: %v", len(got.Rows), len(records), err)
			}
			if len(records[0]) != len(got.Columns) {
				t.Fatalf("CSV has %d columns, preview has %d", len(records[0]), len(got.Columns))
			}
			for i, column := range got.Columns {
				if records[0][i] != column.Label {
					t.Fatalf("CSV column %d: got %q, want %q", i, records[0][i], column.Label)
				}
			}
			if !reflect.DeepEqual(records[1:], got.Rows) {
				t.Fatalf("CSV differs from the preview: %v vs %v", records[1:], got.Rows)
			}
		})
	}
}

func TestIndustryReportEndpointRejectsOutOfScopeAndUnapprovedSelectors(t *testing.T) {
	const base = "/industry-reports?kind=f25&building=hospital-a&energy=electricity&scenario=standard"
	for _, tt := range []struct {
		name, path string
		want       int
	}{
		{"other synthetic project", base + "&project=f06-demo", http.StatusForbidden},
		{"unknown project", base + "&project=another-project", http.StatusForbidden},
		{"empty project", base + "&project=", http.StatusBadRequest},
		{"duplicate project", base + "&project=f12-industry-demo&project=another-project", http.StatusBadRequest},
		{"another object", base + "&object=office-b", http.StatusBadRequest},
		{"another point", base + "&point=office-b-electricity-primary", http.StatusBadRequest},
		{"unknown field", base + "&fields=value,rawTenant", http.StatusBadRequest},
		{"duplicate field", base + "&fields=value,value", http.StatusBadRequest},
		{"unauthorized sort", base + "&sort=rawTenant", http.StatusBadRequest},
		{"unauthorized terminal_id field", base + "&fields=terminal_id", http.StatusBadRequest},
		{"unauthorized meter_id field", base + "&fields=meter_id", http.StatusBadRequest},
		{"unauthorized raw_reading field", base + "&fields=raw_reading", http.StatusBadRequest},
		{"unauthorized operator field", base + "&fields=operator", http.StatusBadRequest},
		{"unauthorized sources field", base + "&fields=sources", http.StatusBadRequest},
		{"unauthorized terminal_id sort", base + "&sort=terminal_id", http.StatusBadRequest},
		{"unauthorized operator sort", base + "&sort=operator", http.StatusBadRequest},
		{"arbitrary field expression", base + "&fields=metric%2B1", http.StatusBadRequest},
		{"arbitrary sort direction", base + "&sort=value&direction=reverse", http.StatusBadRequest},
		{"duplicate sort query", base + "&sort=value&sort=coverage", http.StatusBadRequest},
		{"unknown template", base + "&template=outside", http.StatusBadRequest},
		{"invalid encoding", base + "&fields=%zz", http.StatusBadRequest},
		{"missing kind", "/industry-reports?building=hospital-a&energy=electricity&scenario=standard", http.StatusBadRequest},
		{"unknown building", "/industry-reports?kind=f25&building=unknown-bldg&energy=electricity&scenario=standard", http.StatusBadRequest},
		{"unsupported energy", "/industry-reports?kind=f25&building=hospital-a&energy=gas&scenario=standard", http.StatusBadRequest},
		{"unknown scenario", "/industry-reports?kind=f25&building=hospital-a&energy=electricity&scenario=unknown-scenario", http.StatusBadRequest},
		{"F22 customization", "/industry-reports?kind=f22&building=hospital-a&energy=electricity&scenario=standard&fields=value", http.StatusBadRequest},
		{"F22 sort rejected", "/industry-reports?kind=f22&building=hospital-a&energy=electricity&scenario=standard&sort=value", http.StatusBadRequest},
		{"F22 direction rejected", "/industry-reports?kind=f22&building=hospital-a&energy=electricity&scenario=standard&direction=desc", http.StatusBadRequest},
		{"F22 template rejected", "/industry-reports?kind=f22&building=hospital-a&energy=electricity&scenario=standard&template=hospital", http.StatusBadRequest},
		{"F22 no-template", "/industry-reports?kind=f22&building=hospital-a&energy=electricity&scenario=no-template", http.StatusBadRequest},
		{"stress outside hospital", "/industry-reports?kind=f25&building=school-a&energy=electricity&scenario=display-stress", http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, overviewPath+tt.path, nil)
			request.Header.Set("X-Project-ID", "f06-demo")
			request.Header.Set("X-User-ID", "admin")
			response := httptest.NewRecorder()
			previewHandler().ServeHTTP(response, request)
			body := response.Body.String()
			if response.Code != tt.want || strings.Contains(body, `"csv"`) || strings.Contains(body, `"sources"`) || strings.Contains(body, `"rows"`) {
				t.Fatalf("unexpected report response %d: %s", response.Code, body)
			}
		})
	}
}

func TestIndustryReportEndpointOffersReadOnlyUncachedSnapshots(t *testing.T) {
	query := "/industry-reports?kind=f25&building=hospital-a&energy=electricity&scenario=standard"
	for _, tt := range []struct {
		method string
		status int
	}{
		{http.MethodGet, http.StatusOK},
		{http.MethodPost, http.StatusMethodNotAllowed},
		{http.MethodPut, http.StatusMethodNotAllowed},
		{http.MethodDelete, http.StatusMethodNotAllowed},
	} {
		t.Run(tt.method, func(t *testing.T) {
			response := httptest.NewRecorder()
			previewHandler().ServeHTTP(response, httptest.NewRequest(tt.method, overviewPath+query, nil))
			if response.Code != tt.status || (tt.method == http.MethodGet && response.Header().Get("Cache-Control") != "no-store") {
				t.Fatalf("unexpected report response %d with headers %v", response.Code, response.Header())
			}
		})
	}
}

func TestIndustryReportAuthorizesExportScopeBeforeReading(t *testing.T) {
	const building = "hospital-a"
	preset := industryBuildings[building]
	fixture, err := newIndustryFixture(building, preset, buildingenergy.Electricity)
	if err != nil {
		t.Fatal(err)
	}
	request := industryReportRequest{options: industryReportOptions{Kind: "f22"}, scenario: "standard"}
	expectedScope := fixture.scope()
	ctx := context.Background()
	at := time.Date(2026, time.November, 18, 12, 0, 0, 0, time.UTC)
	var calls []string
	read := func(ctx context.Context, actual f06Fixture, preset industryBuilding, scenario string) (industryReport, error) {
		calls = append(calls, "read")
		if !reflect.DeepEqual(actual.scope(), expectedScope) || scenario != "standard" {
			t.Fatal("measurement read saw a different scope or scenario")
		}
		return calculateIndustrySample(ctx, actual, preset, scenario)
	}
	var reject bool
	policy := exportPolicyFunc(func(ctx context.Context, subject buildingenergy.Subject, action buildingenergy.Action, scope buildingenergy.Scope) error {
		calls = append(calls, "authorize")
		if subject.Issuer != f06FixtureSystem || subject.ID != "local-viewer" ||
			action != buildingenergy.ExportUsage || !reflect.DeepEqual(scope, expectedScope) {
			t.Fatal("export policy saw a different action, identity, or scope")
		}
		if reject {
			return buildingenergy.ErrAccessDenied
		}
		return sampleExportPolicy{fixture: fixture}.Require(ctx, subject, action, scope)
	})

	request.project = "other-project"
	got, err := calculateIndustryExport(ctx, fixture, preset, request, policy, read, at)
	if !errors.Is(err, buildingenergy.ErrAccessDenied) || len(calls) != 0 || got.CSV != "" || len(got.Sources) != 0 {
		t.Fatalf("wrong project reached export policy or reads: calls=%v, err=%v", calls, err)
	}
	request.project = expectedScope.Project.ProjectID
	reject = true
	got, err = calculateIndustryExport(ctx, fixture, preset, request, policy, read, at)
	if !errors.Is(err, buildingenergy.ErrAccessDenied) || !reflect.DeepEqual(calls, []string{"authorize"}) || got.CSV != "" || len(got.Sources) != 0 {
		t.Fatalf("denied export policy reached reads: calls=%v, err=%v", calls, err)
	}
	calls = nil
	reject = false
	got, err = calculateIndustryExport(ctx, fixture, preset, request, policy, read, at)
	if err != nil || !reflect.DeepEqual(calls, []string{"authorize", "read"}) || got.CSV == "" || len(got.Sources) != 3 {
		t.Fatalf("successful export did not use exactly one authorized read: calls=%v, err=%v", calls, err)
	}
}

func TestIndustryReportRejectsMeasurementOutsideExportScope(t *testing.T) {
	const building = "hospital-a"
	preset := industryBuildings[building]
	fixture, err := newIndustryFixture(building, preset, buildingenergy.Electricity)
	if err != nil {
		t.Fatal(err)
	}
	request := industryReportRequest{options: industryReportOptions{Kind: "f22"}, scenario: "standard"}
	for _, tt := range []struct {
		name   string
		change func(*industryReport)
	}{
		{"project", func(report *industryReport) { report.Project = "f06-demo" }},
		{"object", func(report *industryReport) { report.Building = "hotel-a" }},
		{"time range", func(report *industryReport) { report.To = report.To.Add(time.Hour) }},
		{"source", func(report *industryReport) { report.Sources[0].Source = "hotel-a-electricity-primary" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			read := func(ctx context.Context, fixture f06Fixture, preset industryBuilding, scenario string) (industryReport, error) {
				report, err := calculateIndustrySample(ctx, fixture, preset, scenario)
				if err != nil {
					return industryReport{}, err
				}
				tt.change(&report)
				return report, nil
			}
			got, err := calculateIndustryExport(context.Background(), fixture, preset, request, sampleExportPolicy{fixture: fixture}, read, time.Now())
			if !errors.Is(err, buildingenergy.ErrAccessDenied) || got.CSV != "" || len(got.Sources) != 0 || len(got.Rows) != 0 {
				t.Fatalf("report escaped authorized scope: err=%v", err)
			}
		})
	}
}

func TestIndustryReportEndpointExplainsNoApplicableTemplate(t *testing.T) {
	for _, query := range []string{"scenario=no-template", "scenario=standard&template=school"} {
		t.Run(query, func(t *testing.T) {
			var got struct {
				Status, StatusMessage, Notice, CSV string
				Rows                               [][]string
			}
			getSamplePage(t, "/industry-reports?kind=f25&building=hospital-a&energy=electricity&"+query, &got)
			if got.Status != "no_applicable_template" || got.StatusMessage == "" || got.Rows == nil || len(got.Rows) != 0 ||
				got.Notice != "演示规则，非权威标准" || (got.CSV != "" && !strings.Contains(got.CSV, got.Notice)) {
				t.Fatalf("no applicable template was not explicit: %+v", got)
			}
		})
	}
}

func TestF22EndpointRetainsF11F12F13ValuesAndScope(t *testing.T) {
	for _, tt := range []struct{ building, energy string }{
		{"office-a", "electricity"},
		{"office-b", "water"},
		{"hospital-a", "electricity"},
		{"school-a", "electricity"},
		{"hotel-a", "water"},
	} {
		t.Run(tt.building+"/"+tt.energy, func(t *testing.T) {
			scope := "building=" + tt.building + "&energy=" + tt.energy
			measured := industrySampleReport(t, tt.building, tt.energy, "standard")
			var got industryReportSnapshot
			getSamplePage(t, "/industry-reports?kind=f22&"+scope+"&scenario=standard", &got)
			if !reflect.DeepEqual(got.businessUsageScope, measured.businessUsageScope) || len(got.Rows) != len(measured.Metrics) {
				t.Fatalf("F22 snapshot differs from F12/F13 authorized evidence")
			}
			for i, metric := range measured.Metrics {
				denominator := ""
				if metric.Denominator != nil {
					denominator = reportNumber(metric.Denominator.Value)
				}
				for id, want := range map[string]string{
					"metric": metric.Name, "numerator": reportOptionalNumber(measured.Numerator.Usage),
					"denominator": denominator, "value": reportOptionalNumber(metric.Value),
					"unit": metric.Unit, "benchmarkThreshold": reportOptionalNumber(metric.Benchmark.Limit),
					"benchmarkDelta": reportOptionalNumber(metric.Benchmark.Delta),
				} {
					if value := industryReportCell(t, got, i, id); value != want {
						t.Fatalf("F22 column %s: got %q, want %q", id, value, want)
					}
				}
				if metric.Benchmark.Standard != nil &&
					!strings.Contains(industryReportCell(t, got, i, "benchmarkRule"), metric.Benchmark.Standard.Version) {
					t.Fatalf("F22 rule is missing F13 version evidence")
				}
			}
			if strings.HasPrefix(tt.building, "office-") {
				var f11 efficiencyReport
				getSamplePage(t, "/efficiency?"+scope+"&period=day", &f11)
				if !reflect.DeepEqual(got.businessUsageScope, f11.businessUsageScope) || len(got.Rows) != len(f11.Metrics) {
					t.Fatal("F22 snapshot differs from F11 scope")
				}
				for i, metric := range f11.Metrics {
					if industryReportCell(t, got, i, "value") != reportOptionalNumber(metric.Value) {
						t.Fatal("F22 value differs from F11")
					}
				}
			}
		})
	}
}

func TestF25EndpointCanLimitExportToEssentialFields(t *testing.T) {
	const query = "/industry-reports?kind=f25&building=hospital-a&energy=electricity&scenario=standard"
	var defaults, essentials industryReportSnapshot
	getSamplePage(t, query, &defaults)
	getSamplePage(t, query+"&fields=", &essentials)
	want := []string{"metric", "scope", "period", "unit", "quality", "status", "versions", "notice"}
	columns := make([]string, 0, len(essentials.Columns))
	for _, column := range essentials.Columns {
		columns = append(columns, string(column.ID))
	}
	if !reflect.DeepEqual(columns, want) || len(defaults.Columns) <= len(essentials.Columns) {
		t.Fatalf("essential-only export did not limit columns: got %v, want %v", columns, want)
	}
	versions := industryReportCell(t, essentials, 0, "versions")
	for _, evidence := range []string{essentials.ReportID, essentials.RuleVersion, essentials.MetricRuleVersion, essentials.Operator, "来源 3 点位"} {
		if !strings.Contains(versions, evidence) {
			t.Fatalf("essential CSV versions did not retain %q", evidence)
		}
	}
	if !strings.Contains(industryReportCell(t, essentials, 0, "quality"), "缺失率") {
		t.Fatal("essential CSV quality omitted the missing rate")
	}
	assertIndustryReportCSV(t, essentials)
}

func TestIndustryReportDisplayStressRemainsScopedToSyntheticMeterEvidence(t *testing.T) {
	original := industrySampleReport(t, "hospital-a", "electricity", "standard")
	var got industryReportSnapshot
	getSamplePage(t, "/industry-reports?kind=f22&building=hospital-a&energy=electricity&scenario=display-stress", &got)
	if !got.Sample || got.Project != original.Project || got.Building != original.Building ||
		!strings.Contains(industryReportCell(t, got, 0, "scope"), "门诊医技与住院综合能源统计演示建筑") ||
		industryReportCell(t, got, 0, "numerator") != "15360000" || industryReportCell(t, got, 0, "value") != "120000" {
		t.Fatalf("display sample lacks a scoped large value or long name: %+v", got)
	}
	assertIndustryReportCSV(t, got)
}

func TestIndustryReportEndpointEdgeScenarios(t *testing.T) {
	scenarios := []struct {
		building, scenario, wantStatus, wantQuality string
		hasValue, hasDelta                          bool
	}{
		{"hospital-a", "missing-denominator", "缺少有效分母记录", "正常", false, false},
		{"hospital-a", "invalid-denominator", "分母数值、单位或来源无效", "正常", false, false},
		{"hospital-a", "out-of-period", "分母记录与当前统计期不匹配", "正常", false, false},
		{"hospital-a", "partial", "计量覆盖率或数据质量不足", "部分覆盖", false, false},
		{"hospital-a", "not-applicable", "不适用", "正常", true, false},
		{"hospital-a", "incomparable", "不可比较", "正常", true, false},
		{"hospital-a", "no-standard", "无演示规则", "正常", true, false},
		{"hotel-a", "zero", "未超过演示阈值", "有效零", true, true},
	}
	for _, kind := range []string{"f22", "f25"} {
		for _, tc := range scenarios {
			t.Run(kind+"/"+tc.building+"/"+tc.scenario, func(t *testing.T) {
				suffix := "kind=" + kind + "&building=" + tc.building + "&energy=electricity&scenario=" + tc.scenario
				if kind == "f25" {
					suffix += "&fields=value,denominator,coverage,benchmarkThreshold,benchmarkDelta&sort=metric&direction=asc"
				}
				var got industryReportSnapshot
				getSamplePage(t, "/industry-reports?"+suffix, &got)
				assertIndustryReportCSV(t, got)
				if strings.Contains(got.CSV, "已达标") || strings.Contains(got.CSV, "正式合规") {
					t.Fatalf("report must not claim formal compliance: %s", got.CSV)
				}
				targetRow := 0
				if kind == "f25" && tc.building == "hospital-a" {
					// F25 sorts metrics alphabetically by Chinese name: 万元业务收入用量 precedes 床日用量.
					for idx := range got.Rows {
						if industryReportCell(t, got, idx, "metric") == "床日用量" {
							targetRow = idx
							break
						}
					}
				}
				status := industryReportCell(t, got, targetRow, "status")
				quality := industryReportCell(t, got, targetRow, "quality")
				value := industryReportCell(t, got, targetRow, "value")
				delta := industryReportCell(t, got, targetRow, "benchmarkDelta")
				if !strings.Contains(status, tc.wantStatus) || !strings.Contains(quality, tc.wantQuality) ||
					(value != "") != tc.hasValue || (delta != "") != tc.hasDelta {
					t.Fatalf("%s/%s/%s row=%d mismatch: status=%q quality=%q value=%q delta=%q",
						kind, tc.building, tc.scenario, targetRow, status, quality, value, delta)
				}
				if tc.scenario == "missing-denominator" && industryReportCell(t, got, targetRow, "denominator") != "" {
					t.Fatal("missing-denominator must keep denominator cell blank")
				}
				if tc.scenario == "zero" && value != "0" {
					t.Fatalf("effective zero must preserve 0, got %q", value)
				}
				if tc.scenario == "partial" && !strings.Contains(quality, "缺失率") {
					t.Fatalf("partial coverage quality must include missing rate: %q", quality)
				}
			})
		}
	}

	// office-b has area denominator (1.28 kWh/m²) and missing headcount denominator (blank value).
	for _, kind := range []string{"f22", "f25"} {
		t.Run(kind+"/office-b/missing-headcount", func(t *testing.T) {
			suffix := "kind=" + kind + "&building=office-b&energy=electricity&scenario=standard"
			if kind == "f25" {
				suffix += "&fields=value,denominator,coverage"
			}
			var got industryReportSnapshot
			getSamplePage(t, "/industry-reports?"+suffix, &got)
			assertIndustryReportCSV(t, got)
			values := map[string]string{}
			denoms := map[string]string{}
			for i := range got.Rows {
				name := industryReportCell(t, got, i, "metric")
				values[name] = industryReportCell(t, got, i, "value")
				denoms[name] = industryReportCell(t, got, i, "denominator")
			}
			if values["单位面积用量"] != "1.28" || values["人均用量"] != "" || denoms["人均用量"] != "" {
				t.Fatalf("office-b metrics wrong: values=%v denoms=%v", values, denoms)
			}
		})
	}
}
