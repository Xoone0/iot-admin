-- Building Energy Management incremental migration 0004.
-- Additive-only extensions under energy_domain for F18/F19/F20 online lifecycle management:
--   1. Optional space_id scoping on energy_domain.retrofit_project
--   2. Immutable baseline version ledger (energy_domain.retrofit_baseline_revision)
--   3. Auditable F20 M&V verification log ledger (energy_domain.retrofit_verification_log)
--
-- IMPORTANT: Executed ONLY via explicit internal/buildingenergy/migrations.Apply
-- on confirmed local development databases. Never drops or resets existing rows.

ALTER TABLE energy_domain.retrofit_project
    ADD COLUMN IF NOT EXISTS space_id text NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS energy_domain.retrofit_baseline_revision (
    tenant_id             text NOT NULL CHECK (btrim(tenant_id) <> ''),
    project_id            text NOT NULL CHECK (btrim(project_id) <> ''),
    retrofit_id           text NOT NULL CHECK (btrim(retrofit_id) <> ''),
    baseline_version      text NOT NULL CHECK (btrim(baseline_version) <> ''),
    building_id           text NOT NULL CHECK (btrim(building_id) <> ''),
    space_id              text NOT NULL DEFAULT '',
    energy_type           text NOT NULL CHECK (energy_type IN ('electricity', 'water', 'gas', 'cooling', 'heat', 'steam')),
    baseline_from         timestamptz NOT NULL,
    baseline_to           timestamptz NOT NULL,
    baseline_model        text NOT NULL CHECK (btrim(baseline_model) <> ''),
    baseline_factors      text NOT NULL CHECK (btrim(baseline_factors) <> ''),
    baseline_r2           double precision CHECK (baseline_r2 IS NULL OR (baseline_r2 >= 0 AND baseline_r2 <= 1)),
    baseline_coverage     double precision NOT NULL CHECK (baseline_coverage >= 0 AND baseline_coverage <= 1),
    baseline_locked       boolean NOT NULL DEFAULT false,
    baseline_daily_avg    double precision NOT NULL CHECK (baseline_daily_avg >= 0),
    baseline_actual_usage double precision,
    change_reason         text NOT NULL DEFAULT '',
    actor_sub             text NOT NULL DEFAULT 'system',
    recorded_at           timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, project_id, retrofit_id, baseline_version)
);

CREATE TABLE IF NOT EXISTS energy_domain.retrofit_verification_log (
    id                     bigserial PRIMARY KEY,
    tenant_id              text NOT NULL CHECK (btrim(tenant_id) <> ''),
    project_id             text NOT NULL CHECK (btrim(project_id) <> ''),
    retrofit_id            text NOT NULL CHECK (btrim(retrofit_id) <> ''),
    baseline_version       text NOT NULL CHECK (btrim(baseline_version) <> ''),
    report_from            timestamptz NOT NULL,
    report_to              timestamptz NOT NULL,
    adjusted_baseline_use  double precision,
    report_actual_usage    double precision,
    report_coverage_rate   double precision,
    non_routine_adjustment double precision NOT NULL DEFAULT 0,
    verified_savings       double precision,
    verified_savings_rate  double precision,
    verified_carbon_kgco2e double precision,
    weather_data_complete  boolean NOT NULL DEFAULT false,
    mv_status              text NOT NULL CHECK (mv_status IN ('verified_simulated', 'pending_verification', 'estimated_only')),
    mv_note                text NOT NULL DEFAULT '',
    actor_sub              text NOT NULL DEFAULT 'system',
    recorded_at            timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX IF NOT EXISTS idx_retrofit_verif_log_lookup
    ON energy_domain.retrofit_verification_log (tenant_id, project_id, retrofit_id, recorded_at DESC);

-- Backfill existing retrofit projects into baseline revision history idempotently.
INSERT INTO energy_domain.retrofit_baseline_revision
  (tenant_id, project_id, retrofit_id, baseline_version, building_id, space_id, energy_type,
   baseline_from, baseline_to, baseline_model, baseline_factors, baseline_r2, baseline_coverage,
   baseline_locked, baseline_daily_avg, change_reason, actor_sub)
SELECT
  tenant_id, project_id, retrofit_id, baseline_version, building_id, space_id, energy_type,
  baseline_from, baseline_to, baseline_model, baseline_factors, baseline_r2, baseline_coverage,
  baseline_locked, baseline_daily_avg, '初始基线版本（既有项目同步）', 'migration-0004'
FROM energy_domain.retrofit_project
ON CONFLICT (tenant_id, project_id, retrofit_id, baseline_version) DO NOTHING;
