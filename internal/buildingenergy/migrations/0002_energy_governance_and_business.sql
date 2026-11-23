-- Building Energy Management incremental migration 0002.
-- Additive-only extensions under energy_domain for:
--   P1: metering_point_head enabled/disabled governance + audit timestamp
--   P3: building_revision region field for industry standard applicability
--   P4: versioned annual energy targets (F05/F14), anomaly rules (F10/F17),
--       business denominators (F11/F12/F13/F22/F25), benchmark standards (F13/F22/F25),
--       and restricted custom report templates (F25).
--
-- IMPORTANT: Executed ONLY via explicit internal/buildingenergy/migrations.Apply
-- on confirmed local/staging development databases. Never auto-executed on FNOS startup.

ALTER TABLE energy_domain.metering_point_head
    ADD COLUMN IF NOT EXISTS enabled boolean NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    ADD COLUMN IF NOT EXISTS status_reason text NOT NULL DEFAULT '';

ALTER TABLE energy_domain.building_revision
    ADD COLUMN IF NOT EXISTS region text NOT NULL DEFAULT '';

-- 9. Versioned annual energy targets (F05 summary & F14 monthly breakdown).
CREATE TABLE IF NOT EXISTS energy_domain.energy_target_revision (
    tenant_id       text NOT NULL CHECK (btrim(tenant_id) <> ''),
    project_id      text NOT NULL CHECK (btrim(project_id) <> ''),
    object_kind     text NOT NULL CHECK (object_kind IN ('building', 'space', 'organization')),
    object_id       text NOT NULL CHECK (btrim(object_id) <> ''),
    energy_type     text NOT NULL CHECK (energy_type IN ('electricity', 'water', 'gas', 'cooling', 'heat', 'steam')),
    target_year     integer NOT NULL CHECK (target_year BETWEEN 2000 AND 2100),
    revision        text NOT NULL CHECK (btrim(revision) <> ''),
    effective       tstzrange NOT NULL,
    unit            text NOT NULL CHECK (btrim(unit) <> ''),
    annual_milli    bigint NOT NULL CHECK (annual_milli >= 0),
    method          text NOT NULL CHECK (method IN ('linear', 'weighted')),
    weight_ppm      integer[],
    source_system   text NOT NULL DEFAULT 'energy-admin' CHECK (btrim(source_system) <> ''),
    source_id       text NOT NULL CHECK (btrim(source_id) <> ''),
    recorded_at     timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, project_id, object_kind, object_id, energy_type, target_year, revision),
    CHECK (NOT isempty(effective) AND NOT lower_inf(effective) AND lower_inc(effective) AND NOT upper_inc(effective)),
    CHECK (
        (method = 'linear' AND (weight_ppm IS NULL OR array_length(weight_ppm, 1) IS NULL))
        OR (method = 'weighted' AND array_length(weight_ppm, 1) = 12)
    )
);
CREATE INDEX IF NOT EXISTS idx_energy_domain_target_lookup
    ON energy_domain.energy_target_revision (tenant_id, project_id, object_kind, object_id, energy_type, target_year);

-- 10. Versioned usage anomaly detection rules (F10 & F17).
CREATE TABLE IF NOT EXISTS energy_domain.anomaly_rule_revision (
    tenant_id                 text NOT NULL CHECK (btrim(tenant_id) <> ''),
    project_id                text NOT NULL CHECK (btrim(project_id) <> ''),
    object_kind               text NOT NULL CHECK (object_kind IN ('building', 'space', 'organization')),
    object_id                 text NOT NULL CHECK (btrim(object_id) <> ''),
    energy_type               text NOT NULL CHECK (energy_type IN ('electricity', 'water', 'gas', 'cooling', 'heat', 'steam')),
    revision                  text NOT NULL CHECK (btrim(revision) <> ''),
    effective                 tstzrange NOT NULL,
    minimum_history           integer NOT NULL CHECK (minimum_history >= 2 AND minimum_history <= 52),
    minimum_absolute_increase double precision NOT NULL CHECK (minimum_absolute_increase > 0),
    minimum_relative_increase double precision NOT NULL CHECK (minimum_relative_increase > 0),
    source_system             text NOT NULL DEFAULT 'energy-admin' CHECK (btrim(source_system) <> ''),
    source_id                 text NOT NULL CHECK (btrim(source_id) <> ''),
    recorded_at               timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, project_id, object_kind, object_id, energy_type, revision),
    CHECK (NOT isempty(effective) AND NOT lower_inf(effective) AND lower_inc(effective) AND NOT upper_inc(effective))
);
CREATE INDEX IF NOT EXISTS idx_energy_domain_anomaly_rule_lookup
    ON energy_domain.anomaly_rule_revision (tenant_id, project_id, object_kind, object_id, energy_type);

-- 11. Versioned business denominators (F11/F12/F13/F22/F25: area, people, bed_days, room_nights, revenue, students).
CREATE TABLE IF NOT EXISTS energy_domain.business_denominator_revision (
    tenant_id        text NOT NULL CHECK (btrim(tenant_id) <> ''),
    project_id       text NOT NULL CHECK (btrim(project_id) <> ''),
    object_kind      text NOT NULL CHECK (object_kind IN ('building', 'space', 'organization')),
    object_id        text NOT NULL CHECK (btrim(object_id) <> ''),
    denominator_kind text NOT NULL CHECK (denominator_kind IN ('area', 'people', 'bed_days', 'room_nights', 'revenue', 'students')),
    revision         text NOT NULL CHECK (btrim(revision) <> ''),
    period_range     tstzrange NOT NULL,
    effective        tstzrange NOT NULL,
    value            double precision NOT NULL CHECK (value > 0),
    unit             text NOT NULL CHECK (btrim(unit) <> ''),
    basis            text NOT NULL DEFAULT 'manual_archive' CHECK (btrim(basis) <> ''),
    source_system    text NOT NULL DEFAULT 'energy-admin' CHECK (btrim(source_system) <> ''),
    source_id        text NOT NULL CHECK (btrim(source_id) <> ''),
    recorded_at      timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, project_id, object_kind, object_id, denominator_kind, revision),
    CHECK (NOT isempty(period_range) AND NOT lower_inf(period_range) AND NOT upper_inf(period_range) AND lower_inc(period_range) AND NOT upper_inc(period_range)),
    CHECK (NOT isempty(effective) AND NOT lower_inf(effective) AND lower_inc(effective) AND NOT upper_inc(effective))
);
CREATE INDEX IF NOT EXISTS idx_energy_domain_denominator_lookup
    ON energy_domain.business_denominator_revision (tenant_id, project_id, object_kind, object_id, denominator_kind);

-- 12. Versioned benchmark / quota rules (F13/F22/F25; distinguishes demo vs external standards).
CREATE TABLE IF NOT EXISTS energy_domain.benchmark_rule_revision (
    tenant_id           text NOT NULL CHECK (btrim(tenant_id) <> ''),
    project_id          text NOT NULL CHECK (btrim(project_id) <> ''),
    benchmark_id        text NOT NULL CHECK (btrim(benchmark_id) <> ''),
    revision            text NOT NULL CHECK (btrim(revision) <> ''),
    effective           tstzrange NOT NULL,
    name                text NOT NULL CHECK (btrim(name) <> ''),
    publisher           text NOT NULL CHECK (btrim(publisher) <> ''),
    region              text NOT NULL CHECK (btrim(region) <> ''),
    metric_id           text NOT NULL CHECK (btrim(metric_id) <> ''),
    template_version    text NOT NULL CHECK (btrim(template_version) <> ''),
    unit                text NOT NULL CHECK (btrim(unit) <> ''),
    kind                text NOT NULL CHECK (kind IN ('demo', 'external')),
    building_type       text NOT NULL CHECK (building_type IN ('office', 'hospital', 'school', 'hotel')),
    object_kind         text NOT NULL CHECK (object_kind IN ('building', 'space', 'organization')),
    period_granularity  text NOT NULL CHECK (period_granularity IN ('hour', 'day', 'month')),
    maximum_value       double precision NOT NULL CHECK (maximum_value >= 0),
    minimum_denominator double precision CHECK (minimum_denominator IS NULL OR minimum_denominator > 0),
    source_system       text NOT NULL DEFAULT 'energy-admin' CHECK (btrim(source_system) <> ''),
    source_id           text NOT NULL CHECK (btrim(source_id) <> ''),
    recorded_at         timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, project_id, benchmark_id, revision),
    CHECK (NOT isempty(effective) AND NOT lower_inf(effective) AND lower_inc(effective) AND NOT upper_inc(effective))
);
CREATE INDEX IF NOT EXISTS idx_energy_domain_benchmark_lookup
    ON energy_domain.benchmark_rule_revision (tenant_id, project_id, building_type, metric_id);

-- 13. Restricted custom report templates (F25).
CREATE TABLE IF NOT EXISTS energy_domain.custom_report_template (
    tenant_id    text NOT NULL CHECK (btrim(tenant_id) <> ''),
    project_id   text NOT NULL CHECK (btrim(project_id) <> ''),
    template_id  text NOT NULL CHECK (btrim(template_id) <> ''),
    name         text NOT NULL CHECK (btrim(name) <> ''),
    scope_kind   text NOT NULL CHECK (scope_kind IN ('consumption', 'metrics')),
    fields       text[] NOT NULL CHECK (array_length(fields, 1) >= 1),
    created_by   text NOT NULL CHECK (btrim(created_by) <> ''),
    updated_at   timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, project_id, template_id)
);
