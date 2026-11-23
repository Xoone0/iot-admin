package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/admin"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

const dimensionsPath = "/admin/v1/analytics/building-energy/preview/dimensions"

type f07Report struct {
	Sample                    bool                      `json:"sample"`
	View                      buildingenergy.ObjectKind `json:"view"`
	Unit                      string                    `json:"unit"`
	Timezone                  string                    `json:"timezone"`
	From                      time.Time                 `json:"from"`
	To                        time.Time                 `json:"to"`
	AsOf                      time.Time                 `json:"asOf"`
	RuleVersion               string                    `json:"ruleVersion"`
	ClassificationRuleVersion string                    `json:"classificationRuleVersion"`
	CatalogVersion            string                    `json:"catalogVersion"`
	TopologyVersion           string                    `json:"topologyVersion"`
	Rows                      []f07Row                  `json:"rows"`
}

type f07Row struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	f06Value
	Status  buildingenergy.TrendStatus `json:"status"`
	Rank    *int                       `json:"rank"`
	Sources []f07Source                `json:"sources"`
}

type f07Source struct {
	Channel                string   `json:"channel"`
	Usage                  *float64 `json:"usage"`
	Valid                  int      `json:"valid"`
	Missing                int      `json:"missing"`
	Suspect                int      `json:"suspect"`
	Observations           int      `json:"observations"`
	ProfileVersion         string   `json:"profileVersion"`
	ObjectBindingVersion   string   `json:"objectBindingVersion"`
	CategoryBindingVersion string   `json:"categoryBindingVersion,omitempty"`
	MappingGeneration      uint64   `json:"mappingGeneration"`
	MeterRuleVersion       string   `json:"meterRuleVersion"`
}

func sampleDimensions(w http.ResponseWriter, r *http.Request) {
	params, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(params) != 6 || len(params["view"]) != 1 || len(params["building"]) != 1 ||
		len(params["energy"]) != 1 || len(params["period"]) != 1 || len(params["mode"]) != 1 || len(params["scenario"]) != 1 {
		writeResponse(w, http.StatusBadRequest, admin.Response{Code: 1, Message: "仅支持样本建筑、视角、能源、周期、对比口径与场景"})
		return
	}
	view := buildingenergy.ObjectKind(params.Get("view"))
	fixture, err := newDimensionsFixture(view, params.Get("building"), buildingenergy.EnergyType(params.Get("energy")),
		params.Get("period"), params.Get("mode"), params.Get("scenario"))
	if err != nil {
		writeResponse(w, http.StatusBadRequest, admin.Response{Code: 1, Message: "无效的多维统计样本筛选条件"})
		return
	}
	report, err := fixture.calculate(r.Context(), fixture.scope())
	if errors.Is(err, buildingenergy.ErrAccessDenied) {
		writeResponse(w, http.StatusForbidden, admin.Response{Code: 1, Message: "样本范围未授权"})
		return
	}
	if err != nil {
		log.Printf("calculate dimension preview: %v", err)
		writeResponse(w, http.StatusInternalServerError, admin.Response{Code: 1, Message: "多维统计样本计算失败"})
		return
	}
	writeResponse(w, http.StatusOK, admin.Response{Code: 0, Message: "ok", Data: report})
}

func (f dimensionsFixture) calculate(ctx context.Context, scope buildingenergy.Scope) (f07Report, error) {
	// The preview resolves the complete object and point grant before any
	// metering sample is generated. Its subject and directory are synthetic.
	resolved, err := buildingenergy.ResolveScope(ctx, f, f, f, buildingenergy.ReadUsage, scope)
	if err != nil {
		return f07Report{}, err
	}
	catalog, topology, err := f.snapshots()
	if err != nil {
		return f07Report{}, err
	}
	window := f.buildings[0].window
	inputs := make([]buildingenergy.CategoryPointResult, 0, len(scope.Points))
	for _, key := range scope.Points {
		if err := ctx.Err(); err != nil {
			return f07Report{}, err
		}
		building := f.pointBuildings[key]
		point := buildingenergy.PointRef{Project: resolved.Project.Key, Key: key}
		data := building.generateMeter(point, window, func(at time.Time, base float64) (float64, buildingenergy.Quality) {
			if f.qualitySample && key == building.pointKey("primary") && building.object.ID == "office-a" && at.Equal(window.From) {
				return 987654, buildingenergy.Suspect
			}
			return octoberSample(at, base)
		})
		usage, err := buildingenergy.Calculate(data, window)
		if err != nil {
			return f07Report{}, err
		}
		inputs = append(inputs, buildingenergy.CategoryPointResult{Result: usage, Mapping: dimensionMapping(building, point)})
	}
	report, err := buildingenergy.CalculateDimensionUsage(buildingenergy.DimensionUsageQuery{
		Scope: scope, Window: window, View: f.view, Energy: f.buildings[0].energy,
	}, catalog, topology, inputs)
	if err != nil {
		return f07Report{}, err
	}
	data := f07Report{
		Sample: true, View: f.view, Unit: report.Unit, Timezone: window.Timezone,
		From: window.From, To: window.To, AsOf: window.AsOf, RuleVersion: buildingenergy.DimensionRuleVersion,
		ClassificationRuleVersion: report.ClassificationRuleVersion, CatalogVersion: catalog.Meta.Version, TopologyVersion: topology.Meta.Version,
		Rows: make([]f07Row, 0, len(report.Rows)),
	}
	for _, row := range report.Rows {
		data.Rows = append(data.Rows, f07Row{
			ID: row.Object.ID, Name: f.objects[row.Object].name, Status: row.Status, Rank: row.Rank,
			f06Value: f06Measure(row.Value), Sources: f.projectSources(row.Evidence),
		})
	}
	return data, nil
}

// The projection omits raw readings, weights and unscoped totals. Even
// shared-channel quantities here are already allocated to the selected row.
func (f dimensionsFixture) projectSources(evidence []buildingenergy.ClassifiedIntervalEvidence) []f07Source {
	type sourceKey struct {
		point                                buildingenergy.PointKey
		generation                           uint64
		profile, object, category, meterRule string
	}
	sources := make([]f07Source, 0)
	positions := make(map[sourceKey]int)
	project := f.buildings[0].project.Key
	window := f.buildings[0].window
	for _, e := range evidence {
		building, allowed := f.pointBuildings[e.Point.Key]
		if !allowed || e.Point.Project != project || e.Trace.From.Before(window.From) || e.Trace.To.After(window.To) {
			continue
		}
		key := sourceKey{e.Point.Key, e.MappingGeneration, e.ProfileVersion, e.ObjectBindingVersion, e.CategoryBindingVersion, e.MeterRuleVersion}
		i, exists := positions[key]
		if !exists {
			i = len(sources)
			positions[key] = i
			sources = append(sources, f07Source{
				Channel: building.channel(e.Point.Key).ID, ProfileVersion: e.ProfileVersion, ObjectBindingVersion: e.ObjectBindingVersion,
				CategoryBindingVersion: e.CategoryBindingVersion, MappingGeneration: e.MappingGeneration, MeterRuleVersion: e.MeterRuleVersion,
			})
		}
		source := &sources[i]
		source.Observations++
		switch {
		case e.Trace.Usage != nil:
			source.Valid++
		case e.Trace.Quality == buildingenergy.Missing:
			source.Missing++
		default:
			source.Suspect++
		}
		addF06Usage(&source.Usage, e.Trace.Usage)
	}
	return sources
}
