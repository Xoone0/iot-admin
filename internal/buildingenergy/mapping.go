package buildingenergy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

const FullWeightPPM = 1_000_000

var (
	ErrInvalidMapping     = errors.New("invalid energy mapping")
	ErrMappingBoundary    = errors.New("measurement interval crosses an energy mapping boundary")
	ErrMappingUnavailable = errors.New("energy mapping version is unavailable")
	ErrMappingConflict    = errors.New("energy mapping generation conflict")
)

type ProfileRevision struct {
	Profile       PointProfile
	Channel       SourceRef
	RegisterEpoch string
}

type Allocation struct {
	Target         ObjectKey
	TargetRevision string // Authoritative directory object_revision.revision for Target during this binding.
	WeightPPM      int
}

type BindingRevision struct {
	View        ObjectKind
	Meta        VersionMeta
	Allocations []Allocation
}

// MappingTimeline is the complete configuration published for one point at one
// generation. Effective dates and publication generations are independent.
type MappingTimeline struct {
	Point      PointRef
	Generation uint64
	Profiles   []ProfileRevision
	Bindings   []BindingRevision
}

func (m MappingTimeline) Validate() error {
	if !m.Point.Project.valid() || !m.Point.Key.valid() || m.Generation == 0 {
		return ErrInvalidMapping
	}
	profiles := make([]VersionMeta, 0, len(m.Profiles))
	for _, revision := range m.Profiles {
		profile := revision.Profile
		if profile.Point != m.Point || !revision.Channel.valid() || !validRevisionShape(revision) {
			return fmt.Errorf("%w: point profile", ErrInvalidMapping)
		}
		profiles = append(profiles, profile.Meta)
	}
	if err := validateTimeline(profiles); err != nil {
		return err
	}

	views := make(map[ObjectKind][]VersionMeta)
	for _, binding := range m.Bindings {
		if !validMappingView(binding.View) {
			return fmt.Errorf("%w: unsupported view", ErrInvalidMapping)
		}
		if _, err := unallocatedWeight(binding); err != nil {
			return err
		}
		views[binding.View] = append(views[binding.View], binding.Meta)
	}
	for _, view := range []ObjectKind{Building, Space, Organization} {
		if err := validateTimeline(views[view]); err != nil {
			return err
		}
	}
	return nil
}

func validRevisionShape(revision ProfileRevision) bool {
	profile := revision.Profile
	switch profile.Kind {
	case Instantaneous:
		return named(profile.Unit) && revision.RegisterEpoch == "" && profile.Modulus == 0
	case Cumulative, Interval:
		_, _, err := meteringScale(profile)
		return err == nil && ((profile.Kind == Cumulative) == named(revision.RegisterEpoch))
	default:
		return false
	}
}

func validateTimeline(versions []VersionMeta) error {
	slices.SortFunc(versions, func(a, b VersionMeta) int { return a.Effective.From.Compare(b.Effective.From) })
	seen := make(map[string]bool, len(versions))
	for i, meta := range versions {
		effective := meta.Effective
		if !named(meta.Version) || !meta.Source.valid() || effective.From.IsZero() ||
			(!effective.To.IsZero() && !effective.From.Before(effective.To)) || seen[meta.Version] {
			return fmt.Errorf("%w: invalid or duplicate effective version", ErrInvalidMapping)
		}
		seen[meta.Version] = true
		if i > 0 && (versions[i-1].Effective.To.IsZero() || effective.From.Before(versions[i-1].Effective.To)) {
			return fmt.Errorf("%w: overlapping effective versions", ErrInvalidMapping)
		}
	}
	return nil
}

func unallocatedWeight(binding BindingRevision) (int, error) {
	remaining := FullWeightPPM
	seen := make(map[ObjectKey]bool, len(binding.Allocations))
	for _, item := range binding.Allocations {
		if !item.Target.valid() || item.Target.Kind != binding.View || seen[item.Target] ||
			(item.TargetRevision != "" && !named(item.TargetRevision)) ||
			item.WeightPPM <= 0 || item.WeightPPM > remaining {
			return 0, fmt.Errorf("%w: duplicate, out-of-view, or overweight allocation", ErrInvalidMapping)
		}
		seen[item.Target] = true
		remaining -= item.WeightPPM
	}
	return remaining, nil
}

type ResolvedMapping struct {
	Generation     uint64
	Profile        ProfileRevision
	View           ObjectKind
	BindingVersion VersionMeta
	Allocations    []Allocation
	UnallocatedPPM int
}

// Resolve returns one version per configuration layer for an atomic
// measurement interval. If either layer changes within it, the caller must
// verify a real measurement boundary before splitting.
func (m MappingTimeline) Resolve(view ObjectKind, period Range) (ResolvedMapping, error) {
	if !validMappingView(view) || !period.valid() {
		return ResolvedMapping{}, ErrInvalidMapping
	}
	if err := m.Validate(); err != nil {
		return ResolvedMapping{}, err
	}
	return m.resolve(view, period)
}

func (m MappingTimeline) resolve(view ObjectKind, period Range) (ResolvedMapping, error) {
	result := ResolvedMapping{Generation: m.Generation, View: view, UnallocatedPPM: FullWeightPPM}
	for _, revision := range m.Profiles {
		meta := revision.Profile.Meta
		if overlapsWindow(meta.Effective, period) {
			if !meta.covers(period) {
				return ResolvedMapping{}, ErrMappingBoundary
			}
			result.Profile = revision
			break
		}
	}
	if result.Profile.Profile.Meta.Version == "" {
		return ResolvedMapping{}, ErrMappingUnavailable
	}
	for _, binding := range m.Bindings {
		if binding.View != view || !overlapsWindow(binding.Meta.Effective, period) {
			continue
		}
		if !binding.Meta.covers(period) {
			return ResolvedMapping{}, ErrMappingBoundary
		}
		result.BindingVersion = binding.Meta
		result.Allocations = slices.Clone(binding.Allocations)
		for _, allocation := range result.Allocations {
			result.UnallocatedPPM -= allocation.WeightPPM
		}
		break
	}
	return result, nil
}

func overlapsWindow(effective, period Range) bool {
	return effective.From.Before(period.To) && (effective.To.IsZero() || period.From.Before(effective.To))
}

func validMappingView(view ObjectKind) bool {
	return view == Building || view == Space || view == Organization
}

type MappingQuery struct {
	Point      PointRef
	View       ObjectKind
	Period     Range
	Generation uint64
	Objects    []ObjectKey
}

type MappingReader interface {
	ReadMapping(context.Context, PointRef, uint64) (MappingTimeline, error)
}

// ReadAuthorizedMapping returns full allocation and remainder information
// only to a caller authorized for the point and all active targets.
func ReadAuthorizedMapping(ctx context.Context, identity IdentitySource, policy AccessPolicy, catalog Catalog, reader MappingReader, query MappingQuery) (ResolvedMapping, error) {
	if len(query.Objects) == 0 {
		return ResolvedMapping{}, ErrAccessDenied
	}
	if query.Generation == 0 || !validMappingView(query.View) || !query.Period.valid() {
		return ResolvedMapping{}, ErrInvalidMapping
	}

	allowed := make(map[ObjectKey]bool, len(query.Objects))
	scope := Scope{Project: query.Point.Project, Period: query.Period, Points: []PointKey{query.Point.Key}}
	for _, object := range query.Objects {
		if !object.valid() || object.Kind != query.View {
			return ResolvedMapping{}, ErrInvalidMapping
		}
		if !allowed[object] {
			allowed[object] = true
			scope.Objects = append(scope.Objects, object)
		}
	}
	resolvedScope, err := ResolveScope(ctx, identity, policy, catalog, ReadUsage, scope)
	if err != nil {
		return ResolvedMapping{}, err
	}
	catalogRevisions := make(map[ObjectKey]string, len(resolvedScope.Objects))
	for _, obj := range resolvedScope.Objects {
		if obj.Project != query.Point.Project || !named(obj.Meta.Version) {
			return ResolvedMapping{}, ErrCatalogScope
		}
		catalogRevisions[obj.Key] = obj.Meta.Version
	}
	if isNilDep(reader) {
		return ResolvedMapping{}, ErrMappingUnavailable
	}
	timeline, err := reader.ReadMapping(ctx, query.Point, query.Generation)
	if err != nil {
		return ResolvedMapping{}, err
	}
	if timeline.Point != query.Point || timeline.Generation != query.Generation || timeline.Validate() != nil {
		return ResolvedMapping{}, ErrMappingUnavailable
	}
	// Check all targets incident to the period before returning a boundary
	// error, which could otherwise disclose a change on an unauthorized target.
	for _, binding := range timeline.Bindings {
		if binding.View != query.View || !overlapsWindow(binding.Meta.Effective, query.Period) {
			continue
		}
		for _, allocation := range binding.Allocations {
			if !allowed[allocation.Target] {
				return ResolvedMapping{}, ErrAccessDenied
			}
		}
	}
	resolved, err := timeline.resolve(query.View, query.Period)
	if err != nil {
		return ResolvedMapping{}, err
	}
	// Verify every active allocation's TargetRevision matches the authoritative
	// directory object revision covering query.Period.
	for _, allocation := range resolved.Allocations {
		expectedRev, ok := catalogRevisions[allocation.Target]
		if !ok || !named(allocation.TargetRevision) || allocation.TargetRevision != expectedRev {
			return ResolvedMapping{}, fmt.Errorf("%w: allocation target %s revision %q does not match catalog revision %q",
				ErrCatalogScope, allocation.Target.ID, allocation.TargetRevision, expectedRev)
		}
	}
	return resolved, nil
}

type MappingApproval struct {
	At     time.Time
	Reason string
	Ref    SourceRef
}

type MappingAudit struct {
	Actor    Subject
	Approval MappingApproval
}

// MappingChangePolicy verifies approval and the actor's right to modify the
// entire change against the old generation, including old and new targets.
type MappingChangePolicy interface {
	RequireMappingChange(context.Context, MappingAudit, uint64, MappingTimeline) error
}

// MappingWriter guarantees that checking the point's previous generation,
// keeping its history, saving audit evidence and publishing are atomic.
type MappingWriter interface {
	CompareAndSwapMapping(ctx context.Context, expected uint64, next MappingTimeline, audit MappingAudit) error
}

// PublishMapping obtains the actor from trusted context. The storage port must
// compare the same old generation that was used by the policy.
func PublishMapping(ctx context.Context, identity IdentitySource, policy MappingChangePolicy, writer MappingWriter, expected uint64, next MappingTimeline, approval MappingApproval) error {
	if isNilDep(identity) || isNilDep(policy) {
		return ErrAccessDenied
	}
	actor, err := identity.AuthenticatedSubject(ctx)
	if err != nil || !named(actor.Issuer) || !named(actor.ID) {
		return ErrAccessDenied
	}
	if isNilDep(writer) || approval.At.IsZero() || !named(approval.Reason) || !approval.Ref.valid() ||
		next.Generation == 0 || next.Generation-1 != expected {
		return ErrInvalidMapping
	}
	if err := next.Validate(); err != nil {
		return err
	}
	for _, binding := range next.Bindings {
		for _, alloc := range binding.Allocations {
			if !named(alloc.TargetRevision) {
				return fmt.Errorf("%w: allocation target %s missing authoritative target revision", ErrInvalidMapping, alloc.Target.ID)
			}
		}
	}
	audit := MappingAudit{Actor: actor, Approval: approval}
	if err := policy.RequireMappingChange(ctx, audit, expected, next); err != nil {
		return ErrAccessDenied
	}
	return writer.CompareAndSwapMapping(ctx, expected, next, audit)
}

type BoundaryRole string

const (
	BoundaryTotal    BoundaryRole = "total"
	BoundarySubmeter BoundaryRole = "submeter"
)

type BoundaryContributor struct {
	Point PointRef
	Role  BoundaryRole
}

// ValidateBoundaryContributors accepts input for a single prevalidated
// physical boundary. A trusted topology adapter must certify disjoint submeters.
func ValidateBoundaryContributors(project ProjectKey, points []BoundaryContributor) error {
	if !project.valid() || len(points) == 0 {
		return ErrInvalidMapping
	}
	seen := make(map[PointKey]bool, len(points))
	for _, item := range points {
		if item.Point.Project != project || !item.Point.Key.valid() || seen[item.Point.Key] ||
			(item.Role != BoundaryTotal && item.Role != BoundarySubmeter) ||
			(item.Role == BoundaryTotal && len(points) != 1) {
			return ErrInvalidMapping
		}
		seen[item.Point.Key] = true
	}
	return nil
}
