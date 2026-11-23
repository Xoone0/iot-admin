package main

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

const f06FixtureSystem = "f06-synthetic"
const f06TopologySource = "manifest-v1-isolated-circuits"

type f06Meter struct {
	key           buildingenergy.PointKey
	name          string
	base          float64
	category      string
	categoryShare int
}

type f06Fixture struct {
	project   buildingenergy.Project
	object    buildingenergy.ObjectKey
	energy    buildingenergy.EnergyType
	unit      string
	window    buildingenergy.Window
	effective buildingenergy.Range
	nodes     []buildingenergy.CategoryNode
	meters    []f06Meter
}

func newF06Fixture(building string, energy buildingenergy.EnergyType, days int) (f06Fixture, error) {
	if building != "office-a" && building != "office-b" || days != 7 && days != 14 && days != 30 {
		return f06Fixture{}, buildingenergy.ErrInvalidScope
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return f06Fixture{}, err
	}
	end := time.Date(2026, 11, 1, 0, 0, 0, 0, location)
	effective := buildingenergy.Range{From: end.AddDate(0, -1, 0), To: end}
	f := f06Fixture{
		project: buildingenergy.Project{
			Key:      buildingenergy.ProjectKey{TenantID: "preview", ProjectID: "f06-demo"},
			Timezone: location.String(),
			Meta:     f06Meta("directory-v1", "demo-directory", effective),
		},
		object: buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: building},
		energy: energy, effective: effective,
		window: buildingenergy.Window{
			Range: buildingenergy.Range{From: end.AddDate(0, 0, -days), To: end}, Timezone: location.String(),
			AsOf: end, Granularity: buildingenergy.Day,
		},
	}
	var categoryNames, meterNames [3]string
	ratio := 1.0
	switch energy {
	case buildingenergy.Electricity:
		f.unit = "kWh"
		categoryNames = [3]string{"照明与插座", "室内照明", "空调与通风"}
		meterNames = [3]string{"室内照明回路", "通风机组回路", "备用回路"}
	case buildingenergy.Water:
		f.unit = "m³"
		ratio = .25
		categoryNames = [3]string{"生活用水", "卫生间用水", "设施用水"}
		meterNames = [3]string{"卫生间支路", "设施清洁支路", "备用支路"}
	default:
		return f06Fixture{}, buildingenergy.ErrInvalidScope
	}
	if building == "office-b" {
		ratio *= 2
	}
	f.nodes = []buildingenergy.CategoryNode{
		{ID: "01-primary", Name: categoryNames[0], Energy: energy},
		{ID: "01-interior", ParentID: "01-primary", Name: categoryNames[1], Energy: energy},
		{ID: "02-secondary", Name: categoryNames[2], Energy: energy},
	}
	f.meters = []f06Meter{
		{key: f.pointKey("primary"), name: meterNames[0], base: 10 * ratio, category: "01-interior", categoryShare: buildingenergy.FullWeightPPM},
		{key: f.pointKey("secondary"), name: meterNames[1], base: 4 * ratio, category: "02-secondary", categoryShare: 500_000},
		{key: f.pointKey("reserve"), name: meterNames[2], base: 2 * ratio},
	}
	return f, nil
}

func f06Meta(version, id string, effective buildingenergy.Range) buildingenergy.VersionMeta {
	return buildingenergy.VersionMeta{Version: version, Source: buildingenergy.SourceRef{System: f06FixtureSystem, ID: id}, Effective: effective}
}

func (f f06Fixture) pointKey(suffix string) buildingenergy.PointKey {
	return buildingenergy.PointKey{TerminalID: f.object.ID + "-" + string(f.energy) + "-" + suffix, ItemCode: "interval-usage"}
}

func (f f06Fixture) channel(point buildingenergy.PointKey) buildingenergy.SourceRef {
	return buildingenergy.SourceRef{System: f06FixtureSystem, ID: point.TerminalID}
}

func (f f06Fixture) scope() buildingenergy.Scope {
	scope := buildingenergy.Scope{Project: f.project.Key, Period: f.window.Range, Objects: []buildingenergy.ObjectKey{f.object}}
	for _, meter := range f.meters {
		scope.Points = append(scope.Points, meter.key)
	}
	return scope
}

// The local preview grants only the registered synthetic inputs. The
// subject is minted in-process and request headers cannot change it.
func (f06Fixture) AuthenticatedSubject(context.Context) (buildingenergy.Subject, error) {
	return buildingenergy.Subject{Issuer: f06FixtureSystem, ID: "local-viewer"}, nil
}

func (f f06Fixture) Require(_ context.Context, subject buildingenergy.Subject, action buildingenergy.Action, requested buildingenergy.Scope) error {
	allowed := f.scope()
	local := buildingenergy.Subject{Issuer: f06FixtureSystem, ID: "local-viewer"}
	if subject != local || action != buildingenergy.ReadUsage || requested.Project != allowed.Project ||
		!requested.Period.From.Equal(allowed.Period.From) || !requested.Period.To.Equal(allowed.Period.To) ||
		!slices.Equal(requested.Objects, allowed.Objects) || !slices.Equal(requested.Points, allowed.Points) {
		return buildingenergy.ErrAccessDenied
	}
	return nil
}

func (f f06Fixture) Project(_ context.Context, key buildingenergy.ProjectKey, _ time.Time) (buildingenergy.Project, error) {
	if key != f.project.Key {
		return buildingenergy.Project{}, buildingenergy.ErrCatalogScope
	}
	return f.project, nil
}

func (f f06Fixture) Object(_ context.Context, project buildingenergy.ProjectKey, key buildingenergy.ObjectKey, _ time.Time) (buildingenergy.Object, error) {
	if project != f.project.Key || key != f.object {
		return buildingenergy.Object{}, buildingenergy.ErrCatalogScope
	}
	return buildingenergy.Object{Project: project, Key: key, BuildingID: key.ID, Meta: f.project.Meta}, nil
}

type f06Certificate struct {
	object   buildingenergy.ObjectKey
	energy   buildingenergy.EnergyType
	snapshot buildingenergy.CategoryTopologySnapshot
}

// The versioned sample manifest assigns each of these three synthetic series
// to a separate exclusive circuit. This premise is defined by the fixture,
// independently of the requested point list and generated observations.
func (f f06Fixture) syntheticCertificate() f06Certificate {
	t := buildingenergy.CategoryTopologySnapshot{
		Project: f.project.Key, Meta: f06Meta("topology-v1", f06TopologySource, f.effective),
	}
	for _, circuit := range []string{"primary", "secondary", "reserve"} {
		key := f.pointKey(circuit)
		t.Boundaries = append(t.Boundaries, []buildingenergy.CategoryBoundaryContributor{{
			BoundaryContributor: buildingenergy.BoundaryContributor{Point: buildingenergy.PointRef{Project: f.project.Key, Key: key}, Role: buildingenergy.BoundaryTotal},
			MappingGeneration:   1, Channel: f.channel(key),
		}})
	}
	return f06Certificate{object: f.object, energy: f.energy, snapshot: t}
}

func (f f06Fixture) checkedTopology(c f06Certificate) (buildingenergy.CategoryTopologySnapshot, error) {
	t := c.snapshot
	scope := f.scope()
	bad := func() (buildingenergy.CategoryTopologySnapshot, error) {
		return buildingenergy.CategoryTopologySnapshot{}, fmt.Errorf("%w: physical boundary certificate does not cover the sample scope", buildingenergy.ErrInvalidClassification)
	}
	if t.Project != scope.Project || c.object != f.object || c.energy != f.energy ||
		t.Meta.Source != (buildingenergy.SourceRef{System: f06FixtureSystem, ID: f06TopologySource}) ||
		t.Meta.Version != "topology-v1" || t.Meta.Effective.From.IsZero() ||
		t.Meta.Effective.From.After(scope.Period.From) || t.Meta.Effective.To.Before(scope.Period.To) {
		return bad()
	}
	var certified []buildingenergy.PointKey
	for _, group := range t.Boundaries {
		if len(group) != 1 {
			return bad()
		}
		item := group[0]
		if item.Point.Project != scope.Project || item.Role != buildingenergy.BoundaryTotal || item.MappingGeneration != 1 || item.Channel != f.channel(item.Point.Key) {
			return bad()
		}
		certified = append(certified, item.Point.Key)
	}
	if !slices.Equal(certified, scope.Points) {
		return bad()
	}
	return t, nil
}

func (f f06Fixture) profile(point buildingenergy.PointRef) buildingenergy.PointProfile {
	return buildingenergy.PointProfile{
		Point: point, Kind: buildingenergy.Interval, Energy: f.energy, Unit: f.unit, Multiplier: 1,
		Cadence: time.Hour, Meta: f06Meta("profile-v1", point.Key.TerminalID, f.effective),
	}
}

func (f f06Fixture) mapping(point buildingenergy.PointRef) buildingenergy.MappingTimeline {
	return buildingenergy.MappingTimeline{
		Point: point, Generation: 1,
		Profiles: []buildingenergy.ProfileRevision{{Profile: f.profile(point), Channel: f.channel(point.Key)}},
		Bindings: []buildingenergy.BindingRevision{{
			View: buildingenergy.Building, Meta: f06Meta("building-binding-v1", f.object.ID, f.effective),
			Allocations: []buildingenergy.Allocation{{Target: f.object, WeightPPM: buildingenergy.FullWeightPPM}},
		}},
	}
}

func (f f06Fixture) classification() buildingenergy.ClassificationSnapshot {
	c := buildingenergy.ClassificationSnapshot{
		Project: f.project.Key, Meta: f06Meta("category-catalog-v1", "demo-catalog", f.effective), Nodes: f.nodes,
	}
	for _, meter := range f.meters {
		if meter.category == "" {
			continue
		}
		c.Bindings = append(c.Bindings, buildingenergy.CategoryBinding{
			Point: buildingenergy.PointRef{Project: f.project.Key, Key: meter.key},
			Meta:  f06Meta("category-binding-v1", meter.key.TerminalID, f.effective),
			Allocations: []buildingenergy.CategoryAllocation{{
				CategoryID: meter.category, WeightPPM: meter.categoryShare,
			}},
		})
	}
	return c
}

func (f f06Fixture) readMeter(point buildingenergy.PointRef, window buildingenergy.Window) buildingenergy.MeteringData {
	return f.generateMeter(point, window, octoberSample)
}

func octoberSample(start time.Time, base float64) (float64, buildingenergy.Quality) {
	switch start.Day() {
	case 29:
		return 0, buildingenergy.Normal
	case 30:
		return 0, buildingenergy.Missing
	case 31:
		return 987654, buildingenergy.Suspect
	default:
		return base * float64(start.Day()%4+1), buildingenergy.Normal
	}
}

func (f f06Fixture) generateMeter(point buildingenergy.PointRef, window buildingenergy.Window, sample func(time.Time, float64) (float64, buildingenergy.Quality)) buildingenergy.MeteringData {
	if point.Project != f.project.Key {
		return buildingenergy.MeteringData{}
	}
	for _, meter := range f.meters {
		if meter.key != point.Key {
			continue
		}
		data := buildingenergy.MeteringData{Profile: f.profile(point)}
		for start := window.From; start.Before(window.To); start = start.Add(time.Hour) {
			end := start.Add(time.Hour)
			value, quality := sample(start, meter.base)
			if quality == buildingenergy.Missing {
				continue
			}
			data.Samples = append(data.Samples, buildingenergy.Sample{
				At: start, End: end, ReceivedAt: end, Value: value, Quality: quality,
				Source: buildingenergy.SourceRef{System: f06FixtureSystem, ID: "raw-reading/" + point.Key.TerminalID + "/" + start.Format(time.RFC3339)},
			})
		}
		return data
	}
	return buildingenergy.MeteringData{}
}
