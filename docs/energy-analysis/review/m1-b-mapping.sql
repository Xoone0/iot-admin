-- M1-B candidate for review. Apply only after approval in a disposable database.
-- DBA prerequisite for this variant: btree_gist default GiST equality operator
-- classes for text and bigint installed in this database. Deployment ownership
-- and the trusted search_path are subject to DBA approval.
BEGIN;

CREATE SCHEMA energy_mapping;

CREATE TABLE energy_mapping.energy_mapping_head (
    tenant_id text NOT NULL CHECK (btrim(tenant_id) <> ''),
    project_id text NOT NULL CHECK (btrim(project_id) <> ''),
    terminal_id text NOT NULL CHECK (btrim(terminal_id) <> ''),
    item_code text NOT NULL CHECK (btrim(item_code) <> ''),
    current_generation bigint CHECK (current_generation > 0),
    PRIMARY KEY (tenant_id, project_id, terminal_id, item_code)
);

CREATE TABLE energy_mapping.energy_mapping_generation (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    terminal_id text NOT NULL,
    item_code text NOT NULL,
    generation bigint NOT NULL CHECK (generation > 0),
    previous_generation bigint,
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(recorded_at)),
    published_by_issuer text NOT NULL CHECK (btrim(published_by_issuer) <> ''),
    published_by_sub text NOT NULL CHECK (btrim(published_by_sub) <> ''),
    approved_at timestamptz NOT NULL CHECK (isfinite(approved_at)),
    approval_system text NOT NULL CHECK (btrim(approval_system) <> ''),
    approval_id text NOT NULL CHECK (btrim(approval_id) <> ''),
    reason text NOT NULL CHECK (btrim(reason) <> ''),
    PRIMARY KEY (tenant_id, project_id, terminal_id, item_code, generation),
    FOREIGN KEY (tenant_id, project_id, terminal_id, item_code)
        REFERENCES energy_mapping.energy_mapping_head (tenant_id, project_id, terminal_id, item_code),
    FOREIGN KEY (tenant_id, project_id, terminal_id, item_code, previous_generation)
        REFERENCES energy_mapping.energy_mapping_generation (tenant_id, project_id, terminal_id, item_code, generation),
    CHECK (
        (generation = 1 AND previous_generation IS NULL)
        OR (generation > 1 AND previous_generation IS NOT NULL AND previous_generation = generation - 1)
    )
);

ALTER TABLE energy_mapping.energy_mapping_head
    ADD CONSTRAINT current_generation_fk
        FOREIGN KEY (tenant_id, project_id, terminal_id, item_code, current_generation)
        REFERENCES energy_mapping.energy_mapping_generation (tenant_id, project_id, terminal_id, item_code, generation);

CREATE TABLE energy_mapping.energy_point_profile (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    terminal_id text NOT NULL,
    item_code text NOT NULL,
    generation bigint NOT NULL,
    revision text NOT NULL CHECK (btrim(revision) <> ''),
    effective tstzrange NOT NULL,
    source_system text NOT NULL CHECK (btrim(source_system) <> ''),
    source_id text NOT NULL CHECK (btrim(source_id) <> ''),
    channel_system text NOT NULL CHECK (btrim(channel_system) <> ''),
    channel_id text NOT NULL CHECK (btrim(channel_id) <> ''),
    register_epoch text,
    measurement_kind text NOT NULL CHECK (measurement_kind IN ('cumulative', 'interval', 'instantaneous')),
    energy_type text NOT NULL CHECK (btrim(energy_type) <> ''),
    unit text NOT NULL CHECK (btrim(unit) <> ''),
    multiplier numeric CHECK (multiplier > 0 AND multiplier::text NOT IN ('NaN', 'Infinity', '-Infinity')),
    register_modulus numeric CHECK (register_modulus > 0 AND register_modulus::text NOT IN ('NaN', 'Infinity', '-Infinity')),
    cadence_seconds integer CHECK (cadence_seconds > 0),
    PRIMARY KEY (tenant_id, project_id, terminal_id, item_code, generation, revision),
    FOREIGN KEY (tenant_id, project_id, terminal_id, item_code, generation)
        REFERENCES energy_mapping.energy_mapping_generation (tenant_id, project_id, terminal_id, item_code, generation),
    CHECK (NOT isempty(effective) AND NOT lower_inf(effective) AND lower_inc(effective)
        AND NOT upper_inc(effective) AND isfinite(lower(effective))
        AND (upper_inf(effective) OR isfinite(upper(effective)))),
    CHECK ((measurement_kind = 'cumulative' AND register_epoch IS NOT NULL AND btrim(register_epoch) <> '')
        OR (measurement_kind <> 'cumulative' AND register_epoch IS NULL AND register_modulus IS NULL)),
    CHECK (measurement_kind = 'instantaneous' OR (
        multiplier IS NOT NULL AND cadence_seconds BETWEEN 60 AND 3600 AND cadence_seconds IS NOT NULL
        AND cadence_seconds % 60 = 0 AND 3600 % NULLIF(cadence_seconds, 0) = 0
    )),
    CONSTRAINT energy_point_profile_no_overlap EXCLUDE USING gist (
        tenant_id WITH =, project_id WITH =, terminal_id WITH =, item_code WITH =,
        generation WITH =, effective WITH &&
    )
);

CREATE TABLE energy_mapping.energy_point_binding (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    terminal_id text NOT NULL,
    item_code text NOT NULL,
    generation bigint NOT NULL,
    view_kind text NOT NULL CHECK (view_kind IN ('building', 'space', 'organization')),
    view_key text NOT NULL DEFAULT 'default' CHECK (view_key = 'default'),
    revision text NOT NULL CHECK (btrim(revision) <> ''),
    effective tstzrange NOT NULL,
    source_system text NOT NULL CHECK (btrim(source_system) <> ''),
    source_id text NOT NULL CHECK (btrim(source_id) <> ''),
    catalog_snapshot_ref text NOT NULL CHECK (btrim(catalog_snapshot_ref) <> ''),
    PRIMARY KEY (tenant_id, project_id, terminal_id, item_code, generation, view_kind, view_key, revision),
    FOREIGN KEY (tenant_id, project_id, terminal_id, item_code, generation)
        REFERENCES energy_mapping.energy_mapping_generation (tenant_id, project_id, terminal_id, item_code, generation),
    CHECK (NOT isempty(effective) AND NOT lower_inf(effective) AND lower_inc(effective)
        AND NOT upper_inc(effective) AND isfinite(lower(effective))
        AND (upper_inf(effective) OR isfinite(upper(effective)))),
    CONSTRAINT energy_point_binding_no_overlap EXCLUDE USING gist (
        tenant_id WITH =, project_id WITH =, terminal_id WITH =, item_code WITH =,
        generation WITH =, view_kind WITH =, view_key WITH =, effective WITH &&
    )
);

CREATE TABLE energy_mapping.energy_point_binding_allocation (
    tenant_id text NOT NULL,
    project_id text NOT NULL,
    terminal_id text NOT NULL,
    item_code text NOT NULL,
    generation bigint NOT NULL,
    view_kind text NOT NULL,
    view_key text NOT NULL,
    binding_revision text NOT NULL,
    target_id text NOT NULL CHECK (btrim(target_id) <> ''),
    target_revision text NOT NULL CHECK (btrim(target_revision) <> ''),
    weight_ppm integer NOT NULL CHECK (weight_ppm BETWEEN 1 AND 1000000),
    PRIMARY KEY (tenant_id, project_id, terminal_id, item_code, generation, view_kind, view_key, binding_revision, target_id),
    FOREIGN KEY (tenant_id, project_id, terminal_id, item_code, generation, view_kind, view_key, binding_revision)
        REFERENCES energy_mapping.energy_point_binding (
            tenant_id, project_id, terminal_id, item_code, generation, view_kind, view_key, revision
        )
);

REVOKE ALL ON SCHEMA energy_mapping FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA energy_mapping FROM PUBLIC;

COMMIT;

-- The publisher must verify overallocation and authoritative target membership
-- before CAS. Write grants and the rebuild/outbox contract require approval.
-- The lock-only variant removes both named EXCLUDE constraints from the draft,
-- then checks overlap and allocation sums inside the serialized publish transaction.
