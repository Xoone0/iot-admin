package main

import (
	"context"
	"encoding/csv"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

func TestF21CSVMatchesPreviewScopeRowsAndQuality(t *testing.T) {
	for _, tt := range []struct {
		name, query, unit string
		total             float64
	}{
		{"classification", "building=office-a&energy=electricity&period=day", "kWh", 1536},
		{"summary", "building=office-a&energy=water&period=7", "m³", 960},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var data struct {
				Project                                                                                                  string
				Sample                                                                                                   bool
				Building, Energy, Unit, Timezone, From, To, AsOf, GeneratedAt, ReportID, Operator                        string
				RuleVersion, MeterRuleVersion, ClassificationRuleVersion, CatalogVersion, TopologyVersion, CSV, Filename string
				Total                                                                                                    f06Value
				Sources                                                                                                  []f06Source
				Rows                                                                                                     []struct {
					ID, Name, Kind, From, To, Status string
					Depth                            int
					f06Value
					Share *float64
				}
			}
			getSamplePage(t, "/reports?"+tt.query+"&type="+tt.name, &data)
			var classification f06Report
			getSamplePage(t, "/classification?"+tt.query, &classification)
			assertF06Usage(t, data.Total.Usage, tt.total)
			assertF06Usage(t, classification.Total.Usage, tt.total)
			if !data.Sample || data.Unit != tt.unit || len(data.Sources) != 3 || data.AsOf != data.To || data.GeneratedAt == "" || data.Operator == "" || data.ReportID == "" ||
				data.Timezone != classification.Timezone || data.ClassificationRuleVersion != classification.ClassificationRuleVersion || data.TopologyVersion != classification.TopologyVersion || data.CatalogVersion != classification.CatalogVersion {
				t.Fatalf("report scope or provenance incomplete")
			}
			if !strings.HasSuffix(data.Filename, ".csv") || !strings.HasPrefix(data.CSV, "\uFEFF") {
				t.Fatal("download filename or UTF-8 BOM missing")
			}
			records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(data.CSV, "\uFEFF"))).ReadAll()
			if err != nil || len(records) != len(data.Rows)+2 {
				t.Fatalf("one CSV total plus all preview rows expected, got %d: %v", len(records), err)
			}
			columns := make(map[string]int, len(records[0]))
			for i, name := range records[0] {
				columns[name] = i
			}
			for _, name := range []string{"记录", "项目", "建筑", "时区", "期起（含）", "期止（不含）", "能源", "单位", "用量", "占比", "覆盖率", "期望数", "有效数", "缺测数", "可疑数", "缺测率", "质量状态", "数据截止", "生成时间", "报表版本", "计量版本", "分类计算版本", "目录版本", "拓扑版本", "来源点位数", "操作人", "报告编号", "数据标识"} {
				if _, ok := columns[name]; !ok {
					t.Fatalf("CSV column missing: %s", name)
				}
			}
			if total := records[1]; total[columns["记录"]] != "合计" || total[columns["用量"]] != strconv.FormatFloat(tt.total, 'f', -1, 64) || total[columns["期起（含）"]] != data.From || total[columns["数据标识"]] != "合成数据" {
				t.Fatalf("CSV total disagrees with preview: %v", total)
			}
			seen := map[string]bool{}
			var displayedTotal float64
			for i, row := range data.Rows {
				csvRow := records[i+2]
				seen[row.Status] = true
				if row.Depth == 0 && row.Usage != nil {
					displayedTotal += *row.Usage
				}
				for _, field := range []struct {
					column string
					want   string
				}{
					{"记录", row.Name}, {"项目", data.Project}, {"建筑", data.Building}, {"时区", data.Timezone}, {"期起（含）", row.From}, {"期止（不含）", row.To},
					{"能源", data.Energy}, {"单位", data.Unit}, {"用量", csvTestNumber(row.Usage)}, {"覆盖率", csvTestNumber(row.Coverage.Rate)},
					{"占比", csvTestNumber(row.Share)}, {"生成时间", data.GeneratedAt}, {"报告编号", data.ReportID}, {"操作人", data.Operator}, {"质量状态", row.Status},
				} {
					if value := csvRow[columns[field.column]]; value != field.want {
						t.Fatalf("row %d field %s got %q want %q", i, field.column, value, field.want)
					}
				}
			}
			assertF06Usage(t, data.Total.Usage, displayedTotal)
			if tt.name == "summary" && (!seen["valid_zero"] || !seen["missing"] || !seen["suspect"]) {
				t.Fatalf("summary merges zero or missing states: %v", seen)
			}
		})
	}
}

func TestF11AndF21RejectOutOfScopeOrDuplicateSelectors(t *testing.T) {
	query := "?building=office-a&energy=electricity&period=day"
	for _, path := range []string{"/efficiency" + query, "/reports" + query + "&type=summary"} {
		for _, suffix := range []string{"&building=office-b", "&project=client", "&point=office-b-electricity-primary", "&from=%zz"} {
			response := httptest.NewRecorder()
			previewHandler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, overviewPath+path+suffix, nil))
			if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), `"csv"`) || strings.Contains(response.Body.String(), `"sources"`) {
				t.Fatalf("unapproved query returned %d: %s", response.Code, response.Body)
			}
		}
	}
}

func TestF21RequiresExportPermissionBeforeReadingTheAuthorizedScope(t *testing.T) {
	fixture, err := newF06Fixture("office-a", buildingenergy.Electricity, 7)
	if err != nil {
		t.Fatal(err)
	}
	exact := fixture.scope()
	if _, err := buildingenergy.ResolveScope(context.Background(), fixture, fixture, fixture, buildingenergy.ReadUsage, exact); err != nil {
		t.Fatal("the read policy should allow the exact scope", err)
	}
	var checks, reads int
	deny := exportPolicyFunc(func(_ context.Context, subject buildingenergy.Subject, action buildingenergy.Action, scope buildingenergy.Scope) error {
		checks++
		if subject.Issuer != f06FixtureSystem || subject.ID != "local-viewer" || action != buildingenergy.ExportUsage || !reflect.DeepEqual(scope, exact) {
			t.Fatalf("policy saw a different identity, action, or scope")
		}
		return buildingenergy.ErrAccessDenied
	})
	service := f06Service{fixture: fixture, read: func(buildingenergy.PointRef, buildingenergy.Window) buildingenergy.MeteringData {
		reads++
		return buildingenergy.MeteringData{}
	}}
	report, err := calculateReport(context.Background(), service, deny, exact, summaryReport, fixture.window.AsOf)
	if !errors.Is(err, buildingenergy.ErrAccessDenied) || checks != 1 || reads != 0 || report.CSV != "" || len(report.Rows) != 0 || len(report.Sources) != 0 {
		t.Fatalf("export denial was bypassed: err=%v checks=%d reads=%d", err, checks, reads)
	}
	wrongBuilding := fixture.scope()
	wrongBuilding.Objects[0].ID = "office-b"
	report, err = calculateReport(context.Background(), service, sampleExportPolicy{fixture: fixture}, wrongBuilding, summaryReport, time.Now())
	if !errors.Is(err, buildingenergy.ErrAccessDenied) || reads != 0 || report.CSV != "" || len(report.Rows) != 0 || len(report.Sources) != 0 {
		t.Fatalf("changed export scope was accepted: err=%v reads=%d", err, reads)
	}
}

type exportPolicyFunc func(context.Context, buildingenergy.Subject, buildingenergy.Action, buildingenergy.Scope) error

func (policy exportPolicyFunc) Require(ctx context.Context, subject buildingenergy.Subject, action buildingenergy.Action, scope buildingenergy.Scope) error {
	return policy(ctx, subject, action, scope)
}

func csvTestNumber(input *float64) string {
	if input == nil {
		return ""
	}
	return strconv.FormatFloat(*input, 'f', -1, 64)
}
