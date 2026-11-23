package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/admin"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

const reportsPath = overviewPath + "/reports"
const reportRuleVersion = "f21-report-v1"

type reportType string

const (
	classificationsReport reportType = "classification"
	summaryReport         reportType = "summary"
)

type reportRow struct {
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Depth  int          `json:"depth"`
	Kind   string       `json:"kind"`
	From   time.Time    `json:"from"`
	To     time.Time    `json:"to"`
	Share  *float64     `json:"share"`
	Status usageQuality `json:"status"`
	f06Value
}

type energyReport struct {
	businessUsageScope
	Type        reportType  `json:"type"`
	GeneratedAt time.Time   `json:"generatedAt"`
	ReportID    string      `json:"reportId"`
	Operator    string      `json:"operator"`
	RuleVersion string      `json:"ruleVersion"`
	Total       f06Value    `json:"total"`
	Rows        []reportRow `json:"rows"`
	Filename    string      `json:"filename"`
	CSV         string      `json:"csv"`
}

func sampleReports(w http.ResponseWriter, r *http.Request) {
	fixture, selectedType, err := selectBusinessUsage(r.URL.RawQuery, "type")
	if err != nil {
		writeBusinessUsageError(w, err)
		return
	}
	service := f06Service{fixture: fixture, read: fixture.readMeter}
	result, err := calculateReport(r.Context(), service, sampleExportPolicy{fixture: fixture}, fixture.scope(), reportType(selectedType), time.Now())
	if err != nil {
		writeBusinessUsageError(w, err)
		return
	}
	writeResponse(w, http.StatusOK, admin.Response{Code: 0, Message: "ok", Data: result})
}

// No production permission is configured by the preview. This adapter grants
// export only to the same trusted synthetic identity and exact fixture scope
// that the read policy validates. F06 still checks ReadUsage independently.
type sampleExportPolicy struct{ fixture f06Fixture }

func (p sampleExportPolicy) Require(ctx context.Context, subject buildingenergy.Subject, action buildingenergy.Action, scope buildingenergy.Scope) error {
	if action != buildingenergy.ExportUsage {
		return buildingenergy.ErrAccessDenied
	}
	return p.fixture.Require(ctx, subject, buildingenergy.ReadUsage, scope)
}

func calculateReport(ctx context.Context, service f06Service, policy buildingenergy.AccessPolicy, scope buildingenergy.Scope, kind reportType, now time.Time) (energyReport, error) {
	if kind != classificationsReport && kind != summaryReport {
		return energyReport{}, buildingenergy.ErrInvalidScope
	}
	fixture := service.fixture
	resolved, err := buildingenergy.ResolveScope(ctx, fixture, policy, fixture, buildingenergy.ExportUsage, scope)
	if err != nil {
		return energyReport{}, err
	}
	usage, err := service.calculate(ctx, scope)
	if err != nil {
		return energyReport{}, err
	}
	operator, err := fixture.AuthenticatedSubject(ctx)
	if err != nil {
		return energyReport{}, err
	}
	// Generate both views of this immutable result together, including the same
	// timestamps and sampled versions. No second request recalculates the CSV.
	generated := now.In(resolved.Location).Truncate(time.Second)
	result := energyReport{
		businessUsageScope: sampleUsageScope(fixture, usage), Type: kind, GeneratedAt: generated,
		ReportID: fmt.Sprintf("sample-%s-%s-%s-%d", kind, fixture.object.ID, fixture.energy, now.UnixNano()),
		Operator: operator.ID, RuleVersion: reportRuleVersion, Total: usage.Total,
		Filename: fmt.Sprintf("energy-%s-%s-%s-%s.csv", kind, fixture.object.ID, fixture.energy, usage.From.Format(time.DateOnly)),
		Rows:     projectReportRows(kind, usage),
	}
	result.CSV, err = formatReportCSV(result)
	return result, err
}

func projectReportRows(kind reportType, usage f06Report) []reportRow {
	var rows []reportRow
	appendRow := func(id, name, kind string, depth int, from, to time.Time, value f06Value) {
		row := reportRow{ID: id, Name: name, Kind: kind, Depth: depth, From: from, To: to, f06Value: value, Status: measureQuality(value)}
		if value.Usage != nil && usage.Total.Usage != nil && *usage.Total.Usage > 0 {
			share := *value.Usage / *usage.Total.Usage
			row.Share = &share
		}
		rows = append(rows, row)
	}
	if kind == summaryReport {
		for _, day := range usage.Trend {
			date := day.From.Format(time.DateOnly)
			appendRow(date, date, "period", 0, day.From, day.From.AddDate(0, 0, 1), day.Total)
		}
		return rows
	}
	var appendCategories func([]f06Category, int)
	appendCategories = func(categories []f06Category, depth int) {
		for _, category := range categories {
			appendRow(category.ID, category.Name, "category", depth, usage.From, usage.To, category.f06Value)
			appendCategories(category.Children, depth+1)
		}
	}
	appendCategories(usage.Categories, 0)
	appendRow("unclassified", "未分项", "unclassified", 0, usage.From, usage.To, usage.Unclassified)
	return rows
}

func formatReportCSV(report energyReport) (string, error) {
	header := []string{
		"记录", "记录类型", "记录编号", "分类层级", "报表类型", "项目", "建筑", "时区", "期起（含）", "期止（不含）", "能源", "单位", "用量", "占比", "覆盖率",
		"期望数", "有效数", "缺测数", "可疑数", "缺测率", "质量状态", "数据截止", "生成时间", "报表版本", "计量版本", "分类计算版本",
		"目录版本", "拓扑版本", "来源点位数", "操作人", "报告编号", "数据标识",
	}
	return encodeReportCSV(header, func(writer *csv.Writer) error {
		writeRow := func(row reportRow) error {
			coverage := row.Coverage
			missingRate := ""
			if coverage.Expected > 0 {
				missingRate = reportNumber(coverage.Missing / coverage.Expected)
			}
			return writer.Write([]string{
				row.Name, row.Kind, row.ID, strconv.Itoa(row.Depth), string(report.Type), report.Project, report.Building, report.Timezone,
				row.From.Format(time.RFC3339), row.To.Format(time.RFC3339), string(report.Energy), report.Unit, reportOptionalNumber(row.Usage),
				reportOptionalNumber(row.Share), reportOptionalNumber(coverage.Rate), reportNumber(coverage.Expected), reportNumber(coverage.Valid),
				reportNumber(coverage.Missing), reportNumber(coverage.Suspect), missingRate, string(row.Status), report.AsOf.Format(time.RFC3339),
				report.GeneratedAt.Format(time.RFC3339), report.RuleVersion, report.MeterRuleVersion, report.ClassificationRuleVersion, report.CatalogVersion,
				report.TopologyVersion, strconv.Itoa(len(report.Sources)), report.Operator, report.ReportID, "合成数据",
			})
		}
		total := reportRow{ID: "total", Name: "合计", Kind: "total", From: report.From, To: report.To, f06Value: report.Total, Status: measureQuality(report.Total)}
		if err := writeRow(total); err != nil {
			return err
		}
		for _, row := range report.Rows {
			if err := writeRow(row); err != nil {
				return err
			}
		}
		return nil
	})
}

func encodeReportCSV(header []string, rows func(*csv.Writer) error) (string, error) {
	var output strings.Builder
	output.WriteString("\uFEFF")
	writer := csv.NewWriter(&output)
	writer.UseCRLF = true
	if err := writer.Write(header); err != nil {
		return "", err
	}
	if err := rows(writer); err != nil {
		return "", err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", err
	}
	return output.String(), nil
}

func reportOptionalNumber(value *float64) string {
	if value == nil {
		return ""
	}
	return reportNumber(*value)
}

func reportNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
