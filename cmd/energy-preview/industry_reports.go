package main

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/admin"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

const industryReportsPath = overviewPath + "/industry-reports"

const (
	noIndustryTemplateScenario = "no-template"
	industryDisplayStress      = "display-stress"
)

type industryReportRequest struct {
	options  industryReportOptions
	building string
	energy   buildingenergy.EnergyType
	scenario string
	project  string
	template buildingenergy.BuildingType
}

func parseIndustryReportRequest(rawQuery string) (industryReportRequest, error) {
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return industryReportRequest{}, buildingenergy.ErrInvalidScope
	}
	for key, values := range query {
		if len(values) != 1 || (values[0] == "" && key != "fields") {
			return industryReportRequest{}, buildingenergy.ErrInvalidScope
		}
		switch key {
		case "kind", "building", "energy", "scenario", "project":
		case "fields", "sort", "direction", "template":
			if query.Get("kind") != "f25" {
				return industryReportRequest{}, buildingenergy.ErrInvalidScope
			}
		default:
			return industryReportRequest{}, buildingenergy.ErrInvalidScope
		}
	}

	request := industryReportRequest{
		options:  industryReportOptions{Kind: query.Get("kind"), Sort: query.Get("sort"), Direction: query.Get("direction")},
		building: query.Get("building"),
		energy:   buildingenergy.EnergyType(query.Get("energy")),
		scenario: query.Get("scenario"),
		project:  query.Get("project"),
		template: buildingenergy.BuildingType(query.Get("template")),
	}
	if query.Has("fields") {
		request.options.Fields = []string{}
		if fields := query.Get("fields"); fields != "" {
			request.options.Fields = strings.Split(fields, ",")
		}
	}
	if _, known := industryBuildings[request.building]; !known ||
		(request.energy != buildingenergy.Electricity && request.energy != buildingenergy.Water) {
		return industryReportRequest{}, buildingenergy.ErrInvalidScope
	}
	switch request.template {
	case "", buildingenergy.BuildingOffice, buildingenergy.BuildingHospital, buildingenergy.BuildingSchool, buildingenergy.BuildingHotel:
	default:
		return industryReportRequest{}, buildingenergy.ErrInvalidScope
	}
	switch request.scenario {
	case noIndustryTemplateScenario:
		if request.options.Kind != "f25" {
			return industryReportRequest{}, buildingenergy.ErrInvalidScope
		}
	case industryDisplayStress:
		if request.building != "hospital-a" {
			return industryReportRequest{}, buildingenergy.ErrInvalidScope
		}
	default:
		if !supportedIndustryScenario(request.scenario) {
			return industryReportRequest{}, buildingenergy.ErrInvalidScope
		}
	}
	if err := checkIndustryReportOptions(request.options); err != nil {
		return industryReportRequest{}, err
	}
	return request, nil
}

func sampleIndustryReports(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	request, err := parseIndustryReportRequest(r.URL.RawQuery)
	if err != nil {
		writeBusinessUsageError(w, err)
		return
	}
	preset := industryBuildings[request.building]
	if request.scenario == industryDisplayStress {
		preset.name = "合成医院 A（门诊医技与住院综合能源统计演示建筑）"
		preset.meterScale *= 5_000
	}
	fixture, err := newIndustryFixture(request.building, preset, request.energy)
	if err != nil {
		writeBusinessUsageError(w, err)
		return
	}
	result, err := calculateIndustryExport(r.Context(), fixture, preset, request, sampleExportPolicy{fixture: fixture}, calculateIndustrySample, time.Now())
	if err != nil {
		writeBusinessUsageError(w, err)
		return
	}
	writeResponse(w, http.StatusOK, admin.Response{Code: 0, Message: "ok", Data: result})
}

type industrySampleReader func(context.Context, f06Fixture, industryBuilding, string) (industryReport, error)

func calculateIndustryExport(ctx context.Context, fixture f06Fixture, preset industryBuilding, request industryReportRequest,
	policy buildingenergy.AccessPolicy, read industrySampleReader, now time.Time) (industryReportSnapshot, error) {
	scope := fixture.scope()
	if request.project != "" && request.project != scope.Project.ProjectID {
		return industryReportSnapshot{}, buildingenergy.ErrAccessDenied
	}
	resolved, err := buildingenergy.ResolveScope(ctx, fixture, policy, fixture, buildingenergy.ExportUsage, scope)
	if err != nil {
		return industryReportSnapshot{}, err
	}
	operator, err := fixture.AuthenticatedSubject(ctx)
	if err != nil {
		return industryReportSnapshot{}, err
	}
	scenario := request.scenario
	if request.template != "" && request.template != preset.kind {
		scenario = noIndustryTemplateScenario
	}
	// These report-only scenarios keep the original industry's measurement
	// and quality logic. The fixture scope still gates all reads.
	measurementScenario := scenario
	if scenario == noIndustryTemplateScenario || scenario == industryDisplayStress {
		measurementScenario = "standard"
	}
	result, err := read(ctx, fixture, preset, measurementScenario)
	if err != nil {
		return industryReportSnapshot{}, err
	}
	if err := checkIndustryExportScope(fixture, scope, result.businessUsageScope); err != nil {
		return industryReportSnapshot{}, err
	}
	result.Scenario = scenario
	if scenario == noIndustryTemplateScenario {
		result.Metrics = nil
	}
	return projectIndustryReport(result, request.options, now.In(resolved.Location), operator.ID)
}

func checkIndustryExportScope(fixture f06Fixture, scope buildingenergy.Scope, result businessUsageScope) error {
	if result.Project != scope.Project.ProjectID || result.Building != fixture.object.ID || result.Energy != fixture.energy ||
		result.Unit != fixture.unit || result.Timezone != fixture.window.Timezone || !result.From.Equal(scope.Period.From) ||
		!result.To.Equal(scope.Period.To) || !result.AsOf.Equal(fixture.window.AsOf) {
		return buildingenergy.ErrAccessDenied
	}
	for _, source := range result.Sources {
		if !slices.ContainsFunc(scope.Points, func(point buildingenergy.PointKey) bool {
			return fixture.channel(point).ID == source.Source
		}) {
			return buildingenergy.ErrAccessDenied
		}
	}
	return nil
}
