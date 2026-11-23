package main

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

type dimensionsFixture struct {
	view           buildingenergy.ObjectKind
	buildings      []f06Fixture
	objects        map[buildingenergy.ObjectKey]dimensionObject
	pointBuildings map[buildingenergy.PointKey]f06Fixture
	qualitySample  bool
}

type dimensionObject struct {
	name   string
	record buildingenergy.Object
}

func newDimensionsFixture(view buildingenergy.ObjectKind, selection string, energy buildingenergy.EnergyType, period, mode, scenario string) (dimensionsFixture, error) {
	if !slices.Contains([]buildingenergy.ObjectKind{buildingenergy.Building, buildingenergy.Space, buildingenergy.Organization}, view) ||
		!slices.Contains([]string{"all", "office-a", "office-b"}, selection) ||
		!slices.Contains([]string{"standard", "quality"}, scenario) ||
		!slices.Contains([]string{"complete", "to-date"}, mode) ||
		(mode == "to-date" && period != "day" && period != "month") {
		return dimensionsFixture{}, buildingenergy.ErrInvalidScope
	}
	days := 7
	var grain buildingenergy.Granularity
	switch period {
	case "7":
	case "14":
		days = 14
	case "30":
		days = 30
	case "hour", "day", "month":
		grain = buildingenergy.Granularity(period)
	default:
		return dimensionsFixture{}, buildingenergy.ErrInvalidWindow
	}
	f := dimensionsFixture{
		view: view, qualitySample: scenario == "quality",
		objects:        make(map[buildingenergy.ObjectKey]dimensionObject),
		pointBuildings: make(map[buildingenergy.PointKey]f06Fixture),
	}
	for _, building := range []struct{ id, name string }{{"office-a", "办公楼 A"}, {"office-b", "办公楼 B"}} {
		if selection != "all" && selection != building.id {
			continue
		}
		seed, err := newF06Fixture(building.id, energy, days)
		if err != nil {
			return dimensionsFixture{}, err
		}
		if grain != "" {
			seed, _, err = planTrendFixture(seed, grain, mode == "to-date")
			if err != nil {
				return dimensionsFixture{}, err
			}
		}
		f.buildings = append(f.buildings, seed)
		for _, meter := range seed.meters {
			f.pointBuildings[meter.key] = seed
		}
		switch view {
		case buildingenergy.Building:
			f.addObject(seed, seed.object, building.name)
		case buildingenergy.Space:
			f.addObject(seed, spaceKey(seed, "east"), building.name+" · 东区")
			f.addObject(seed, spaceKey(seed, "west"), building.name+" · 西区")
		case buildingenergy.Organization:
			f.addObject(seed, organizationKey("operations"), "运维组")
			f.addObject(seed, organizationKey("occupants"), "办公组")
		}
	}
	return f, nil
}

func (f dimensionsFixture) addObject(building f06Fixture, key buildingenergy.ObjectKey, name string) {
	record := buildingenergy.Object{Project: building.project.Key, Key: key, Meta: building.project.Meta}
	if key.Kind != buildingenergy.Organization {
		record.BuildingID = building.object.ID
	}
	if key.Kind == buildingenergy.Space {
		record.Parent = building.object
	}
	f.objects[key] = dimensionObject{name: name, record: record}
}

func spaceKey(building f06Fixture, section string) buildingenergy.ObjectKey {
	return buildingenergy.ObjectKey{Kind: buildingenergy.Space, ID: building.object.ID + "-" + section}
}

func organizationKey(key string) buildingenergy.ObjectKey {
	return buildingenergy.ObjectKey{Kind: buildingenergy.Organization, ID: key}
}

func dimensionMapping(building f06Fixture, point buildingenergy.PointRef) buildingenergy.MappingTimeline {
	mapping := building.mapping(point)
	var spaces, organizations []buildingenergy.Allocation
	east, west := spaceKey(building, "east"), spaceKey(building, "west")
	operations, occupants := organizationKey("operations"), organizationKey("occupants")
	switch point.Key {
	case building.pointKey("primary"):
		spaces = []buildingenergy.Allocation{{Target: east, WeightPPM: buildingenergy.FullWeightPPM}}
		organizations = []buildingenergy.Allocation{{Target: operations, WeightPPM: buildingenergy.FullWeightPPM}}
	case building.pointKey("secondary"):
		spaces = []buildingenergy.Allocation{{Target: west, WeightPPM: buildingenergy.FullWeightPPM}}
		organizations = []buildingenergy.Allocation{{Target: occupants, WeightPPM: buildingenergy.FullWeightPPM}}
	case building.pointKey("reserve"):
		spaces = []buildingenergy.Allocation{{Target: east, WeightPPM: 500_000}, {Target: west, WeightPPM: 500_000}}
		organizations = []buildingenergy.Allocation{{Target: operations, WeightPPM: 250_000}, {Target: occupants, WeightPPM: 750_000}}
	}
	mapping.Bindings = append(mapping.Bindings,
		buildingenergy.BindingRevision{View: buildingenergy.Space, Meta: f06Meta("space-binding-v1", point.Key.TerminalID, building.effective), Allocations: spaces},
		buildingenergy.BindingRevision{View: buildingenergy.Organization, Meta: f06Meta("organization-binding-v1", point.Key.TerminalID, building.effective), Allocations: organizations},
	)
	return mapping
}

func (f dimensionsFixture) scope() buildingenergy.Scope {
	s := buildingenergy.Scope{Project: f.buildings[0].project.Key, Period: f.buildings[0].window.Range}
	for key := range f.objects {
		s.Objects = append(s.Objects, key)
	}
	slices.SortFunc(s.Objects, func(a, b buildingenergy.ObjectKey) int { return cmp.Compare(a.ID, b.ID) })
	for _, building := range f.buildings {
		s.Points = append(s.Points, building.scope().Points...)
	}
	return s
}

func (f dimensionsFixture) AuthenticatedSubject(ctx context.Context) (buildingenergy.Subject, error) {
	return f.buildings[0].AuthenticatedSubject(ctx)
}

func (f dimensionsFixture) Require(ctx context.Context, subject buildingenergy.Subject, action buildingenergy.Action, requested buildingenergy.Scope) error {
	allowed := f.scope()
	local, err := f.AuthenticatedSubject(ctx)
	if err != nil || subject != local || action != buildingenergy.ReadUsage || requested.Project != allowed.Project ||
		!requested.Period.From.Equal(allowed.Period.From) || !requested.Period.To.Equal(allowed.Period.To) ||
		!slices.Equal(requested.Objects, allowed.Objects) || !slices.Equal(requested.Points, allowed.Points) {
		return buildingenergy.ErrAccessDenied
	}
	return nil
}

func (f dimensionsFixture) Project(ctx context.Context, project buildingenergy.ProjectKey, at time.Time) (buildingenergy.Project, error) {
	return f.buildings[0].Project(ctx, project, at)
}

func (f dimensionsFixture) Object(_ context.Context, project buildingenergy.ProjectKey, key buildingenergy.ObjectKey, _ time.Time) (buildingenergy.Object, error) {
	object, found := f.objects[key]
	if !found || object.record.Project != project {
		return buildingenergy.Object{}, buildingenergy.ErrCatalogScope
	}
	return object.record, nil
}

func (f dimensionsFixture) snapshots() (buildingenergy.ClassificationSnapshot, buildingenergy.CategoryTopologySnapshot, error) {
	catalog := f.buildings[0].classification()
	topology := buildingenergy.CategoryTopologySnapshot{Project: catalog.Project}
	catalog.Bindings = nil
	for _, building := range f.buildings {
		verified, err := building.checkedTopology(building.syntheticCertificate())
		if err != nil {
			return buildingenergy.ClassificationSnapshot{}, buildingenergy.CategoryTopologySnapshot{}, err
		}
		catalog.Bindings = append(catalog.Bindings, building.classification().Bindings...)
		topology.Meta = verified.Meta
		topology.Boundaries = append(topology.Boundaries, verified.Boundaries...)
	}
	if len(f.buildings) > 1 {
		// This fixture defines two independent supply networks under the
		// combined manifest. The individual channel IDs prove no such relation.
		topology.Meta = f06Meta("combined-topology-v1", "synthetic-two-independent-building-networks", f.buildings[0].effective)
	}
	return catalog, topology, nil
}
