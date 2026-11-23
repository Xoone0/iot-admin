package pgadapter

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

// ChannelStatus describes the P0 metering-channel governance state of a
// terminal + thing_model_property pair discovered from existing iot-admin tables.
type ChannelStatus string

const (
	// ChannelConfigured means an explicit, enabled energy_domain.metering_point_profile exists.
	ChannelConfigured ChannelStatus = "configured"
	// ChannelDisabled means a profile exists in energy_domain but is currently disabled.
	ChannelDisabled ChannelStatus = "disabled"
	// ChannelUnconfigured means the channel is NOT configured in energy_domain;
	// it is displayed as "待配置" and MUST NOT enter formal energy calculations.
	ChannelUnconfigured ChannelStatus = "unconfigured"
	// ChannelExcludedNonEnergy means the channel is power-quality or non-energy telemetry
	// (voltage, current, active/reactive power, power factor, frequency, temperature, etc.)
	// and is excluded from energy consumption accounting by PRD §2.2 / P0.
	ChannelExcludedNonEnergy ChannelStatus = "excluded_non_energy"
)

// SourceTable identifies which existing iot-admin data table supplies readings for a point.
type SourceTable string

const (
	SourceTableSnapshotCoarse SourceTable = "snapshot_coarse"
	SourceTableRawItem        SourceTable = "raw_item"
)

// IOTBuildingRecord represents an existing building discovered read-only from public.energy_building.
type IOTBuildingRecord struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// IOTAreaRecord represents an existing area discovered read-only from public.energy_area.
type IOTAreaRecord struct {
	ID         int64  `json:"id"`
	BuildingID int64  `json:"buildingId"`
	Name       string `json:"name"`
}

// ChannelClassification captures P0 non-authoritative candidate classification for a raw IoT property.
// It NEVER authorizes formal energy calculation on its own.
type ChannelClassification struct {
	Status          ChannelStatus                  `json:"status"`
	ExclusionReason string                         `json:"exclusionReason,omitempty"`
	SuggestedEnergy buildingenergy.EnergyType      `json:"suggestedEnergy,omitempty"`
	SuggestedKind   buildingenergy.MeasurementKind `json:"suggestedKind,omitempty"`
}

// ClassifyChannelCandidate inspects a raw IoT numeric property to:
//  1. Explicitly exclude non-energy electrical/telemetry monitoring channels
//     (voltage, current, active/reactive/apparent power, power factor, frequency, etc.).
//  2. Never default unknown channels to Electricity; unrecognized channels stay
//     ChannelUnconfigured with empty SuggestedEnergy ("待配置").
//  3. Distinguish cumulative, interval, and instantaneous semantics.
func ClassifyChannelCandidate(domainCode, domainName, typeCode, typeName, itemCode, itemName, unit string) ChannelClassification {
	code := strings.ToLower(strings.TrimSpace(itemCode))
	name := strings.ToLower(strings.TrimSpace(itemName))
	ctxText := strings.ToLower(strings.TrimSpace(domainCode + " " + domainName + " " + typeCode + " " + typeName))
	u := strings.TrimSpace(unit)
	uLower := strings.ToLower(u)

	// 1. Explicit exclusion of electrical monitoring & instantaneous non-energy telemetry (PRD §2.2 & P0-2).
	if reason := nonEnergyTelemetryReason(code, name, uLower); reason != "" {
		return ChannelClassification{
			Status:          ChannelExcludedNonEnergy,
			ExclusionReason: reason,
			SuggestedKind:   buildingenergy.Instantaneous,
		}
	}

	// 2. Determine suggested measurement kind (never silently conflating cumulative/interval/instantaneous).
	suggestedKind := buildingenergy.MeasurementKind("")
	switch {
	case strings.Contains(code, "total") || strings.Contains(code, "cumul") || strings.Contains(code, "epi") ||
		strings.Contains(code, "reading") || strings.Contains(name, "累计") || strings.Contains(name, "总表") ||
		strings.Contains(name, "表底") || strings.Contains(name, "示数") || strings.Contains(name, "总用"):
		suggestedKind = buildingenergy.Cumulative
	case strings.Contains(code, "interval") || strings.Contains(code, "delta") || strings.Contains(code, "diff") ||
		strings.Contains(name, "区间") || strings.Contains(name, "时段用量") || strings.Contains(name, "增量"):
		suggestedKind = buildingenergy.Interval
	case strings.Contains(code, "inst") || strings.Contains(name, "瞬时"):
		return ChannelClassification{
			Status:          ChannelExcludedNonEnergy,
			ExclusionReason: "瞬时量通道不可直接累加为能耗用量",
			SuggestedKind:   buildingenergy.Instantaneous,
		}
	}

	// 3. Recognize energy type ONLY from explicit unit or explicit domain/item signal — NEVER default to Electricity.
	var suggestedEnergy buildingenergy.EnergyType
	switch {
	case uLower == "kwh" || uLower == "mwh" || uLower == "wh":
		suggestedEnergy = buildingenergy.Electricity
	case uLower == "gj" || uLower == "mj" || uLower == "kwh_th":
		if strings.Contains(ctxText+" "+name, "cool") || strings.Contains(ctxText+" "+name, "冷") {
			suggestedEnergy = buildingenergy.Cooling
		} else if strings.Contains(ctxText+" "+name, "steam") || strings.Contains(ctxText+" "+name, "蒸汽") {
			suggestedEnergy = buildingenergy.Steam
		} else {
			suggestedEnergy = buildingenergy.Heat
		}
	case uLower == "t" || uLower == "kg":
		if strings.Contains(ctxText+" "+name, "steam") || strings.Contains(ctxText+" "+name, "蒸汽") {
			suggestedEnergy = buildingenergy.Steam
		}
	case uLower == "nm3" || uLower == "nm³":
		suggestedEnergy = buildingenergy.Gas
	case uLower == "m3" || uLower == "m³" || uLower == "l":
		switch {
		case strings.Contains(ctxText+" "+name, "gas") || strings.Contains(ctxText+" "+name, "燃气") || strings.Contains(ctxText+" "+name, "天然气"):
			suggestedEnergy = buildingenergy.Gas
		case strings.Contains(ctxText+" "+name, "water") || strings.Contains(ctxText+" "+name, "水"):
			suggestedEnergy = buildingenergy.Water
		}
	}

	// Always starts as ChannelUnconfigured until explicitly published in energy_domain.metering_point_profile.
	return ChannelClassification{
		Status:          ChannelUnconfigured,
		SuggestedEnergy: suggestedEnergy,
		SuggestedKind:   suggestedKind,
	}
}

func nonEnergyTelemetryReason(code, name, uLower string) string {
	// Unit-based hard exclusions (instantaneous electrical & process telemetry).
	switch uLower {
	case "v", "kv", "mv":
		return "电压监测通道（非能耗计量点）"
	case "a", "ma", "ka":
		return "电流监测通道（非能耗计量点）"
	case "w", "kw", "mw", "var", "kvar", "mvar", "va", "kva", "mva":
		return "功率监测通道（瞬时功率不可作为能耗累计量）"
	case "hz", "khz":
		return "频率监测通道（非能耗计量点）"
	case "℃", "°c", "c", "k", "%", "%rh", "pa", "kpa", "mpa", "bar", "m3/h", "m³/h", "l/s", "l/min":
		return "工况瞬时监测通道（非能耗计量点）"
	}
	// Keyword-based exclusions on item_code / item_name.
	for _, kw := range []string{
		"voltage", "volt", "u_a", "u_b", "u_c", "uab", "ubc", "uca", "电压",
	} {
		if strings.Contains(code, kw) || strings.Contains(name, kw) {
			return "电压监测通道（非能耗计量点）"
		}
	}
	for _, kw := range []string{
		"current", "amp", "i_a", "i_b", "i_c", "电流",
	} {
		if strings.Contains(code, kw) || strings.Contains(name, kw) {
			return "电流监测通道（非能耗计量点）"
		}
	}
	for _, kw := range []string{
		"power_factor", "pf", "cos_phi", "cosphi", "功率因数",
	} {
		if strings.Contains(code, kw) || strings.Contains(name, kw) {
			return "功率因数监测通道（非能耗计量点）"
		}
	}
	for _, kw := range []string{
		"active_power", "reactive_power", "apparent_power", "demand_kw", "有功功率", "无功功率", "视在功率", "瞬时功率", "负荷功率",
	} {
		if strings.Contains(code, kw) || strings.Contains(name, kw) {
			return "功率监测通道（瞬时功率不可作为能耗累计量）"
		}
	}
	for _, kw := range []string{
		"frequency", "freq", "harmonic", "thd", "频率", "谐波",
	} {
		if strings.Contains(code, kw) || strings.Contains(name, kw) {
			return "频率/谐波监测通道（非能耗计量点）"
		}
	}
	for _, kw := range []string{
		"temperature", "humidity", "pressure", "flow_rate", "温度", "湿度", "压力", "瞬时流量",
	} {
		if strings.Contains(code, kw) || strings.Contains(name, kw) {
			return "工况瞬时监测通道（非能耗计量点）"
		}
	}
	return ""
}

// IOTMeterRecord represents a terminal + thing_model_property channel discovered from public.*
// enriched with its P0 candidate classification and (when present) explicit energy_domain profile.
type IOTMeterRecord struct {
	TerminalID       string                         `json:"terminalId"`
	TerminalName     string                         `json:"terminalName"`
	GatewayID        string                         `json:"gatewayId"`
	DomainCode       string                         `json:"domainCode"`
	DomainName       string                         `json:"domainName"`
	TypeCode         string                         `json:"typeCode"`
	TypeName         string                         `json:"typeName"`
	ItemCode         string                         `json:"itemCode"`
	ItemName         string                         `json:"itemName"`
	Unit             string                         `json:"unit"`
	ChannelStatus    ChannelStatus                  `json:"channelStatus"`
	ExclusionReason  string                         `json:"exclusionReason,omitempty"`
	SuggestedEnergy  buildingenergy.EnergyType      `json:"suggestedEnergy,omitempty"`
	SuggestedKind    buildingenergy.MeasurementKind `json:"suggestedKind,omitempty"`
	Configured       bool                           `json:"configured"`
	PointEnabled     bool                           `json:"pointEnabled"`
	StatusReason     string                         `json:"statusReason,omitempty"`
	Generation       uint64                         `json:"generation,omitempty"`
	ProfileVersion   string                         `json:"profileVersion,omitempty"`
	SourceTable      SourceTable                    `json:"sourceTable,omitempty"`
	Energy           buildingenergy.EnergyType      `json:"energy,omitempty"`
	MeasurementKind  buildingenergy.MeasurementKind `json:"measurementKind,omitempty"`
	Multiplier       float64                        `json:"multiplier,omitempty"`
	RegisterModulus  *float64                       `json:"registerModulus,omitempty"`
	RegisterEpoch    string                         `json:"registerEpoch,omitempty"`
	CadenceSeconds   int                            `json:"cadenceSeconds,omitempty"`
	EffectiveFrom    *time.Time                     `json:"effectiveFrom,omitempty"`
	EffectiveTo      *time.Time                     `json:"effectiveTo,omitempty"`
	Enabled          bool                           `json:"enabled"`
	Online           bool                           `json:"online"`
	BuildingID       *int64                         `json:"buildingId,omitempty"`
	BuildingName     string                         `json:"buildingName,omitempty"`
	AreaID           *int64                         `json:"areaId,omitempty"`
	AreaName         string                         `json:"areaName,omitempty"`
	BoundBuildingID  string                         `json:"boundBuildingId,omitempty"`
	BoundSpaceID     string                         `json:"boundSpaceId,omitempty"`
	BoundOrgID       string                         `json:"boundOrgId,omitempty"`
	HourRows         int64                          `json:"hourRows"`
	MinHourStart     *time.Time                     `json:"minHourStart,omitempty"`
	MaxHourStart     *time.Time                     `json:"maxHourStart,omitempty"`
	RawRows          int64                          `json:"rawRows"`
	MinRawAt         *time.Time                     `json:"minRawAt,omitempty"`
	MaxRawAt         *time.Time                     `json:"maxRawAt,omitempty"`
}

// IOTMetadataSnapshot captures a point-in-time read-only inventory of existing iot-admin tables.
type IOTMetadataSnapshot struct {
	Project     buildingenergy.ProjectKey
	Timezone    string
	EffectiveAt time.Time
	Buildings   []IOTBuildingRecord
	Areas       []IOTAreaRecord
	Meters      []IOTMeterRecord
}

const (
	checkEnergyLocationsTableSQL = `SELECT to_regclass('public.energy_building') IS NOT NULL AND to_regclass('public.energy_area') IS NOT NULL AND to_regclass('public.energy_meter_location') IS NOT NULL`

	selectIOTBuildingsSQL = `SELECT id, name FROM energy_building ORDER BY id ASC`

	selectIOTAreasSQL = `SELECT id, building_id, name FROM energy_area ORDER BY building_id ASC, id ASC`

	selectIOTMetersWithLocationsSQL = `
SELECT t.id, t.name, coalesce(t.gateway_id, ''), t.enabled, t.online,
       d.code, d.name, dt.code, dt.name,
       p.item_code, p.name, coalesce(p.unit, ''),
       ml.building_id, coalesce(b.name, ''), ml.area_id, coalesce(a.name, '')
FROM terminal t
JOIN device_type dt ON dt.id = t.type_id
JOIN data_domain d ON d.id = dt.domain_id
JOIN thing_model_property p ON p.type_id = dt.id AND p.value_type = 'number'
LEFT JOIN energy_meter_location ml ON ml.terminal_id = t.id
LEFT JOIN energy_building b ON b.id = ml.building_id
LEFT JOIN energy_area a ON a.id = ml.area_id
WHERE t.deleted_at IS NULL
ORDER BY t.id ASC, p.item_code ASC`

	selectIOTMetersBaseSQL = `
SELECT t.id, t.name, coalesce(t.gateway_id, ''), t.enabled, t.online,
       d.code, d.name, dt.code, dt.name,
       p.item_code, p.name, coalesce(p.unit, '')
FROM terminal t
JOIN device_type dt ON dt.id = t.type_id
JOIN data_domain d ON d.id = dt.domain_id
JOIN thing_model_property p ON p.type_id = dt.id AND p.value_type = 'number'
WHERE t.deleted_at IS NULL
ORDER BY t.id ASC, p.item_code ASC`

	selectIOTSnapshotHorizonsSQL = `
SELECT terminal_id, item_code, count(*), min(segment_start), max(segment_start)
FROM snapshot_coarse
WHERE granularity = 'hour'
GROUP BY terminal_id, item_code`

	selectIOTRawCountsSQL = `
SELECT terminal_id, item_code, count(*), min(collected_at), max(collected_at)
FROM raw_item
WHERE status = 'written'
GROUP BY terminal_id, item_code`
)

// LoadIOTMetadata performs a strictly read-only inventory of existing iot-admin
// PostgreSQL tables (compatible with both 0001..0024 and 0025_energy_locations.sql).
// Every discovered property is classified with P0 rules and remains ChannelUnconfigured
// or ChannelExcludedNonEnergy until an explicit energy_domain.metering_point_profile is applied.
func LoadIOTMetadata(ctx context.Context, db Querier, project buildingenergy.ProjectKey, timezone string, effectiveAt time.Time) (*IOTMetadataSnapshot, error) {
	if isNilInterface(db) {
		return nil, buildingenergy.ErrCatalogScope
	}
	if strings.TrimSpace(project.TenantID) == "" || strings.TrimSpace(project.ProjectID) == "" {
		return nil, buildingenergy.ErrInvalidScope
	}
	if strings.TrimSpace(timezone) == "" {
		timezone = "Asia/Shanghai"
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return nil, buildingenergy.ErrInvalidScope
	}
	if effectiveAt.IsZero() {
		effectiveAt = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	}

	snap := &IOTMetadataSnapshot{
		Project:     project,
		Timezone:    timezone,
		EffectiveAt: effectiveAt.UTC(),
	}

	var hasLocations bool
	if err := db.QueryRow(ctx, checkEnergyLocationsTableSQL).Scan(&hasLocations); err != nil {
		return nil, fmt.Errorf("check energy location tables: %w", err)
	}

	if hasLocations {
		bRows, err := db.Query(ctx, selectIOTBuildingsSQL)
		if err != nil {
			return nil, fmt.Errorf("query energy_building: %w", err)
		}
		for bRows.Next() {
			var b IOTBuildingRecord
			if err := bRows.Scan(&b.ID, &b.Name); err != nil {
				bRows.Close()
				return nil, err
			}
			snap.Buildings = append(snap.Buildings, b)
		}
		if err := bRows.Err(); err != nil {
			bRows.Close()
			return nil, err
		}
		bRows.Close()

		aRows, err := db.Query(ctx, selectIOTAreasSQL)
		if err != nil {
			return nil, fmt.Errorf("query energy_area: %w", err)
		}
		for aRows.Next() {
			var a IOTAreaRecord
			if err := aRows.Scan(&a.ID, &a.BuildingID, &a.Name); err != nil {
				aRows.Close()
				return nil, err
			}
			snap.Areas = append(snap.Areas, a)
		}
		if err := aRows.Err(); err != nil {
			aRows.Close()
			return nil, err
		}
		aRows.Close()
	}

	applyClassification := func(m *IOTMeterRecord) {
		cls := ClassifyChannelCandidate(m.DomainCode, m.DomainName, m.TypeCode, m.TypeName, m.ItemCode, m.ItemName, m.Unit)
		m.ChannelStatus = cls.Status
		m.ExclusionReason = cls.ExclusionReason
		m.SuggestedEnergy = cls.SuggestedEnergy
		m.SuggestedKind = cls.SuggestedKind
		// Formal energy / measurement_kind remain empty until configured in energy_domain.
	}

	if hasLocations {
		mRows, err := db.Query(ctx, selectIOTMetersWithLocationsSQL)
		if err != nil {
			return nil, fmt.Errorf("query terminals with locations: %w", err)
		}
		for mRows.Next() {
			var m IOTMeterRecord
			if err := mRows.Scan(
				&m.TerminalID, &m.TerminalName, &m.GatewayID, &m.Enabled, &m.Online,
				&m.DomainCode, &m.DomainName, &m.TypeCode, &m.TypeName,
				&m.ItemCode, &m.ItemName, &m.Unit,
				&m.BuildingID, &m.BuildingName, &m.AreaID, &m.AreaName,
			); err != nil {
				mRows.Close()
				return nil, err
			}
			applyClassification(&m)
			snap.Meters = append(snap.Meters, m)
		}
		if err := mRows.Err(); err != nil {
			mRows.Close()
			return nil, err
		}
		mRows.Close()
	} else {
		mRows, err := db.Query(ctx, selectIOTMetersBaseSQL)
		if err != nil {
			return nil, fmt.Errorf("query terminals: %w", err)
		}
		for mRows.Next() {
			var m IOTMeterRecord
			if err := mRows.Scan(
				&m.TerminalID, &m.TerminalName, &m.GatewayID, &m.Enabled, &m.Online,
				&m.DomainCode, &m.DomainName, &m.TypeCode, &m.TypeName,
				&m.ItemCode, &m.ItemName, &m.Unit,
			); err != nil {
				mRows.Close()
				return nil, err
			}
			applyClassification(&m)
			snap.Meters = append(snap.Meters, m)
		}
		if err := mRows.Err(); err != nil {
			mRows.Close()
			return nil, err
		}
		mRows.Close()
	}

	type meterKey struct{ terminal, item string }
	idxMap := make(map[meterKey]int, len(snap.Meters))
	for i, m := range snap.Meters {
		idxMap[meterKey{m.TerminalID, m.ItemCode}] = i
	}

	if hRows, err := db.Query(ctx, selectIOTSnapshotHorizonsSQL); err == nil {
		for hRows.Next() {
			var tid, code string
			var cnt int64
			var minT, maxT *time.Time
			if err := hRows.Scan(&tid, &code, &cnt, &minT, &maxT); err == nil {
				if idx, ok := idxMap[meterKey{tid, code}]; ok {
					snap.Meters[idx].HourRows = cnt
					snap.Meters[idx].MinHourStart = minT
					snap.Meters[idx].MaxHourStart = maxT
				}
			}
		}
		hRows.Close()
	}

	if rRows, err := db.Query(ctx, selectIOTRawCountsSQL); err == nil {
		for rRows.Next() {
			var tid, code string
			var cnt int64
			var minT, maxT *time.Time
			if err := rRows.Scan(&tid, &code, &cnt, &minT, &maxT); err == nil {
				if idx, ok := idxMap[meterKey{tid, code}]; ok {
					snap.Meters[idx].RawRows = cnt
					snap.Meters[idx].MinRawAt = minT
					snap.Meters[idx].MaxRawAt = maxT
				}
			}
		}
		rRows.Close()
	}

	return snap, nil
}

// BuildingObjectID formats a deterministic ObjectKey ID for an existing energy_building row.
func BuildingObjectID(id int64) string { return "bldg-" + strconv.FormatInt(id, 10) }

// AreaObjectID formats a deterministic ObjectKey ID for an existing energy_area row.
func AreaObjectID(id int64) string { return "area-" + strconv.FormatInt(id, 10) }

// ExplicitPointConfig carries an authoritative, operator-configured metering profile
// for IOTReadOnlyCatalog (used in read-only smoke verification before energy_domain exists,
// or bridged from energy_domain.metering_point_profile).
type ExplicitPointConfig struct {
	Energy          buildingenergy.EnergyType
	Kind            buildingenergy.MeasurementKind
	Unit            string
	Multiplier      float64
	RegisterModulus float64
	RegisterEpoch   string
	Cadence         time.Duration
	Effective       buildingenergy.Range
}

// IOTReadOnlyCatalog implements buildingenergy.Catalog and buildingenergy.MappingReader
// strictly for points that have an EXPLICIT configuration (never auto-enrolling
// unconfigured or excluded non-energy channels).
type IOTReadOnlyCatalog struct {
	Snapshot         *IOTMetadataSnapshot
	ExplicitProfiles map[buildingenergy.PointKey]ExplicitPointConfig
}

// Project implements buildingenergy.Catalog.
func (c *IOTReadOnlyCatalog) Project(_ context.Context, key buildingenergy.ProjectKey, at time.Time) (buildingenergy.Project, error) {
	if c == nil || c.Snapshot == nil || key != c.Snapshot.Project || at.Before(c.Snapshot.EffectiveAt) {
		return buildingenergy.Project{}, buildingenergy.ErrCatalogScope
	}
	return buildingenergy.Project{
		Key:      c.Snapshot.Project,
		Timezone: c.Snapshot.Timezone,
		Meta: buildingenergy.VersionMeta{
			Version:   "iot-proj-v1",
			Effective: buildingenergy.Range{From: c.Snapshot.EffectiveAt},
			Source:    buildingenergy.SourceRef{System: "iot-public-readonly", ID: key.ProjectID},
		},
	}, nil
}

// Object implements buildingenergy.Catalog.
func (c *IOTReadOnlyCatalog) Object(_ context.Context, project buildingenergy.ProjectKey, key buildingenergy.ObjectKey, at time.Time) (buildingenergy.Object, error) {
	if c == nil || c.Snapshot == nil || project != c.Snapshot.Project || at.Before(c.Snapshot.EffectiveAt) {
		return buildingenergy.Object{}, buildingenergy.ErrCatalogScope
	}
	eff := buildingenergy.Range{From: c.Snapshot.EffectiveAt}
	switch key.Kind {
	case buildingenergy.Building:
		for _, b := range c.Snapshot.Buildings {
			if BuildingObjectID(b.ID) == key.ID {
				return buildingenergy.Object{
					Project:    project,
					Key:        key,
					BuildingID: key.ID,
					Meta: buildingenergy.VersionMeta{
						Version:   "iot-bldg-" + strconv.FormatInt(b.ID, 10) + "-v1",
						Effective: eff,
						Source:    buildingenergy.SourceRef{System: "iot-public-readonly", ID: strconv.FormatInt(b.ID, 10)},
					},
				}, nil
			}
		}
	case buildingenergy.Space:
		for _, a := range c.Snapshot.Areas {
			if AreaObjectID(a.ID) == key.ID {
				bID := BuildingObjectID(a.BuildingID)
				return buildingenergy.Object{
					Project:    project,
					Key:        key,
					Parent:     buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: bID},
					BuildingID: bID,
					Meta: buildingenergy.VersionMeta{
						Version:   "iot-area-" + strconv.FormatInt(a.ID, 10) + "-v1",
						Effective: eff,
						Source:    buildingenergy.SourceRef{System: "iot-public-readonly", ID: strconv.FormatInt(a.ID, 10)},
					},
				}, nil
			}
		}
	}
	return buildingenergy.Object{}, buildingenergy.ErrCatalogScope
}

// ReadMapping implements buildingenergy.MappingReader ONLY for explicitly configured points.
// Unconfigured or excluded non-energy channels return ErrMappingUnavailable (P0-3 & P0-9).
func (c *IOTReadOnlyCatalog) ReadMapping(_ context.Context, point buildingenergy.PointRef, generation uint64) (buildingenergy.MappingTimeline, error) {
	if c == nil || c.Snapshot == nil || point.Project != c.Snapshot.Project || generation != 1 {
		return buildingenergy.MappingTimeline{}, buildingenergy.ErrMappingUnavailable
	}
	cfg, ok := c.ExplicitProfiles[point.Key]
	if !ok || cfg.Energy == "" || cfg.Kind == "" || strings.TrimSpace(cfg.Unit) == "" || cfg.Multiplier <= 0 || cfg.Cadence <= 0 {
		return buildingenergy.MappingTimeline{}, buildingenergy.ErrMappingUnavailable
	}
	for _, m := range c.Snapshot.Meters {
		if m.TerminalID != point.Key.TerminalID || m.ItemCode != point.Key.ItemCode {
			continue
		}
		if m.ChannelStatus == ChannelExcludedNonEnergy {
			return buildingenergy.MappingTimeline{}, buildingenergy.ErrMappingUnavailable
		}
		eff := cfg.Effective
		if eff.From.IsZero() {
			eff = buildingenergy.Range{From: c.Snapshot.EffectiveAt}
		}
		epoch := strings.TrimSpace(cfg.RegisterEpoch)
		if epoch == "" {
			epoch = "iot-epoch-v1"
		}
		tl := buildingenergy.MappingTimeline{
			Point:      point,
			Generation: 1,
			Profiles: []buildingenergy.ProfileRevision{{
				Profile: buildingenergy.PointProfile{
					Point:      point,
					Kind:       cfg.Kind,
					Energy:     cfg.Energy,
					Unit:       cfg.Unit,
					Multiplier: cfg.Multiplier,
					Modulus:    cfg.RegisterModulus,
					Cadence:    cfg.Cadence,
					Meta: buildingenergy.VersionMeta{
						Version:   "iot-prof-" + m.TerminalID + "-v1",
						Effective: eff,
						Source:    buildingenergy.SourceRef{System: "energy-explicit-config", ID: m.TerminalID + ":" + m.ItemCode},
					},
				},
				Channel:       buildingenergy.SourceRef{System: "iot-terminal", ID: m.TerminalID + ":" + m.ItemCode},
				RegisterEpoch: epoch,
			}},
		}
		if m.BuildingID != nil {
			bID := BuildingObjectID(*m.BuildingID)
			tl.Bindings = append(tl.Bindings, buildingenergy.BindingRevision{
				View: buildingenergy.Building,
				Meta: buildingenergy.VersionMeta{
					Version:   "iot-bind-bldg-" + m.TerminalID + "-v1",
					Effective: eff,
					Source:    buildingenergy.SourceRef{System: "iot-public-readonly", ID: m.TerminalID},
				},
				Allocations: []buildingenergy.Allocation{{
					Target:         buildingenergy.ObjectKey{Kind: buildingenergy.Building, ID: bID},
					TargetRevision: "iot-bldg-" + strconv.FormatInt(*m.BuildingID, 10) + "-v1",
					WeightPPM:      buildingenergy.FullWeightPPM,
				}},
			})
		}
		if m.AreaID != nil {
			aID := AreaObjectID(*m.AreaID)
			tl.Bindings = append(tl.Bindings, buildingenergy.BindingRevision{
				View: buildingenergy.Space,
				Meta: buildingenergy.VersionMeta{
					Version:   "iot-bind-area-" + m.TerminalID + "-v1",
					Effective: eff,
					Source:    buildingenergy.SourceRef{System: "iot-public-readonly", ID: m.TerminalID},
				},
				Allocations: []buildingenergy.Allocation{{
					Target:         buildingenergy.ObjectKey{Kind: buildingenergy.Space, ID: aID},
					TargetRevision: "iot-area-" + strconv.FormatInt(*m.AreaID, 10) + "-v1",
					WeightPPM:      buildingenergy.FullWeightPPM,
				}},
			})
		}
		return tl, nil
	}
	return buildingenergy.MappingTimeline{}, buildingenergy.ErrMappingUnavailable
}

// RegisterEventEntry represents one confirmed meter register event loaded from
// energy_domain.meter_register_event.
type RegisterEventEntry struct {
	TerminalID  string
	ItemCode    string
	EventKind   string
	EffectiveAt time.Time
	NewEpoch    string
}

// StaticRegisterEventResolver implements RegisterEventResolver using confirmed
// energy_domain.meter_register_event rows loaded for the query.
type StaticRegisterEventResolver struct {
	Events []RegisterEventEntry
}

// ResolveRegisterState applies confirmed meter_swap (epoch switch) and rollover
// markers at sampleAt so M1-A never turns meter swaps or wrap-arounds into fake
// negative energy consumption.
func (r *StaticRegisterEventResolver) ResolveRegisterState(_ context.Context, point buildingenergy.PointRef, sampleAt, _ time.Time, defaultEpoch string) (string, bool, error) {
	if r == nil || len(r.Events) == 0 {
		return defaultEpoch, false, nil
	}
	matched := make([]RegisterEventEntry, 0)
	for _, ev := range r.Events {
		if ev.TerminalID == point.Key.TerminalID && ev.ItemCode == point.Key.ItemCode {
			matched = append(matched, ev)
		}
	}
	if len(matched) == 0 {
		return defaultEpoch, false, nil
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].EffectiveAt.Before(matched[j].EffectiveAt) })

	epoch := defaultEpoch
	rollover := false
	for _, ev := range matched {
		if ev.EffectiveAt.After(sampleAt) {
			continue
		}
		switch ev.EventKind {
		case "meter_swap":
			if strings.TrimSpace(ev.NewEpoch) != "" {
				epoch = strings.TrimSpace(ev.NewEpoch)
			}
		case "rollover":
			// A rollover recorded at effective_at (start of the wrapping interval) applies strictly
			// to the closing boundary reading of that interval (effective_at + 1h for hourly snapshots,
			// or exact effective_at for raw_item readings stamped at the closing instant).
			if sampleAt.Equal(ev.EffectiveAt.Add(time.Hour)) {
				rollover = true
			}
		}
	}
	return epoch, rollover, nil
}
