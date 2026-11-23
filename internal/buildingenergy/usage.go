// Package buildingenergy computes usage from explicitly configured metering data.
package buildingenergy

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

var (
	ErrUnsupportedMeasurement = errors.New("unsupported energy measurement")
	ErrInvalidProfile         = errors.New("invalid energy point profile")
	ErrInvalidSample          = errors.New("invalid energy sample")
)

const calculationVersion = "m1-a-v1"

type Quality string

const (
	Normal    Quality = "normal"
	Missing   Quality = "missing"
	Suspect   Quality = "suspect"
	Manual    Quality = "manual"
	Estimated Quality = "estimated"
	Corrected Quality = "corrected"
)

func (quality Quality) valid() bool {
	switch quality {
	case Normal, Missing, Suspect, Manual, Estimated, Corrected:
		return true
	default:
		return false
	}
}

func (quality Quality) usable() bool {
	return quality == Normal || quality == Manual || quality == Corrected
}

type Sample struct {
	At, End    time.Time // End is present only for a published interval quantity.
	ReceivedAt time.Time // First visibility of this source revision.
	Value      float64   // Raw register for cumulative points, quantity for interval points.
	Epoch      string    // Confirmed register generation for cumulative points.
	Rollover   bool      // Confirms one wrap since the immediately preceding scheduled reading.
	Quality    Quality
	Source     SourceRef
}

type Coverage struct {
	Expected int `json:"expected"`
	Valid    int `json:"valid"`
	Missing  int `json:"missing"`
	Suspect  int `json:"suspect"`
}

func (c Coverage) CoverageRate() float64 {
	if c.Expected == 0 {
		return 0
	}
	return float64(c.Valid) / float64(c.Expected)
}

type Reason string

const (
	MissingSample       Reason = "missing_sample"
	ConflictingSample   Reason = "conflicting_sample"
	SourceQuality       Reason = "source_quality"
	InvalidValue        Reason = "invalid_value"
	EpochChange         Reason = "epoch_change"
	NegativeJump        Reason = "negative_jump"
	UnconfirmedRollover Reason = "unconfirmed_rollover"
)

type IntervalTrace struct {
	Range
	Usage   *float64
	Quality Quality
	Reason  Reason
	Sources []SourceRef
}

type Bucket struct {
	From  time.Time `json:"from"`
	To    time.Time `json:"to"`
	Usage *float64  `json:"usage"`
	Coverage
}

type Result struct {
	Usage       *float64        `json:"usage"`
	LatestEnd   *time.Time      `json:"latestEnd"`
	Buckets     []Bucket        `json:"buckets"`
	Unit        string          `json:"-"`
	Window      Window          `json:"-"`
	Profile     PointProfile    `json:"-"`
	RuleVersion string          `json:"-"`
	Intervals   []IntervalTrace `json:"-"`
	Coverage
}

func (r Result) complete() bool {
	return r.Usage != nil && r.Expected > 0 && r.Valid == r.Expected
}

// Calculate normalizes and aggregates one point at a fixed visibility cutoff.
// A nil Usage represents no valid contribution; a pointer to zero is measured zero.
func Calculate(data MeteringData, window Window) (Result, error) {
	window, err := localWindow(window)
	if err != nil {
		return Result{}, err
	}
	return calculate(data, window)
}

func calculate(data MeteringData, window Window) (Result, error) {
	profile := data.Profile
	if profile.Kind != Cumulative && profile.Kind != Interval {
		return Result{}, ErrUnsupportedMeasurement
	}
	if !profile.Point.Project.valid() || !profile.Point.Key.valid() || !profile.Meta.covers(window.Range) {
		return Result{}, ErrInvalidProfile
	}
	unit, scale, err := meteringScale(profile)
	if err != nil {
		return Result{}, err
	}

	samples, err := indexSamples(data.Samples, profile, window)
	if err != nil {
		return Result{}, err
	}
	result := Result{Unit: unit, Window: window, Profile: profile, RuleVersion: calculationVersion}
	var total float64
	for start := window.From; start.Before(window.To); {
		end := bucketEnd(start, window.Granularity)
		if !start.Before(end) || end.After(window.To) ||
			end.Sub(start)%profile.Cadence != 0 || !aligned(end, window.Granularity) {
			return Result{}, fmt.Errorf("%w: local bucket does not align to the source cadence", ErrInvalidWindow)
		}
		bucket := Bucket{From: start, To: end}
		var usage float64
		for at := start; at.Before(end); at = at.Add(profile.Cadence) {
			next := at.Add(profile.Cadence)
			trace := measure(profile, samples[at.UTC()], samples[next.UTC()], scale)
			trace.Range = Range{From: at, To: next}
			result.Intervals = append(result.Intervals, trace)
			bucket.Expected++
			switch {
			case trace.Usage != nil:
				bucket.Valid++
				usage += *trace.Usage
				result.LatestEnd = &next
			case trace.Quality == Missing:
				bucket.Missing++
			default:
				bucket.Suspect++
			}
		}
		if bucket.Valid > 0 {
			bucket.Usage = &usage
			total += usage
		}
		result.Expected += bucket.Expected
		result.Valid += bucket.Valid
		result.Missing += bucket.Missing
		result.Suspect += bucket.Suspect
		result.Buckets = append(result.Buckets, bucket)
		start = end
	}
	if !finite(total) {
		return Result{}, errors.New("aggregate usage exceeds numeric range")
	}
	if result.Valid > 0 {
		result.Usage = &total
	}
	return result, nil
}

func meteringScale(profile PointProfile) (string, float64, error) {
	if !finite(profile.Multiplier) || profile.Multiplier <= 0 || !finite(profile.Modulus) || profile.Modulus < 0 ||
		(profile.Kind == Interval && profile.Modulus != 0) ||
		profile.Cadence < time.Minute || profile.Cadence%time.Minute != 0 || time.Hour%profile.Cadence != 0 {
		return "", 0, ErrInvalidProfile
	}
	unit, conversion, err := unitConversion(profile.Energy, profile.Unit)
	if err != nil {
		return "", 0, err
	}
	scale := profile.Multiplier * conversion
	if !finite(scale) || scale == 0 {
		return "", 0, ErrInvalidProfile
	}
	return unit, scale, nil
}

type indexedSample struct {
	sample   Sample
	sources  []SourceRef
	conflict bool
}

func indexSamples(input []Sample, profile PointProfile, window Window) (map[time.Time]*indexedSample, error) {
	samples := make(map[time.Time]*indexedSample)
	isInterval := profile.Kind == Interval
	for _, sample := range input {
		if sample.ReceivedAt.After(window.AsOf) || sample.At.After(window.To) {
			continue
		}
		if sample.At.Before(window.From) {
			if isInterval && sample.End.After(window.From) {
				return nil, fmt.Errorf("%w: interval overlaps the start boundary", ErrInvalidSample)
			}
			continue
		}
		visibleFrom := sample.At
		if isInterval {
			if sample.At.Equal(window.To) {
				continue
			}
			if !sample.End.Equal(sample.At.Add(profile.Cadence)) || sample.End.After(window.To) || sample.Rollover || sample.Epoch != "" {
				return nil, fmt.Errorf("%w: interval quantity must cover exactly one source slot", ErrInvalidSample)
			}
			visibleFrom = sample.End
		} else if !sample.End.IsZero() || (sample.Quality.usable() && !named(sample.Epoch)) {
			return nil, fmt.Errorf("%w: cumulative reading requires a known register epoch", ErrInvalidSample)
		}
		if sample.At.Sub(window.From)%profile.Cadence != 0 || sample.ReceivedAt.IsZero() ||
			sample.ReceivedAt.Before(visibleFrom) || !sample.Source.valid() || !sample.Quality.valid() {
			return nil, fmt.Errorf("%w: unaligned or untraceable source record", ErrInvalidSample)
		}
		key := sample.At.UTC()
		if group := samples[key]; group != nil {
			first := group.sample
			group.conflict = group.conflict || !sameValue(first.Value, sample.Value) || first.Epoch != sample.Epoch ||
				first.Rollover != sample.Rollover || first.Quality != sample.Quality
			group.sources = append(group.sources, sample.Source)
		} else {
			samples[key] = &indexedSample{sample: sample, sources: []SourceRef{sample.Source}}
		}
	}
	return samples, nil
}

func measure(profile PointProfile, left, right *indexedSample, scale float64) IntervalTrace {
	var trace IntervalTrace
	endpoints := []*indexedSample{left}
	if profile.Kind == Cumulative {
		endpoints = append(endpoints, right)
	}
	for _, endpoint := range endpoints {
		if endpoint != nil {
			trace.Sources = append(trace.Sources, endpoint.sources...)
		}
	}
	slices.SortFunc(trace.Sources, func(a, b SourceRef) int {
		if order := cmp.Compare(a.System, b.System); order != 0 {
			return order
		}
		return cmp.Compare(a.ID, b.ID)
	})
	trace.Sources = slices.Compact(trace.Sources)
	for _, endpoint := range endpoints {
		if endpoint != nil && endpoint.conflict {
			trace.Quality, trace.Reason = Suspect, ConflictingSample
			return trace
		}
	}
	trace.Quality = Normal
	for _, endpoint := range endpoints {
		if endpoint == nil || endpoint.sample.Quality == Missing {
			trace.Quality, trace.Reason = Missing, MissingSample
			return trace
		}
	}
	for _, endpoint := range endpoints {
		sample := endpoint.sample
		if !sample.Quality.usable() {
			trace.Quality, trace.Reason = sample.Quality, SourceQuality
			return trace
		}
		if !finite(sample.Value) || sample.Value < 0 ||
			(profile.Modulus > 0 && sample.Value >= profile.Modulus) {
			trace.Quality, trace.Reason = Suspect, InvalidValue
			return trace
		}
		if sample.Quality == Corrected || (sample.Quality == Manual && trace.Quality == Normal) {
			trace.Quality = sample.Quality
		}
	}

	value := left.sample.Value
	if profile.Kind == Cumulative {
		last := right.sample
		switch {
		case left.sample.Epoch != last.Epoch:
			trace.Quality, trace.Reason = Suspect, EpochChange
			return trace
		case last.Rollover && profile.Modulus == 0:
			trace.Quality, trace.Reason = Suspect, UnconfirmedRollover
			return trace
		case last.Rollover:
			value = profile.Modulus - value + last.Value
		case last.Value < value:
			trace.Quality, trace.Reason = Suspect, NegativeJump
			return trace
		default:
			value = last.Value - value
		}
	}
	usage := value * scale
	if !finite(usage) {
		trace.Quality, trace.Reason = Suspect, InvalidValue
		return trace
	}
	trace.Usage = &usage
	return trace
}

func sameValue(a, b float64) bool { return a == b || (math.IsNaN(a) && math.IsNaN(b)) }

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
