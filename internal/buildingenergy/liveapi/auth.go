// Package liveapi implements authenticated, PostgreSQL-backed HTTP APIs for
// building-energy management (Phase 2) and real business analytics F02/F06/F07/
// F08/F09/F11 (Phase 3), reusing internal/buildingenergy calculation engines
// without mixing real database records and synthetic preview fixtures.
package liveapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/authguard"
)

var (
	ErrInvalidBearerToken = errors.New("invalid or malformed bearer token")
	ErrInvalidSignature   = errors.New("bearer token signature verification failed")
)

// jwtHeader represents a minimal JOSE header for HS256 verification.
type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ,omitempty"`
}

// TokenClaims represents signed claims carried in a server-verified HS256 JWT.
type TokenClaims struct {
	Issuer           string `json:"iss"`
	Subject          string `json:"sub"`
	Audience         string `json:"aud"`
	SessionID        string `json:"sid,omitempty"`
	TokenID          string `json:"jti,omitempty"`
	IssuedAt         int64  `json:"iat"`
	SessionCreatedAt int64  `json:"session_created_at,omitempty"`
	ExpiresAt        int64  `json:"exp"`
}

// HMACBearerAuthenticator verifies Authorization: Bearer <HS256-JWT> tokens
// using a server-side shared secret (minimum 16 bytes) and produces a trusted
// authguard.Principal for downstream validation by authguard.Guard.
type HMACBearerAuthenticator struct {
	Secret []byte
}

// Authenticate implements authguard.Authenticator.
func (a *HMACBearerAuthenticator) Authenticate(_ context.Context, r *http.Request) (authguard.Principal, error) {
	if a == nil || len(a.Secret) < 16 || r == nil {
		return authguard.Principal{}, authguard.ErrIdentityUnavailable
	}
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(authz) <= 7 || !strings.EqualFold(authz[:7], "Bearer ") {
		return authguard.Principal{}, ErrInvalidBearerToken
	}
	token := strings.TrimSpace(authz[7:])
	return ParseAndVerifyHMACToken(token, a.Secret)
}

// ParseAndVerifyHMACToken validates an HS256 JWT signature and decodes its Principal.
func ParseAndVerifyHMACToken(token string, secret []byte) (authguard.Principal, error) {
	if len(secret) < 16 {
		return authguard.Principal{}, authguard.ErrIdentityUnavailable
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return authguard.Principal{}, ErrInvalidBearerToken
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return authguard.Principal{}, ErrInvalidBearerToken
	}
	var hdr jwtHeader
	if err := json.Unmarshal(headerBytes, &hdr); err != nil || hdr.Alg != "HS256" {
		return authguard.Principal{}, ErrInvalidBearerToken
	}
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return authguard.Principal{}, ErrInvalidBearerToken
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0]))
	mac.Write([]byte("."))
	mac.Write([]byte(parts[1]))
	if !hmac.Equal(sigBytes, mac.Sum(nil)) {
		return authguard.Principal{}, ErrInvalidSignature
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return authguard.Principal{}, ErrInvalidBearerToken
	}
	var claims TokenClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return authguard.Principal{}, ErrInvalidBearerToken
	}
	sessionID := strings.TrimSpace(claims.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(claims.TokenID)
	}
	p := authguard.Principal{
		Subject: buildingenergy.Subject{
			Issuer: strings.TrimSpace(claims.Issuer),
			ID:     strings.TrimSpace(claims.Subject),
		},
		Audience:  strings.TrimSpace(claims.Audience),
		SessionID: sessionID,
	}
	if claims.IssuedAt > 0 {
		p.IssuedAt = time.Unix(claims.IssuedAt, 0).UTC()
	}
	if claims.SessionCreatedAt > 0 {
		p.SessionCreatedAt = time.Unix(claims.SessionCreatedAt, 0).UTC()
	}
	if claims.ExpiresAt > 0 {
		p.ExpiresAt = time.Unix(claims.ExpiresAt, 0).UTC()
	}
	return p, nil
}

// SignHMACToken creates a signed HS256 bearer token from TokenClaims.
func SignHMACToken(claims TokenClaims, secret []byte) (string, error) {
	if len(secret) < 16 {
		return "", errors.New("liveapi: HMAC secret must be at least 16 bytes")
	}
	hdrJSON, err := json.Marshal(jwtHeader{Alg: "HS256", Typ: "JWT"})
	if err != nil {
		return "", err
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	hEnc := base64.RawURLEncoding.EncodeToString(hdrJSON)
	pEnc := base64.RawURLEncoding.EncodeToString(payloadJSON)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(hEnc))
	mac.Write([]byte("."))
	mac.Write([]byte(pEnc))
	sEnc := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return hEnc + "." + pEnc + "." + sEnc, nil
}

// MemoryRevocationStore is a concurrency-safe in-memory revocation registry
// implementing authguard.RevocationChecker.
type MemoryRevocationStore struct {
	mu       sync.RWMutex
	subjects map[string]bool
	sessions map[string]bool
}

// NewMemoryRevocationStore creates an initialized revocation store.
func NewMemoryRevocationStore() *MemoryRevocationStore {
	return &MemoryRevocationStore{
		subjects: make(map[string]bool),
		sessions: make(map[string]bool),
	}
}

func (s *MemoryRevocationStore) RevokeSubject(sub buildingenergy.Subject) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subjects[sub.Issuer+"|"+sub.ID] = true
}

func (s *MemoryRevocationStore) RevokeSession(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionID] = true
}

func (s *MemoryRevocationStore) IsRevoked(_ context.Context, p authguard.Principal) (bool, error) {
	if s == nil {
		return true, authguard.ErrIdentityRevoked
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.subjects[p.Subject.Issuer+"|"+p.Subject.ID] {
		return true, nil
	}
	if p.SessionID != "" && s.sessions[p.SessionID] {
		return true, nil
	}
	return false, nil
}

// EnvAuthConfig holds formal authentication settings loaded from environment variables.
type EnvAuthConfig struct {
	Secret        string
	Issuers       []string
	Audience      string
	MaxTTL        time.Duration
	TenantID      string
	ProjectID     string
	ReadSubjects  []string
	AdminSubjects []string
}

// LoadEnvAuthConfig reads formal building-energy authentication configuration from env.
// If any mandatory setting (secret >= 16 bytes, issuers, audience) is missing, returns ok=false
// so callers remain fail-closed (401 energy_auth_unconfigured), never falling back to AllowAll.
func LoadEnvAuthConfig() (EnvAuthConfig, bool) {
	secret := strings.TrimSpace(os.Getenv("IOT_ENERGY_AUTH_SECRET"))
	issuers := splitCSV(os.Getenv("IOT_ENERGY_AUTH_ISSUERS"))
	audience := strings.TrimSpace(os.Getenv("IOT_ENERGY_AUTH_AUDIENCE"))
	if len(secret) < 16 || len(issuers) == 0 || audience == "" {
		return EnvAuthConfig{}, false
	}
	maxTTL := 8 * time.Hour
	if raw := strings.TrimSpace(os.Getenv("IOT_ENERGY_AUTH_MAX_TTL")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 && parsed <= authguard.HardMaxCredentialTTL {
			maxTTL = parsed
		} else {
			return EnvAuthConfig{}, false
		}
	}
	tenant := strings.TrimSpace(os.Getenv("IOT_ENERGY_TENANT_ID"))
	if tenant == "" {
		tenant = "default"
	}
	project := strings.TrimSpace(os.Getenv("IOT_ENERGY_PROJECT_ID"))
	if project == "" {
		project = "iot-local"
	}
	return EnvAuthConfig{
		Secret:        secret,
		Issuers:       issuers,
		Audience:      audience,
		MaxTTL:        maxTTL,
		TenantID:      tenant,
		ProjectID:     project,
		ReadSubjects:  splitCSV(os.Getenv("IOT_ENERGY_READ_SUBJECTS")),
		AdminSubjects: splitCSV(os.Getenv("IOT_ENERGY_ADMIN_SUBJECTS")),
	}, true
}

// BuildGuard constructs a hardened, fail-closed authguard.Guard from EnvAuthConfig.
func BuildGuard(cfg EnvAuthConfig, revocations authguard.RevocationChecker, now func() time.Time) (*authguard.Guard, *authguard.ScopedPolicy, error) {
	if revocations == nil {
		revocations = NewMemoryRevocationStore()
	}
	project := buildingenergy.ProjectKey{TenantID: cfg.TenantID, ProjectID: cfg.ProjectID}
	validFrom := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	var grants []authguard.Grant
	for _, iss := range cfg.Issuers {
		for _, sub := range cfg.ReadSubjects {
			grants = append(grants, authguard.Grant{
				Subject:          buildingenergy.Subject{Issuer: iss, ID: sub},
				Actions:          []buildingenergy.Action{buildingenergy.ReadUsage, buildingenergy.ExportUsage, buildingenergy.DownloadReport},
				Project:          project,
				AllowProjectWide: true,
				AllowAllPoints:   true,
				ValidRange:       buildingenergy.Range{From: validFrom},
			})
		}
		for _, sub := range cfg.AdminSubjects {
			grants = append(grants, authguard.Grant{
				Subject:          buildingenergy.Subject{Issuer: iss, ID: sub},
				Actions:          []buildingenergy.Action{buildingenergy.ReadUsage, buildingenergy.ExportUsage, buildingenergy.DownloadReport, buildingenergy.ManageBindings},
				Project:          project,
				AllowProjectWide: true,
				AllowAllPoints:   true,
				ValidRange:       buildingenergy.Range{From: validFrom},
			})
		}
	}

	policy := &authguard.ScopedPolicy{
		Grants:      grants,
		Revocations: revocations,
	}
	guard := &authguard.Guard{
		Authenticator: &HMACBearerAuthenticator{Secret: []byte(cfg.Secret)},
		Identity: authguard.ContextIdentitySource{
			AllowedIssuers:   cfg.Issuers,
			ExpectedAudience: cfg.Audience,
			MaxCredentialTTL: cfg.MaxTTL,
			Revocations:      revocations,
			Now:              now,
		},
		Policy: policy,
	}
	if err := guard.ValidateFormalConfig(); err != nil {
		return nil, nil, err
	}
	return guard, policy, nil
}

func splitCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
