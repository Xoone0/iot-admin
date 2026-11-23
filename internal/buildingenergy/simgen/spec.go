package simgen

import (
	"time"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/pgadapter"
)

// Config controls deterministic generation of the unified 5-energy simulation dataset.
type Config struct {
	DatasetID   string
	DatasetName string
	Version     string
	Seed        int64
	Project     buildingenergy.ProjectKey
	Timezone    string
	DataFrom    time.Time // 2025-01-01 00:00+08
	DataTo      time.Time // 2026-11-19 00:00+08 (covers through 2026-11-18 23:59)
	HourlyFrom  time.Time // Recent 90 days hourly: 2026-08-20 00:00+08
	RawFrom     time.Time // Recent 7 days 15-min raw_item: 2026-11-11 00:00+08
	Reset       bool
}

// DefaultConfig returns the standard reproducible configuration for the unified 5-energy dataset.
func DefaultConfig() Config {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	return Config{
		DatasetID:   "unified-5energy-sim",
		DatasetName: "五能源建筑能耗统一模拟数据集（办公/商业/教育三园区）",
		Version:     "sim-v1.0.0",
		Seed:        20261118,
		Project:     buildingenergy.ProjectKey{TenantID: "default", ProjectID: "iot-local"},
		Timezone:    "Asia/Shanghai",
		DataFrom:    time.Date(2025, 1, 1, 0, 0, 0, 0, loc),
		DataTo:      time.Date(2026, 11, 19, 0, 0, 0, 0, loc),
		HourlyFrom:  time.Date(2026, 8, 20, 0, 0, 0, 0, loc),
		RawFrom:     time.Date(2026, 11, 11, 0, 0, 0, 0, loc),
		Reset:       true,
	}
}

type AnomalyManifestEntry struct {
	Code        string `json:"code"`
	Kind        string `json:"kind"`
	BuildingID  string `json:"buildingId"`
	TerminalID  string `json:"terminalId"`
	Date        string `json:"date"`
	Description string `json:"description"`
}

type buildingSpec struct {
	ID              string
	LegacyID        int64
	Name            string
	BuildingType    string // office | commercial | school
	Region          string
	AreaOld         float64 // 2025 revision
	AreaNew         float64 // 2026 revision (tests P3 historical versioning)
	PeopleOld       float64
	PeopleNew       float64
	GatewayID       string
	GatewayName     string
	Spaces          []spaceSpec
	Orgs            []orgSpec
}

type spaceSpec struct {
	ID       string
	LegacyID int64
	ParentID string
	Name     string
	AreaSqm  float64
}

type orgSpec struct {
	ID        string
	ParentID  string
	Name      string
	Headcount float64
}

type meterSpec struct {
	TerminalID      string
	Name            string
	BuildingID      string
	SpaceID         string
	OrgID           string
	DomainCode      string
	TypeCode        string
	Energy          buildingenergy.EnergyType
	Unit            string
	Multiplier      float64
	RegisterModulus *float64
	SourceTable     pgadapter.SourceTable
	CadenceSeconds  int
	IsTotal         bool
	ParentTerminal  string // empty for total meters
	ZoneCode        string // non-empty for direct submeters
	CategoryID      string // empty intentionally for unclassified anomaly sample
	SecondCatID     string // optional secondary category for weighted split
	SecondWeightPPM int64
	BaseHourly      float64 // typical daytime hourly consumption in canonical unit
	Role            string  // lighting | hvac | power | socket | domestic | cooling_makeup | irrigation | boiler | kitchen | hot_water | floor_heat | area_heat | chiller_cool | area_cool
	Raw15Min        bool    // also generate 7-day 15-min raw_item readings
}

type categorySpec struct {
	ID       string
	ParentID string
	Name     string
	Energy   buildingenergy.EnergyType
}

func standardCategories() []categorySpec {
	return []categorySpec{
		// Electricity
		{ID: "elec-lighting", Name: "照明系统", Energy: buildingenergy.Electricity},
		{ID: "elec-hvac", Name: "暖通空调", Energy: buildingenergy.Electricity},
		{ID: "elec-power", Name: "动力系统", Energy: buildingenergy.Electricity},
		{ID: "elec-socket", Name: "插座与办公设备", Energy: buildingenergy.Electricity},
		// Water
		{ID: "water-domestic", Name: "生活用水", Energy: buildingenergy.Water},
		{ID: "water-makeup", Name: "空调补水", Energy: buildingenergy.Water},
		{ID: "water-irrigation", Name: "绿化用水", Energy: buildingenergy.Water},
		// Gas
		{ID: "gas-boiler", Name: "锅炉用气", Energy: buildingenergy.Gas},
		{ID: "gas-kitchen", Name: "厨房燃气", Energy: buildingenergy.Gas},
		{ID: "gas-hotwater", Name: "热水系统用气", Energy: buildingenergy.Gas},
		// Heat
		{ID: "heat-floor", Name: "楼层供热", Energy: buildingenergy.Heat},
		{ID: "heat-area", Name: "区域供热", Energy: buildingenergy.Heat},
		// Cooling
		{ID: "cool-chiller", Name: "空调冷量", Energy: buildingenergy.Cooling},
		{ID: "cool-area", Name: "区域供冷", Energy: buildingenergy.Cooling},
	}
}

func standardBuildings() []buildingSpec {
	return []buildingSpec{
		{
			ID: "bldg-a", LegacyID: 101, Name: "A · 星云办公园区", BuildingType: "office", Region: "上海",
			AreaOld: 30000, AreaNew: 32000, PeopleOld: 1480, PeopleNew: 1600,
			GatewayID: "sim-gw-a", GatewayName: "模拟网关 · A 办公园区",
			Spaces: []spaceSpec{
				{ID: "sp-a-t1", LegacyID: 1011, Name: "A1 主办公楼（F1–F6）", AreaSqm: 18000},
				{ID: "sp-a-t1-f3", LegacyID: 1012, ParentID: "sp-a-t1", Name: "A1 三层研发办公区", AreaSqm: 6000},
				{ID: "sp-a-t2", LegacyID: 1013, Name: "A2 研发中心与机房", AreaSqm: 14000},
			},
			Orgs: []orgSpec{
				{ID: "org-a-rd", Name: "星云研发中心", Headcount: 920},
				{ID: "org-a-ops", Name: "园区物业与运营部", Headcount: 680},
			},
		},
		{
			ID: "bldg-b", LegacyID: 102, Name: "B · 汇景商业综合体", BuildingType: "commercial", Region: "上海",
			AreaOld: 58000, AreaNew: 58000, PeopleOld: 4000, PeopleNew: 4200,
			GatewayID: "sim-gw-b", GatewayName: "模拟网关 · B 商业综合体",
			Spaces: []spaceSpec{
				{ID: "sp-b-mall", LegacyID: 1021, Name: "B1 购物中心主馆（F1–F4）", AreaSqm: 34000},
				{ID: "sp-b-food", LegacyID: 1022, Name: "B2 餐饮美食街区（F5）", AreaSqm: 14000},
				{ID: "sp-b-atrium", LegacyID: 1023, ParentID: "sp-b-mall", Name: "中庭与地下设备区（B1）", AreaSqm: 10000},
			},
			Orgs: []orgSpec{
				{ID: "org-b-retail", Name: "商业零售运营部", Headcount: 2400},
				{ID: "org-b-dining", Name: "餐饮街区商管部", Headcount: 1800},
			},
		},
		{
			ID: "bldg-c", LegacyID: 103, Name: "C · 启明教育园区", BuildingType: "school", Region: "上海",
			AreaOld: 45000, AreaNew: 45000, PeopleOld: 3400, PeopleNew: 3500,
			GatewayID: "sim-gw-c", GatewayName: "模拟网关 · C 教育园区",
			Spaces: []spaceSpec{
				{ID: "sp-c-teach", LegacyID: 1031, Name: "C1 公共教学楼（F1–F4）", AreaSqm: 20000},
				{ID: "sp-c-lab", LegacyID: 1032, Name: "C2 科研实验与图书馆", AreaSqm: 13000},
				{ID: "sp-c-dorm", LegacyID: 1033, Name: "C3 学生宿舍与食堂区", AreaSqm: 12000},
			},
			Orgs: []orgSpec{
				{ID: "org-c-acad", Name: "教务与科研实验中心", Headcount: 2300},
				{ID: "org-c-campus", Name: "校园后勤保障部", Headcount: 1200},
			},
		},
	}
}

func standardMeters(buildings []buildingSpec) []meterSpec {
	var out []meterSpec
	rollMod := 100000.0
	for _, b := range buildings {
		p := b.ID[len(b.ID)-1:] // "a", "b", "c"
		sp0, sp1, sp2 := b.Spaces[0].ID, b.Spaces[1].ID, b.Spaces[2].ID
		org0, org1 := b.Orgs[0].ID, b.Orgs[1].ID

		scale := 1.0
		switch b.BuildingType {
		case "commercial":
			scale = 1.65
		case "school":
			scale = 1.25
		}

		// 1. Electricity (1 total + 4 submeters)
		eMain := "sim-" + p + "-e-main"
		out = append(out,
			meterSpec{TerminalID: eMain, Name: b.Name + " · 电力总进线", BuildingID: b.ID, SpaceID: sp0, OrgID: org1, DomainCode: "sim_electric", TypeCode: "sim_elec_main", Energy: buildingenergy.Electricity, Unit: "kWh", Multiplier: 40, SourceTable: pgadapter.SourceTableSnapshotCoarse, CadenceSeconds: 3600, IsTotal: true, Raw15Min: true},
			meterSpec{TerminalID: "sim-" + p + "-e-light", Name: b.Name + " · 照明分表", BuildingID: b.ID, SpaceID: sp0, OrgID: org0, DomainCode: "sim_electric", TypeCode: "sim_elec_sub", Energy: buildingenergy.Electricity, Unit: "kWh", Multiplier: 1, SourceTable: pgadapter.SourceTableSnapshotCoarse, CadenceSeconds: 3600, ParentTerminal: eMain, ZoneCode: "elec-lighting", CategoryID: "elec-lighting", BaseHourly: 42 * scale, Role: "lighting", Raw15Min: true},
			meterSpec{TerminalID: "sim-" + p + "-e-hvac", Name: b.Name + " · 空调分表", BuildingID: b.ID, SpaceID: sp2, OrgID: org1, DomainCode: "sim_electric", TypeCode: "sim_elec_sub", Energy: buildingenergy.Electricity, Unit: "kWh", Multiplier: 10, SourceTable: pgadapter.SourceTableSnapshotCoarse, CadenceSeconds: 3600, ParentTerminal: eMain, ZoneCode: "elec-hvac", CategoryID: "elec-hvac", BaseHourly: 78 * scale, Role: "hvac", Raw15Min: true},
			meterSpec{TerminalID: "sim-" + p + "-e-power", Name: b.Name + " · 动力分表", BuildingID: b.ID, SpaceID: sp2, OrgID: org1, DomainCode: "sim_electric", TypeCode: "sim_elec_sub", Energy: buildingenergy.Electricity, Unit: "kWh", Multiplier: 5, SourceTable: pgadapter.SourceTableSnapshotCoarse, CadenceSeconds: 3600, ParentTerminal: eMain, ZoneCode: "elec-power", CategoryID: "elec-power", BaseHourly: 34 * scale, Role: "power"},
			meterSpec{TerminalID: "sim-" + p + "-e-socket", Name: b.Name + " · 插座分表", BuildingID: b.ID, SpaceID: sp1, OrgID: org0, DomainCode: "sim_electric", TypeCode: "sim_elec_sub", Energy: buildingenergy.Electricity, Unit: "kWh", Multiplier: 1, SourceTable: pgadapter.SourceTableSnapshotCoarse, CadenceSeconds: 3600, ParentTerminal: eMain, ZoneCode: "elec-socket", CategoryID: "elec-socket", BaseHourly: 28 * scale, Role: "socket"},
		)
		// Apply controlled rollover modulus to bldg-b lighting meter & unclassified anomaly to bldg-b socket meter
		if b.ID == "bldg-b" {
			out[len(out)-4].RegisterModulus = &rollMod // sim-b-e-light
			out[len(out)-1].CategoryID = ""            // sim-b-e-socket -> intentional Unclassified (未分项)!
		}
		// Apply weighted multi-category split (80% hvac + 20% power) to bldg-a HVAC meter to exercise P2 multi-category weights
		if b.ID == "bldg-a" {
			out[len(out)-3].SecondCatID = "elec-power"
			out[len(out)-3].SecondWeightPPM = 200000
		}

		// 2. Water (1 total + 3 submeters)
		wMain := "sim-" + p + "-w-main"
		out = append(out,
			meterSpec{TerminalID: wMain, Name: b.Name + " · 总水表", BuildingID: b.ID, SpaceID: sp0, OrgID: org1, DomainCode: "sim_water", TypeCode: "sim_water_meter", Energy: buildingenergy.Water, Unit: "m³", Multiplier: 1, SourceTable: pgadapter.SourceTableSnapshotCoarse, CadenceSeconds: 3600, IsTotal: true, Raw15Min: true},
			meterSpec{TerminalID: "sim-" + p + "-w-domestic", Name: b.Name + " · 生活用水", BuildingID: b.ID, SpaceID: sp0, OrgID: org0, DomainCode: "sim_water", TypeCode: "sim_water_meter", Energy: buildingenergy.Water, Unit: "m³", Multiplier: 1, SourceTable: pgadapter.SourceTableSnapshotCoarse, CadenceSeconds: 3600, ParentTerminal: wMain, ZoneCode: "water-domestic", CategoryID: "water-domestic", BaseHourly: 2.2 * scale, Role: "domestic"},
			meterSpec{TerminalID: "sim-" + p + "-w-makeup", Name: b.Name + " · 空调补水", BuildingID: b.ID, SpaceID: sp2, OrgID: org1, DomainCode: "sim_water", TypeCode: "sim_water_meter", Energy: buildingenergy.Water, Unit: "m³", Multiplier: 1, SourceTable: pgadapter.SourceTableSnapshotCoarse, CadenceSeconds: 3600, ParentTerminal: wMain, ZoneCode: "water-makeup", CategoryID: "water-makeup", BaseHourly: 0.9 * scale, Role: "cooling_makeup"},
			meterSpec{TerminalID: "sim-" + p + "-w-green", Name: b.Name + " · 绿化用水", BuildingID: b.ID, SpaceID: sp1, OrgID: org1, DomainCode: "sim_water", TypeCode: "sim_water_meter", Energy: buildingenergy.Water, Unit: "m³", Multiplier: 1, SourceTable: pgadapter.SourceTableSnapshotCoarse, CadenceSeconds: 3600, ParentTerminal: wMain, ZoneCode: "water-irrigation", CategoryID: "water-irrigation", BaseHourly: 0.45 * scale, Role: "irrigation"},
		)

		// 3. Gas (1 total + 3 submeters)
		gMain := "sim-" + p + "-g-main"
		out = append(out,
			meterSpec{TerminalID: gMain, Name: b.Name + " · 总燃气表", BuildingID: b.ID, SpaceID: sp1, OrgID: org1, DomainCode: "sim_gas", TypeCode: "sim_gas_meter", Energy: buildingenergy.Gas, Unit: "Nm³", Multiplier: 1, SourceTable: pgadapter.SourceTableSnapshotCoarse, CadenceSeconds: 3600, IsTotal: true, Raw15Min: true},
			meterSpec{TerminalID: "sim-" + p + "-g-boiler", Name: b.Name + " · 锅炉燃气", BuildingID: b.ID, SpaceID: sp2, OrgID: org1, DomainCode: "sim_gas", TypeCode: "sim_gas_meter", Energy: buildingenergy.Gas, Unit: "Nm³", Multiplier: 1, SourceTable: pgadapter.SourceTableSnapshotCoarse, CadenceSeconds: 3600, ParentTerminal: gMain, ZoneCode: "gas-boiler", CategoryID: "gas-boiler", BaseHourly: 6.5 * scale, Role: "boiler"},
			meterSpec{TerminalID: "sim-" + p + "-g-kitchen", Name: b.Name + " · 厨房燃气", BuildingID: b.ID, SpaceID: sp1, OrgID: org0, DomainCode: "sim_gas", TypeCode: "sim_gas_meter", Energy: buildingenergy.Gas, Unit: "Nm³", Multiplier: 1, SourceTable: pgadapter.SourceTableSnapshotCoarse, CadenceSeconds: 3600, ParentTerminal: gMain, ZoneCode: "gas-kitchen", CategoryID: "gas-kitchen", BaseHourly: 4.8 * scale, Role: "kitchen"},
			meterSpec{TerminalID: "sim-" + p + "-g-hotwater", Name: b.Name + " · 热水系统", BuildingID: b.ID, SpaceID: sp0, OrgID: org1, DomainCode: "sim_gas", TypeCode: "sim_gas_meter", Energy: buildingenergy.Gas, Unit: "Nm³", Multiplier: 1, SourceTable: pgadapter.SourceTableSnapshotCoarse, CadenceSeconds: 3600, ParentTerminal: gMain, ZoneCode: "gas-hotwater", CategoryID: "gas-hotwater", BaseHourly: 2.1 * scale, Role: "hot_water"},
		)

		// 4. Heat (1 total + 2 submeters)
		hMain := "sim-" + p + "-h-main"
		out = append(out,
			meterSpec{TerminalID: hMain, Name: b.Name + " · 总供热表", BuildingID: b.ID, SpaceID: sp2, OrgID: org1, DomainCode: "sim_heat", TypeCode: "sim_heat_meter", Energy: buildingenergy.Heat, Unit: "GJ", Multiplier: 1, SourceTable: pgadapter.SourceTableSnapshotCoarse, CadenceSeconds: 3600, IsTotal: true, Raw15Min: true},
			meterSpec{TerminalID: "sim-" + p + "-h-floor", Name: b.Name + " · 楼层供热", BuildingID: b.ID, SpaceID: sp0, OrgID: org0, DomainCode: "sim_heat", TypeCode: "sim_heat_meter", Energy: buildingenergy.Heat, Unit: "GJ", Multiplier: 1, SourceTable: pgadapter.SourceTableSnapshotCoarse, CadenceSeconds: 3600, ParentTerminal: hMain, ZoneCode: "heat-floor", CategoryID: "heat-floor", BaseHourly: 0.85 * scale, Role: "floor_heat"},
			meterSpec{TerminalID: "sim-" + p + "-h-area", Name: b.Name + " · 区域供热", BuildingID: b.ID, SpaceID: sp1, OrgID: org1, DomainCode: "sim_heat", TypeCode: "sim_heat_meter", Energy: buildingenergy.Heat, Unit: "GJ", Multiplier: 1, SourceTable: pgadapter.SourceTableSnapshotCoarse, CadenceSeconds: 3600, ParentTerminal: hMain, ZoneCode: "heat-area", CategoryID: "heat-area", BaseHourly: 0.65 * scale, Role: "area_heat"},
		)

		// 5. Cooling (1 total + 2 submeters)
		cMain := "sim-" + p + "-c-main"
		out = append(out,
			meterSpec{TerminalID: cMain, Name: b.Name + " · 总供冷表", BuildingID: b.ID, SpaceID: sp2, OrgID: org1, DomainCode: "sim_cooling", TypeCode: "sim_cool_meter", Energy: buildingenergy.Cooling, Unit: "GJ", Multiplier: 1, SourceTable: pgadapter.SourceTableSnapshotCoarse, CadenceSeconds: 3600, IsTotal: true, Raw15Min: true},
			meterSpec{TerminalID: "sim-" + p + "-c-chiller", Name: b.Name + " · 空调冷量", BuildingID: b.ID, SpaceID: sp0, OrgID: org0, DomainCode: "sim_cooling", TypeCode: "sim_cool_meter", Energy: buildingenergy.Cooling, Unit: "GJ", Multiplier: 1, SourceTable: pgadapter.SourceTableSnapshotCoarse, CadenceSeconds: 3600, ParentTerminal: cMain, ZoneCode: "cool-chiller", CategoryID: "cool-chiller", BaseHourly: 1.15 * scale, Role: "chiller_cool"},
			meterSpec{TerminalID: "sim-" + p + "-c-area", Name: b.Name + " · 区域供冷", BuildingID: b.ID, SpaceID: sp1, OrgID: org1, DomainCode: "sim_cooling", TypeCode: "sim_cool_meter", Energy: buildingenergy.Cooling, Unit: "GJ", Multiplier: 1, SourceTable: pgadapter.SourceTableSnapshotCoarse, CadenceSeconds: 3600, ParentTerminal: cMain, ZoneCode: "cool-area", CategoryID: "cool-area", BaseHourly: 0.72 * scale, Role: "area_cool"},
		)
	}
	return out
}

func standardAnomalyManifest() []AnomalyManifestEntry {
	return []AnomalyManifestEntry{
		{Code: "ANOM-01", Kind: "missing", BuildingID: "bldg-a", TerminalID: "sim-a-e-power", Date: "2026-11-14", Description: "02:00–07:00 连续 6 小时缺测（覆盖率 18/24 = 75%）"},
		{Code: "ANOM-02", Kind: "partial_coverage", BuildingID: "bldg-c", TerminalID: "sim-c-w-green", Date: "2026-11-15", Description: "08:00–17:00 缺测 10 小时（覆盖率 14/24 = 58.3%，触发不可排名/覆盖不足）"},
		{Code: "ANOM-03", Kind: "abnormal_spike", BuildingID: "bldg-a", TerminalID: "sim-a-e-hvac", Date: "2026-11-17", Description: "空调电耗较同星期历史中位数激增 +52%，触发 F10 用能异常候选与 F17 诊断线索"},
		{Code: "ANOM-04", Kind: "sudden_drop", BuildingID: "bldg-b", TerminalID: "sim-b-g-kitchen", Date: "2026-11-13", Description: "厨房燃气日用量突发骤降 -72%（模拟停气检修/计量偏低样本）"},
		{Code: "ANOM-05", Kind: "rollover", BuildingID: "bldg-b", TerminalID: "sim-b-e-light", Date: "2026-11-12", Description: "14:00 表底跨越满量程 100,000 回卷并登记 rollover 事件，M1-A 正确折算正电量"},
		{Code: "ANOM-06", Kind: "meter_swap", BuildingID: "bldg-c", TerminalID: "sim-c-e-hvac", Date: "2026-11-11", Description: "10:00 换表归零并切换 register_epoch，M1-A 避免产生负能耗"},
		{Code: "ANOM-07", Kind: "zero_usage", BuildingID: "bldg-c", TerminalID: "sim-c-c-area", Date: "2026-11-16", Description: "冬季供冷停机零值日（24/24 有效零读数，明确区别于缺测）"},
		{Code: "ANOM-08", Kind: "topology_imbalance", BuildingID: "bldg-b", TerminalID: "sim-b-e-main", Date: "2026-11-17", Description: "商业综合体电力总进线较直接分表之和高 +8.8%，F09 标为未分配/待核查差额"},
		{Code: "ANOM-09", Kind: "unclassified_category", BuildingID: "bldg-b", TerminalID: "sim-b-e-socket", Date: "2025-01-01~2026-11-18", Description: "插座分表刻意未绑定正式分类，F02/F06/F21 如实归入未分项"},
	}
}
