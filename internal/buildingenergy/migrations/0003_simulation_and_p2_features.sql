-- Building Energy Management incremental migration 0003.
-- Additive-only extensions under energy_domain for:
--   1. Unified 5-energy simulation dataset metadata ledger (dataset_metadata)
--   2. Commercial building type support in building_revision & benchmark_rule_revision
--   3. F15 Organization/Area Assessment rules & review records (assessment_rule, assessment_review)
--   4. F18/F19/F20/F24 Retrofit project, pre-retrofit baseline, potential scenario & M&V records
--
-- IMPORTANT: Executed ONLY via explicit internal/buildingenergy/migrations.Apply
-- on confirmed local development databases. Never auto-executed on FNOS startup.

ALTER TABLE energy_domain.building_revision
    DROP CONSTRAINT IF EXISTS building_revision_building_type_check;
ALTER TABLE energy_domain.building_revision
    ADD CONSTRAINT building_revision_building_type_check
    CHECK (building_type IN ('office', 'commercial', 'hospital', 'school', 'hotel', 'other'));

ALTER TABLE energy_domain.benchmark_rule_revision
    DROP CONSTRAINT IF EXISTS benchmark_rule_revision_building_type_check;
ALTER TABLE energy_domain.benchmark_rule_revision
    ADD CONSTRAINT benchmark_rule_revision_building_type_check
    CHECK (building_type IN ('office', 'commercial', 'hospital', 'school', 'hotel'));

-- 14. Unified simulation dataset metadata (single source of truth for all F01-F25 pages & CSV exports).
CREATE TABLE IF NOT EXISTS energy_domain.dataset_metadata (
    tenant_id        text NOT NULL CHECK (btrim(tenant_id) <> ''),
    project_id       text NOT NULL CHECK (btrim(project_id) <> ''),
    dataset_id       text NOT NULL CHECK (btrim(dataset_id) <> ''),
    dataset_name     text NOT NULL CHECK (btrim(dataset_name) <> ''),
    dataset_version  text NOT NULL CHECK (btrim(dataset_version) <> ''),
    seed             bigint NOT NULL,
    is_simulated     boolean NOT NULL DEFAULT true,
    generated_at     timestamptz NOT NULL DEFAULT clock_timestamp(),
    data_from        timestamptz NOT NULL,
    data_to          timestamptz NOT NULL,
    hourly_from      timestamptz NOT NULL,
    raw_from         timestamptz NOT NULL,
    building_scope   text NOT NULL CHECK (btrim(building_scope) <> ''),
    energy_scope     text NOT NULL CHECK (btrim(energy_scope) <> ''),
    anomaly_manifest jsonb NOT NULL DEFAULT '[]'::jsonb,
    PRIMARY KEY (tenant_id, project_id, dataset_id)
);

-- 15. F15 Organization / Space Assessment rules & review records (PRD §6.3).
CREATE TABLE IF NOT EXISTS energy_domain.assessment_rule (
    tenant_id          text NOT NULL CHECK (btrim(tenant_id) <> ''),
    project_id         text NOT NULL CHECK (btrim(project_id) <> ''),
    rule_id            text NOT NULL CHECK (btrim(rule_id) <> ''),
    revision           text NOT NULL CHECK (btrim(revision) <> ''),
    effective          tstzrange NOT NULL,
    object_kind        text NOT NULL CHECK (object_kind IN ('organization', 'space', 'building')),
    object_id          text NOT NULL CHECK (btrim(object_id) <> ''),
    energy_type        text NOT NULL CHECK (energy_type IN ('electricity', 'water', 'gas', 'cooling', 'heat', 'steam')),
    metric_label       text NOT NULL CHECK (btrim(metric_label) <> ''),
    target_value       double precision NOT NULL CHECK (target_value >= 0),
    unit               text NOT NULL CHECK (btrim(unit) <> ''),
    weight_pct         double precision NOT NULL CHECK (weight_pct > 0 AND weight_pct <= 100),
    public_share_pct   double precision NOT NULL DEFAULT 0 CHECK (public_share_pct >= 0 AND public_share_pct <= 100),
    exempt             boolean NOT NULL DEFAULT false,
    exempt_reason      text NOT NULL DEFAULT '',
    scoring_formula    text NOT NULL CHECK (btrim(scoring_formula) <> ''),
    min_coverage_rate  double precision NOT NULL DEFAULT 0.90 CHECK (min_coverage_rate > 0 AND min_coverage_rate <= 1.0),
    review_status      text NOT NULL DEFAULT 'pending_review' CHECK (review_status IN ('pending_review', 'confirmed', 'appealed', 'blocked_low_quality')),
    review_note        text NOT NULL DEFAULT '',
    recorded_at        timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, project_id, rule_id, revision),
    CHECK (NOT isempty(effective) AND NOT lower_inf(effective) AND lower_inc(effective) AND NOT upper_inc(effective))
);

-- 16. F18/F19/F20/F24 Retrofit project, baseline, potential & M&V verification (PRD §7.2-§7.3).
CREATE TABLE IF NOT EXISTS energy_domain.retrofit_project (
    tenant_id              text NOT NULL CHECK (btrim(tenant_id) <> ''),
    project_id             text NOT NULL CHECK (btrim(project_id) <> ''),
    retrofit_id            text NOT NULL CHECK (btrim(retrofit_id) <> ''),
    building_id            text NOT NULL CHECK (btrim(building_id) <> ''),
    energy_type            text NOT NULL CHECK (energy_type IN ('electricity', 'water', 'gas', 'cooling', 'heat', 'steam')),
    name                   text NOT NULL CHECK (btrim(name) <> ''),
    measure_summary        text NOT NULL CHECK (btrim(measure_summary) <> ''),
    unit                   text NOT NULL CHECK (btrim(unit) <> ''),
    -- F18 Pre-retrofit baseline
    baseline_version       text NOT NULL CHECK (btrim(baseline_version) <> ''),
    baseline_from          timestamptz NOT NULL,
    baseline_to            timestamptz NOT NULL,
    baseline_model         text NOT NULL CHECK (btrim(baseline_model) <> ''),
    baseline_factors       text NOT NULL CHECK (btrim(baseline_factors) <> ''),
    baseline_r2            double precision CHECK (baseline_r2 IS NULL OR (baseline_r2 >= 0 AND baseline_r2 <= 1)),
    baseline_coverage      double precision NOT NULL CHECK (baseline_coverage >= 0 AND baseline_coverage <= 1),
    baseline_locked        boolean NOT NULL DEFAULT false,
    baseline_daily_avg     double precision NOT NULL CHECK (baseline_daily_avg >= 0),
    -- F19 Potential & financial model estimate (explicitly non-verified estimate)
    est_savings_low        double precision NOT NULL CHECK (est_savings_low >= 0),
    est_savings_base       double precision NOT NULL CHECK (est_savings_base >= 0),
    est_savings_high       double precision NOT NULL CHECK (est_savings_high >= 0),
    ref_unit_price         double precision NOT NULL CHECK (ref_unit_price >= 0),
    capex_budget           double precision NOT NULL CHECK (capex_budget >= 0),
    -- F20 Post-retrofit M&V verification
    report_from            timestamptz NOT NULL,
    report_to              timestamptz NOT NULL,
    adjusted_baseline_use  double precision,
    non_routine_adjustment double precision NOT NULL DEFAULT 0,
    weather_data_complete  boolean NOT NULL DEFAULT true,
    mv_standard_ref        text NOT NULL DEFAULT 'GB/T 28750 (模拟参考口径)',
    mv_status              text NOT NULL CHECK (mv_status IN ('verified_simulated', 'pending_verification', 'estimated_only')),
    mv_note                text NOT NULL DEFAULT '',
    updated_at             timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, project_id, retrofit_id)
);
