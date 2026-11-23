package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/admin"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

const targetsPath = overviewPath + "/targets"

type targetPeriodView struct {
	Index    int                         `json:"index"`
	From     time.Time                   `json:"from"`
	To       time.Time                   `json:"to"`
	Target   *float64                    `json:"target"`
	Usage    *float64                    `json:"usage"`
	Coverage *f06Coverage                `json:"coverage"`
	Status   buildingenergy.TargetStatus `json:"status"`
	Ratio    *float64                    `json:"ratio"`
	Delta    *float64                    `json:"delta"`
}

type targetVersionView struct {
	Version    string    `json:"version"`
	Method     string    `json:"method"`
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	Source     string    `json:"source"`
	WeightsPPM []int64   `json:"weightsPPM"`
}

type targetMeasurementSource struct {
	Name              string `json:"name"`
	Source            string `json:"source"`
	ProfileVersion    string `json:"profileVersion"`
	BindingVersion    string `json:"bindingVersion"`
	MappingGeneration uint64 `json:"mappingGeneration"`
}

type targetMeasurementView struct {
	MeterRuleVersion          string                    `json:"meterRuleVersion"`
	ClassificationRuleVersion string                    `json:"classificationRuleVersion"`
	CatalogVersion            string                    `json:"catalogVersion"`
	TopologyVersion           string                    `json:"topologyVersion"`
	Sources                   []targetMeasurementSource `json:"sources"`
}

type targetPreview struct {
	Sample           bool                      `json:"sample"`
	Project          string                    `json:"project"`
	Building         string                    `json:"building"`
	Energy           buildingenergy.EnergyType `json:"energy"`
	Year             int                       `json:"year"`
	Unit             string                    `json:"unit"`
	Timezone         string                    `json:"timezone"`
	AsOf             time.Time                 `json:"asOf"`
	TargetSelectedAt time.Time                 `json:"targetSelectedAt"`
	RuleVersion      string                    `json:"ruleVersion"`
	Target           *targetVersionView        `json:"target"`
	Annual           targetPeriodView          `json:"annual"`
	Months           [12]targetPeriodView      `json:"months"`
	Measurement      *targetMeasurementView    `json:"measurement"`
}

func sampleTargets(w http.ResponseWriter, r *http.Request) {
	query, parseErr := url.ParseQuery(r.URL.RawQuery)
	year, yearErr := strconv.Atoi(query.Get("year"))
	if parseErr != nil || yearErr != nil || (year != 2025 && year != 2026) ||
		len(query) != 3 || len(query["building"]) != 1 || len(query["energy"]) != 1 || len(query["year"]) != 1 {
		writeResponse(w, http.StatusBadRequest, admin.Response{Code: 1, Message: "仅支持样本建筑、能源和年度"})
		return
	}
	fixture, err := newF06Fixture(query.Get("building"), buildingenergy.EnergyType(query.Get("energy")), 7)
	if err != nil {
		writeResponse(w, http.StatusBadRequest, admin.Response{Code: 1, Message: "无效的目标样本筛选条件"})
		return
	}
	preview, err := calculateTargetSample(r.Context(), fixture, year)
	if errors.Is(err, buildingenergy.ErrAccessDenied) {
		writeResponse(w, http.StatusForbidden, admin.Response{Code: 1, Message: "样本范围未授权"})
		return
	}
	if err != nil {
		log.Printf("calculate energy target preview: %v", err)
		writeResponse(w, http.StatusInternalServerError, admin.Response{Code: 1, Message: "目标样本计算失败"})
		return
	}
	writeResponse(w, http.StatusOK, admin.Response{Code: 0, Message: "ok", Data: preview})
}

func calculateTargetSample(ctx context.Context, fixture f06Fixture, year int) (targetPreview, error) {
	// Use the same synthetic meter readings, rules, and knowledge cutoff as
	// complete monthly F08. Manual target numbers never generate meter readings.
	f, trend, err := planTrendFixture(fixture, buildingenergy.Month, false)
	if err != nil {
		return targetPreview{}, err
	}
	loc := f.effective.From.Location()
	yearRange := buildingenergy.Range{
		From: time.Date(year, 1, 1, 0, 0, 0, 0, loc),
		To:   time.Date(year+1, 1, 1, 0, 0, 0, 0, loc),
	}

	// The synthetic target directory covers the whole selectable year. This
	// grant only authorizes viewing the target definition; measured data below
	// is still scoped and read for each closed interval against the meter cutoff.
	targetDirectory := f
	targetDirectory.project.Meta.Effective.To = time.Date(2027, 1, 1, 0, 0, 0, 0, loc)
	targetDirectory.window.Range = yearRange
	resolved, err := buildingenergy.ResolveScope(ctx, targetDirectory, targetDirectory, targetDirectory,
		buildingenergy.ReadUsage, targetDirectory.scope())
	if err != nil {
		return targetPreview{}, err
	}
	scope := buildingenergy.TargetScope{Project: resolved.Project.Key, Object: resolved.Objects[0].Key,
		Energy: f.energy, Unit: f.unit, Year: year, Timezone: resolved.Project.Timezone}
	selectedAt := f.window.AsOf
	if !selectedAt.Before(yearRange.To) {
		selectedAt = yearRange.To.Add(-time.Nanosecond)
	}
	plan, err := buildingenergy.PlanEnergyTarget(scope, selectedAt, sampleTargetVersions(scope, yearRange))
	if err != nil {
		return targetPreview{}, err
	}
	out := targetPreview{
		Sample: true, Project: scope.Project.ProjectID, Building: scope.Object.ID,
		Energy: scope.Energy, Year: scope.Year, Unit: scope.Unit, Timezone: scope.Timezone,
		AsOf: f.window.AsOf, TargetSelectedAt: selectedAt, RuleVersion: buildingenergy.TargetRuleVersion,
	}
	if selected := plan.Version; selected != nil {
		out.Target = &targetVersionView{Version: selected.Meta.Version, Method: "linear",
			From: selected.Meta.Effective.From, To: selected.Meta.Effective.To, Source: selected.Meta.Source.ID}
		if selected.Method == buildingenergy.TargetWeighted {
			out.Target.Method = "seasonal"
			out.Target.WeightsPPM = selected.WeightPPM[:]
		}
	}

	read := func(period buildingenergy.TargetPeriod, month time.Month) (targetPeriodView, error) {
		if err := ctx.Err(); err != nil {
			return targetPeriodView{}, err
		}
		var report *buildingenergy.ClassifiedUsageReport
		if !period.To.After(f.window.AsOf) {
			span := f
			span.window.Range = period.Range
			service := f06Service{fixture: span, read: func(point buildingenergy.PointRef, window buildingenergy.Window) buildingenergy.MeteringData {
				return span.generateMeter(point, window, func(at time.Time, base float64) (float64, buildingenergy.Quality) {
					return trendSample(at, base, trend.Previous, "standard")
				})
			}}
			calculated, err := service.classify(ctx, span.scope(), span.classification())
			if err != nil {
				return targetPeriodView{}, err
			}
			report = &calculated.report
			if out.Measurement == nil {
				out.Measurement = projectTargetMeasurement(span, calculated)
			}
		}
		compared, err := buildingenergy.CompareEnergyTarget(plan, month, report)
		if err != nil {
			return targetPeriodView{}, err
		}
		row := targetPeriodView{Index: int(month), From: period.From, To: period.To, Status: compared.Status,
			Usage: compared.Usage, Ratio: compared.Ratio, Delta: compared.Delta}
		if period.TargetMilli != nil {
			value := float64(*period.TargetMilli) / 1000
			row.Target = &value
		}
		if report != nil && compared.Coverage.Expected > 0 {
			value := f06Measure(buildingenergy.CategoryValue{Coverage: compared.Coverage})
			row.Coverage = &value.Coverage
		}
		if period.To.After(f.window.AsOf) && row.Status == buildingenergy.TargetMissingUsage {
			row.Status = buildingenergy.TargetIncompletePeriod
		}
		return row, nil
	}
	if out.Annual, err = read(plan.Annual, 0); err != nil {
		return targetPreview{}, err
	}
	for month, period := range plan.Months {
		if out.Months[month], err = read(period, time.Month(month+1)); err != nil {
			return targetPreview{}, err
		}
	}
	return out, nil
}

func sampleTargetVersions(scope buildingenergy.TargetScope, effective buildingenergy.Range) []buildingenergy.TargetVersion {
	if scope.Object.ID != "office-a" {
		return nil
	}
	target := buildingenergy.TargetVersion{
		Scope: scope, Method: buildingenergy.TargetLinear,
		Meta: buildingenergy.VersionMeta{
			Version:   "sample-target-" + strconv.Itoa(scope.Year) + "-" + string(scope.Energy) + "-v1",
			Source:    buildingenergy.SourceRef{System: "target-synthetic", ID: "annual-target-fixture"},
			Effective: effective,
		},
	}
	switch {
	case scope.Energy == buildingenergy.Electricity && scope.Year == 2025:
		target.AnnualMilli = 240_000_000
	case scope.Energy == buildingenergy.Electricity && scope.Year == 2026:
		target.AnnualMilli = 360_000_000
		target.Method = buildingenergy.TargetWeighted
		target.WeightPPM = [12]int64{70_000, 65_000, 70_000, 75_000, 80_000, 95_000, 110_000, 110_000, 90_000, 80_000, 75_000, 80_000}
	case scope.Energy == buildingenergy.Water && scope.Year == 2026:
		target.AnnualMilli = 90_000_000
	default:
		return nil
	}
	return []buildingenergy.TargetVersion{target}
}

func projectTargetMeasurement(f f06Fixture, calculated sampleClassificationResult) *targetMeasurementView {
	report := calculated.report
	out := &targetMeasurementView{MeterRuleVersion: calculated.inputs[0].Result.RuleVersion,
		ClassificationRuleVersion: report.RuleVersion, CatalogVersion: report.CatalogVersion.Version,
		TopologyVersion: report.TopologyVersion.Version, Sources: make([]targetMeasurementSource, 0, len(f.meters))}
	for _, source := range f.projectSources(report.Query, report) {
		out.Sources = append(out.Sources, targetMeasurementSource{Name: source.Name, Source: source.Source,
			ProfileVersion: source.ProfileVersion, BindingVersion: source.BindingVersion, MappingGeneration: source.MappingGeneration})
	}
	return out
}
