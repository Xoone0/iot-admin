package pgadapter

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

// DirectoryCatalog implements buildingenergy.Catalog against the shared
// platform_directory schema (docs/energy-analysis/review/platform-directory-candidate.sql).
// It reads immutable project/object revisions effective at the requested instant.
type DirectoryCatalog struct {
	DB Querier
}

// NewDirectoryCatalog creates a read-only platform directory catalog adapter.
func NewDirectoryCatalog(db Querier) *DirectoryCatalog {
	return &DirectoryCatalog{DB: db}
}

const selectProjectRevisionSQL = `
SELECT tenant_id, project_id, revision, lower(effective), upper(effective),
       timezone, source_system, source_id
FROM platform_directory.project_revision
WHERE tenant_id = $1 AND project_id = $2 AND effective @> $3::timestamptz`

const selectObjectRevisionSQL = `
SELECT tenant_id, project_id, object_kind, object_id, revision,
       lower(effective), upper(effective),
       parent_kind, parent_id, building_id, source_system, source_id
FROM platform_directory.object_revision
WHERE tenant_id = $1 AND project_id = $2 AND object_kind = $3 AND object_id = $4
  AND effective @> $5::timestamptz`

// Project implements buildingenergy.Catalog.
func (c *DirectoryCatalog) Project(ctx context.Context, key buildingenergy.ProjectKey, at time.Time) (buildingenergy.Project, error) {
	if c == nil || isNilInterface(c.DB) {
		return buildingenergy.Project{}, buildingenergy.ErrCatalogScope
	}
	if at.IsZero() {
		return buildingenergy.Project{}, buildingenergy.ErrInvalidScope
	}
	var (
		tenantID, projectID, revision, tz, srcSys, srcID string
		effFrom                                          time.Time
		effTo                                            *time.Time
	)
	err := c.DB.QueryRow(ctx, selectProjectRevisionSQL, key.TenantID, key.ProjectID, at.UTC()).
		Scan(&tenantID, &projectID, &revision, &effFrom, &effTo, &tz, &srcSys, &srcID)
	if errors.Is(err, pgx.ErrNoRows) {
		return buildingenergy.Project{}, buildingenergy.ErrCatalogScope
	}
	if err != nil {
		return buildingenergy.Project{}, err
	}
	if tenantID != key.TenantID || projectID != key.ProjectID ||
		strings.TrimSpace(revision) == "" || strings.TrimSpace(srcSys) == "" || strings.TrimSpace(srcID) == "" {
		return buildingenergy.Project{}, buildingenergy.ErrCatalogScope
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return buildingenergy.Project{}, buildingenergy.ErrCatalogScope
	}
	eff, err := RangeFromBounds(effFrom, effTo)
	if err != nil {
		return buildingenergy.Project{}, buildingenergy.ErrCatalogScope
	}
	return buildingenergy.Project{
		Key:      buildingenergy.ProjectKey{TenantID: tenantID, ProjectID: projectID},
		Timezone: tz,
		Meta: buildingenergy.VersionMeta{
			Version:   revision,
			Effective: eff,
			Source:    buildingenergy.SourceRef{System: srcSys, ID: srcID},
		},
	}, nil
}

// Object implements buildingenergy.Catalog.
func (c *DirectoryCatalog) Object(ctx context.Context, project buildingenergy.ProjectKey, key buildingenergy.ObjectKey, at time.Time) (buildingenergy.Object, error) {
	if c == nil || isNilInterface(c.DB) {
		return buildingenergy.Object{}, buildingenergy.ErrCatalogScope
	}
	if at.IsZero() {
		return buildingenergy.Object{}, buildingenergy.ErrInvalidScope
	}
	var (
		tenantID, projectID, kind, id, revision, srcSys, srcID string
		effFrom                                                time.Time
		effTo                                                  *time.Time
		parentKind, parentID, buildingID                       *string
	)
	err := c.DB.QueryRow(ctx, selectObjectRevisionSQL, project.TenantID, project.ProjectID, string(key.Kind), key.ID, at.UTC()).
		Scan(&tenantID, &projectID, &kind, &id, &revision, &effFrom, &effTo, &parentKind, &parentID, &buildingID, &srcSys, &srcID)
	if errors.Is(err, pgx.ErrNoRows) {
		return buildingenergy.Object{}, buildingenergy.ErrCatalogScope
	}
	if err != nil {
		return buildingenergy.Object{}, err
	}
	if tenantID != project.TenantID || projectID != project.ProjectID ||
		buildingenergy.ObjectKind(kind) != key.Kind || id != key.ID ||
		strings.TrimSpace(revision) == "" || strings.TrimSpace(srcSys) == "" || strings.TrimSpace(srcID) == "" {
		return buildingenergy.Object{}, buildingenergy.ErrCatalogScope
	}
	eff, err := RangeFromBounds(effFrom, effTo)
	if err != nil {
		return buildingenergy.Object{}, buildingenergy.ErrCatalogScope
	}
	obj := buildingenergy.Object{
		Project: buildingenergy.ProjectKey{TenantID: tenantID, ProjectID: projectID},
		Key:     buildingenergy.ObjectKey{Kind: buildingenergy.ObjectKind(kind), ID: id},
		Meta: buildingenergy.VersionMeta{
			Version:   revision,
			Effective: eff,
			Source:    buildingenergy.SourceRef{System: srcSys, ID: srcID},
		},
	}
	if parentKind != nil && parentID != nil {
		obj.Parent = buildingenergy.ObjectKey{Kind: buildingenergy.ObjectKind(*parentKind), ID: *parentID}
	}
	if buildingID != nil {
		obj.BuildingID = *buildingID
	}
	return obj, nil
}
