package liveapi

import (
	"fmt"
	"math"
	"strings"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
)

const CarbonFactorVersion = "sim-carbon-factors-v1"

// CarbonFactorSpec defines a versioned, unit-checked reference CO2e emission factor.
type CarbonFactorSpec struct {
	Energy          buildingenergy.EnergyType
	ExpectedUnit    string
	Factor          float64 // kgCO2e per ExpectedUnit
	FactorUnit      string
	IncludedInTotal bool
	Note            string
}

// CarbonEstimate holds a single energy or project carbon emission reference calculation.
type CarbonEstimate struct {
	KgCO2e          *float64 `json:"kgCO2e"`
	Factor          float64  `json:"factor"`
	FactorUnit      string   `json:"factorUnit"`
	FactorVersion   string   `json:"factorVersion"`
	IncludedInTotal bool     `json:"includedInTotal"`
	Status          string   `json:"status"` // calculated | excluded_water | missing_usage | unit_mismatch
	Note            string   `json:"note"`
}

// CarbonSummary aggregates valid energy carbon references for a building/window
// while strictly excluding water consumption and never treating missing data as zero.
type CarbonSummary struct {
	TotalKgCO2e      *float64 `json:"totalKgCO2e"`
	FactorVersion    string   `json:"factorVersion"`
	Status           string   `json:"status"` // complete | partial | unavailable
	IncludedEnergies []string `json:"includedEnergies"`
	ExcludedEnergies []string `json:"excludedEnergies"`
	MissingEnergies  []string `json:"missingEnergies,omitempty"`
	Disclaimer       string   `json:"disclaimer"`
}

// LookupCarbonFactor returns the deterministic reference emission factor for an energy & unit.
// Water is explicitly excluded from energy carbon totals per PRD CALC-04.
func LookupCarbonFactor(energy buildingenergy.EnergyType, unit string) (CarbonFactorSpec, bool) {
	u := strings.ToLower(strings.TrimSpace(unit))
	switch energy {
	case buildingenergy.Electricity:
		if u == "" || u == "kwh" {
			return CarbonFactorSpec{
				Energy: energy, ExpectedUnit: "kWh", Factor: 0.5703, FactorUnit: "kgCO₂e/kWh",
				IncludedInTotal: true, Note: "参考电网平均排放因子（模拟参考值）",
			}, true
		}
		if u == "mwh" {
			return CarbonFactorSpec{
				Energy: energy, ExpectedUnit: "MWh", Factor: 570.3, FactorUnit: "kgCO₂e/MWh",
				IncludedInTotal: true, Note: "参考电网平均排放因子（MWh 折算）",
			}, true
		}
	case buildingenergy.Gas:
		if u == "" || u == "nm³" || u == "nm3" || u == "m³" || u == "m3" {
			return CarbonFactorSpec{
				Energy: energy, ExpectedUnit: "Nm³", Factor: 2.162, FactorUnit: "kgCO₂e/Nm³",
				IncludedInTotal: true, Note: "天然气燃烧参考排放因子（模拟参考值）",
			}, true
		}
	case buildingenergy.Heat:
		if u == "" || u == "gj" {
			return CarbonFactorSpec{
				Energy: energy, ExpectedUnit: "GJ", Factor: 110.0, FactorUnit: "kgCO₂e/GJ",
				IncludedInTotal: true, Note: "外购热力参考排放因子（模拟参考值）",
			}, true
		}
	case buildingenergy.Cooling:
		if u == "" || u == "gj" {
			// Electric chiller reference: 1 GJ = 277.778 kWh_th, at COP 3.0 -> 92.593 kWh_e * 0.5703 ≈ 52.80 kgCO2e/GJ.
			return CarbonFactorSpec{
				Energy: energy, ExpectedUnit: "GJ", Factor: 52.8, FactorUnit: "kgCO₂e/GJ",
				IncludedInTotal: true, Note: "外购/集中供冷电制冷折算参考因子（COP≈3.0，区别于热力因子）",
			}, true
		}
	case buildingenergy.Steam:
		if u == "" || u == "t" {
			return CarbonFactorSpec{
				Energy: energy, ExpectedUnit: "t", Factor: 180.0, FactorUnit: "kgCO₂e/t",
				IncludedInTotal: true, Note: "外购蒸汽参考排放因子（模拟参考值）",
			}, true
		}
	case buildingenergy.Water:
		return CarbonFactorSpec{
			Energy: energy, ExpectedUnit: "m³", Factor: 0, FactorUnit: "kgCO₂e/m³",
			IncludedInTotal: false, Note: "水资源消费单列，不计入能源碳排放总计（PRD CALC-04）",
		}, true
	}
	return CarbonFactorSpec{}, false
}

// EstimateCarbon computes a single energy carbon reference without treating nil/NaN as zero.
func EstimateCarbon(energy buildingenergy.EnergyType, unit string, usage *float64) CarbonEstimate {
	spec, ok := LookupCarbonFactor(energy, unit)
	if !ok {
		return CarbonEstimate{
			FactorVersion:   CarbonFactorVersion,
			IncludedInTotal: false,
			Status:          "unit_mismatch",
			Note:            fmt.Sprintf("未配置 %s (%s) 的合法折碳系数，禁止估算碳排放", energy, unit),
		}
	}
	if !spec.IncludedInTotal {
		return CarbonEstimate{
			Factor:          0,
			FactorUnit:      spec.FactorUnit,
			FactorVersion:   CarbonFactorVersion,
			IncludedInTotal: false,
			Status:          "excluded_water",
			Note:            spec.Note,
		}
	}
	if usage == nil || math.IsNaN(*usage) || math.IsInf(*usage, 0) {
		return CarbonEstimate{
			Factor:          spec.Factor,
			FactorUnit:      spec.FactorUnit,
			FactorVersion:   CarbonFactorVersion,
			IncludedInTotal: true,
			Status:          "missing_usage",
			Note:            "当前时段缺少有效用量读数，碳排放留空（不按 0 计）",
		}
	}
	kg := math.Round((*usage*spec.Factor)*1000) / 1000
	return CarbonEstimate{
		KgCO2e:          &kg,
		Factor:          spec.Factor,
		FactorUnit:      spec.FactorUnit,
		FactorVersion:   CarbonFactorVersion,
		IncludedInTotal: true,
		Status:          "calculated",
		Note:            spec.Note,
	}
}

// SummarizeCarbon aggregates energy cards into a building-level reference carbon summary.
func SummarizeCarbon(cards []LiveOverviewEnergyCard) *CarbonSummary {
	var total float64
	var included, excluded, missing []string
	hasAny := false
	hasLowQuality := false

	for _, c := range cards {
		est := c.Carbon
		if est == nil {
			e := EstimateCarbon(c.Energy, c.Unit, c.Usage)
			est = &e
		}
		if !est.IncludedInTotal {
			excluded = append(excluded, string(c.Energy))
			continue
		}
		if est.KgCO2e == nil {
			missing = append(missing, string(c.Energy))
			continue
		}
		if c.Coverage.Rate != nil && *c.Coverage.Rate < 1.0 {
			hasLowQuality = true
		}
		total += *est.KgCO2e
		included = append(included, string(c.Energy))
		hasAny = true
	}

	status := "unavailable"
	var totalPtr *float64
	if hasAny {
		v := math.Round(total*1000) / 1000
		totalPtr = &v
		if len(missing) > 0 || hasLowQuality {
			status = "partial"
		} else {
			status = "complete"
		}
	}
	return &CarbonSummary{
		TotalKgCO2e:      totalPtr,
		FactorVersion:    CarbonFactorVersion,
		Status:           status,
		IncludedEnergies: included,
		ExcludedEnergies: excluded,
		MissingEnergies:  missing,
		Disclaimer:       "基于模拟参考折碳系数（sim-carbon-factors-v1）按各能源用量折算，水资源消费单列不计入能源碳排；仅供模拟分析参考，不作为正式碳核查或履约凭证。",
	}
}
