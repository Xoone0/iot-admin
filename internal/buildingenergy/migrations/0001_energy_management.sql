-- Building Energy Management incremental migration 0001.
-- Additive-only schema under energy_domain: does NOT alter or drop any existing
-- public.* table and does NOT delete historical data.
-- Compatible with PostgreSQL 15/16/17 without requiring btree_gist (uses B-tree
-- lookup indexes plus serialized FOR UPDATE publication transactions).
--
-- IMPORTANT: Executed ONLY via explicit internal/buildingenergy/migrations.Apply.
-- Never auto-executed on FNOS cmd/server startup without backup and deployment approval.

CREATE SCHEMA IF NOT EXISTS energy_domain;

CREATE TABLE IF NOT EXISTS energy_domain.schema_migrations (
    version    text PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- 1. Authoritative project archive (effective-dated revisions).
CREATE TABLE IF NOT EXISTS energy_domain.project_revision (
    tenant_id     text NOT NULL CHECK (btrim(tenant_id) <> ''),
    project_id    text NOT NULL CHECK (btrim(project_id) <> ''),
    revision      text NOT NULL CHECK (btrim(revision) <> ''),
    effective     tstzrange NOT NULL,
    name          text NOT NULL CHECK (btrim(name) <> ''),
    timezone      text NOT NULL DEFAULT 'Asia/Shanghai' CHECK (btrim(timezone) <> ''),
    source_system text NOT NULL DEFAULT 'energy-admin' CHECK (btrim(source_system) <> ''),
    source_id     text NOT NULL CHECK (btrim(source_id) <> ''),
    recorded_at   timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, project_id, revision),
    CHECK (NOT isempty(effective) AND NOT lower_inf(effective) AND lower_inc(effective) AND NOT upper_inc(effective))
);

-- 2. Building archive (gross area, occupied headcount, building type, optional link to public.energy_building).
CREATE TABLE IF NOT EXISTS energy_domain.building_revision (
    tenant_id          text NOT NULL CHECK (btrim(tenant_id) <> ''),
    project_id         text NOT NULL CHECK (btrim(project_id) <> ''),
    building_id        text NOT NULL CHECK (btrim(building_id) <> ''),
    revision           text NOT NULL CHECK (btrim(revision) <> ''),
    effective          tstzrange NOT NULL,
    name               text NOT NULL CHECK (btrim(name) <> ''),
    building_type      text NOT NULL DEFAULT 'office' CHECK (building_type IN ('office', 'hospital', 'school', 'hotel', 'other')),
    gross_area_sqm     double precision CHECK (gross_area_sqm IS NULL OR gross_area_sqm > 0),
    occupied_headcount double precision CHECK (occupied_headcount IS NULL OR occupied_headcount > 0),
    timezone           text NOT NULL DEFAULT 'Asia/Shanghai' CHECK (btrim(timezone) <> ''),
    legacy_building_id bigint,
    source_system      text NOT NULL DEFAULT 'energy-admin' CHECK (btrim(source_system) <> ''),
    source_id          text NOT NULL CHECK (btrim(source_id) <> ''),
    recorded_at        timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, project_id, building_id, revision),
    CHECK (NOT isempty(effective) AND NOT lower_inf(effective) AND lower_inc(effective) AND NOT upper_inc(effective))
);
CREATE INDEX IF NOT EXISTS idx_energy_domain_building_lookup
    ON energy_domain.building_revision (tenant_id, project_id, building_id);

-- 3. Space / area hierarchy under buildings (optional link to public.energy_area).
CREATE TABLE IF NOT EXISTS energy_domain.space_revision (
    tenant_id       text NOT NULL CHECK (btrim(tenant_id) <> ''),
    project_id      text NOT NULL CHECK (btrim(project_id) <> ''),
    space_id        text NOT NULL CHECK (btrim(space_id) <> ''),
    revision        text NOT NULL CHECK (btrim(revision) <> ''),
    effective       tstzrange NOT NULL,
    building_id     text NOT NULL CHECK (btrim(building_id) <> ''),
    parent_space_id text CHECK (parent_space_id IS NULL OR btrim(parent_space_id) <> ''),
    name            text NOT NULL CHECK (btrim(name) <> ''),
    area_sqm        double precision CHECK (area_sqm IS NULL OR area_sqm > 0),
    legacy_area_id  bigint,
    source_system   text NOT NULL DEFAULT 'energy-admin' CHECK (btrim(source_system) <> ''),
    source_id       text NOT NULL CHECK (btrim(source_id) <> ''),
    recorded_at     timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, project_id, space_id, revision),
    CHECK (NOT isempty(effective) AND NOT lower_inf(effective) AND lower_inc(effective) AND NOT upper_inc(effective))
);
CREATE INDEX IF NOT EXISTS idx_energy_domain_space_lookup
    ON energy_domain.space_revision (tenant_id, project_id, building_id, space_id);

-- 4. Organization hierarchy (for department/tenant energy attribution).
CREATE TABLE IF NOT EXISTS energy_domain.organization_revision (
    tenant_id     text NOT NULL CHECK (btrim(tenant_id) <> ''),
    project_id    text NOT NULL CHECK (btrim(project_id) <> ''),
    org_id        text NOT NULL CHECK (btrim(org_id) <> ''),
    revision      text NOT NULL CHECK (btrim(revision) <> ''),
    effective     tstzrange NOT NULL,
    parent_org_id text CHECK (parent_org_id IS NULL OR btrim(parent_org_id) <> ''),
    name          text NOT NULL CHECK (btrim(name) <> ''),
    headcount     double precision CHECK (headcount IS NULL OR headcount > 0),
    source_system text NOT NULL DEFAULT 'energy-admin' CHECK (btrim(source_system) <> ''),
    source_id     text NOT NULL CHECK (btrim(source_id) <> ''),
    recorded_at   timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, project_id, org_id, revision),
    CHECK (NOT isempty(effective) AND NOT lower_inf(effective) AND lower_inc(effective) AND NOT upper_inc(effective))
);
CREATE INDEX IF NOT EXISTS idx_energy_domain_org_lookup
    ON energy_domain.organization_revision (tenant_id, project_id, org_id);

-- 5. Versioned metering point configuration (terminal_id + item_code -> energy profile, multiplier, bindings, history).
CREATE TABLE IF NOT EXISTS energy_domain.metering_point_head (
    tenant_id          text NOT NULL CHECK (btrim(tenant_id) <> ''),
    project_id         text NOT NULL CHECK (btrim(project_id) <> ''),
    terminal_id        text NOT NULL CHECK (btrim(terminal_id) <> ''),
    item_code          text NOT NULL CHECK (btrim(item_code) <> ''),
    current_generation bigint CHECK (current_generation > 0),
    PRIMARY KEY (tenant_id, project_id, terminal_id, item_code)
);

CREATE TABLE IF NOT EXISTS energy_domain.metering_point_generation (
    tenant_id           text NOT NULL,
    project_id          text NOT NULL,
    terminal_id         text NOT NULL,
    item_code           text NOT NULL,
    generation          bigint NOT NULL CHECK (generation > 0),
    previous_generation bigint,
    recorded_at         timestamptz NOT NULL DEFAULT clock_timestamp(),
    published_by_issuer text NOT NULL CHECK (btrim(published_by_issuer) <> ''),
    published_by_sub    text NOT NULL CHECK (btrim(published_by_sub) <> ''),
    approved_at         timestamptz NOT NULL,
    approval_system     text NOT NULL CHECK (btrim(approval_system) <> ''),
    approval_id         text NOT NULL CHECK (btrim(approval_id) <> ''),
    reason              text NOT NULL CHECK (btrim(reason) <> ''),
    PRIMARY KEY (tenant_id, project_id, terminal_id, item_code, generation),
    FOREIGN KEY (tenant_id, project_id, terminal_id, item_code)
        REFERENCES energy_domain.metering_point_head (tenant_id, project_id, terminal_id, item_code),
    CHECK ((generation = 1 AND previous_generation IS NULL) OR (generation > 1 AND previous_generation = generation - 1))
);

CREATE TABLE IF NOT EXISTS energy_domain.metering_point_profile (
    tenant_id        text NOT NULL,
    project_id       text NOT NULL,
    terminal_id      text NOT NULL,
    item_code        text NOT NULL,
    generation       bigint NOT NULL,
    revision         text NOT NULL CHECK (btrim(revision) <> ''),
    effective        tstzrange NOT NULL,
    source_table     text NOT NULL DEFAULT 'snapshot_coarse' CHECK (source_table IN ('snapshot_coarse', 'raw_item')),
    source_system    text NOT NULL CHECK (btrim(source_system) <> ''),
    source_id        text NOT NULL CHECK (btrim(source_id) <> ''),
    channel_system   text NOT NULL CHECK (btrim(channel_system) <> ''),
    channel_id       text NOT NULL CHECK (btrim(channel_id) <> ''),
    register_epoch   text,
    measurement_kind text NOT NULL CHECK (measurement_kind IN ('cumulative', 'interval', 'instantaneous')),
    energy_type      text NOT NULL CHECK (energy_type IN ('electricity', 'water', 'gas', 'cooling', 'heat', 'steam')),
    unit             text NOT NULL CHECK (btrim(unit) <> ''),
    multiplier       numeric CHECK (multiplier > 0),
    register_modulus numeric CHECK (register_modulus > 0),
    cadence_seconds  integer CHECK (cadence_seconds BETWEEN 60 AND 3600 AND cadence_seconds % 60 = 0 AND 3600 % cadence_seconds = 0),
    PRIMARY KEY (tenant_id, project_id, terminal_id, item_code, generation, revision),
    FOREIGN KEY (tenant_id, project_id, terminal_id, item_code, generation)
        REFERENCES energy_domain.metering_point_generation (tenant_id, project_id, terminal_id, item_code, generation),
    CHECK (NOT isempty(effective) AND NOT lower_inf(effective) AND lower_inc(effective) AND NOT upper_inc(effective))
);

CREATE TABLE IF NOT EXISTS energy_domain.metering_point_binding (
    tenant_id            text NOT NULL,
    project_id           text NOT NULL,
    terminal_id          text NOT NULL,
    item_code            text NOT NULL,
    generation           bigint NOT NULL,
    view_kind            text NOT NULL CHECK (view_kind IN ('building', 'space', 'organization')),
    revision             text NOT NULL CHECK (btrim(revision) <> ''),
    effective            tstzrange NOT NULL,
    source_system        text NOT NULL CHECK (btrim(source_system) <> ''),
    source_id            text NOT NULL CHECK (btrim(source_id) <> ''),
    catalog_snapshot_ref text NOT NULL CHECK (btrim(catalog_snapshot_ref) <> ''),
    PRIMARY KEY (tenant_id, project_id, terminal_id, item_code, generation, view_kind, revision),
    FOREIGN KEY (tenant_id, project_id, terminal_id, item_code, generation)
        REFERENCES energy_domain.metering_point_generation (tenant_id, project_id, terminal_id, item_code, generation),
    CHECK (NOT isempty(effective) AND NOT lower_inf(effective) AND lower_inc(effective) AND NOT upper_inc(effective))
);

CREATE TABLE IF NOT EXISTS energy_domain.metering_point_binding_allocation (
    tenant_id        text NOT NULL,
    project_id       text NOT NULL,
    terminal_id      text NOT NULL,
    item_code        text NOT NULL,
    generation       bigint NOT NULL,
    view_kind        text NOT NULL,
    binding_revision text NOT NULL,
    target_id        text NOT NULL CHECK (btrim(target_id) <> ''),
    target_revision  text NOT NULL CHECK (btrim(target_revision) <> ''),
    weight_ppm       integer NOT NULL CHECK (weight_ppm BETWEEN 1 AND 1000000),
    PRIMARY KEY (tenant_id, project_id, terminal_id, item_code, generation, view_kind, binding_revision, target_id),
    FOREIGN KEY (tenant_id, project_id, terminal_id, item_code, generation, view_kind, binding_revision)
        REFERENCES energy_domain.metering_point_binding (tenant_id, project_id, terminal_id, item_code, generation, view_kind, revision)
);

-- 6. Classification taxonomy & point-to-category assignments (F02/F06/F07/F08/F11).
CREATE TABLE IF NOT EXISTS energy_domain.energy_category (
    tenant_id      text NOT NULL CHECK (btrim(tenant_id) <> ''),
    project_id     text NOT NULL CHECK (btrim(project_id) <> ''),
    category_id    text NOT NULL CHECK (btrim(category_id) <> ''),
    parent_id      text NOT NULL DEFAULT '',
    name           text NOT NULL CHECK (btrim(name) <> ''),
    energy_type    text NOT NULL CHECK (energy_type IN ('electricity', 'water', 'gas', 'cooling', 'heat', 'steam')),
    PRIMARY KEY (tenant_id, project_id, category_id)
);

CREATE TABLE IF NOT EXISTS energy_domain.point_category_binding (
    tenant_id   text NOT NULL,
    project_id  text NOT NULL,
    terminal_id text NOT NULL,
    item_code   text NOT NULL,
    category_id text NOT NULL,
    revision    text NOT NULL CHECK (btrim(revision) <> ''),
    effective   tstzrange NOT NULL,
    weight_ppm  integer NOT NULL CHECK (weight_ppm BETWEEN 1 AND 1000000),
    PRIMARY KEY (tenant_id, project_id, terminal_id, item_code, category_id, revision),
    FOREIGN KEY (tenant_id, project_id, category_id)
        REFERENCES energy_domain.energy_category (tenant_id, project_id, category_id),
    CHECK (NOT isempty(effective) AND NOT lower_inf(effective) AND lower_inc(effective) AND NOT upper_inc(effective))
);

-- 7. Physical parent-child total/submeter topology (F09 balance).
CREATE TABLE IF NOT EXISTS energy_domain.meter_topology (
    tenant_id          text NOT NULL CHECK (btrim(tenant_id) <> ''),
    project_id         text NOT NULL CHECK (btrim(project_id) <> ''),
    topology_version   text NOT NULL CHECK (btrim(topology_version) <> ''),
    effective          tstzrange NOT NULL,
    building_id        text NOT NULL CHECK (btrim(building_id) <> ''),
    energy_type        text NOT NULL CHECK (energy_type IN ('electricity', 'water', 'gas', 'cooling', 'heat', 'steam')),
    parent_terminal_id text NOT NULL CHECK (btrim(parent_terminal_id) <> ''),
    parent_item_code   text NOT NULL CHECK (btrim(parent_item_code) <> ''),
    child_terminal_id  text NOT NULL CHECK (btrim(child_terminal_id) <> ''),
    child_item_code    text NOT NULL CHECK (btrim(child_item_code) <> ''),
    zone_code          text NOT NULL CHECK (btrim(zone_code) <> ''),
    source_system      text NOT NULL DEFAULT 'energy-admin' CHECK (btrim(source_system) <> ''),
    source_id          text NOT NULL CHECK (btrim(source_id) <> ''),
    PRIMARY KEY (tenant_id, project_id, topology_version, building_id, energy_type, parent_terminal_id, child_terminal_id),
    CHECK (parent_terminal_id <> child_terminal_id),
    CHECK (NOT isempty(effective) AND NOT lower_inf(effective) AND lower_inc(effective) AND NOT upper_inc(effective))
);

-- 8. Confirmed meter register events (meter swap, rollover, manual/suspect quality markers).
CREATE TABLE IF NOT EXISTS energy_domain.meter_register_event (
    id           bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
    tenant_id    text NOT NULL CHECK (btrim(tenant_id) <> ''),
    project_id   text NOT NULL CHECK (btrim(project_id) <> ''),
    terminal_id  text NOT NULL CHECK (btrim(terminal_id) <> ''),
    item_code    text NOT NULL CHECK (btrim(item_code) <> ''),
    event_kind   text NOT NULL CHECK (event_kind IN ('meter_swap', 'rollover', 'quality_suspect', 'quality_corrected')),
    effective_at timestamptz NOT NULL,
    new_epoch    text,
    reason       text NOT NULL CHECK (btrim(reason) <> ''),
    actor_issuer text NOT NULL CHECK (btrim(actor_issuer) <> ''),
    actor_sub    text NOT NULL CHECK (btrim(actor_sub) <> ''),
    recorded_at  timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX IF NOT EXISTS idx_energy_domain_register_event_lookup
    ON energy_domain.meter_register_event (tenant_id, project_id, terminal_id, item_code, effective_at);
