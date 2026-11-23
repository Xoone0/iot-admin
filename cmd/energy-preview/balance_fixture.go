package main

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

const (
	balanceSystem         = "f09-synthetic"
	balanceTopologySource = "synthetic-single-feed-east-west-common-v1"
)

type balanceSample struct {
	project    buildingenergy.Project
	object     buildingenergy.ObjectKey
	window     buildingenergy.Window
	scenario   string
	topology   buildingenergy.BalanceTopology
	unmetered  []string
	dailyUsage map[buildingenergy.PointKey]int
	perturbed  buildingenergy.PointKey
}

func newBalanceSample(rawQuery string) (balanceSample, error) {
	// Use the F06 preview's validated project-local calendar and selectors.
	// The physical scope and all metering records are authored separately below.
	selection, scenario, err := selectBusinessUsage(rawQuery, "scenario")
	if err != nil {
		return balanceSample{}, err
	}
	switch scenario {
	case "standard", "negative", "missing", "suspect", "replacement", "time-mismatch":
	default:
		return balanceSample{}, buildingenergy.ErrInvalidScope
	}

	f := balanceSample{
		project: buildingenergy.Project{
			Key:      buildingenergy.ProjectKey{TenantID: "preview", ProjectID: "f09-demo"},
			Timezone: selection.project.Timezone,
			Meta:     balanceMeta("f09-directory-v1", "synthetic-building-directory", selection.effective),
		},
		object: selection.object, window: selection.window, scenario: scenario,
	}
	// The versioned diagram premise for each standalone building-resource scope:
	//
	//   utility → parent meter → single distribution node
	//                              ├─ east meter → east region
	//                              ├─ west meter → west region
	//                              └─ common feed → common service region (unmetered)
	//
	// The regions are discrete terminal branches. There are no ties, bypasses,
	// local generation or storage. The diagram defines physical membership before
	// any observations are generated. The two child channels cannot consume from
	// one another's regions. Meter IDs do not establish that premise.
	// The common branch allows an unallocated balance; the difference is not an
	// independent observation of common consumption and cannot establish losses.
	region := []string{"东区", "西区", "公共支路"}
	f.unmetered = []string{region[2]}
	f.topology = buildingenergy.BalanceTopology{
		Project: f.project.Key, Object: f.object, Energy: selection.energy, Unit: selection.unit,
		Meta:   balanceMeta("f09-topology-v1", balanceTopologySource, selection.effective),
		Parent: f.meter(selection.energy, "main", "建筑总表", region),
		Children: []buildingenergy.BalanceMeter{
			f.meter(selection.energy, "east", "东区分表", region[:1]),
			f.meter(selection.energy, "west", "西区分表", region[1:2]),
		},
	}
	for index := range f.topology.Children {
		f.topology.Children[index].Parent = f.topology.Parent.Point
	}

	main, east, west := 100, 70, 20
	if selection.energy == buildingenergy.Water {
		main, east, west = 40, 28, 8
	}
	if scenario == "negative" {
		// Independent recorded usage can disagree even with an intact diagram.
		// The east reading is deliberately inconsistent with the parent's total.
		east = main - west + main/20
	}
	multiplier := 1
	if f.object.ID == "office-b" {
		multiplier = 2
	}
	f.perturbed = f.topology.Children[0].Point.Key
	f.dailyUsage = map[buildingenergy.PointKey]int{
		f.topology.Parent.Point.Key:      main * multiplier,
		f.topology.Children[0].Point.Key: east * multiplier,
		f.topology.Children[1].Point.Key: west * multiplier,
	}
	return f, nil
}

func balanceMeta(version, source string, effective buildingenergy.Range) buildingenergy.VersionMeta {
	return buildingenergy.VersionMeta{
		Version: version, Effective: effective, Source: buildingenergy.SourceRef{System: balanceSystem, ID: source},
	}
}

func (f balanceSample) meter(energy buildingenergy.EnergyType, suffix, name string, regions []string) buildingenergy.BalanceMeter {
	channel := strings.Join([]string{f.object.ID, string(energy), suffix}, "-")
	return buildingenergy.BalanceMeter{
		Point: buildingenergy.PointRef{
			Project: f.project.Key,
			Key:     buildingenergy.PointKey{TerminalID: channel, ItemCode: "cumulative-usage"},
		},
		Channel: buildingenergy.SourceRef{System: balanceSystem, ID: channel},
		Name:    name, Zones: slices.Clone(regions), MappingGeneration: 1,
	}
}

func (f balanceSample) meters() []buildingenergy.BalanceMeter {
	return append([]buildingenergy.BalanceMeter{f.topology.Parent}, f.topology.Children...)
}

func (f balanceSample) scope() buildingenergy.Scope {
	scope := buildingenergy.Scope{Project: f.project.Key, Period: f.window.Range, Objects: []buildingenergy.ObjectKey{f.object}}
	for _, meter := range f.meters() {
		scope.Points = append(scope.Points, meter.Point.Key)
	}
	return scope
}

func (f balanceSample) AuthenticatedSubject(context.Context) (buildingenergy.Subject, error) {
	return buildingenergy.Subject{Issuer: balanceSystem, ID: "local-viewer"}, nil
}

func (f balanceSample) Require(_ context.Context, subject buildingenergy.Subject, action buildingenergy.Action, requested buildingenergy.Scope) error {
	allowed := f.scope()
	local := buildingenergy.Subject{Issuer: balanceSystem, ID: "local-viewer"}
	if subject != local || action != buildingenergy.ReadUsage || requested.Project != allowed.Project ||
		!requested.Period.From.Equal(allowed.Period.From) || !requested.Period.To.Equal(allowed.Period.To) ||
		!slices.Equal(requested.Objects, allowed.Objects) || !slices.Equal(requested.Points, allowed.Points) {
		return buildingenergy.ErrAccessDenied
	}
	return nil
}

func (f balanceSample) Project(_ context.Context, key buildingenergy.ProjectKey, _ time.Time) (buildingenergy.Project, error) {
	if key != f.project.Key {
		return buildingenergy.Project{}, buildingenergy.ErrCatalogScope
	}
	return f.project, nil
}

func (f balanceSample) Object(_ context.Context, project buildingenergy.ProjectKey, key buildingenergy.ObjectKey, _ time.Time) (buildingenergy.Object, error) {
	if project != f.project.Key || key != f.object {
		return buildingenergy.Object{}, buildingenergy.ErrCatalogScope
	}
	return buildingenergy.Object{Project: project, Key: key, BuildingID: key.ID, Meta: f.project.Meta}, nil
}

func (f balanceSample) readMeter(meter buildingenergy.BalanceMeter, window buildingenergy.Window) (buildingenergy.MeteringData, buildingenergy.MappingTimeline) {
	profile := buildingenergy.PointProfile{
		Point: meter.Point, Kind: buildingenergy.Cumulative, Energy: f.topology.Energy, Unit: f.topology.Unit,
		Multiplier: 1, Cadence: time.Hour,
		Meta: balanceMeta("f09-profile-v1", meter.Channel.ID, f.topology.Meta.Effective),
	}
	mapping := buildingenergy.MappingTimeline{
		Point: meter.Point, Generation: meter.MappingGeneration,
		Profiles: []buildingenergy.ProfileRevision{{Profile: profile, Channel: meter.Channel, RegisterEpoch: "installed"}},
		Bindings: []buildingenergy.BindingRevision{{
			View: buildingenergy.Building, Meta: balanceMeta("f09-building-binding-v1", f.object.ID, f.topology.Meta.Effective),
			Allocations: []buildingenergy.Allocation{{Target: f.object, WeightPPM: buildingenergy.FullWeightPPM}},
		}},
	}
	data := buildingenergy.MeteringData{Profile: profile}
	register, epoch := 1000, "installed"
	dailyAmount := f.dailyUsage[meter.Point.Key]
	fault := f.window.From.Add(13 * time.Hour)
	for at := window.From; !at.After(window.To); at = at.Add(time.Hour) {
		if at.After(window.From) {
			hour := at.Add(-time.Hour).Hour()
			// Split the authored daily quantity into integer hourly increments.
			// The cumulative register drops only on a simulated epoch change.
			register += (hour+1)*dailyAmount/24 - hour*dailyAmount/24
		}
		quality := buildingenergy.Normal
		if meter.Point.Key == f.perturbed && at.Equal(fault) {
			switch f.scenario {
			case "missing":
				continue
			case "suspect":
				quality = buildingenergy.Suspect
			case "replacement":
				register, epoch = 3, "replacement"
			}
		}
		data.Samples = append(data.Samples, buildingenergy.Sample{
			At: at, ReceivedAt: at, Value: float64(register), Epoch: epoch, Quality: quality,
			Source: buildingenergy.SourceRef{
				System: balanceSystem, ID: meter.Channel.ID + "/" + strconv.FormatInt(at.Unix(), 10),
			},
		})
	}
	return data, mapping
}
