\set ON_ERROR_STOP on
-- Review-only psql fixture. Run only on the DBA-approved disposable database.
-- Required variables: phase, review_db, review_major, review_address, review_port, variant, case_id,
-- reader_role, caller_role, expected (0 or 1 for phase=cas).
SELECT :'phase' = 'preflight' AS is_preflight,
       :'phase' = 'fixtures' AS is_fixtures,
       :'phase' IN ('cas', 'cas_hold', 'cas_rollback') AS is_cas,
       :'phase' IN ('cas_hold', 'cas_rollback') AS hold_cas,
       :'phase' = 'cas_rollback' AS rollback_cas
\gset
\if :is_preflight
BEGIN READ ONLY;
\else
BEGIN ISOLATION LEVEL READ COMMITTED;
\endif

SET LOCAL lock_timeout = '15s';
SET LOCAL statement_timeout = '45s';
SELECT set_config('energy_review.database', :'review_db', true) AS guard_database,
       set_config('energy_review.major', :'review_major', true) AS guard_major,
       set_config('energy_review.address', :'review_address', true) AS guard_address,
       set_config('energy_review.port', :'review_port', true) AS guard_port,
       set_config('energy_review.phase', :'phase', true) AS guard_phase,
       set_config('energy_review.variant', :'variant', true) AS guard_variant,
       set_config('energy_review.case_id', :'case_id', true) AS guard_case,
       set_config('energy_review.expected', :'expected', true) AS guard_expected
\gset

DO $guard$
BEGIN
    IF current_database() <> current_setting('energy_review.database')
       OR current_setting('server_version_num')::integer / 10000 <> current_setting('energy_review.major')::integer
       OR coalesce(host(inet_server_addr()), 'local-socket') <> current_setting('energy_review.address')
       OR current_setting('port')::integer <> current_setting('energy_review.port')::integer THEN
        RAISE EXCEPTION 'Isolation endpoint, database or PostgreSQL major does not match the approved target';
    END IF;
    IF current_setting('energy_review.phase') NOT IN ('preflight', 'fixtures', 'cas', 'cas_hold', 'cas_rollback')
       OR current_setting('energy_review.variant') NOT IN ('gist', 'lock')
       OR current_setting('energy_review.case_id') !~ '^[a-f0-9]{12,32}$' THEN
        RAISE EXCEPTION 'Invalid review phase, variant, or fixture case ID';
    END IF;
END
$guard$;

\if :is_preflight
SELECT current_database(), current_user, session_user,
       coalesce(host(inet_server_addr()), 'local-socket') AS server_address,
       current_setting('port') AS server_port,
       current_setting('server_version_num') AS version_num, current_setting('search_path') AS search_path,
       current_setting('transaction_read_only') AS transaction_read_only;

SELECT a.name, a.default_version, e.extversion AS installed_version, n.nspname AS installed_schema
FROM (SELECT 'btree_gist'::name AS name) AS wanted
LEFT JOIN pg_available_extensions AS a ON a.name = wanted.name
LEFT JOIN pg_extension AS e ON e.extname = wanted.name
LEFT JOIN pg_namespace AS n ON n.oid = e.extnamespace;

SELECT c.opcname, c.opcintype::regtype AS input_type, n.nspname,
       pg_opclass_is_visible(c.oid) AS visible
FROM pg_opclass AS c
JOIN pg_am AS a ON a.oid = c.opcmethod
JOIN pg_namespace AS n ON n.oid = c.opcnamespace
WHERE a.amname = 'gist' AND c.opcdefault AND c.opcintype IN ('text'::regtype, 'bigint'::regtype)
ORDER BY input_type, n.nspname;

WITH requested(role_name) AS (VALUES (:'reader_role'), (:'caller_role'))
SELECT requested.role_name, r.rolname IS NOT NULL AS role_exists, r.rolsuper, r.rolbypassrls,
       c.relname AS table_name, pg_get_userbyid(c.relowner) AS owner,
       pg_has_role(r.oid, c.relowner, 'MEMBER') AS owner_membership,
       has_schema_privilege(r.oid, n.oid, 'USAGE') AS schema_usage,
       has_schema_privilege(r.oid, n.oid, 'CREATE') AS schema_create,
       has_table_privilege(r.oid, c.oid, 'SELECT') AS direct_select,
       has_table_privilege(r.oid, c.oid, 'INSERT, UPDATE, DELETE, TRUNCATE, REFERENCES, TRIGGER') AS sensitive_table_privilege,
       has_any_column_privilege(r.oid, c.oid, 'INSERT, UPDATE, REFERENCES') AS column_write_or_reference
FROM requested
LEFT JOIN pg_roles AS r ON r.rolname = requested.role_name
LEFT JOIN pg_namespace AS n ON n.nspname = 'energy_mapping'
LEFT JOIN pg_class AS c ON c.relnamespace = n.oid AND c.relkind IN ('r', 'p')
ORDER BY requested.role_name, c.relname;

ROLLBACK;
\endif

\if :is_fixtures
DO $fixtures$
DECLARE
    tenant_a text := 'review_a_' || current_setting('energy_review.case_id');
    tenant_b text := 'review_b_' || current_setting('energy_review.case_id');
    use_gist boolean := current_setting('energy_review.variant') = 'gist';
    actual_constraints integer;
    head_generation bigint;
BEGIN
    SELECT count(*) INTO actual_constraints
      FROM pg_constraint
      WHERE contype = 'x'
        AND ((conrelid = 'energy_mapping.energy_point_profile'::regclass AND conname = 'energy_point_profile_no_overlap')
          OR (conrelid = 'energy_mapping.energy_point_binding'::regclass AND conname = 'energy_point_binding_no_overlap'));
    IF actual_constraints <> (CASE WHEN use_gist THEN 2 ELSE 0 END) THEN
        RAISE EXCEPTION 'Isolation schema differs from the selected constraint variant';
    END IF;

    INSERT INTO energy_mapping.energy_mapping_head (tenant_id, project_id, terminal_id, item_code)
    VALUES (tenant_a, 'p1', 'e1', 'total'), (tenant_a, 'p2', 'e1', 'total'), (tenant_b, 'p1', 'e1', 'total');
    INSERT INTO energy_mapping.energy_mapping_generation
      (tenant_id, project_id, terminal_id, item_code, generation, previous_generation,
       published_by_issuer, published_by_sub, approved_at, approval_system, approval_id, reason)
    VALUES (tenant_a, 'p1', 'e1', 'total', 1, NULL, 'fixture', 'writer', now(), 'fixture', 'a1', 'seed'),
           (tenant_a, 'p2', 'e1', 'total', 1, NULL, 'fixture', 'writer', now(), 'fixture', 'b1', 'seed'),
           (tenant_b, 'p1', 'e1', 'total', 1, NULL, 'fixture', 'writer', now(), 'fixture', 'c1', 'seed');
    INSERT INTO energy_mapping.energy_mapping_generation
      (tenant_id, project_id, terminal_id, item_code, generation, previous_generation,
       published_by_issuer, published_by_sub, approved_at, approval_system, approval_id, reason)
    VALUES (tenant_a, 'p2', 'e1', 'total', 2, 1, 'fixture', 'writer', now(), 'fixture', 'b2', 'history'),
           (tenant_b, 'p1', 'e1', 'total', 2, 1, 'fixture', 'writer', now(), 'fixture', 'c2', 'history');
    UPDATE energy_mapping.energy_mapping_head SET current_generation = 1
      WHERE tenant_id = tenant_a AND project_id = 'p1' AND terminal_id = 'e1' AND item_code = 'total';

    BEGIN
        UPDATE energy_mapping.energy_mapping_head SET current_generation = 2
          WHERE tenant_id = tenant_a AND project_id = 'p1' AND terminal_id = 'e1' AND item_code = 'total';
        RAISE EXCEPTION 'Unexpected cross-tenant or cross-project head reference';
    EXCEPTION WHEN foreign_key_violation THEN
        NULL;
    END;

    INSERT INTO energy_mapping.energy_point_profile
      (tenant_id, project_id, terminal_id, item_code, generation, revision, effective,
       source_system, source_id, channel_system, channel_id,
       register_epoch, measurement_kind, energy_type, unit, multiplier, cadence_seconds)
    VALUES
      (tenant_a, 'p1', 'e1', 'total', 1, 'before', '[2026-11-01 00:00Z,2026-11-01 00:30Z)', 'fixture', 'old', 'fixture', 'c1', 'epoch-a', 'cumulative', 'electricity', 'kWh', 2, 900),
      (tenant_a, 'p1', 'e1', 'total', 1, 'after', '[2026-11-01 00:30Z,)', 'fixture', 'new', 'fixture', 'c2', 'epoch-b', 'cumulative', 'electricity', 'kWh', 1, 900);

    IF use_gist THEN
        BEGIN
            INSERT INTO energy_mapping.energy_point_profile
            SELECT tenant_id, project_id, terminal_id, item_code, generation, 'overlap', effective,
                   source_system, source_id, channel_system, channel_id, register_epoch,
                   measurement_kind, energy_type, unit, multiplier, register_modulus, cadence_seconds
            FROM energy_mapping.energy_point_profile
            WHERE tenant_id = tenant_a AND project_id = 'p1' AND generation = 1 AND revision = 'before';
            RAISE EXCEPTION 'Overlapping profile was accepted';
        EXCEPTION WHEN exclusion_violation THEN
            NULL;
        END;
    END IF;

    INSERT INTO energy_mapping.energy_point_binding
      (tenant_id, project_id, terminal_id, item_code, generation, view_kind, revision, effective,
       source_system, source_id, catalog_snapshot_ref)
    VALUES (tenant_a, 'p1', 'e1', 'total', 1, 'space', 's1',
            '[2026-11-01 00:00Z,)', 'fixture', 'b1', 's1-v1');
    IF use_gist THEN
        BEGIN
            INSERT INTO energy_mapping.energy_point_binding
              (tenant_id, project_id, terminal_id, item_code, generation, view_kind, revision, effective,
               source_system, source_id, catalog_snapshot_ref)
            VALUES (tenant_a, 'p1', 'e1', 'total', 1, 'space', 'overlap',
                    '[2026-11-01 00:30Z,)', 'fixture', 'b1b', 's1-v1');
            RAISE EXCEPTION 'Overlapping binding was accepted';
        EXCEPTION WHEN exclusion_violation THEN
            NULL;
        END;
    END IF;
    INSERT INTO energy_mapping.energy_point_binding_allocation
    VALUES (tenant_a, 'p1', 'e1', 'total', 1, 'space', 'default', 's1', 'target1', 'rev1', 600000);
    BEGIN
        INSERT INTO energy_mapping.energy_point_binding_allocation
        VALUES (tenant_a, 'p1', 'e1', 'total', 1, 'space', 'default', 's1', 'target2', 'rev1', 1000001);
        RAISE EXCEPTION 'Invalid single allocation weight was accepted';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;
    BEGIN
        INSERT INTO energy_mapping.energy_point_binding_allocation
        VALUES (tenant_a, 'p1', 'e1', 'total', 1, 'space', 'default', 's1', 'target1', 'rev2', 1);
        RAISE EXCEPTION 'Duplicate target was accepted';
    EXCEPTION WHEN unique_violation THEN
        NULL;
    END;

    BEGIN
        INSERT INTO energy_mapping.energy_mapping_generation
          (tenant_id, project_id, terminal_id, item_code, generation, previous_generation,
           published_by_issuer, published_by_sub, approved_at, approval_system, approval_id, reason)
        VALUES (tenant_a, 'p1', 'e1', 'total', 2, 1, 'fixture', 'writer', now(), 'fixture', 'a2', 'rollback test');
        UPDATE energy_mapping.energy_mapping_head SET current_generation = 2
          WHERE tenant_id = tenant_a AND project_id = 'p1' AND terminal_id = 'e1' AND item_code = 'total';
        RAISE EXCEPTION USING ERRCODE = 'P1001', MESSAGE = 'Injected failure before commit';
    EXCEPTION WHEN SQLSTATE 'P1001' THEN
        NULL;
    END;
    SELECT current_generation INTO head_generation FROM energy_mapping.energy_mapping_head
      WHERE tenant_id = tenant_a AND project_id = 'p1' AND terminal_id = 'e1' AND item_code = 'total';
    IF head_generation IS DISTINCT FROM 1 OR EXISTS (
        SELECT FROM energy_mapping.energy_mapping_generation
        WHERE tenant_id = tenant_a AND project_id = 'p1' AND terminal_id = 'e1' AND item_code = 'total' AND generation = 2
    ) THEN
        RAISE EXCEPTION 'Failed publication did not roll back head and candidate together';
    END IF;
    IF (SELECT count(*) FROM energy_mapping.energy_point_profile
        WHERE tenant_id = tenant_a AND project_id = 'p1' AND terminal_id = 'e1'
          AND item_code = 'total' AND generation = 1) <> 2
       OR NOT EXISTS (
        SELECT FROM energy_mapping.energy_point_binding_allocation
        WHERE tenant_id = tenant_a AND project_id = 'p1' AND terminal_id = 'e1'
          AND item_code = 'total' AND generation = 1 AND target_id = 'target1' AND weight_ppm = 600000
    ) THEN
        RAISE EXCEPTION 'Pinned old profile or allocation is missing';
    END IF;
    RAISE NOTICE 'M1B_SCHEMA_FIXTURES_OK variant=%', current_setting('energy_review.variant');
END
$fixtures$;
ROLLBACK;
\endif

\if :is_cas
DO $cas$
DECLARE
    fixture_tenant text := 'review_cas_' || current_setting('energy_review.case_id');
    expected bigint := current_setting('energy_review.expected')::bigint;
    old_generation bigint;
    updated integer;
BEGIN
    IF expected NOT IN (0, 1) THEN
        RAISE EXCEPTION 'Expected generation must be 0 or 1 for this fixture';
    END IF;
    INSERT INTO energy_mapping.energy_mapping_head (tenant_id, project_id, terminal_id, item_code)
    VALUES (fixture_tenant, 'p1', 'e1', 'total') ON CONFLICT DO NOTHING;
    SELECT current_generation INTO STRICT old_generation
      FROM energy_mapping.energy_mapping_head
      WHERE tenant_id = fixture_tenant AND project_id = 'p1' AND terminal_id = 'e1' AND item_code = 'total'
      FOR UPDATE;
    IF coalesce(old_generation, 0) <> expected THEN
        IF old_generation IS DISTINCT FROM expected + 1 THEN
            RAISE EXCEPTION 'Unexpected CAS starting state, got % for expected %', old_generation, expected;
        END IF;
        RAISE NOTICE 'M1B_CAS_CONFLICT expected=% observed=%', expected, old_generation;
        RETURN;
    END IF;
    INSERT INTO energy_mapping.energy_mapping_generation
      (tenant_id, project_id, terminal_id, item_code, generation, previous_generation,
       published_by_issuer, published_by_sub, approved_at, approval_system, approval_id, reason)
    VALUES (fixture_tenant, 'p1', 'e1', 'total', expected + 1, old_generation,
            'fixture', 'writer', now(), 'fixture', 'race', 'concurrency test');
    UPDATE energy_mapping.energy_mapping_head SET current_generation = expected + 1
      WHERE tenant_id = fixture_tenant AND project_id = 'p1' AND terminal_id = 'e1' AND item_code = 'total'
        AND current_generation IS NOT DISTINCT FROM old_generation;
    GET DIAGNOSTICS updated = ROW_COUNT;
    IF updated <> 1 THEN
        RAISE EXCEPTION 'CAS update failed';
    END IF;
    IF current_setting('energy_review.phase') IN ('cas_hold', 'cas_rollback') THEN
        RAISE NOTICE 'M1B_CAS_READY expected=% next=%', expected, expected + 1;
    ELSE
        RAISE NOTICE 'M1B_CAS_WIN expected=% next=%', expected, expected + 1;
    END IF;
END
$cas$;
\if :hold_cas
\prompt 'After observing the second client waiting, press Enter: ' resume
\endif
\if :rollback_cas
ROLLBACK;
\else
COMMIT;
\endif
SELECT h.current_generation, array_agg(g.generation ORDER BY g.generation) AS retained_generations
FROM energy_mapping.energy_mapping_head AS h
JOIN energy_mapping.energy_mapping_generation AS g USING (tenant_id, project_id, terminal_id, item_code)
WHERE h.tenant_id = ('review_cas_' || :'case_id')
  AND h.project_id = 'p1' AND h.terminal_id = 'e1' AND h.item_code = 'total'
GROUP BY h.current_generation;
\endif
