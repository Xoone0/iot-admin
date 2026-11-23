package simgen

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/migrations"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/pgadapter"
)

// Summary captures deterministic generation counts for verification & handoff.
type Summary struct {
	DatasetID      string    `json:"datasetId"`
	Version        string    `json:"version"`
	Seed           int64     `json:"seed"`
	GeneratedAt    time.Time `json:"generatedAt"`
	Buildings      int       `json:"buildings"`
	Spaces         int       `json:"spaces"`
	Organizations  int       `json:"organizations"`
	EnergyMeters   int       `json:"energyMeters"`
	ExcludedChans  int       `json:"excludedChannels"`
	SnapshotRows   int64     `json:"snapshotRows"`
	RawItemRows    int64     `json:"rawItemRows"`
	TopologyEdges  int       `json:"topologyEdges"`
	AnomalySamples int       `json:"anomalySamples"`
}

// Generate deterministically populates the confirmed local dev PostgreSQL database
// (127.0.0.1/localhost + iot_preview) with the unified 3-building, 5-energy simulation
// dataset across public.* and energy_domain.* tables.
func Generate(ctx context.Context, pool *pgxpool.Pool, cfg Config) (Summary, error) {
	if pool == nil {
		return Summary{}, fmt.Errorf("simgen: nil pool")
	}
	if strings.TrimSpace(cfg.Version) == "" || len(cfg.Version) > 64 {
		return Summary{}, fmt.Errorf("simgen: invalid version %q", cfg.Version)
	}
	cc := pool.Config().ConnConfig
	if cc.Host != "127.0.0.1" && cc.Host != "localhost" && cc.Host != "::1" {
		return Summary{}, fmt.Errorf("simgen SAFETY GUARD: refusing non-local host %q", cc.Host)
	}
	if cc.Database != "iot_preview" {
		return Summary{}, fmt.Errorf("simgen SAFETY GUARD: refusing database %q (expected iot_preview)", cc.Database)
	}
	// Server-side verification prevents SSH port-forwarding or remote aliases from touching any non-preview DB.
	var srvDB, srvAddr string
	if err := pool.QueryRow(ctx, `SELECT current_database(), COALESCE(host(inet_server_addr()), 'local')`).Scan(&srvDB, &srvAddr); err != nil {
		return Summary{}, fmt.Errorf("simgen SAFETY GUARD: server identity check failed: %w", err)
	}
	if srvDB != "iot_preview" {
		return Summary{}, fmt.Errorf("simgen SAFETY GUARD: server reports database %q (expected iot_preview)", srvDB)
	}
	if srvAddr != "127.0.0.1" && srvAddr != "::1" && srvAddr != "local" {
		return Summary{}, fmt.Errorf("simgen SAFETY GUARD: server reports non-loopback address %q", srvAddr)
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return Summary{}, err
	}

	// 1. Ensure incremental energy_domain migrations 0001..0003 are applied.
	if _, err := migrations.Apply(ctx, pool); err != nil {
		return Summary{}, fmt.Errorf("simgen apply migrations: %w", err)
	}

	buildings := standardBuildings()
	meters := standardMeters(buildings)
	categories := standardCategories()
	anomalies := standardAnomalyManifest()

	// 2. Clean previous simulation rows idempotently when cfg.Reset is true.
	if cfg.Reset {
		if err := resetSimulationData(ctx, pool, cfg.Project); err != nil {
			return Summary{}, fmt.Errorf("simgen reset: %w", err)
		}
	}

	// 3. Upsert gateways, data_domains, device_types, thing_model_properties (including 4 P0-excluded electrical channels), and terminals.
	if err := seedPublicCatalog(ctx, pool, buildings, meters); err != nil {
		return Summary{}, fmt.Errorf("simgen public catalog: %w", err)
	}

	// 4. Generate deterministic multi-granularity time series (2025-01-01..2026-11-18)
	//    from a single cumulative physical register per meter so hour/day/month/year/raw_item reconcile 100%.
	snapRows, rawRows, err := generateAndLoadSeries(ctx, pool, cfg, loc, buildings, meters)
	if err != nil {
		return Summary{}, fmt.Errorf("simgen series: %w", err)
	}

	// 5. Seed energy_domain governance records: dataset_metadata, buildings (with historical 2025->2026 revision),
	//    spaces, organizations, metering_point profiles/bindings, formal categories/bindings, 15 topologies,
	//    confirmed register events (swap + rollover), targets (2025+2026), anomaly rules, denominators,
	//    simulated benchmarks, F15 assessment rules, and F18-F20 retrofit projects.
	topoCount, err := seedEnergyDomain(ctx, pool, cfg, loc, buildings, meters, categories, anomalies)
	if err != nil {
		return Summary{}, fmt.Errorf("simgen energy_domain: %w", err)
	}

	return Summary{
		DatasetID:      cfg.DatasetID,
		Version:        cfg.Version,
		Seed:           cfg.Seed,
		GeneratedAt:    time.Now().UTC(),
		Buildings:      len(buildings),
		Spaces:         len(buildings) * 3,
		Organizations:  len(buildings) * 2,
		EnergyMeters:   len(meters),
		ExcludedChans:  len(buildings) * 4, // 4 electrical monitoring properties on each building's main electric meter
		SnapshotRows:   snapRows,
		RawItemRows:    rawRows,
		TopologyEdges:  topoCount,
		AnomalySamples: len(anomalies),
	}, nil
}

func resetSimulationData(ctx context.Context, pool *pgxpool.Pool, proj buildingenergy.ProjectKey) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	stmts := []string{
		`DELETE FROM energy_domain.retrofit_verification_log WHERE tenant_id = $1 AND project_id = $2`,
		`DELETE FROM energy_domain.retrofit_baseline_revision WHERE tenant_id = $1 AND project_id = $2`,
		`DELETE FROM energy_domain.retrofit_project WHERE tenant_id = $1 AND project_id = $2`,
		`DELETE FROM energy_domain.assessment_rule WHERE tenant_id = $1 AND project_id = $2`,
		`DELETE FROM energy_domain.dataset_metadata WHERE tenant_id = $1 AND project_id = $2`,
		`DELETE FROM energy_domain.custom_report_template WHERE tenant_id = $1 AND project_id = $2`,
		`DELETE FROM energy_domain.benchmark_rule_revision WHERE tenant_id = $1 AND project_id = $2`,
		`DELETE FROM energy_domain.business_denominator_revision WHERE tenant_id = $1 AND project_id = $2`,
		`DELETE FROM energy_domain.anomaly_rule_revision WHERE tenant_id = $1 AND project_id = $2`,
		`DELETE FROM energy_domain.energy_target_revision WHERE tenant_id = $1 AND project_id = $2`,
		`DELETE FROM energy_domain.meter_register_event WHERE tenant_id = $1 AND project_id = $2`,
		`DELETE FROM energy_domain.meter_topology WHERE tenant_id = $1 AND project_id = $2`,
		`DELETE FROM energy_domain.point_category_binding WHERE tenant_id = $1 AND project_id = $2`,
		`DELETE FROM energy_domain.energy_category WHERE tenant_id = $1 AND project_id = $2`,
		`DELETE FROM energy_domain.metering_point_binding_allocation WHERE tenant_id = $1 AND project_id = $2`,
		`DELETE FROM energy_domain.metering_point_binding WHERE tenant_id = $1 AND project_id = $2`,
		`DELETE FROM energy_domain.metering_point_profile WHERE tenant_id = $1 AND project_id = $2`,
		`DELETE FROM energy_domain.metering_point_generation WHERE tenant_id = $1 AND project_id = $2`,
		`DELETE FROM energy_domain.metering_point_head WHERE tenant_id = $1 AND project_id = $2`,
		`DELETE FROM energy_domain.organization_revision WHERE tenant_id = $1 AND project_id = $2`,
		`DELETE FROM energy_domain.space_revision WHERE tenant_id = $1 AND project_id = $2`,
		`DELETE FROM energy_domain.building_revision WHERE tenant_id = $1 AND project_id = $2`,
		`DELETE FROM energy_domain.project_revision WHERE tenant_id = $1 AND project_id = $2`,
	}
	for _, q := range stmts {
		if _, err := tx.Exec(ctx, q, proj.TenantID, proj.ProjectID); err != nil {
			return err
		}
	}
	// Clean only sim-% telemetry/terminals from public tables (never touching legacy demo-% rows!).
	for _, q := range []string{
		`DELETE FROM snapshot_coarse WHERE terminal_id LIKE 'sim-%'`,
		`DELETE FROM raw_item WHERE terminal_id LIKE 'sim-%'`,
		`DELETE FROM raw_message WHERE gateway_id LIKE 'sim-gw-%'`,
		`DELETE FROM exception_count WHERE terminal_id LIKE 'sim-%'`,
		`DELETE FROM energy_meter_location WHERE terminal_id LIKE 'sim-%'`,
		`DELETE FROM terminal WHERE id LIKE 'sim-%'`,
	} {
		if _, err := tx.Exec(ctx, q); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func seedPublicCatalog(ctx context.Context, pool *pgxpool.Pool, buildings []buildingSpec, meters []meterSpec) error {
	// Gateways
	for _, b := range buildings {
		if _, err := pool.Exec(ctx, `
INSERT INTO gateway (gateway_id, name, enabled, online, remark, last_seen_at)
VALUES ($1, $2, true, true, '统一五能源模拟网关', clock_timestamp())
ON CONFLICT (gateway_id) DO UPDATE SET name = EXCLUDED.name, enabled = true, online = true`, b.GatewayID, b.GatewayName); err != nil {
			return err
		}
	}

	// Data domains (5 energies)
	domains := []struct{ code, name string }{
		{"sim_electric", "模拟 · 用电"},
		{"sim_water", "模拟 · 用水"},
		{"sim_gas", "模拟 · 燃气"},
		{"sim_heat", "模拟 · 供热"},
		{"sim_cooling", "模拟 · 供冷"},
	}
	domainIDs := map[string]int64{}
	for _, d := range domains {
		var id int64
		if err := pool.QueryRow(ctx, `
INSERT INTO data_domain (code, name) VALUES ($1, $2)
ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name
RETURNING id`, d.code, d.name).Scan(&id); err != nil {
			return err
		}
		domainIDs[d.code] = id
	}

	// Device types
	dtypes := []struct {
		code, name, domain, unit string
		withPowerQuality         bool
	}{
		{"sim_elec_main", "模拟 · 电力总进线表", "sim_electric", "kWh", true},
		{"sim_elec_sub", "模拟 · 电力分项表", "sim_electric", "kWh", false},
		{"sim_water_meter", "模拟 · 远传水表", "sim_water", "m³", false},
		{"sim_gas_meter", "模拟 · 燃气流量计", "sim_gas", "Nm³", false},
		{"sim_heat_meter", "模拟 · 超声波热量表", "sim_heat", "GJ", false},
		{"sim_cool_meter", "模拟 · 中央空调冷量表", "sim_cooling", "GJ", false},
	}
	typeIDs := map[string]int64{}
	for _, dt := range dtypes {
		var id int64
		if err := pool.QueryRow(ctx, `
INSERT INTO device_type (code, name, domain_id) VALUES ($1, $2, $3)
ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name, domain_id = EXCLUDED.domain_id
RETURNING id`, dt.code, dt.name, domainIDs[dt.domain]).Scan(&id); err != nil {
			return err
		}
		typeIDs[dt.code] = id
		if _, err := pool.Exec(ctx, `
INSERT INTO thing_model_property (type_id, item_code, name, value_type, unit, precision)
VALUES ($1, 'total_usage', '累计计量表底', 'number', $2, 3)
ON CONFLICT (type_id, item_code) DO UPDATE SET unit = EXCLUDED.unit`, id, dt.unit); err != nil {
			return err
		}
		// Add 4 P0-excluded non-energy electrical monitoring channels on main electric meter!
		if dt.withPowerQuality {
			pq := []struct{ code, name, unit string }{
				{"voltage_a", "A相电压（监测）", "V"},
				{"current_a", "A相电流（监测）", "A"},
				{"active_power", "总有功功率（瞬时）", "kW"},
				{"power_factor", "功率因数（监测）", ""},
			}
			for _, p := range pq {
				if _, err := pool.Exec(ctx, `
INSERT INTO thing_model_property (type_id, item_code, name, value_type, unit, precision)
VALUES ($1, $2, $3, 'number', $4, 2)
ON CONFLICT (type_id, item_code) DO UPDATE SET name = EXCLUDED.name, unit = EXCLUDED.unit`, id, p.code, p.name, p.unit); err != nil {
					return err
				}
			}
		}
	}

	// Terminals
	gwByBldg := map[string]string{}
	for _, b := range buildings {
		gwByBldg[b.ID] = b.GatewayID
	}
	for _, m := range meters {
		if _, err := pool.Exec(ctx, `
INSERT INTO terminal (id, type_id, gateway_id, name, enabled, online, last_seen_at)
VALUES ($1, $2, $3, $4, true, true, clock_timestamp())
ON CONFLICT (id) DO UPDATE SET type_id = EXCLUDED.type_id, gateway_id = EXCLUDED.gateway_id, name = EXCLUDED.name, enabled = true, online = true, deleted_at = NULL`,
			m.TerminalID, typeIDs[m.TypeCode], gwByBldg[m.BuildingID], m.Name); err != nil {
			return err
		}
	}

	// Record controlled metering quality exceptions in public.exception_count for F10 visibility.
	for _, ex := range []struct {
		tid, errType string
		cnt          int
	}{
		{"sim-a-e-power", "missing_hourly_packets", 6},
		{"sim-c-w-green", "missing_hourly_packets", 10},
		{"sim-b-g-kitchen", "low_flow_alert", 3},
	} {
		_, _ = pool.Exec(ctx, `
INSERT INTO exception_count (bucket, terminal_id, error_type, count)
VALUES ('2026-11-17 00:00:00+08'::timestamptz, $1, $2, $3)
ON CONFLICT DO NOTHING`, ex.tid, ex.errType, ex.cnt)
	}
	return nil
}

// ── Deterministic Physics & Seasonal Model ──────────────────────────────────

func isHoliday(t time.Time) bool {
	m, d := t.Month(), t.Day()
	return (m == 1 && d <= 3) || (m == 5 && d <= 5) || (m == 10 && d <= 7)
}

func seasonalFactor(role string, bldgType string, t time.Time) float64 {
	m := int(t.Month())
	// Summer peak Jun-Sep, Winter peak Nov-Mar
	summer := 0.5*(1-math.Cos(2*math.Pi*float64(m-1)/12.0)) // peaks near July (m=7)
	winter := 1.0 - summer
	switch role {
	case "hvac":
		// Electric HVAC peaks in both summer (cooling) and winter (heating)
		return 0.55 + 0.75*math.Pow(summer, 1.5) + 0.35*math.Pow(winter, 1.6)
	case "chiller_cool", "area_cool":
		if m >= 5 && m <= 9 {
			return 0.45 + 1.15*math.Sin(math.Pi*float64(m-4)/6.0)
		}
		if m == 4 || m == 10 {
			return 0.22
		}
		return 0.04 // minimal winter server-room/base cooling
	case "floor_heat", "area_heat":
		if m == 11 || m == 12 || m == 1 || m == 2 || m == 3 {
			return 0.70 + 0.85*math.Pow(winter, 1.3)
		}
		if m == 4 || m == 10 {
			return 0.15
		}
		return 0.02
	case "boiler":
		if m == 11 || m == 12 || m == 1 || m == 2 || m == 3 {
			return 0.80 + 0.90*winter
		}
		return 0.22
	case "cooling_makeup":
		return 0.25 + 1.10*summer
	case "irrigation":
		if m >= 4 && m <= 10 {
			return 0.8 + 0.5*summer
		}
		return 0.15
	default:
		if bldgType == "school" && (m == 2 || m == 8) {
			return 0.42 // winter/summer vacation dip for school campus
		}
		return 0.92 + 0.14*summer
	}
}

func diurnalFactor(role, bldgType string, wd time.Weekday, holiday bool, hour int) float64 {
	weekend := wd == time.Saturday || wd == time.Sunday || holiday
	switch bldgType {
	case "office":
		if weekend {
			if role == "power" || role == "hvac" {
				return 0.28
			}
			return 0.18
		}
		if hour >= 8 && hour <= 18 {
			return 1.15 + 0.25*math.Sin(math.Pi*float64(hour-8)/10.0)
		}
		if hour == 7 || hour == 19 || hour == 20 {
			return 0.62
		}
		return 0.22
	case "commercial":
		mult := 1.0
		if weekend {
			mult = 1.28 // commercial complex peaks on weekends!
		}
		if role == "kitchen" {
			if (hour >= 11 && hour <= 14) || (hour >= 17 && hour <= 21) {
				return 1.65 * mult
			}
			return 0.20
		}
		if hour >= 10 && hour <= 21 {
			return (1.10 + 0.30*math.Sin(math.Pi*float64(hour-10)/11.0)) * mult
		}
		return 0.26
	case "school":
		if weekend {
			if role == "domestic" || role == "hot_water" || role == "kitchen" {
				return 0.82
			}
			return 0.36
		}
		if role == "kitchen" {
			if hour == 7 || hour == 11 || hour == 12 || hour == 17 || hour == 18 {
				return 1.55
			}
			return 0.15
		}
		if hour >= 8 && hour <= 21 {
			return 1.05 + 0.22*math.Sin(math.Pi*float64(hour-8)/13.0)
		}
		return 0.25
	default:
		return 1.0
	}
}

func generateAndLoadSeries(
	ctx context.Context,
	pool *pgxpool.Pool,
	cfg Config,
	loc *time.Location,
	buildings []buildingSpec,
	meters []meterSpec,
) (int64, int64, error) {
	bldgByID := map[string]buildingSpec{}
	for _, b := range buildings {
		bldgByID[b.ID] = b
	}

	// Deterministic PRNG seeded by cfg.Seed + version hash.
	var vHash uint64 = 0x9e3779b97f4a7c15
	for i := 0; i < len(cfg.Version); i++ {
		vHash = vHash*31 + uint64(cfg.Version[i])
	}
	rng := rand.New(rand.NewPCG(uint64(cfg.Seed), vHash))

	// Opening boundary: 1 hour before DataFrom (2024-12-31 23:00+08) and 1 day before DataFrom (2024-12-31).
	startDay := cfg.DataFrom.In(loc)
	endDay := cfg.DataTo.In(loc)
	totalDays := int(endDay.Sub(startDay).Hours() / 24)

	// Hourly windows persisted to snapshot_coarse(granularity='hour'):
	// 1. Recent 90 days [HourlyFrom - 1h .. DataTo)
	// 2. Prior-year YoY window [2025-11-01 .. 2025-11-21) so F08 YoY (2026-11-17 vs 2025-11-17) is 100% real hourly comparable!
	yoyFrom := time.Date(2025, 11, 1, 0, 0, 0, 0, loc)
	yoyTo := time.Date(2025, 11, 21, 0, 0, 0, 0, loc)
	hourCutoff := cfg.HourlyFrom.In(loc).Add(-time.Hour)
	rawCutoff := cfg.RawFrom.In(loc).Add(-15 * time.Minute)

	// Group submeters by parent total meter so total meter = exact sum(children) * factor!
	childrenByParent := map[string][]int{}
	idxByTID := map[string]int{}
	for i, m := range meters {
		idxByTID[m.TerminalID] = i
		if m.ParentTerminal != "" {
			childrenByParent[m.ParentTerminal] = append(childrenByParent[m.ParentTerminal], i)
		}
	}

	// First compute canonical hourly energy usage [day][hour] for every submeter, then derive total meters.
	// Store daily totals [meterIdx][dayIdx] and hourly values ONLY for hours inside (yoy window OR recent 90d window) to keep RAM tiny!
	type hourKey struct {
		dayIdx int
		hour   int
	}
	// Precompute which dayIdx need hourly detail.
	needHourlyDay := make([]bool, totalDays)
	for d := 0; d < totalDays; d++ {
		dayT := startDay.AddDate(0, 0, d)
		if (!dayT.Before(yoyFrom.AddDate(0, 0, -1)) && dayT.Before(yoyTo)) || !dayT.Before(hourCutoff.AddDate(0, 0, -1)) {
			needHourlyDay[d] = true
		}
	}

	// Hourly usage per meter for hours that need hourly detail (and full daily usage for all 687 days).
	dailyUsage := make([][]float64, len(meters))
	hourlyUsage := make([]map[hourKey]float64, len(meters))
	for i := range meters {
		dailyUsage[i] = make([]float64, totalDays)
		hourlyUsage[i] = make(map[hourKey]float64, 2700)
	}

	// Pass 1: Generate submeter hourly & daily canonical usage.
	for i, m := range meters {
		if m.IsTotal {
			continue
		}
		bldg := bldgByID[m.BuildingID]
		for d := 0; d < totalDays; d++ {
			dayT := startDay.AddDate(0, 0, d)
			dateStr := dayT.Format("2006-01-02")
			sf := seasonalFactor(m.Role, bldg.BuildingType, dayT)
			wd := dayT.Weekday()
			hol := isHoliday(dayT)
			// Slight year-over-year efficiency improvement (-2.5% in 2026; retrofit on bldg-a HVAC -16% after 2026-06-01!)
			yoyScale := 1.0
			if dayT.Year() == 2026 {
				yoyScale = 0.975
				if m.TerminalID == "sim-a-e-hvac" && dayT.Month() >= 6 && dateStr != "2026-11-17" {
					yoyScale = 0.84 // realistic F20 post-retrofit savings on Office HVAC!
				}
			}

			var daySum float64
			for h := 0; h < 24; h++ {
				df := diurnalFactor(m.Role, bldg.BuildingType, wd, hol, h)
				noise := 0.94 + 0.12*rng.Float64()
				u := m.BaseHourly * sf * df * yoyScale * noise

				// Controlled Anomaly Injections (exact, auditable, reproducible):
				// ANOM-03: Abnormal spike on bldg-a HVAC 2026-11-17 (+52%!)
				if m.TerminalID == "sim-a-e-hvac" && dateStr == "2026-11-17" {
					u *= 1.52
				}
				// ANOM-04: Sudden drop on bldg-b kitchen gas 2026-11-13 (-72%!)
				if m.TerminalID == "sim-b-g-kitchen" && dateStr == "2026-11-13" {
					u *= 0.28
				}
				// ANOM-07: Controlled valid zero-usage day on bldg-c area cooling 2026-11-16
				if m.TerminalID == "sim-c-c-area" && dateStr == "2026-11-16" {
					u = 0
				}

				u = math.Round(u*1000) / 1000
				daySum += u
				if needHourlyDay[d] {
					hourlyUsage[i][hourKey{d, h}] = u
				}
			}
			dailyUsage[i][d] = math.Round(daySum*1000) / 1000
		}
	}

	// Pass 2: Derive total meters from direct submeters + controlled realistic distribution diff / ANOM-08 imbalance!
	for i, m := range meters {
		if !m.IsTotal {
			continue
		}
		children := childrenByParent[m.TerminalID]
		for d := 0; d < totalDays; d++ {
			dayT := startDay.AddDate(0, 0, d)
			dateStr := dayT.Format("2006-01-02")
			factor := 1.008 // normal ~0.8% distribution/transformer tolerance
			// ANOM-08: Commercial complex main electricity meter +8.8% unexplained imbalance on 2026-11-17 (and Nov 15-18)!
			if m.TerminalID == "sim-b-e-main" && (dateStr >= "2026-11-15" && dateStr <= "2026-11-18") {
				factor = 1.088
			}
			var daySum float64
			if needHourlyDay[d] {
				for h := 0; h < 24; h++ {
					var hSum float64
					for _, ci := range children {
						hSum += hourlyUsage[ci][hourKey{d, h}]
					}
					u := math.Round(hSum*factor*1000) / 1000
					hourlyUsage[i][hourKey{d, h}] = u
					daySum += u
				}
			} else {
				var cSum float64
				for _, ci := range children {
					cSum += dailyUsage[ci][d]
				}
				daySum = cSum * factor
			}
			dailyUsage[i][d] = math.Round(daySum*1000) / 1000
		}
	}

	// Pass 3: Build cumulative raw register rows for snapshot_coarse (hour, day, month, year) and raw_item (15-min recent 7d).
	// Remember: raw_register_delta = canonical_usage / multiplier (so M1-A multiplying by multiplier recovers exact canonical usage!).
	var snapBatch [][]any
	var rawBatch [][]any

	// Insert 1 synthetic raw_message envelope per gateway for raw_item message_id reference.
	gwMsgID := map[string]int64{}
	for _, b := range buildings {
		var msgID int64
		if err := pool.QueryRow(ctx, `
INSERT INTO raw_message (gateway_id, source, received_at, payload, terminal_count, byte_size)
VALUES ($1, 'mqtt', $2, '{"simulated":true}'::jsonb, 19, 128)
RETURNING id`, b.GatewayID, cfg.DataTo.Add(-time.Hour)).Scan(&msgID); err != nil {
			return 0, 0, fmt.Errorf("insert raw_message: %w", err)
		}
		gwMsgID[b.GatewayID] = msgID
	}
	gwByBldg := map[string]string{}
	for _, b := range buildings {
		gwByBldg[b.ID] = b.GatewayID
	}

	for i, m := range meters {
		mult := m.Multiplier
		if mult <= 0 {
			mult = 1
		}
		// Initial register reading (calibrated for rollover test on sim-b-e-light!)
		reg := 10000.0 + float64(i)*350.0
		if m.TerminalID == "sim-b-e-light" && m.RegisterModulus != nil {
			// Compute total usage before 2026-11-12 14:00 so register sits right below 100,000 and wraps cleanly at 14:00!
			reg = 5000.0
		}

		// Pre-compute wrap offset for sim-b-e-light so it crosses 100,000 specifically on 2026-11-12 14:00!
		if m.TerminalID == "sim-b-e-light" && m.RegisterModulus != nil {
			mod := *m.RegisterModulus
			var beforeWrap float64
			for d := 0; d < totalDays; d++ {
				dayT := startDay.AddDate(0, 0, d)
				ds := dayT.Format("2006-01-02")
				if ds < "2026-11-12" {
					beforeWrap += dailyUsage[i][d] / mult
				} else if ds == "2026-11-12" {
					for h := 0; h < 14; h++ {
						beforeWrap += hourlyUsage[i][hourKey{d, h}] / mult
					}
					break
				}
			}
			// Position register at 99,985 right before 2026-11-12 14:00 so the 14:00 hour wraps cleanly past 100,000!
			rem := math.Mod(beforeWrap, mod)
			reg = math.Mod(mod-rem-15.0+mod, mod)
		}

		applyMod := func(v float64) float64 {
			if m.RegisterModulus != nil && *m.RegisterModulus > 0 {
				v = math.Mod(v, *m.RegisterModulus)
				if v < 0 {
					v += *m.RegisterModulus
				}
			}
			return math.Round(v*10000) / 10000
		}
		reg = applyMod(reg)

		// Write opening day snapshot at 2024-12-31 and opening month 2024-12 (both inside snapshot_coarse_202412).
		openDay := startDay.AddDate(0, 0, -1)
		snapBatch = append(snapBatch,
			[]any{m.TerminalID, "total_usage", "day", openDay, reg, m.Unit, openDay.Add(24 * time.Hour), int16(0), 0, openDay.Add(24 * time.Hour)},
			[]any{m.TerminalID, "total_usage", "month", time.Date(2024, 12, 1, 0, 0, 0, 0, loc), reg, m.Unit, startDay, int16(0), 0, startDay},
		)

		for d := 0; d < totalDays; d++ {
			dayT := startDay.AddDate(0, 0, d)
			dateStr := dayT.Format("2006-01-02")

			if needHourlyDay[d] {
				for h := 0; h < 24; h++ {
					segStart := dayT.Add(time.Duration(h) * time.Hour)
					segEnd := segStart.Add(time.Hour)
					hu := hourlyUsage[i][hourKey{d, h}]

					// ANOM-06: Meter swap on sim-c-e-hvac at 2026-11-11 10:00+08 -> register resets to 0 on new meter!
					if m.TerminalID == "sim-c-e-hvac" && dateStr == "2026-11-11" && h == 10 {
						reg = 0
					}

					// Generate 15-min raw_item readings for recent 7 days (4 quarters summing to exact hu!).
					if m.Raw15Min && !segStart.Before(rawCutoff) && !segEnd.After(endDay) {
						qStartReg := reg
						for q := 1; q <= 4; q++ {
							qAt := segStart.Add(time.Duration(q*15) * time.Minute)
							if qAt.Before(cfg.RawFrom.In(loc)) || qAt.After(endDay) {
								continue
							}
							qReg := applyMod(qStartReg + (hu/mult)*float64(q)/4.0)
							rawBatch = append(rawBatch, []any{
								gwMsgID[gwByBldg[m.BuildingID]],
								qAt.Add(2 * time.Second), // received_at inside daily partition
								m.TerminalID,
								"total_usage",
								qReg,
								m.Unit,
								qAt,
								int16(0),
								"written",
							})
						}
					}

					reg = applyMod(reg + hu/mult)

					// ANOM-01: Missing 6 hours (02:00..07:00) on sim-a-e-power on 2026-11-14
					if m.TerminalID == "sim-a-e-power" && dateStr == "2026-11-14" && h >= 2 && h <= 7 {
						continue
					}
					// ANOM-02: Partial coverage missing 10 hours (08:00..17:00) on sim-c-w-green on 2026-11-15
					if m.TerminalID == "sim-c-w-green" && dateStr == "2026-11-15" && h >= 8 && h <= 17 {
						continue
					}

					inYoY := !segStart.Before(yoyFrom.Add(-time.Hour)) && segStart.Before(yoyTo)
					inRecent := !segStart.Before(hourCutoff) && segStart.Before(endDay)
					if inYoY || inRecent {
						snapBatch = append(snapBatch, []any{
							m.TerminalID, "total_usage", "hour", segStart, reg, m.Unit, segEnd, int16(0), 0, segEnd,
						})
					}
				}
			} else {
				reg = applyMod(reg + dailyUsage[i][d]/mult)
			}

			// Emit day closing snapshot (aligned to local midnight dayT).
			dayEnd := dayT.AddDate(0, 0, 1)
			snapBatch = append(snapBatch, []any{
				m.TerminalID, "total_usage", "day", dayT, reg, m.Unit, dayEnd, int16(0), 0, dayEnd,
			})

			// Emit month closing snapshot at end of calendar month or final day.
			if dayEnd.Day() == 1 || d == totalDays-1 {
				mStart := time.Date(dayT.Year(), dayT.Month(), 1, 0, 0, 0, 0, loc)
				snapBatch = append(snapBatch, []any{
					m.TerminalID, "total_usage", "month", mStart, reg, m.Unit, dayEnd, int16(0), 0, dayEnd,
				})
			}
			// Emit year closing snapshot at end of calendar year or final day.
			if (dayEnd.Month() == 1 && dayEnd.Day() == 1) || d == totalDays-1 {
				yStart := time.Date(dayT.Year(), 1, 1, 0, 0, 0, 0, loc)
				snapBatch = append(snapBatch, []any{
					m.TerminalID, "total_usage", "year", yStart, reg, m.Unit, dayEnd, int16(0), 0, dayEnd,
				})
			}
		}
	}

	// Bulk load snapshot_coarse via pgx.CopyFrom!
	snapCols := []string{"terminal_id", "item_code", "granularity", "segment_start", "value_num", "unit", "value_time", "quality", "revision", "updated_at"}
	snapCopied, err := pool.CopyFrom(ctx, pgx.Identifier{"snapshot_coarse"}, snapCols, pgx.CopyFromRows(snapBatch))
	if err != nil {
		return 0, 0, fmt.Errorf("copy snapshot_coarse (%d rows): %w", len(snapBatch), err)
	}

	// Bulk load raw_item via pgx.CopyFrom!
	rawCols := []string{"message_id", "received_at", "terminal_id", "item_code", "value_num", "unit", "value_time", "quality", "status"}
	rawCopied, err := pool.CopyFrom(ctx, pgx.Identifier{"raw_item"}, rawCols, pgx.CopyFromRows(rawBatch))
	if err != nil {
		return snapCopied, 0, fmt.Errorf("copy raw_item (%d rows): %w", len(rawBatch), err)
	}

	return snapCopied, rawCopied, nil
}

func seedEnergyDomain(
	ctx context.Context,
	pool *pgxpool.Pool,
	cfg Config,
	loc *time.Location,
	buildings []buildingSpec,
	meters []meterSpec,
	categories []categorySpec,
	anomalies []AnomalyManifestEntry,
) (int, error) {
	p := cfg.Project
	t2025 := time.Date(2025, 1, 1, 0, 0, 0, 0, loc).UTC()
	t2026 := time.Date(2026, 1, 1, 0, 0, 0, 0, loc).UTC()
	effAll, _ := pgadapter.FormatTstzrange(buildingenergy.Range{From: t2025})
	eff2025, _ := pgadapter.FormatTstzrange(buildingenergy.Range{From: t2025, To: t2026})
	eff2026, _ := pgadapter.FormatTstzrange(buildingenergy.Range{From: t2026})

	// 1. dataset_metadata
	manifestJSON, _ := json.Marshal(anomalies)
	if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.dataset_metadata
  (tenant_id, project_id, dataset_id, dataset_name, dataset_version, seed, is_simulated, generated_at,
   data_from, data_to, hourly_from, raw_from, building_scope, energy_scope, anomaly_manifest)
VALUES ($1, $2, $3, $4, $5, $6, true, clock_timestamp(), $7, $8, $9, $10, $11, $12, $13::jsonb)`,
		p.TenantID, p.ProjectID, cfg.DatasetID, cfg.DatasetName, cfg.Version, cfg.Seed,
		cfg.DataFrom.UTC(), cfg.DataTo.UTC(), cfg.HourlyFrom.UTC(), cfg.RawFrom.UTC(),
		"A 星云办公园区 / B 汇景商业综合体 / C 启明教育园区（3 栋）",
		"电 (kWh) · 水 (m³) · 气 (Nm³) · 热 (GJ) · 冷 (GJ)",
		string(manifestJSON)); err != nil {
		return 0, err
	}

	// 2. project_revision
	if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.project_revision (tenant_id, project_id, revision, effective, name, timezone, source_system, source_id)
VALUES ($1, $2, $3, $4::tstzrange, $5, $6, 'energy-simgen', $2)`,
		p.TenantID, p.ProjectID, "proj-"+cfg.Version, effAll, cfg.DatasetName, cfg.Timezone); err != nil {
		return 0, err
	}

	// 3. Buildings (bldg-a has 2 historical revisions: 2025 30,000m² -> 2026 32,000m²!), Spaces, Organizations
	for _, b := range buildings {
		if b.AreaOld != b.AreaNew {
			if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.building_revision
  (tenant_id, project_id, building_id, revision, effective, name, building_type, region, gross_area_sqm, occupied_headcount, timezone, legacy_building_id, source_system, source_id)
VALUES ($1, $2, $3, $4, $5::tstzrange, $6, $7, $8, $9, $10, $11, $12, 'energy-simgen', $3)`,
				p.TenantID, p.ProjectID, b.ID, b.ID+"-2025", eff2025, b.Name, b.BuildingType, b.Region, b.AreaOld, b.PeopleOld, cfg.Timezone, b.LegacyID); err != nil {
				return 0, err
			}
			if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.building_revision
  (tenant_id, project_id, building_id, revision, effective, name, building_type, region, gross_area_sqm, occupied_headcount, timezone, legacy_building_id, source_system, source_id)
VALUES ($1, $2, $3, $4, $5::tstzrange, $6, $7, $8, $9, $10, $11, $12, 'energy-simgen', $3)`,
				p.TenantID, p.ProjectID, b.ID, b.ID+"-2026", eff2026, b.Name, b.BuildingType, b.Region, b.AreaNew, b.PeopleNew, cfg.Timezone, b.LegacyID); err != nil {
				return 0, err
			}
		} else {
			if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.building_revision
  (tenant_id, project_id, building_id, revision, effective, name, building_type, region, gross_area_sqm, occupied_headcount, timezone, legacy_building_id, source_system, source_id)
VALUES ($1, $2, $3, $4, $5::tstzrange, $6, $7, $8, $9, $10, $11, $12, 'energy-simgen', $3)`,
				p.TenantID, p.ProjectID, b.ID, b.ID+"-2025", effAll, b.Name, b.BuildingType, b.Region, b.AreaNew, b.PeopleNew, cfg.Timezone, b.LegacyID); err != nil {
				return 0, err
			}
		}

		for _, sp := range b.Spaces {
			var parent *string
			if sp.ParentID != "" {
				s := sp.ParentID
				parent = &s
			}
			if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.space_revision
  (tenant_id, project_id, space_id, revision, effective, building_id, parent_space_id, name, area_sqm, legacy_area_id, source_system, source_id)
VALUES ($1, $2, $3, $4, $5::tstzrange, $6, $7, $8, $9, $10, 'energy-simgen', $3)`,
				p.TenantID, p.ProjectID, sp.ID, sp.ID+"-v1", effAll, b.ID, parent, sp.Name, sp.AreaSqm, sp.LegacyID); err != nil {
				return 0, err
			}
		}
		for _, o := range b.Orgs {
			var parent *string
			if o.ParentID != "" {
				s := o.ParentID
				parent = &s
			}
			if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.organization_revision
  (tenant_id, project_id, org_id, revision, effective, parent_org_id, name, headcount, source_system, source_id)
VALUES ($1, $2, $3, $4, $5::tstzrange, $6, $7, $8, 'energy-simgen', $3)`,
				p.TenantID, p.ProjectID, o.ID, o.ID+"-v1", effAll, parent, o.Name, o.Headcount); err != nil {
				return 0, err
			}
		}
	}

	// 4. Categories
	for _, c := range categories {
		if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.energy_category (tenant_id, project_id, category_id, parent_id, name, energy_type)
VALUES ($1, $2, $3, $4, $5, $6)`,
			p.TenantID, p.ProjectID, c.ID, c.ParentID, c.Name, string(c.Energy)); err != nil {
			return 0, err
		}
	}

	// 5. Metering Point heads, generations, profiles, multi-view bindings (with exact historical target_revision slicing!), category bindings, and 15 topologies.
	bldgHasSplit := map[string]bool{}
	for _, b := range buildings {
		if b.AreaOld != b.AreaNew {
			bldgHasSplit[b.ID] = true
		}
	}
	nowUTC := time.Now().UTC()
	topoEdges := 0
	for _, m := range meters {
		if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.metering_point_head (tenant_id, project_id, terminal_id, item_code, current_generation, enabled, status_reason)
VALUES ($1, $2, $3, 'total_usage', 1, true, 'simgen')`,
			p.TenantID, p.ProjectID, m.TerminalID); err != nil {
			return 0, err
		}
		if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.metering_point_generation
  (tenant_id, project_id, terminal_id, item_code, generation, previous_generation, published_by_issuer, published_by_sub, approved_at, approval_system, approval_id, reason)
VALUES ($1, $2, $3, 'total_usage', 1, NULL, 'energy-simgen', 'generator', $4, 'energy-simgen', $5, $6)`,
			p.TenantID, p.ProjectID, m.TerminalID, nowUTC, "simgen-"+cfg.Version+"-"+m.TerminalID, "统一五能源模拟数据初始口径"); err != nil {
			return 0, err
		}
		if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.metering_point_profile
  (tenant_id, project_id, terminal_id, item_code, generation, revision, effective,
   source_table, source_system, source_id, channel_system, channel_id, register_epoch,
   measurement_kind, energy_type, unit, multiplier, register_modulus, cadence_seconds)
VALUES ($1, $2, $3, 'total_usage', 1, $4, $5::tstzrange, $6, 'energy-simgen', $7, 'iot-terminal', $7, 'epoch-init', 'cumulative', $8, $9, $10, $11, $12)`,
			p.TenantID, p.ProjectID, m.TerminalID, "prof-"+m.TerminalID+"-v1", effAll,
			string(m.SourceTable), m.TerminalID+":total_usage", string(m.Energy), m.Unit, m.Multiplier, m.RegisterModulus, m.CadenceSeconds); err != nil {
			return 0, err
		}

		// Building bindings (split cleanly for bldg-a 2025 vs 2026 so P0-1 target_revision matches!)
		if bldgHasSplit[m.BuildingID] {
			for _, sl := range []struct{ rev, eff, trev string }{
				{"bind-bldg-" + m.TerminalID + "-2025", eff2025, m.BuildingID + "-2025"},
				{"bind-bldg-" + m.TerminalID + "-2026", eff2026, m.BuildingID + "-2026"},
			} {
				if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.metering_point_binding
  (tenant_id, project_id, terminal_id, item_code, generation, view_kind, revision, effective, source_system, source_id, catalog_snapshot_ref)
VALUES ($1, $2, $3, 'total_usage', 1, 'building', $4, $5::tstzrange, 'energy-simgen', $3, $6)`,
					p.TenantID, p.ProjectID, m.TerminalID, sl.rev, sl.eff, cfg.Version); err != nil {
					return 0, err
				}
				if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.metering_point_binding_allocation
  (tenant_id, project_id, terminal_id, item_code, generation, view_kind, binding_revision, target_id, target_revision, weight_ppm)
VALUES ($1, $2, $3, 'total_usage', 1, 'building', $4, $5, $6, 1000000)`,
					p.TenantID, p.ProjectID, m.TerminalID, sl.rev, m.BuildingID, sl.trev); err != nil {
					return 0, err
				}
			}
		} else {
			bRev := "bind-bldg-" + m.TerminalID + "-v1"
			if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.metering_point_binding
  (tenant_id, project_id, terminal_id, item_code, generation, view_kind, revision, effective, source_system, source_id, catalog_snapshot_ref)
VALUES ($1, $2, $3, 'total_usage', 1, 'building', $4, $5::tstzrange, 'energy-simgen', $3, $6)`,
				p.TenantID, p.ProjectID, m.TerminalID, bRev, effAll, cfg.Version); err != nil {
				return 0, err
			}
			if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.metering_point_binding_allocation
  (tenant_id, project_id, terminal_id, item_code, generation, view_kind, binding_revision, target_id, target_revision, weight_ppm)
VALUES ($1, $2, $3, 'total_usage', 1, 'building', $4, $5, $6, 1000000)`,
				p.TenantID, p.ProjectID, m.TerminalID, bRev, m.BuildingID, m.BuildingID+"-2025"); err != nil {
				return 0, err
			}
		}

		// Space & Organization bindings (for submeters; total meters bind to building only so F07 Space/Org never double-count!)
		if !m.IsTotal {
			for _, vb := range []struct{ view, target, trev string }{
				{"space", m.SpaceID, m.SpaceID + "-v1"},
				{"organization", m.OrgID, m.OrgID + "-v1"},
			} {
				vRev := "bind-" + vb.view + "-" + m.TerminalID + "-v1"
				if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.metering_point_binding
  (tenant_id, project_id, terminal_id, item_code, generation, view_kind, revision, effective, source_system, source_id, catalog_snapshot_ref)
VALUES ($1, $2, $3, 'total_usage', 1, $4, $5, $6::tstzrange, 'energy-simgen', $3, $7)`,
					p.TenantID, p.ProjectID, m.TerminalID, vb.view, vRev, effAll, cfg.Version); err != nil {
					return 0, err
				}
				if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.metering_point_binding_allocation
  (tenant_id, project_id, terminal_id, item_code, generation, view_kind, binding_revision, target_id, target_revision, weight_ppm)
VALUES ($1, $2, $3, 'total_usage', 1, $4, $5, $6, $7, 1000000)`,
					p.TenantID, p.ProjectID, m.TerminalID, vb.view, vRev, vb.target, vb.trev); err != nil {
					return 0, err
				}
			}
		}

		// Formal Category Binding (skip sim-b-e-socket intentionally for ANOM-09 Unclassified!)
		if !m.IsTotal && m.CategoryID != "" {
			cbRev := "cb-" + m.TerminalID + "-v1"
			w1 := int64(buildingenergy.FullWeightPPM) - m.SecondWeightPPM
			if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.point_category_binding (tenant_id, project_id, terminal_id, item_code, category_id, revision, effective, weight_ppm)
VALUES ($1, $2, $3, 'total_usage', $4, $5, $6::tstzrange, $7)`,
				p.TenantID, p.ProjectID, m.TerminalID, m.CategoryID, cbRev, effAll, w1); err != nil {
				return 0, err
			}
			if m.SecondCatID != "" && m.SecondWeightPPM > 0 {
				if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.point_category_binding (tenant_id, project_id, terminal_id, item_code, category_id, revision, effective, weight_ppm)
VALUES ($1, $2, $3, 'total_usage', $4, $5, $6::tstzrange, $7)`,
					p.TenantID, p.ProjectID, m.TerminalID, m.SecondCatID, cbRev, effAll, m.SecondWeightPPM); err != nil {
					return 0, err
				}
			}
		}

		// Physical Parent-Child Topology (15 complete 5-energy topologies across 3 buildings!)
		if m.ParentTerminal != "" {
			tVer := "topo-" + m.BuildingID + "-" + string(m.Energy) + "-v1"
			if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.meter_topology
  (tenant_id, project_id, topology_version, effective, building_id, energy_type,
   parent_terminal_id, parent_item_code, child_terminal_id, child_item_code, zone_code, source_system, source_id)
VALUES ($1, $2, $3, $4::tstzrange, $5, $6, $7, 'total_usage', $8, 'total_usage', $9, 'energy-simgen', $3)`,
				p.TenantID, p.ProjectID, tVer, effAll, m.BuildingID, string(m.Energy), m.ParentTerminal, m.TerminalID, m.ZoneCode); err != nil {
				return 0, err
			}
			topoEdges++
		}
	}

	// 6. Confirmed Meter Register Events (ANOM-05 rollover + ANOM-06 meter swap!)
	rollAt := time.Date(2026, 11, 12, 14, 0, 0, 0, loc).UTC()
	swapAt := time.Date(2026, 11, 11, 10, 0, 0, 0, loc).UTC()
	for _, ev := range []struct {
		tid, kind, epoch, reason string
		at                       time.Time
	}{
		{"sim-b-e-light", "rollover", "", "ANOM-05: 模拟电表跨越满量程 100,000 回卷确认", rollAt},
		{"sim-c-e-hvac", "meter_swap", "epoch-swap-20261111", "ANOM-06: 模拟空调分表更换新表归零确认", swapAt},
	} {
		var ep *string
		if ev.epoch != "" {
			s := ev.epoch
			ep = &s
		}
		if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.meter_register_event
  (tenant_id, project_id, terminal_id, item_code, event_kind, effective_at, new_epoch, reason, actor_issuer, actor_sub)
VALUES ($1, $2, $3, 'total_usage', $4, $5, $6, $7, 'energy-simgen', 'generator')`,
			p.TenantID, p.ProjectID, ev.tid, ev.kind, ev.at, ep, ev.reason); err != nil {
			return 0, err
		}
	}

	// 7. Annual Targets (2025 & 2026 for all 3 buildings × 5 energies — F05 & F14!)
	for _, b := range buildings {
		sc := 1.0
		if b.BuildingType == "commercial" {
			sc = 1.65
		} else if b.BuildingType == "school" {
			sc = 1.25
		}
		for _, yr := range []int{2025, 2026} {
			yFrom := time.Date(yr, 1, 1, 0, 0, 0, 0, loc).UTC()
			yTo := time.Date(yr+1, 1, 1, 0, 0, 0, 0, loc).UTC()
			yEff, _ := pgadapter.FormatTstzrange(buildingenergy.Range{From: yFrom, To: yTo})
			specs := []struct {
				e   buildingenergy.EnergyType
				u   string
				val float64
			}{
				{buildingenergy.Electricity, "kWh", 1250000 * sc},
				{buildingenergy.Water, "m³", 26000 * sc},
				{buildingenergy.Gas, "Nm³", 65000 * sc},
				{buildingenergy.Heat, "GJ", 4200 * sc},
				{buildingenergy.Cooling, "GJ", 5400 * sc},
			}
			for _, sp := range specs {
				rev := fmt.Sprintf("tgt-%s-%s-%d-v1", b.ID, sp.e, yr)
				if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.energy_target_revision
  (tenant_id, project_id, object_kind, object_id, energy_type, target_year, revision, effective, unit, annual_milli, method, weight_ppm, source_system, source_id)
VALUES ($1, $2, 'building', $3, $4, $5, $6, $7::tstzrange, $8, $9, 'linear', NULL, 'energy-simgen', $6)`,
					p.TenantID, p.ProjectID, b.ID, string(sp.e), yr, rev, yEff, sp.u, int64(math.Round(sp.val*1000))); err != nil {
					return 0, err
				}
			}
		}
	}

	// 8. Anomaly Rules (F10 & F17 for all 3 buildings)
	for _, b := range buildings {
		if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.anomaly_rule_revision
  (tenant_id, project_id, object_kind, object_id, energy_type, revision, effective, minimum_history, minimum_absolute_increase, minimum_relative_increase, source_system, source_id)
VALUES ($1, $2, 'building', $3, 'electricity', $4, $5::tstzrange, 2, 200, 0.10, 'energy-simgen', $4)`,
			p.TenantID, p.ProjectID, b.ID, "anom-"+b.ID+"-elec-v1", effAll); err != nil {
			return 0, err
		}
	}

	// 9. Business Denominators (F12: students for C school, revenue for B commercial; leave A office revenue intentionally unconfigured to test missing_denominator!)
	day17From := time.Date(2026, 11, 17, 0, 0, 0, 0, loc).UTC()
	day17To := time.Date(2026, 11, 18, 0, 0, 0, 0, loc).UTC()
	day17Range, _ := pgadapter.FormatTstzrange(buildingenergy.Range{From: day17From, To: day17To})
	t2027 := time.Date(2027, 1, 1, 0, 0, 0, 0, loc).UTC()
	twoYearRange, _ := pgadapter.FormatTstzrange(buildingenergy.Range{From: t2025, To: t2027})
	for _, dn := range []struct {
		bldg, kind, rev, unit, basis string
		pRange                       string
		val                          float64
	}{
		{"bldg-c", "students", "denom-c-students-v1", "人", "模拟教务学籍人数", twoYearRange, 3200},
		{"bldg-b", "revenue", "denom-b-rev-20261117", "元", "模拟商业综合体日营业额", day17Range, 1850000},
	} {
		if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.business_denominator_revision
  (tenant_id, project_id, object_kind, object_id, denominator_kind, revision, period_range, effective, value, unit, basis, source_system, source_id)
VALUES ($1, $2, 'building', $3, $4, $5, $6::tstzrange, $7::tstzrange, $8, $9, $10, 'energy-simgen', $5)`,
			p.TenantID, p.ProjectID, dn.bldg, dn.kind, dn.rev, dn.pRange, effAll, dn.val, dn.unit, dn.basis); err != nil {
			return 0, err
		}
	}

	// 10. Simulated Benchmark Rules (F13: kind='demo', explicitly labeled 模拟参考规则，不冒充国家标准!)
	for _, bm := range []struct {
		id, name, metricID, tmplVer, unit, bType string
		maxVal                                   float64
	}{
		{"sim-bm-office-elec-day", "模拟参考阈值 · 办公建筑日用电强度", "office-electricity-area", "tmpl-office-v1", "kWh/m²", "office", 0.14},
		{"sim-bm-comm-elec-day", "模拟参考阈值 · 商业综合体日用电强度", "commercial-electricity-area", "tmpl-commercial-v1", "kWh/m²", "commercial", 0.18},
		{"sim-bm-school-elec-day", "模拟参考阈值 · 教育园区日用电强度", "school-electricity-area", "tmpl-school-v1", "kWh/m²", "school", 0.12},
	} {
		if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.benchmark_rule_revision
  (tenant_id, project_id, benchmark_id, revision, effective, name, publisher, region, metric_id, template_version, unit, kind,
   building_type, object_kind, period_granularity, maximum_value, minimum_denominator, source_system, source_id)
VALUES ($1, $2, $3, 'v1', $4::tstzrange, $5, '模拟规则库（非国标）', '上海', $6, $7, $8, 'demo', $9, 'building', 'day', $10, NULL, 'energy-simgen', $3)`,
			p.TenantID, p.ProjectID, bm.id, effAll, bm.name, bm.metricID, bm.tmplVer, bm.unit, bm.bType, bm.maxVal); err != nil {
			return 0, err
		}
	}

	// 11. F15 Organization / Space Assessment Rules (PRD §6.3)
	for _, ar := range []struct {
		id, kind, objID, energy, label, unit, formula, status, note string
		target, weight, pubShare                                    float64
		exempt                                                      bool
		exemptReason                                                string
	}{
		{"assess-a-rd", "organization", "org-a-rd", "electricity", "研发中心日用电考核", "kWh", "实际用电 ≤ 目标值获满分，每超 1% 扣 2 分", "confirmed", "周期覆盖率 100%，考核复核通过（模拟）", 1200, 40, 15, false, ""},
		{"assess-a-ops", "organization", "org-a-ops", "electricity", "园区物业与运营部日用电考核", "kWh", "含公区暖通动力分摊 20%，每超 1% 扣 2 分", "appealed", "因 2026-11-17 空调异常激增提起复核申诉（模拟）", 2100, 60, 20, false, ""},
		{"assess-b-retail", "organization", "org-b-retail", "electricity", "商业零售运营部日用电考核", "kWh", "实际用电 ≤ 目标值获满分，每超 1% 扣 2 分", "confirmed", "正常营业日覆盖完整（模拟）", 2800, 55, 10, false, ""},
		{"assess-b-dining", "organization", "org-b-dining", "gas", "餐饮街区商管部日燃气考核", "Nm³", "餐饮用气考核（允许厨房波动，每超 1% 扣 1 分）", "pending_review", "待确认餐饮营业时长修正（模拟）", 240, 45, 5, false, ""},
		{"assess-c-campus", "organization", "org-c-campus", "water", "校园后勤保障部日用水考核", "m³", "含绿化补水与校园公区，每超 1% 扣 2 分（缺测期禁止正式确认）", "pending_review", "绿化表存在部分缺测日，自动校验覆盖率门槛", 95, 50, 25, false, ""},
		{"assess-c-lab", "space", "sp-c-lab", "electricity", "科研实验与图书馆豁免考核样本", "kWh", "重点科研设备24小时连续运行豁免", "confirmed", "依据 PRD §6.3 设为豁免对象（模拟）", 1500, 50, 0, true, "重点科研实验连续运行豁免"},
	} {
		if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.assessment_rule
  (tenant_id, project_id, rule_id, revision, effective, object_kind, object_id, energy_type, metric_label,
   target_value, unit, weight_pct, public_share_pct, exempt, exempt_reason, scoring_formula, min_coverage_rate, review_status, review_note)
VALUES ($1, $2, $3, 'v1', $4::tstzrange, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, 0.90, $16, $17)`,
			p.TenantID, p.ProjectID, ar.id, effAll, ar.kind, ar.objID, ar.energy, ar.label,
			ar.target, ar.unit, ar.weight, ar.pubShare, ar.exempt, ar.exemptReason, ar.formula, ar.status, ar.note); err != nil {
			return 0, err
		}
	}

	// 12. F18 / F19 / F20 / F24 Retrofit Projects (explicitly modeled M&V, PRD §7.2-§7.3)
	bFrom := time.Date(2025, 6, 1, 0, 0, 0, 0, loc).UTC()
	bTo := time.Date(2025, 11, 1, 0, 0, 0, 0, loc).UTC()
	rFrom := time.Date(2026, 10, 1, 0, 0, 0, 0, loc).UTC()
	rTo := time.Date(2026, 11, 1, 0, 0, 0, 0, loc).UTC()
	adjBaseA := 96500.0
	adjBaseB := 148000.0
	for _, rp := range []struct {
		id, bldg, energy, name, measure, unit, bVer, model, factors, status, note string
		r2                                                                        *float64
		cov, dailyAvg, low, base, high, price, capex, nonRoutine                  float64
		adjBase                                                                   *float64
		locked, weatherOK                                                         bool
	}{
		{
			id: "ret-a-hvac-vfd", bldg: "bldg-a", energy: "electricity",
			name: "A 园区空调冷机与水泵变频群控改造（模拟）", measure: "加装高效变频器与负荷自适应群控策略，优化过渡季冷机启停",
			unit: "kWh", bVer: "base-a-hvac-v1", model: "多变量回归（CDD/HDD + 营业日系数）", factors: "日均室外温度、CDD、工作日/周末占用率",
			r2: ptrF(0.91), cov: 0.992, dailyAvg: 3112.9, low: 110000, base: 145000, high: 175000, price: 0.88, capex: 360000,
			adjBase: &adjBaseA, nonRoutine: 1200, locked: true, weatherOK: true, status: "verified_simulated",
			note: "模拟 M&V 报告期（2026-10）实际电耗由 PostgreSQL snapshot_coarse 实时核算；核验结果仅属模拟模型测算。",
		},
		{
			id: "ret-b-light-led", bldg: "bldg-b", energy: "electricity",
			name: "B 商业综合体公区智能 LED 调光改造（模拟）", measure: "替换中庭与通道高耗灯具并引入照度感应分时调光",
			unit: "kWh", bVer: "base-b-light-v1", model: "营业时长分段基线模型", factors: "每日营业时长、周末客流系数",
			r2: ptrF(0.86), cov: 0.985, dailyAvg: 4774.2, low: 160000, base: 210000, high: 255000, price: 0.95, capex: 520000,
			adjBase: &adjBaseB, nonRoutine: 0, locked: true, weatherOK: false, status: "pending_verification",
			note: "报告期缺少经确认的客流与外部气象修正因子，按 PRD §7.3 标为「待核验」，禁止标记已核验。",
		},
		{
			id: "ret-c-heat-bal", bldg: "bldg-c", energy: "heat",
			name: "C 教育园区教学楼供热气候补偿改造方案（草案）", measure: "加装电动调节阀与分时分温气候补偿控制（处于方案测算阶段）",
			unit: "GJ", bVer: "base-c-heat-v0", model: "采暖度日数 HDD 线性基线（未锁定）", factors: "采暖度日数 HDD18、寒假空置时段",
			r2: nil, cov: 0.78, dailyAvg: 24.5, low: 480, base: 650, high: 820, price: 85.0, capex: 240000,
			adjBase: nil, nonRoutine: 0, locked: false, weatherOK: false, status: "estimated_only",
			note: "尚未完成改造后完整采暖季观测，仅输出 F19 潜力与静态回收期模型测算，不冒充已发生节能量。",
		},
	} {
		if _, err := pool.Exec(ctx, `
INSERT INTO energy_domain.retrofit_project
  (tenant_id, project_id, retrofit_id, building_id, energy_type, name, measure_summary, unit,
   baseline_version, baseline_from, baseline_to, baseline_model, baseline_factors, baseline_r2, baseline_coverage, baseline_locked, baseline_daily_avg,
   est_savings_low, est_savings_base, est_savings_high, ref_unit_price, capex_budget,
   report_from, report_to, adjusted_baseline_use, non_routine_adjustment, weather_data_complete, mv_status, mv_note)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8,
        $9, $10, $11, $12, $13, $14, $15, $16, $17,
        $18, $19, $20, $21, $22,
        $23, $24, $25, $26, $27, $28, $29)`,
			p.TenantID, p.ProjectID, rp.id, rp.bldg, rp.energy, rp.name, rp.measure, rp.unit,
			rp.bVer, bFrom, bTo, rp.model, rp.factors, rp.r2, rp.cov, rp.locked, rp.dailyAvg,
			rp.low, rp.base, rp.high, rp.price, rp.capex,
			rFrom, rTo, rp.adjBase, rp.nonRoutine, rp.weatherOK, rp.status, rp.note); err != nil {
			return 0, err
		}
	}

	return topoEdges, nil
}

func ptrF(v float64) *float64 { return &v }
