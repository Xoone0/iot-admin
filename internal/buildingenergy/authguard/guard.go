// Package authguard enforces a fail-closed identity and authorization boundary
// for formal building-energy HTTP APIs without altering legacy IoT routes or
// importing HTTP/SQL into the pure calculation package internal/buildingenergy.
package authguard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

const (
	FormalRoutePrefix    = "/admin/v1/energy-analysis/"
	HardMaxCredentialTTL = 24 * time.Hour
	HardMaxClockSkew     = 2 * time.Minute
)

var (
	ErrIdentityUnavailable     = errors.New("trusted identity source is not configured")
	ErrIdentityConfigInvalid   = errors.New("formal identity source configuration is incomplete or invalid")
	ErrIdentityInvalid         = errors.New("authenticated principal is missing or invalid")
	ErrIdentityMissingIssuedAt = errors.New("authenticated principal is missing a valid issued or session creation time")
	ErrIdentityFutureIssuedAt  = errors.New("authenticated principal issued or session creation time is in the future")
	ErrIdentityMissingExpiry   = errors.New("authenticated principal is missing a valid expiration time")
	ErrIdentityExpired         = errors.New("authenticated principal has expired")
	ErrIdentityTTLTooLong      = errors.New("authenticated principal lifetime exceeds maximum allowed duration")
	ErrIdentityRevoked         = errors.New("authenticated principal has been revoked")
	ErrSyntheticIdentity       = errors.New("synthetic preview identity is not permitted on formal endpoints")
	ErrPolicyUnavailable       = errors.New("authorization policy is not configured")
)

func isNilInterface(v any) bool {
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

// syntheticPreviewIssuers enumerates issuers used by cmd/energy-preview fixtures.
// Formal endpoints must never accept preview/fixture issuers as production principals.
var syntheticPreviewIssuers = []string{
	"preview",
	"f06-preview",
	"industry-synthetic",
	"f09-demo",
	"f06-demo",
	"f12-industry-demo",
}

// Principal represents a server-verified identity extracted from a trusted
// upstream authenticator (e.g., OIDC/JWT verifier or verified mTLS/session layer).
// Untrusted client headers (X-User-ID, X-Project-ID, X-Tenant-ID, X-Forwarded-User)
// must never be mapped into Principal directly.
type Principal struct {
	Subject          buildingenergy.Subject
	Audience         string
	SessionID        string
	IssuedAt         time.Time // Credential issue time (required unless SessionCreatedAt is provided)
	SessionCreatedAt time.Time // Equivalent trusted session creation time (checked against MaxCredentialTTL when present)
	ExpiresAt        time.Time
}

// Authenticator extracts and cryptographically/server-side verifies a Principal
// from an incoming HTTP request.
type Authenticator interface {
	Authenticate(ctx context.Context, r *http.Request) (Principal, error)
}

// RevocationChecker checks whether a verified principal or session has been revoked.
// Any error returned by IsRevoked MUST be treated as fail-closed (denied).
type RevocationChecker interface {
	IsRevoked(ctx context.Context, principal Principal) (bool, error)
}

type principalContextKey struct{}

// WithPrincipal attaches a verified Principal to ctx. Only trusted server-side
// middleware should call this after authenticating the request.
func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

// PrincipalFromContext retrieves the verified Principal stored by trusted middleware.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	if ctx == nil {
		return Principal{}, false
	}
	principal, ok := ctx.Value(principalContextKey{}).(Principal)
	return principal, ok
}

// ContextIdentitySource implements buildingenergy.IdentitySource from a
// server-verified Principal in request context.
//
// Formal endpoints fail closed unless ALL four required controls are configured:
//   1. Non-empty AllowedIssuers whitelist (no blanks, no synthetic preview issuers).
//   2. Non-empty ExpectedAudience.
//   3. Positive finite MaxCredentialTTL (<= HardMaxCredentialTTL), enforced on every Principal.ExpiresAt.
//   4. Non-nil Revocations checker (fail-closed on backend error).
type ContextIdentitySource struct {
	AllowedIssuers   []string
	ExpectedAudience string
	MaxCredentialTTL time.Duration
	MaxClockSkew     time.Duration // Optional bounded tolerance (0 <= MaxClockSkew <= HardMaxClockSkew); default 0 (strict).
	Revocations      RevocationChecker
	Now              func() time.Time
}

func (s ContextIdentitySource) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// ValidateConfig checks that ContextIdentitySource satisfies formal production
// security prerequisites before any formal building-energy endpoint is enabled.
func (s ContextIdentitySource) ValidateConfig() error {
	if len(s.AllowedIssuers) == 0 {
		return ErrIdentityConfigInvalid
	}
	for _, rawIssuer := range s.AllowedIssuers {
		issuer := strings.TrimSpace(rawIssuer)
		if issuer == "" {
			return ErrIdentityConfigInvalid
		}
		if slices.Contains(syntheticPreviewIssuers, strings.ToLower(issuer)) {
			return ErrIdentityConfigInvalid
		}
	}
	if strings.TrimSpace(s.ExpectedAudience) == "" {
		return ErrIdentityConfigInvalid
	}
	if s.MaxCredentialTTL <= 0 || s.MaxCredentialTTL > HardMaxCredentialTTL {
		return ErrIdentityConfigInvalid
	}
	if s.MaxClockSkew < 0 || s.MaxClockSkew > HardMaxClockSkew {
		return ErrIdentityConfigInvalid
	}
	if isNilInterface(s.Revocations) {
		return ErrIdentityConfigInvalid
	}
	return nil
}

func (s ContextIdentitySource) issuerAllowed(issuer string) bool {
	for _, allowed := range s.AllowedIssuers {
		if strings.TrimSpace(allowed) == issuer {
			return true
		}
	}
	return false
}

// ValidatePrincipal checks formal config readiness, issuer whitelist, subject ID,
// synthetic-issuer rejection, expected audience, mandatory trusted IssuedAt/SessionCreatedAt,
// mandatory bounded expiry (MaxCredentialTTL), and live revocation (fail-closed on errors).
func (s ContextIdentitySource) ValidatePrincipal(ctx context.Context, principal Principal) error {
	if err := s.ValidateConfig(); err != nil {
		return err
	}
	issuer := strings.TrimSpace(principal.Subject.Issuer)
	id := strings.TrimSpace(principal.Subject.ID)
	if issuer == "" || id == "" {
		return ErrIdentityInvalid
	}
	if slices.Contains(syntheticPreviewIssuers, strings.ToLower(issuer)) {
		return ErrSyntheticIdentity
	}
	if !s.issuerAllowed(issuer) {
		return ErrIdentityInvalid
	}
	if strings.TrimSpace(principal.Audience) != strings.TrimSpace(s.ExpectedAudience) {
		return ErrIdentityInvalid
	}
	now := s.now()
	if principal.IssuedAt.IsZero() && principal.SessionCreatedAt.IsZero() {
		return ErrIdentityMissingIssuedAt
	}
	if principal.ExpiresAt.IsZero() {
		return ErrIdentityMissingExpiry
	}
	if !now.Before(principal.ExpiresAt) {
		return ErrIdentityExpired
	}
	if principal.ExpiresAt.Sub(now) > s.MaxCredentialTTL {
		return ErrIdentityTTLTooLong
	}
	for _, createdAt := range []time.Time{principal.IssuedAt, principal.SessionCreatedAt} {
		if createdAt.IsZero() {
			continue
		}
		if createdAt.After(now.Add(s.MaxClockSkew)) {
			return ErrIdentityFutureIssuedAt
		}
		if !createdAt.Before(principal.ExpiresAt) {
			return ErrIdentityInvalid
		}
		if principal.ExpiresAt.Sub(createdAt) > s.MaxCredentialTTL {
			return ErrIdentityTTLTooLong
		}
	}
	revoked, err := s.Revocations.IsRevoked(ctx, principal)
	if err != nil || revoked {
		return ErrIdentityRevoked
	}
	return nil
}

// AuthenticatedSubject implements buildingenergy.IdentitySource.
func (s ContextIdentitySource) AuthenticatedSubject(ctx context.Context) (buildingenergy.Subject, error) {
	principal, ok := PrincipalFromContext(ctx)
	if !ok {
		return buildingenergy.Subject{}, ErrIdentityInvalid
	}
	if err := s.ValidatePrincipal(ctx, principal); err != nil {
		return buildingenergy.Subject{}, err
	}
	return principal.Subject, nil
}

// Grant defines an explicit, least-privilege authorization grant for one
// principal, project, action set, object/point boundary, and business time window.
type Grant struct {
	Subject          buildingenergy.Subject
	Actions          []buildingenergy.Action
	Project          buildingenergy.ProjectKey
	Objects          []buildingenergy.ObjectKey
	AllowProjectWide bool
	Points           []buildingenergy.PointKey
	AllowAllPoints   bool
	ValidRange       buildingenergy.Range // Half-open [From, To); zero To means open-ended; zero From denies.
}

func (g Grant) coversAction(action buildingenergy.Action) bool {
	return slices.Contains(g.Actions, action)
}

func (g Grant) coversPeriod(period buildingenergy.Range) bool {
	if g.ValidRange.From.IsZero() || period.From.IsZero() {
		return false
	}
	if !g.ValidRange.To.IsZero() && !g.ValidRange.From.Before(g.ValidRange.To) {
		return false
	}
	if period.From.Before(g.ValidRange.From) {
		return false
	}
	if period.To.IsZero() {
		// Open-ended revision requires an open-ended grant.
		return g.ValidRange.To.IsZero()
	}
	if !period.From.Before(period.To) {
		return false
	}
	return g.ValidRange.To.IsZero() || !period.To.After(g.ValidRange.To)
}

// ScopedPolicy implements buildingenergy.AccessPolicy and
// buildingenergy.MappingChangePolicy with default-deny semantics.
type ScopedPolicy struct {
	Grants         []Grant
	Revocations    RevocationChecker
	PreviousReader buildingenergy.MappingReader
}

func (p *ScopedPolicy) checkRevocation(ctx context.Context, subject buildingenergy.Subject) error {
	if isNilInterface(p.Revocations) {
		return buildingenergy.ErrAccessDenied
	}
	principal, ok := PrincipalFromContext(ctx)
	if !ok || principal.Subject != subject {
		principal = Principal{Subject: subject}
	}
	revoked, err := p.Revocations.IsRevoked(ctx, principal)
	if err != nil || revoked {
		return buildingenergy.ErrAccessDenied
	}
	return nil
}

func (p *ScopedPolicy) matchingGrants(subject buildingenergy.Subject, action buildingenergy.Action, project buildingenergy.ProjectKey, period buildingenergy.Range) []Grant {
	var matched []Grant
	for _, grant := range p.Grants {
		if grant.Subject != subject || grant.Project != project {
			continue
		}
		if !grant.coversAction(action) || !grant.coversPeriod(period) {
			continue
		}
		matched = append(matched, grant)
	}
	return matched
}

// Require enforces least-privilege authorization across project, objects, points,
// business period, and explicit action (read_usage / manage_bindings / export_usage / download_report).
func (p *ScopedPolicy) Require(ctx context.Context, subject buildingenergy.Subject, action buildingenergy.Action, scope buildingenergy.Scope) error {
	if p == nil || strings.TrimSpace(subject.Issuer) == "" || strings.TrimSpace(subject.ID) == "" {
		return buildingenergy.ErrAccessDenied
	}
	if slices.Contains(syntheticPreviewIssuers, strings.ToLower(strings.TrimSpace(subject.Issuer))) {
		return buildingenergy.ErrAccessDenied
	}
	if err := p.checkRevocation(ctx, subject); err != nil {
		return err
	}
	grants := p.matchingGrants(subject, action, scope.Project, scope.Period)
	if len(grants) == 0 {
		return buildingenergy.ErrAccessDenied
	}

	if len(scope.Objects) == 0 {
		if !slices.ContainsFunc(grants, func(g Grant) bool { return g.AllowProjectWide }) {
			return buildingenergy.ErrAccessDenied
		}
	} else {
		for _, obj := range scope.Objects {
			allowed := slices.ContainsFunc(grants, func(g Grant) bool {
				return g.AllowProjectWide || slices.Contains(g.Objects, obj)
			})
			if !allowed {
				return buildingenergy.ErrAccessDenied
			}
		}
	}

	if len(scope.Points) == 0 {
		if len(scope.Objects) == 0 && !slices.ContainsFunc(grants, func(g Grant) bool { return g.AllowAllPoints }) {
			return buildingenergy.ErrAccessDenied
		}
	} else {
		for _, point := range scope.Points {
			allowed := slices.ContainsFunc(grants, func(g Grant) bool {
				return g.AllowAllPoints || slices.Contains(g.Points, point)
			})
			if !allowed {
				return buildingenergy.ErrAccessDenied
			}
		}
	}
	return nil
}

// RequireMappingChange implements buildingenergy.MappingChangePolicy:
// verifies ManageBindings on the point, all old and new targets, and all effective
// periods, plus non-empty approval evidence.
func (p *ScopedPolicy) RequireMappingChange(ctx context.Context, audit buildingenergy.MappingAudit, expected uint64, next buildingenergy.MappingTimeline) error {
	if p == nil || audit.Approval.At.IsZero() || strings.TrimSpace(audit.Approval.Reason) == "" ||
		strings.TrimSpace(audit.Approval.Ref.System) == "" || strings.TrimSpace(audit.Approval.Ref.ID) == "" {
		return buildingenergy.ErrAccessDenied
	}
	if err := next.Validate(); err != nil {
		return buildingenergy.ErrAccessDenied
	}
	if err := p.checkRevocation(ctx, audit.Actor); err != nil {
		return err
	}
	timelines := []buildingenergy.MappingTimeline{next}
	if expected > 0 {
		if isNilInterface(p.PreviousReader) {
			return buildingenergy.ErrAccessDenied
		}
		prev, err := p.PreviousReader.ReadMapping(ctx, next.Point, expected)
		if err != nil || prev.Point != next.Point || prev.Generation != expected {
			return buildingenergy.ErrAccessDenied
		}
		timelines = append(timelines, prev)
	}
	for _, timeline := range timelines {
		for _, profile := range timeline.Profiles {
			if err := p.requirePointRange(audit.Actor, buildingenergy.ManageBindings, next.Point.Project,
				profile.Profile.Meta.Effective, next.Point.Key); err != nil {
				return buildingenergy.ErrAccessDenied
			}
		}
		for _, binding := range timeline.Bindings {
			targets := make([]buildingenergy.ObjectKey, 0, len(binding.Allocations))
			for _, alloc := range binding.Allocations {
				targets = append(targets, alloc.Target)
			}
			if err := p.requireRange(ctx, audit.Actor, buildingenergy.ManageBindings, next.Point.Project,
				binding.Meta.Effective, targets, []buildingenergy.PointKey{next.Point.Key}); err != nil {
				return buildingenergy.ErrAccessDenied
			}
		}
	}
	return nil
}

func (p *ScopedPolicy) requirePointRange(subject buildingenergy.Subject, action buildingenergy.Action,
	project buildingenergy.ProjectKey, period buildingenergy.Range, point buildingenergy.PointKey) error {
	grants := p.matchingGrants(subject, action, project, period)
	if len(grants) == 0 {
		return buildingenergy.ErrAccessDenied
	}
	if !slices.ContainsFunc(grants, func(g Grant) bool {
		return g.AllowAllPoints || slices.Contains(g.Points, point)
	}) {
		return buildingenergy.ErrAccessDenied
	}
	return nil
}

func (p *ScopedPolicy) requireRange(ctx context.Context, subject buildingenergy.Subject, action buildingenergy.Action,
	project buildingenergy.ProjectKey, period buildingenergy.Range, objects []buildingenergy.ObjectKey, points []buildingenergy.PointKey) error {
	grants := p.matchingGrants(subject, action, project, period)
	if len(grants) == 0 {
		return buildingenergy.ErrAccessDenied
	}
	if len(objects) == 0 {
		if !slices.ContainsFunc(grants, func(g Grant) bool { return g.AllowProjectWide }) {
			return buildingenergy.ErrAccessDenied
		}
	} else {
		for _, obj := range objects {
			if !slices.ContainsFunc(grants, func(g Grant) bool {
				return g.AllowProjectWide || slices.Contains(g.Objects, obj)
			}) {
				return buildingenergy.ErrAccessDenied
			}
		}
	}
	for _, point := range points {
		if !slices.ContainsFunc(grants, func(g Grant) bool {
			return g.AllowAllPoints || slices.Contains(g.Points, point)
		}) {
			return buildingenergy.ErrAccessDenied
		}
	}
	return nil
}

// Guard coordinates fail-closed HTTP authentication and domain-level scope
// authorization (including secondary re-authorization on report downloads).
type Guard struct {
	Authenticator Authenticator
	Identity      ContextIdentitySource
	Policy        buildingenergy.AccessPolicy
}

// ValidateFormalConfig verifies that all mandatory authentication, identity,
// revocation, and policy components are configured (and non-typed-nil) before
// formal routes can serve.
func (g *Guard) ValidateFormalConfig() error {
	if g == nil || isNilInterface(g.Authenticator) {
		return ErrIdentityUnavailable
	}
	if isNilInterface(g.Policy) {
		return ErrPolicyUnavailable
	}
	if err := g.Identity.ValidateConfig(); err != nil {
		return err
	}
	if sp, ok := g.Policy.(*ScopedPolicy); ok && (sp == nil || isNilInterface(sp.Revocations)) {
		return ErrIdentityConfigInvalid
	}
	return nil
}

// Middleware enforces fail-closed authentication before any formal energy handler runs.
func (g *Guard) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if err := g.ValidateFormalConfig(); err != nil {
			writeAuthError(w, http.StatusUnauthorized, "energy_auth_unconfigured: 建筑能耗正式接口默认拒绝，缺少可信签发方、受众、凭据期限、撤销机制或授权策略")
			return
		}
		principal, err := g.Authenticator.Authenticate(r.Context(), r)
		if err != nil {
			writeAuthError(w, http.StatusUnauthorized, "unauthorized: 缺少或无效的可信身份凭据")
			return
		}
		if err := g.Identity.ValidatePrincipal(r.Context(), principal); err != nil {
			switch {
			case errors.Is(err, ErrIdentityConfigInvalid):
				writeAuthError(w, http.StatusUnauthorized, "energy_auth_unconfigured: 正式身份配置不完整")
			case errors.Is(err, ErrIdentityRevoked):
				writeAuthError(w, http.StatusUnauthorized, "identity_revoked: 当前会话或身份已撤权")
			case errors.Is(err, ErrIdentityExpired):
				writeAuthError(w, http.StatusUnauthorized, "identity_expired: 当前身份凭据已过期")
			case errors.Is(err, ErrIdentityMissingIssuedAt), errors.Is(err, ErrIdentityFutureIssuedAt):
				writeAuthError(w, http.StatusUnauthorized, "identity_invalid_issued_at: 当前身份凭据缺少或包含无效签发/会话创建时间")
			case errors.Is(err, ErrIdentityMissingExpiry), errors.Is(err, ErrIdentityTTLTooLong):
				writeAuthError(w, http.StatusUnauthorized, "identity_invalid_expiry: 当前身份凭据缺少有效期限或超期")
			case errors.Is(err, ErrSyntheticIdentity):
				writeAuthError(w, http.StatusForbidden, "synthetic_identity_rejected: 合成预览身份不可访问正式建筑能耗接口")
			default:
				writeAuthError(w, http.StatusUnauthorized, "unauthorized: 身份校验失败")
			}
			return
		}
		if isNilInterface(next) {
			writeAuthError(w, http.StatusNotImplemented, "energy_integration_not_ready: 正式业务数据源尚未签认接入")
			return
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
	})
}

// AuthorizeScope delegates to buildingenergy.ResolveScope so authentication,
// authorization, and catalog boundary validation occur in strict M0 order.
func (g *Guard) AuthorizeScope(ctx context.Context, catalog buildingenergy.Catalog, action buildingenergy.Action, scope buildingenergy.Scope) (buildingenergy.ResolvedScope, error) {
	if g == nil || isNilInterface(g.Policy) || g.Identity.ValidateConfig() != nil {
		return buildingenergy.ResolvedScope{}, buildingenergy.ErrAccessDenied
	}
	return buildingenergy.ResolveScope(ctx, g.Identity, g.Policy, catalog, action, scope)
}

// AuthorizeDownload performs mandatory secondary re-authorization at report
// download time using Action=DownloadReport and the original report snapshot scope.
func (g *Guard) AuthorizeDownload(ctx context.Context, catalog buildingenergy.Catalog, reportScope buildingenergy.Scope) (buildingenergy.ResolvedScope, error) {
	return g.AuthorizeScope(ctx, catalog, buildingenergy.DownloadReport, reportScope)
}

// NewFormalRouter returns the fail-closed HTTP router for /admin/v1/energy-analysis/*.
// When guard is nil or incomplete (the default before M1B-02 signoff), every route
// fails closed with 401 and Cache-Control: no-store.
func NewFormalRouter(guard *Guard) http.Handler {
	mux := http.NewServeMux()
	unimplemented := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeAuthError(w, http.StatusNotImplemented, "energy_integration_not_ready: 上游目录、计量来源或数据库尚未签认（M1B-01..04 No-Go）")
	})
	mux.Handle(FormalRoutePrefix, unimplemented)
	return guard.Middleware(mux)
}

func writeAuthError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 1, "message": message})
}
