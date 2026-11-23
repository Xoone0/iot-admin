-- M1B-01 candidate for platform review. Apply only after platform/DBA approval
-- in a disposable database. NOT part of official migrations/.
--
-- Ownership boundary:
--   - Owned by the platform core / shared master-data service (NOT energy_mapping).
--   - Building energy analysis consumes this schema read-only via DirectoryCatalog
--     (internal/buildingenergy/pgadapter/catalog.go) so energy analysis never
--     maintains an isolated, un-migratable shadow directory.
--   - If the platform connects to an external authoritative CMDB/BIM/IAM directory,
--     that service either implements buildingenergy.Catalog directly or syncs
--     immutable effective-dated revisions into platform_directory.
--   - Compatible with dev's flat location hierarchy (e.g. energy_locations) by
--     retaining (tenant_id, project_id, object_kind, object_id, parent_kind, parent_id,
--     building_id) while adding immutable effective-dated revisions for AT-08
--     organization/space migration history preservation.
--
-- DBA prerequisite for this GiST variant: btree_gist default GiST equality operator
-- classes for text installed in this database. For the lock-only variant, remove
-- the two named EXCLUDE USING gist constraints and serialize revision writes per
-- (tenant_id, project_id[, object_kind, object_id]) inside the directory publisher.
BEGIN;

CREATE SCHEMA platform_directory;

CREATE TABLE platform_directory.project_revision (
    tenant_id text NOT NULL CHECK (btrim(tenant_id) <> ''),
    project_id text NOT NULL CHECK (btrim(project_id) <> ''),
    revision text NOT NULL CHECK (btrim(revision) <> ''),
    effective tstzrange NOT NULL,
    name text NOT NULL CHECK (btrim(name) <> ''),
    timezone text NOT NULL CHECK (btrim(timezone) <> ''),
    source_system text NOT NULL CHECK (btrim(source_system) <> ''),
    source_id text NOT NULL CHECK (btrim(source_id) <> ''),
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(recorded_at)),
    PRIMARY KEY (tenant_id, project_id, revision),
    CHECK (
        NOT isempty(effective)
        AND NOT lower_inf(effective)
        AND lower_inc(effective)
        AND NOT upper_inc(effective)
        AND isfinite(lower(effective))
        AND (upper_inf(effective) OR isfinite(upper(effective)))
    ),
    CONSTRAINT project_revision_no_overlap EXCLUDE USING gist (
        tenant_id WITH =, project_id WITH =, effective WITH &&
    )
);

CREATE TABLE platform_directory.object_revision (
    tenant_id text NOT NULL CHECK (btrim(tenant_id) <> ''),
    project_id text NOT NULL CHECK (btrim(project_id) <> ''),
    object_kind text NOT NULL CHECK (object_kind IN ('building', 'space', 'organization')),
    object_id text NOT NULL CHECK (btrim(object_id) <> ''),
    revision text NOT NULL CHECK (btrim(revision) <> ''),
    effective tstzrange NOT NULL,
    name text NOT NULL CHECK (btrim(name) <> ''),
    parent_kind text CHECK (parent_kind IS NULL OR parent_kind IN ('building', 'space', 'organization')),
    parent_id text CHECK (parent_id IS NULL OR btrim(parent_id) <> ''),
    building_id text CHECK (building_id IS NULL OR btrim(building_id) <> ''),
    source_system text NOT NULL CHECK (btrim(source_system) <> ''),
    source_id text NOT NULL CHECK (btrim(source_id) <> ''),
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(recorded_at)),
    PRIMARY KEY (tenant_id, project_id, object_kind, object_id, revision),
    CHECK ((parent_kind IS NULL) = (parent_id IS NULL)),
    CHECK (
        NOT isempty(effective)
        AND NOT lower_inf(effective)
        AND lower_inc(effective)
        AND NOT upper_inc(effective)
        AND isfinite(lower(effective))
        AND (upper_inf(effective) OR isfinite(upper(effective)))
    ),
    CHECK (object_kind <> 'building' OR building_id IS NULL OR building_id = object_id),
    CONSTRAINT object_revision_no_overlap EXCLUDE USING gist (
        tenant_id WITH =, project_id WITH =, object_kind WITH =, object_id WITH =, effective WITH &&
    )
);

CREATE INDEX idx_platform_directory_project_effective
    ON platform_directory.project_revision USING gist (tenant_id, project_id, effective);

CREATE INDEX idx_platform_directory_object_effective
    ON platform_directory.object_revision USING gist (tenant_id, project_id, object_kind, object_id, effective);

REVOKE ALL ON SCHEMA platform_directory FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA platform_directory FROM PUBLIC;

COMMIT;

-- Least-privilege guidance (subject to DBA approval):
--   - Platform directory service role: USAGE + SELECT/INSERT on platform_directory
--     (revisions are append-only; historical closures update only upper(effective)
--     or supersede via versioned publication according to platform governance).
--   - Energy analysis runtime role: USAGE + SELECT ONLY on platform_directory.
--   - If deploying without btree_gist (lock-only variant), also drop the two GiST
--     indexes above and create B-tree lookup indexes on (tenant_id, project_id)
--     and (tenant_id, project_id, object_kind, object_id).
