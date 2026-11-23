package buildingenergy

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func versionFixture(start time.Time) VersionMeta {
	return VersionMeta{Version: "test-v1", Effective: Range{From: start}, Source: SourceRef{System: "fixture", ID: "approval-1"}}
}

func pointFixture(start time.Time, energy EnergyType, kind MeasurementKind, unit string) PointProfile {
	return PointProfile{
		Point: PointRef{Project: ProjectKey{TenantID: "demo-tenant", ProjectID: "demo-project"},
			Key: PointKey{TerminalID: "test-meter", ItemCode: string(energy)}},
		Kind: kind, Energy: energy, Unit: unit, Multiplier: 1, Cadence: time.Hour, Meta: versionFixture(start),
	}
}

func fixtureWindow(from, to time.Time, grain Granularity) Window {
	return Window{Range: Range{From: from, To: to}, Timezone: from.Location().String(), Granularity: grain, AsOf: to.Add(time.Hour)}
}

func readFixture(at time.Time, value float64, id string) Sample {
	return Sample{At: at, ReceivedAt: at, Value: value, Epoch: "epoch-a", Quality: Normal, Source: SourceRef{System: "fixture", ID: id}}
}

func intervalFixture(at time.Time, value float64, id string) Sample {
	sample := readFixture(at, value, id)
	sample.End, sample.ReceivedAt, sample.Epoch = at.Add(time.Hour), at.Add(time.Hour), ""
	return sample
}

func requireUsage(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("usage: got nil, want %g", want)
	}
	if *got != want {
		t.Fatalf("usage: got %g, want %g", *got, want)
	}
}

func TestAT01CumulativeMultiplierAndProvenance(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	samples := []Sample{readFixture(from, 1000, "start"), readFixture(to, 1040, "end")}
	for _, tc := range []struct{ multiplier, want float64 }{{1, 40}, {2, 80}} {
		profile := pointFixture(from, Electricity, Cumulative, "kWh")
		profile.Multiplier = tc.multiplier
		result, err := Calculate(MeteringData{Profile: profile, Samples: samples}, fixtureWindow(from, to, Hour))
		if err != nil {
			t.Fatal(err)
		}
		requireUsage(t, result.Usage, tc.want)
		if result.Unit != "kWh" || result.Profile.Meta != profile.Meta || result.Profile.Multiplier != tc.multiplier ||
			result.RuleVersion == "" || result.Valid != 1 || result.CoverageRate() != 1 || len(result.Intervals) != 1 {
			t.Fatalf("missing trace or coverage: %+v", result)
		}
		trace := result.Intervals[0]
		if !reflect.DeepEqual(trace.Sources, []SourceRef{samples[1].Source, samples[0].Source}) ||
			trace.Quality != Normal || trace.Reason != "" || !trace.From.Equal(from) || !trace.To.Equal(to) {
			t.Fatalf("invalid endpoint provenance: %+v", trace)
		}
		t.Logf("1000 → 1040 × %g: actual %g kWh, coverage %g", tc.multiplier, *result.Usage, result.CoverageRate())
	}
}

func TestAT03LifecycleAndQualityReasons(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(h int) time.Time { return from.Add(time.Duration(h) * time.Hour) }
	profile := pointFixture(from, Electricity, Cumulative, "kWh")
	profile.Modulus = 1000
	samples := []Sample{
		readFixture(at(0), 990, "a"), readFixture(at(1), 5, "roll"), readFixture(at(2), 2, "negative"),
		readFixture(at(3), 400, "replacement"), readFixture(at(4), 1, "reset"),
		readFixture(at(5), 3, "recover"),
	}
	samples[1].Rollover = true
	samples[3].Epoch = "epoch-b"
	samples[4].Epoch, samples[5].Epoch = "epoch-c", "epoch-c"
	samples[5].Quality = Corrected
	got, err := Calculate(MeteringData{Profile: profile, Samples: samples}, fixtureWindow(from, at(5), Hour))
	if err != nil {
		t.Fatal(err)
	}
	requireUsage(t, got.Usage, 17)
	wantReasons := []Reason{"", NegativeJump, EpochChange, EpochChange, ""}
	for i, want := range wantReasons {
		if got.Intervals[i].Reason != want {
			t.Errorf("hour %d reason=%q, want %q", i, got.Intervals[i].Reason, want)
		}
	}
	if got.Suspect != 3 || got.Valid != 2 || got.Intervals[4].Quality != Corrected || got.Buckets[2].Usage != nil {
		t.Fatalf("lifecycle quality: %+v", got)
	}
	profile.Modulus = 0
	unconfirmed, err := Calculate(MeteringData{Profile: profile, Samples: samples[:2]}, fixtureWindow(from, at(1), Hour))
	if err != nil || unconfirmed.Usage != nil || unconfirmed.Intervals[0].Reason != UnconfirmedRollover {
		t.Fatalf("unconfirmed rollover was accepted: %+v (%v)", unconfirmed, err)
	}
	t.Logf("confirmed rollover 15 + recovered 2 = %g kWh; %d suspect lifecycle intervals", *got.Usage, got.Suspect)
}

func TestP0LateReplayReplacesMissingSlotsAndIsDeterministic(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(h int) time.Time { return from.Add(time.Duration(h) * time.Hour) }
	window := fixtureWindow(from, at(3), Hour)
	late := readFixture(at(1), 105, "late")
	late.ReceivedAt = at(5)
	duplicate := late
	duplicate.Source.ID = "duplicate"
	data := MeteringData{Profile: pointFixture(from, Electricity, Cumulative, "kWh"),
		Samples: []Sample{readFixture(at(3), 120, "end"), late, readFixture(at(2), 110, "third"), duplicate, readFixture(at(0), 100, "start"), late}}
	before, err := Calculate(data, window)
	if err != nil {
		t.Fatal(err)
	}
	requireUsage(t, before.Usage, 10)
	if before.Missing != 2 || before.Valid != 1 || before.Intervals[0].Reason != MissingSample {
		t.Fatalf("late input leaked into prior cutoff: %+v", before)
	}
	window.AsOf = at(5)
	after, err := Calculate(data, window)
	if err != nil {
		t.Fatal(err)
	}
	requireUsage(t, after.Usage, 20)
	if after.Missing != 0 || after.Valid != 3 || len(after.Intervals[0].Sources) != 3 {
		t.Fatalf("incorrect late recomputation: %+v", after)
	}
	for i, j := 0, len(data.Samples)-1; i < j; i, j = i+1, j-1 {
		data.Samples[i], data.Samples[j] = data.Samples[j], data.Samples[i]
	}
	repeated, err := Calculate(data, window)
	if err != nil || !reflect.DeepEqual(after, repeated) {
		t.Fatalf("replay changed results or provenance: %v", err)
	}
	requireUsage(t, before.Usage, 10)

	conflict := late
	conflict.Source.ID, conflict.Value = "conflict", 106
	data.Samples = append(data.Samples, conflict)
	disputed, err := Calculate(data, window)
	if err != nil {
		t.Fatal(err)
	}
	requireUsage(t, disputed.Usage, 10)
	if disputed.Suspect != 2 || disputed.Intervals[0].Reason != ConflictingSample || disputed.Intervals[1].Reason != ConflictingSample {
		t.Fatalf("conflicting endpoint chosen silently: %+v", disputed)
	}
	t.Logf("before/after late arrival %g → %g kWh; after conflicting duplicate %g kWh, %d suspect; replay identical", *before.Usage, *after.Usage, *disputed.Usage, disputed.Suspect)
}

func TestP0IntervalQualityPreservesZeroAndNeverProrates(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	window := fixtureWindow(from, from.Add(4*time.Hour), Hour)
	samples := []Sample{intervalFixture(from, 0, "zero"), intervalFixture(from.Add(time.Hour), 30, "manual"),
		intervalFixture(from.Add(2*time.Hour), 90, "estimated")}
	samples[1].Quality, samples[2].Quality = Manual, Estimated
	data := MeteringData{Profile: pointFixture(from, Water, Interval, "L"), Samples: samples}
	got, err := Calculate(data, window)
	if err != nil {
		t.Fatal(err)
	}
	requireUsage(t, got.Buckets[0].Usage, 0)
	requireUsage(t, got.Usage, 0.03)
	if got.Buckets[2].Usage != nil || got.Buckets[3].Usage != nil || got.Valid != 2 || got.Suspect != 1 || got.Missing != 1 ||
		got.CoverageRate() != 0.5 || got.Intervals[1].Quality != Manual || got.Intervals[2].Quality != Estimated || got.Unit != "m³" {
		t.Fatalf("wrong interval zero, provenance, or quality: %+v", got)
	}
	samples[2].End = samples[2].End.Add(time.Hour)
	samples[2].ReceivedAt = samples[2].End
	if _, err := Calculate(data, window); !errors.Is(err, ErrInvalidSample) {
		t.Fatalf("multi-hour interval was allocated without a policy: %v", err)
	}
	t.Logf("known zero 0; manual 30 L = %g m³, one estimated excluded, one missing, coverage %.0f%%", *got.Usage, got.CoverageRate()*100)
}

func TestAT05PhysicalUnitsAndApprovedCoal(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	window := fixtureWindow(from, from.Add(time.Hour), Hour)
	var results []Result
	for _, tc := range []struct {
		energy      EnergyType
		input, unit string
		value, want float64
	}{
		{Electricity, "Wh", "kWh", 2000, 2},
		{Water, "L", "m³", 3000, 3},
		{Heat, "MJ", "GJ", 4000, 4},
	} {
		data := MeteringData{Profile: pointFixture(from, tc.energy, Interval, tc.input), Samples: []Sample{intervalFixture(from, tc.value, "quantity")}}
		got, err := Calculate(data, window)
		if err != nil {
			t.Fatal(err)
		}
		requireUsage(t, got.Usage, tc.want)
		if got.Unit != tc.unit {
			t.Fatalf("unit %q, want %q", got.Unit, tc.unit)
		}
		results = append(results, got)
	}
	project := results[0].Profile.Point.Project
	electric := CoalFactor{Project: project, Energy: Electricity, Unit: "kWh", KgcePerUnit: 0.5, Approved: true, Meta: versionFixture(from)}
	thermal := CoalFactor{Project: project, Energy: Heat, Unit: "GJ", KgcePerUnit: 2, Approved: true, Meta: versionFixture(from)}
	noCoal, err := FoldCoal(results, []CoalFactor{electric})
	if err != nil || noCoal.Kilograms != nil || !reflect.DeepEqual(noCoal.Unavailable, []EnergyType{Heat}) {
		t.Fatalf("missing coefficient produced a mixed total: %+v (%v)", noCoal, err)
	}
	coal, err := FoldCoal(results, []CoalFactor{electric, thermal})
	if err != nil {
		t.Fatal(err)
	}
	requireUsage(t, coal.Kilograms, 9)
	if len(coal.Factors) != 2 {
		t.Fatalf("missing factor evidence: %+v", coal)
	}
	expired := thermal
	expired.Meta.Effective.To = from.Add(30 * time.Minute)
	stale, err := FoldCoal(results, []CoalFactor{electric, expired})
	if err != nil || stale.Kilograms != nil {
		t.Fatalf("a factor whose validity covered only part of the window was used: %+v (%v)", stale, err)
	}
	if _, err := FoldCoal(results, []CoalFactor{electric, thermal, thermal}); err == nil {
		t.Fatal("multiple eligible coefficient versions produced an ambiguous result")
	}
	thermal.Approved = false
	unapproved, err := FoldCoal(results, []CoalFactor{electric, thermal})
	if err != nil || unapproved.Kilograms != nil {
		t.Fatalf("unapproved factor was used: %+v (%v)", unapproved, err)
	}
	foreign := slices.Clone(results)
	foreign[1].Profile.Point.Project.ProjectID = "other-project"
	if _, err := FoldCoal(foreign, []CoalFactor{electric, thermal}); err == nil {
		t.Fatal("coal aggregate crossed a project boundary")
	}
	point := pointFixture(from, Electricity, Interval, "A")
	if _, err := Calculate(MeteringData{Profile: point}, window); !errors.Is(err, ErrInvalidUnit) {
		t.Fatalf("current accepted as energy: %v", err)
	}
	point = pointFixture(from, Gas, Interval, "m³")
	if _, err := Calculate(MeteringData{Profile: point}, window); !errors.Is(err, ErrInvalidUnit) {
		t.Fatalf("uncalibrated gas volume treated as normal volume: %v", err)
	}
	t.Logf("2 kWh, 3 m³ water, 4 GJ; missing factor: nil; approved factors: %g kgce", *coal.Kilograms)
}

func TestP0RejectsUnconfirmedProfileAndUntraceableSamples(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	window := fixtureWindow(from, from.Add(time.Hour), Hour)
	data := MeteringData{Profile: pointFixture(from, Electricity, Cumulative, "kWh"),
		Samples: []Sample{readFixture(from, 1000, "start"), readFixture(window.To, 1040, "end")}}
	data.Profile.Meta.Effective.To = from.Add(30 * time.Minute)
	if _, err := Calculate(data, window); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("profile expired in the middle of the interval: %v", err)
	}
	data.Profile.Meta.Effective.To = window.To
	got, err := Calculate(data, window)
	if err != nil {
		t.Fatal(err)
	}
	requireUsage(t, got.Usage, 40)
	data.Samples[1].Source.ID = ""
	if _, err := Calculate(data, window); !errors.Is(err, ErrInvalidSample) {
		t.Fatalf("untraceable sample affected usage: %v", err)
	}
	data.Samples[1].Source.ID = "end"
	data.Samples[1].Epoch = ""
	if _, err := Calculate(data, window); !errors.Is(err, ErrInvalidSample) {
		t.Fatalf("ambiguous register epoch affected usage: %v", err)
	}
	window.Timezone = strings.Repeat("invalid/", 3)
	if _, err := Calculate(data, window); err == nil {
		t.Fatal("unknown project timezone was silently accepted")
	}
}
