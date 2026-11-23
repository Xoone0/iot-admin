package buildingenergy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"
)

func isNilDep(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return rv.IsNil()
	default:
		return false
	}
}

var (
	ErrAccessDenied  = errors.New("building energy access denied")
	ErrInvalidScope  = errors.New("invalid building energy scope")
	ErrCatalogScope  = errors.New("catalog record does not cover the requested scope")
	ErrMeteringScope = errors.New("metering source does not match the requested point")
)

// Range is half-open. VersionMeta may leave the end unset for an open version.
type Range struct {
	From, To time.Time
}

func (r Range) valid() bool {
	return !r.From.IsZero() && r.From.Before(r.To)
}

type SourceRef struct {
	System string
	ID     string
}

func (r SourceRef) valid() bool { return named(r.System) && named(r.ID) }

type VersionMeta struct {
	Version   string
	Effective Range
	Source    SourceRef
}

func (m VersionMeta) covers(period Range) bool {
	return named(m.Version) && m.Source.valid() && !m.Effective.From.IsZero() &&
		!period.From.Before(m.Effective.From) && (m.Effective.To.IsZero() || !period.To.After(m.Effective.To))
}

type ProjectKey struct {
	TenantID, ProjectID string
}

func (k ProjectKey) valid() bool { return named(k.TenantID) && named(k.ProjectID) }

type ObjectKind string

const (
	Building     ObjectKind = "building"
	Space        ObjectKind = "space"
	Organization ObjectKind = "organization"
)

type ObjectKey struct {
	Kind ObjectKind
	ID   string
}

func (k ObjectKey) valid() bool {
	return named(k.ID) && (k.Kind == Building || k.Kind == Space || k.Kind == Organization)
}

type PointKey struct {
	TerminalID, ItemCode string
}

func (k PointKey) valid() bool { return named(k.TerminalID) && named(k.ItemCode) }

type PointRef struct {
	Project ProjectKey
	Key     PointKey
}

type Project struct {
	Key      ProjectKey
	Timezone string
	Meta     VersionMeta
}

type Object struct {
	Project    ProjectKey
	Key        ObjectKey
	Parent     ObjectKey
	BuildingID string
	Meta       VersionMeta
}

// Catalog reads immutable records effective at the given instant. Directory
// adapters return keys from the requested project and preserve their revisions.
type Catalog interface {
	Project(context.Context, ProjectKey, time.Time) (Project, error)
	Object(context.Context, ProjectKey, ObjectKey, time.Time) (Object, error)
}

type Subject struct {
	Issuer, ID string
}

// IdentitySource returns an authenticated principal from trusted request context.
type IdentitySource interface {
	AuthenticatedSubject(context.Context) (Subject, error)
}

type Action string

const (
	ReadUsage      Action = "read_usage"
	ManageBindings Action = "manage_bindings"
	ExportUsage    Action = "export_usage"
	DownloadReport Action = "download_report"
)

type Scope struct {
	Project ProjectKey
	Period  Range
	Objects []ObjectKey
	Points  []PointKey
}

// AccessPolicy must check the entire requested scope. An empty object/point
// selection requests project-level access, with no implicit grant.
type AccessPolicy interface {
	Require(context.Context, Subject, Action, Scope) error
}

type ResolvedScope struct {
	Project  Project
	Location *time.Location
	Objects  []Object
}

// ResolveScope validates the authorized key and version before returning data.
// The caller must apply the same scope when resolving mappings and fetching data.
func ResolveScope(ctx context.Context, identity IdentitySource, policy AccessPolicy, catalog Catalog, action Action, scope Scope) (ResolvedScope, error) {
	if isNilDep(identity) || isNilDep(policy) {
		return ResolvedScope{}, ErrAccessDenied
	}
	if !scope.Project.valid() || !scope.Period.valid() {
		return ResolvedScope{}, ErrInvalidScope
	}
	switch action {
	case ReadUsage, ManageBindings, ExportUsage, DownloadReport:
	default:
		return ResolvedScope{}, ErrInvalidScope
	}
	for _, object := range scope.Objects {
		if !object.valid() {
			return ResolvedScope{}, ErrInvalidScope
		}
	}
	for _, point := range scope.Points {
		if !point.valid() {
			return ResolvedScope{}, ErrInvalidScope
		}
	}
	subject, err := identity.AuthenticatedSubject(ctx)
	if err != nil || !named(subject.Issuer) || !named(subject.ID) {
		return ResolvedScope{}, ErrAccessDenied
	}
	if err := policy.Require(ctx, subject, action, scope); err != nil {
		return ResolvedScope{}, ErrAccessDenied
	}
	if isNilDep(catalog) {
		return ResolvedScope{}, ErrCatalogScope
	}
	project, err := catalog.Project(ctx, scope.Project, scope.Period.From)
	if err != nil {
		return ResolvedScope{}, err
	}
	if project.Key != scope.Project || !project.Meta.covers(scope.Period) {
		return ResolvedScope{}, ErrCatalogScope
	}
	location, err := projectLocation(project.Timezone)
	if err != nil {
		return ResolvedScope{}, err
	}
	result := ResolvedScope{Project: project, Location: location}
	for _, key := range scope.Objects {
		object, err := catalog.Object(ctx, scope.Project, key, scope.Period.From)
		if err != nil {
			return ResolvedScope{}, err
		}
		if object.Key != key || object.Project != scope.Project || !object.Meta.covers(scope.Period) {
			return ResolvedScope{}, ErrCatalogScope
		}
		result.Objects = append(result.Objects, object)
	}
	return result, nil
}

type MeasurementKind string

const (
	Cumulative    MeasurementKind = "cumulative"
	Interval      MeasurementKind = "interval"
	Instantaneous MeasurementKind = "instantaneous"
)

type PointProfile struct {
	Point      PointRef
	Kind       MeasurementKind
	Energy     EnergyType
	Unit       string        // Effective raw unit of this channel.
	Multiplier float64       // Confirmed register multiplier, or 1 if already applied.
	Modulus    float64       // Confirmed cycle size in raw register units; 0 means unknown.
	Cadence    time.Duration // Expected spacing of the source samples.
	Meta       VersionMeta
}

type MeteringData struct {
	Profile PointProfile
	Samples []Sample
}

// MeteringSource reads a single authorized point at the supplied visibility
// cutoff. Cumulative adapters may include the exact right endpoint.
type MeteringSource interface {
	Load(context.Context, PointRef, Range, time.Time) (MeteringData, error)
}

// CalculateFromSource reads a point that the caller has already authorized.
func CalculateFromSource(ctx context.Context, source MeteringSource, point PointRef, window Window) (Result, error) {
	if isNilDep(source) || !point.Project.valid() || !point.Key.valid() {
		return Result{}, ErrMeteringScope
	}
	window, err := localWindow(window)
	if err != nil {
		return Result{}, err
	}
	data, err := source.Load(ctx, point, window.Range, window.AsOf)
	if err != nil {
		return Result{}, err
	}
	if data.Profile.Point != point {
		return Result{}, ErrMeteringScope
	}
	return calculate(data, window)
}

func named(value string) bool { return strings.TrimSpace(value) != "" }
