package liveapi

import (
	"time"

	"github.com/RenJieWeia/iot-admin/internal/buildingenergy"
	"github.com/RenJieWeia/iot-admin/internal/buildingenergy/pgadapter"
)

// ── Management & Governance Models (P0 / P1 / P2 / P3 / P4) ────────────────

type BuildingProfile struct {
	BuildingID        string     `json:"buildingId"`
	Revision          string     `json:"revision"`
	Name              string     `json:"name"`
	BuildingType      string     `json:"buildingType"`
	Region            string     `json:"region,omitempty"`
	GrossAreaSqm      *float64   `json:"grossAreaSqm,omitempty"`
	OccupiedHeadcount *float64   `json:"occupiedHeadcount,omitempty"`
	Timezone          string     `json:"timezone"`
	EffectiveFrom     time.Time  `json:"effectiveFrom"`
	EffectiveTo       *time.Time `json:"effectiveTo,omitempty"`
	LegacyBuildingID  *int64     `json:"legacyBuildingId,omitempty"`
	SourceSystem      string     `json:"sourceSystem"`
}

type SpaceProfile struct {
	SpaceID       string     `json:"spaceId"`
	Revision      string     `json:"revision"`
	BuildingID    string     `json:"buildingId"`
	ParentSpaceID string     `json:"parentSpaceId,omitempty"`
	Name          string     `json:"name"`
	AreaSqm       *float64   `json:"areaSqm,omitempty"`
	EffectiveFrom time.Time  `json:"effectiveFrom"`
	EffectiveTo   *time.Time `json:"effectiveTo,omitempty"`
	LegacyAreaID  *int64     `json:"legacyAreaId,omitempty"`
	SourceSystem  string     `json:"sourceSystem"`
}

type OrganizationProfile struct {
	OrgID         string     `json:"orgId"`
	Revision      string     `json:"revision"`
	ParentOrgID   string     `json:"parentOrgId,omitempty"`
	Name          string     `json:"name"`
	Headcount     *float64   `json:"headcount,omitempty"`
	EffectiveFrom time.Time  `json:"effectiveFrom"`
	EffectiveTo   *time.Time `json:"effectiveTo,omitempty"`
	SourceSystem  string     `json:"sourceSystem"`
}

type CategoryNodeRecord struct {
	CategoryID string                    `json:"categoryId"`
	ParentID   string                    `json:"parentId,omitempty"`
	Name       string                    `json:"name"`
	Energy     buildingenergy.EnergyType `json:"energy"`
}

type CategoryAllocationInput struct {
	CategoryID string `json:"categoryId"`
	WeightPPM  int64  `json:"weightPpm"`
}

type PointCategoryBindingRecord struct {
	TerminalID    string                    `json:"terminalId"`
	ItemCode      string                    `json:"itemCode"`
	Revision      string                    `json:"revision"`
	EffectiveFrom time.Time                 `json:"effectiveFrom"`
	EffectiveTo   *time.Time                `json:"effectiveTo,omitempty"`
	Allocations   []CategoryAllocationInput `json:"allocations"`
}

type MeteringProfileRevisionRecord struct {
	Generation      uint64                         `json:"generation"`
	Revision        string                         `json:"revision"`
	EffectiveFrom   time.Time                      `json:"effectiveFrom"`
	EffectiveTo     *time.Time                     `json:"effectiveTo,omitempty"`
	SourceTable     pgadapter.SourceTable          `json:"sourceTable"`
	Energy          buildingenergy.EnergyType      `json:"energy"`
	MeasurementKind buildingenergy.MeasurementKind `json:"measurementKind"`
	Unit            string                         `json:"unit"`
	Multiplier      float64                        `json:"multiplier"`
	RegisterModulus *float64                       `json:"registerModulus,omitempty"`
	RegisterEpoch   string                         `json:"registerEpoch,omitempty"`
	CadenceSeconds  int                            `json:"cadenceSeconds"`
}

type MeteringBindingAllocationRecord struct {
	TargetID       string `json:"targetId"`
	TargetRevision string `json:"targetRevision"`
	WeightPPM      int64  `json:"weightPpm"`
}

type MeteringBindingRevisionRecord struct {
	Generation    uint64                            `json:"generation"`
	ViewKind      buildingenergy.ObjectKind         `json:"viewKind"`
	Revision      string                            `json:"revision"`
	EffectiveFrom time.Time                         `json:"effectiveFrom"`
	EffectiveTo   *time.Time                        `json:"effectiveTo,omitempty"`
	Allocations   []MeteringBindingAllocationRecord `json:"allocations"`
}

type MeteringGenerationRecord struct {
	Generation    uint64                          `json:"generation"`
	PreviousGen   *uint64                         `json:"previousGeneration,omitempty"`
	RecordedAt    time.Time                       `json:"recordedAt"`
	PublishedBy   string                          `json:"publishedBy"`
	Reason        string                          `json:"reason"`
	Profiles      []MeteringProfileRevisionRecord `json:"profiles"`
	Bindings      []MeteringBindingRevisionRecord `json:"bindings"`
}

type MeteringPointHistoryResponse struct {
	TerminalID       string                       `json:"terminalId"`
	ItemCode         string                       `json:"itemCode"`
	CurrentGen       uint64                       `json:"currentGeneration"`
	Enabled          bool                         `json:"enabled"`
	StatusReason     string                       `json:"statusReason,omitempty"`
	Generations      []MeteringGenerationRecord   `json:"generations"`
	CategoryBindings []PointCategoryBindingRecord `json:"categoryBindings"`
	RegisterEvents   []RegisterEventRecord        `json:"registerEvents"`
}

type ExceptionSummary struct {
	TerminalID string `json:"terminalId"`
	ErrorType  string `json:"errorType"`
	Count      int64  `json:"count"`
}

type TopologyEdge struct {
	TopologyVersion  string                    `json:"topologyVersion"`
	EffectiveFrom    time.Time                 `json:"effectiveFrom"`
	EffectiveTo      *time.Time                `json:"effectiveTo,omitempty"`
	BuildingID       string                    `json:"buildingId"`
	Energy           buildingenergy.EnergyType `json:"energy"`
	ParentTerminalID string                    `json:"parentTerminalId"`
	ParentItemCode   string                    `json:"parentItemCode"`
	ChildTerminalID  string                    `json:"childTerminalId"`
	ChildItemCode    string                    `json:"childItemCode"`
	ZoneCode         string                    `json:"zoneCode"`
}

type RegisterEventRecord struct {
	ID          int64     `json:"id"`
	TerminalID  string    `json:"terminalId"`
	ItemCode    string    `json:"itemCode"`
	EventKind   string    `json:"eventKind"`
	EffectiveAt time.Time `json:"effectiveAt"`
	NewEpoch    string    `json:"newEpoch,omitempty"`
	Reason      string    `json:"reason"`
	Actor       string    `json:"actor"`
}

type TargetConfigRecord struct {
	ObjectKind    buildingenergy.ObjectKind   `json:"objectKind"`
	ObjectID      string                      `json:"objectId"`
	Energy        buildingenergy.EnergyType   `json:"energy"`
	Year          int                         `json:"year"`
	Revision      string                      `json:"revision"`
	EffectiveFrom time.Time                   `json:"effectiveFrom"`
	EffectiveTo   *time.Time                  `json:"effectiveTo,omitempty"`
	Unit          string                      `json:"unit"`
	AnnualMilli   int64                       `json:"annualMilli"`
	Method        buildingenergy.TargetMethod `json:"method"`
	WeightPPM     []int64                     `json:"weightPpm,omitempty"`
	SourceSystem  string                      `json:"sourceSystem"`
}

type AnomalyRuleConfigRecord struct {
	ObjectKind              buildingenergy.ObjectKind `json:"objectKind"`
	ObjectID                string                    `json:"objectId"`
	Energy                  buildingenergy.EnergyType `json:"energy"`
	Revision                string                    `json:"revision"`
	EffectiveFrom           time.Time                 `json:"effectiveFrom"`
	EffectiveTo             *time.Time                `json:"effectiveTo,omitempty"`
	MinimumHistory          int                       `json:"minimumHistory"`
	MinimumAbsoluteIncrease float64                   `json:"minimumAbsoluteIncrease"`
	MinimumRelativeIncrease float64                   `json:"minimumRelativeIncrease"`
	SourceSystem            string                    `json:"sourceSystem"`
}

type DenominatorConfigRecord struct {
	ObjectKind      buildingenergy.ObjectKind      `json:"objectKind"`
	ObjectID        string                         `json:"objectId"`
	DenominatorKind buildingenergy.DenominatorKind `json:"denominatorKind"`
	Revision        string                         `json:"revision"`
	PeriodFrom      time.Time                      `json:"periodFrom"`
	PeriodTo        time.Time                      `json:"periodTo"`
	EffectiveFrom   time.Time                      `json:"effectiveFrom"`
	EffectiveTo     *time.Time                     `json:"effectiveTo,omitempty"`
	Value           float64                        `json:"value"`
	Unit            string                         `json:"unit"`
	Basis           string                         `json:"basis"`
	SourceSystem    string                         `json:"sourceSystem"`
}

type BenchmarkConfigRecord struct {
	BenchmarkID        string                       `json:"benchmarkId"`
	Revision           string                       `json:"revision"`
	EffectiveFrom      time.Time                    `json:"effectiveFrom"`
	EffectiveTo        *time.Time                   `json:"effectiveTo,omitempty"`
	Name               string                       `json:"name"`
	Publisher          string                       `json:"publisher"`
	Region             string                       `json:"region"`
	MetricID           string                       `json:"metricId"`
	TemplateVersion    string                       `json:"templateVersion"`
	Unit               string                       `json:"unit"`
	Kind               buildingenergy.BenchmarkKind `json:"kind"`
	BuildingType       buildingenergy.BuildingType  `json:"buildingType"`
	ObjectKind         buildingenergy.ObjectKind    `json:"objectKind"`
	PeriodGranularity  buildingenergy.Granularity   `json:"periodGranularity"`
	MaximumValue       float64                      `json:"maximumValue"`
	MinimumDenominator *float64                     `json:"minimumDenominator,omitempty"`
	SourceSystem       string                       `json:"sourceSystem"`
}

type CustomReportTemplateRecord struct {
	TemplateID string    `json:"templateId"`
	Name       string    `json:"name"`
	ScopeKind  string    `json:"scopeKind"`
	Fields     []string  `json:"fields"`
	CreatedBy  string    `json:"createdBy"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type DatasetAnomalyItem struct {
	Code        string `json:"code"`
	Kind        string `json:"kind"`
	BuildingID  string `json:"buildingId"`
	TerminalID  string `json:"terminalId"`
	Date        string `json:"date"`
	Description string `json:"description"`
}

// DatasetMeta carries unified simulation dataset provenance for all F01-F25 pages & CSV exports.
type DatasetMeta struct {
	DatasetID       string               `json:"datasetId"`
	DatasetName     string               `json:"datasetName"`
	DatasetVersion  string               `json:"datasetVersion"`
	Seed            int64                `json:"seed"`
	IsSimulated     bool                 `json:"isSimulated"`
	GeneratedAt     time.Time            `json:"generatedAt"`
	DataFrom        time.Time            `json:"dataFrom"`
	DataTo          time.Time            `json:"dataTo"`
	HourlyFrom      time.Time            `json:"hourlyFrom"`
	RawFrom         time.Time            `json:"rawFrom"`
	BuildingScope   string               `json:"buildingScope"`
	EnergyScope     string               `json:"energyScope"`
	AnomalyManifest []DatasetAnomalyItem `json:"anomalyManifest,omitempty"`
}

type InventoryResponse struct {
	DataMode              string                       `json:"dataMode"`
	DataOriginNotice      string                       `json:"dataOriginNotice"`
	Dataset               *DatasetMeta                 `json:"dataset,omitempty"`
	Project               buildingenergy.ProjectKey    `json:"project"`
	Timezone              string                       `json:"timezone"`
	EnergyDomainReady     bool                         `json:"energyDomainReady"`
	ConfiguredMeterCount  int                          `json:"configuredMeterCount"`
	UnconfiguredCount     int                          `json:"unconfiguredCount"`
	ExcludedChannelCount  int                          `json:"excludedChannelCount"`
	DisabledMeterCount    int                          `json:"disabledMeterCount"`
	Buildings             []BuildingProfile            `json:"buildings"`
	BuildingHistory       []BuildingProfile            `json:"buildingHistory"`
	Spaces                []SpaceProfile               `json:"spaces"`
	SpaceHistory          []SpaceProfile               `json:"spaceHistory"`
	Organizations         []OrganizationProfile        `json:"organizations"`
	OrganizationHistory   []OrganizationProfile        `json:"organizationHistory"`
	Meters                []pgadapter.IOTMeterRecord   `json:"meters"`
	Categories            []CategoryNodeRecord         `json:"categories"`
	CategoryBindings      []PointCategoryBindingRecord `json:"categoryBindings"`
	Topology              []TopologyEdge               `json:"topology"`
	RegisterEvents        []RegisterEventRecord        `json:"registerEvents"`
	Targets               []TargetConfigRecord         `json:"targets"`
	AnomalyRules          []AnomalyRuleConfigRecord    `json:"anomalyRules"`
	Denominators          []DenominatorConfigRecord    `json:"denominators"`
	Benchmarks            []BenchmarkConfigRecord      `json:"benchmarks"`
	CustomReportTemplates []CustomReportTemplateRecord `json:"customReportTemplates"`
	Exceptions            []ExceptionSummary           `json:"exceptions"`
}

// ── Management Write Requests ───────────────────────────────────────────────

type PublishMeteringPointRequest struct {
	TerminalID        string                         `json:"terminalId"`
	ItemCode          string                         `json:"itemCode"`
	SourceTable       pgadapter.SourceTable          `json:"sourceTable"`
	Energy            buildingenergy.EnergyType      `json:"energy"`
	MeasurementKind   buildingenergy.MeasurementKind `json:"measurementKind"`
	Unit              string                         `json:"unit"`
	Multiplier        float64                        `json:"multiplier"`
	CadenceSeconds    int                            `json:"cadenceSeconds"`
	RegisterModulus   *float64                       `json:"registerModulus,omitempty"`
	RegisterEpoch     string                         `json:"registerEpoch,omitempty"`
	EffectiveFrom     time.Time                      `json:"effectiveFrom"`
	EffectiveTo       *time.Time                     `json:"effectiveTo,omitempty"`
	BuildingAlloc     []ObjectAllocationInput        `json:"buildingAllocations,omitempty"`
	SpaceAlloc        []ObjectAllocationInput        `json:"spaceAllocations,omitempty"`
	OrganizationAlloc []ObjectAllocationInput        `json:"organizationAllocations,omitempty"`
	PreserveHistory   bool                           `json:"preserveHistory"`
	Reason            string                         `json:"reason"`
}

type ObjectAllocationInput struct {
	TargetID  string `json:"targetId"`
	WeightPPM int64  `json:"weightPpm"`
}

type SetMeteringPointStatusRequest struct {
	TerminalID string `json:"terminalId"`
	ItemCode   string `json:"itemCode"`
	Enabled    bool   `json:"enabled"`
	Reason     string `json:"reason"`
}

type UpsertBuildingRequest struct {
	BuildingID        string     `json:"buildingId"`
	Revision          string     `json:"revision"`
	Name              string     `json:"name"`
	BuildingType      string     `json:"buildingType"`
	Region            string     `json:"region"`
	GrossAreaSqm      *float64   `json:"grossAreaSqm"`
	OccupiedHeadcount *float64   `json:"occupiedHeadcount"`
	Timezone          string     `json:"timezone"`
	EffectiveFrom     *time.Time `json:"effectiveFrom,omitempty"`
	EffectiveTo       *time.Time `json:"effectiveTo,omitempty"`
	LegacyBuildingID  *int64     `json:"legacyBuildingId"`
}

type UpsertSpaceRequest struct {
	SpaceID       string     `json:"spaceId"`
	Revision      string     `json:"revision"`
	BuildingID    string     `json:"buildingId"`
	ParentSpaceID string     `json:"parentSpaceId"`
	Name          string     `json:"name"`
	AreaSqm       *float64   `json:"areaSqm"`
	EffectiveFrom *time.Time `json:"effectiveFrom,omitempty"`
	EffectiveTo   *time.Time `json:"effectiveTo,omitempty"`
	LegacyAreaID  *int64     `json:"legacyAreaId"`
}

type UpsertOrganizationRequest struct {
	OrgID         string     `json:"orgId"`
	Revision      string     `json:"revision"`
	ParentOrgID   string     `json:"parentOrgId"`
	Name          string     `json:"name"`
	Headcount     *float64   `json:"headcount"`
	EffectiveFrom *time.Time `json:"effectiveFrom,omitempty"`
	EffectiveTo   *time.Time `json:"effectiveTo,omitempty"`
}

type UpsertCategoryRequest struct {
	CategoryID string                    `json:"categoryId"`
	ParentID   string                    `json:"parentId"`
	Name       string                    `json:"name"`
	Energy     buildingenergy.EnergyType `json:"energy"`
}

type PublishCategoryBindingRequest struct {
	TerminalID    string                    `json:"terminalId"`
	ItemCode      string                    `json:"itemCode"`
	Revision      string                    `json:"revision"`
	EffectiveFrom *time.Time                `json:"effectiveFrom,omitempty"`
	EffectiveTo   *time.Time                `json:"effectiveTo,omitempty"`
	Allocations   []CategoryAllocationInput `json:"allocations"`
}

type TopologyChildInput struct {
	TerminalID string `json:"terminalId"`
	ItemCode   string `json:"itemCode"`
	ZoneCode   string `json:"zoneCode"`
}

type PublishTopologyRequest struct {
	TopologyVersion  string                    `json:"topologyVersion"`
	BuildingID       string                    `json:"buildingId"`
	Energy           buildingenergy.EnergyType `json:"energy"`
	EffectiveFrom    *time.Time                `json:"effectiveFrom,omitempty"`
	EffectiveTo      *time.Time                `json:"effectiveTo,omitempty"`
	ParentTerminalID string                    `json:"parentTerminalId"`
	ParentItemCode   string                    `json:"parentItemCode"`
	Children         []TopologyChildInput      `json:"children"`
}

type RecordEventRequest struct {
	TerminalID  string    `json:"terminalId"`
	ItemCode    string    `json:"itemCode"`
	EventKind   string    `json:"eventKind"`
	EffectiveAt time.Time `json:"effectiveAt"`
	NewEpoch    string    `json:"newEpoch"`
	Reason      string    `json:"reason"`
}

type PublishTargetRequest struct {
	ObjectKind    buildingenergy.ObjectKind   `json:"objectKind"`
	ObjectID      string                      `json:"objectId"`
	Energy        buildingenergy.EnergyType   `json:"energy"`
	Year          int                         `json:"year"`
	Revision      string                      `json:"revision"`
	EffectiveFrom *time.Time                  `json:"effectiveFrom,omitempty"`
	EffectiveTo   *time.Time                  `json:"effectiveTo,omitempty"`
	Unit          string                      `json:"unit"`
	AnnualValue   float64                     `json:"annualValue"`
	Method        buildingenergy.TargetMethod `json:"method"`
	WeightPPM     []int64                     `json:"weightPpm,omitempty"`
}

type PublishAnomalyRuleRequest struct {
	ObjectKind              buildingenergy.ObjectKind `json:"objectKind"`
	ObjectID                string                    `json:"objectId"`
	Energy                  buildingenergy.EnergyType `json:"energy"`
	Revision                string                    `json:"revision"`
	EffectiveFrom           *time.Time                `json:"effectiveFrom,omitempty"`
	EffectiveTo             *time.Time                `json:"effectiveTo,omitempty"`
	MinimumHistory          int                       `json:"minimumHistory"`
	MinimumAbsoluteIncrease float64                   `json:"minimumAbsoluteIncrease"`
	MinimumRelativeIncrease float64                   `json:"minimumRelativeIncrease"`
}

type PublishDenominatorRequest struct {
	ObjectKind      buildingenergy.ObjectKind      `json:"objectKind"`
	ObjectID        string                         `json:"objectId"`
	DenominatorKind buildingenergy.DenominatorKind `json:"denominatorKind"`
	Revision        string                         `json:"revision"`
	PeriodFrom      time.Time                      `json:"periodFrom"`
	PeriodTo        time.Time                      `json:"periodTo"`
	EffectiveFrom   *time.Time                     `json:"effectiveFrom,omitempty"`
	EffectiveTo     *time.Time                     `json:"effectiveTo,omitempty"`
	Value           float64                        `json:"value"`
	Unit            string                         `json:"unit"`
	Basis           string                         `json:"basis"`
}

type PublishBenchmarkRequest struct {
	BenchmarkID        string                       `json:"benchmarkId"`
	Revision           string                       `json:"revision"`
	EffectiveFrom      *time.Time                   `json:"effectiveFrom,omitempty"`
	EffectiveTo        *time.Time                   `json:"effectiveTo,omitempty"`
	Name               string                       `json:"name"`
	Publisher          string                       `json:"publisher"`
	Region             string                       `json:"region"`
	MetricID           string                       `json:"metricId"`
	TemplateVersion    string                       `json:"templateVersion"`
	Unit               string                       `json:"unit"`
	Kind               buildingenergy.BenchmarkKind `json:"kind"`
	BuildingType       buildingenergy.BuildingType  `json:"buildingType"`
	ObjectKind         buildingenergy.ObjectKind    `json:"objectKind"`
	PeriodGranularity  buildingenergy.Granularity   `json:"periodGranularity"`
	MaximumValue       float64                      `json:"maximumValue"`
	MinimumDenominator *float64                     `json:"minimumDenominator,omitempty"`
}

// ── Analytics & Business View DTOs (F01–F25) ────────────────────────────────

type LiveCoverage struct {
	Expected float64  `json:"expected"`
	Valid    float64  `json:"valid"`
	Missing  float64  `json:"missing"`
	Suspect  float64  `json:"suspect"`
	Rate     *float64 `json:"rate"`
}

func makeCoverage(c buildingenergy.WeightedCoverage) LiveCoverage {
	var rate *float64
	if c.Expected > 0 {
		v := c.Valid / c.Expected
		rate = &v
	}
	return LiveCoverage{Expected: c.Expected, Valid: c.Valid, Missing: c.Missing, Suspect: c.Suspect, Rate: rate}
}

type LiveSourceTrace struct {
	TerminalID             string                         `json:"terminalId"`
	TerminalName           string                         `json:"terminalName"`
	ItemCode               string                         `json:"itemCode"`
	SourceTable            pgadapter.SourceTable          `json:"sourceTable"`
	SourceSystem           string                         `json:"sourceSystem"`
	MeasurementKind        buildingenergy.MeasurementKind `json:"measurementKind"`
	Usage                  *float64                       `json:"usage"`
	Unit                   string                         `json:"unit"`
	Multiplier             float64                        `json:"multiplier"`
	CadenceSeconds         int                            `json:"cadenceSeconds"`
	RegisterEpoch          string                         `json:"registerEpoch,omitempty"`
	RegisterModulus        *float64                       `json:"registerModulus,omitempty"`
	Valid                  int                            `json:"valid"`
	Expected               int                            `json:"expected"`
	Missing                int                            `json:"missing"`
	Suspect                int                            `json:"suspect"`
	ProfileVersion         string                         `json:"profileVersion"`
	ObjectBindingVersion   string                         `json:"objectBindingVersion"`
	CategoryBindingVersion string                         `json:"categoryBindingVersion,omitempty"`
	MappingGeneration      uint64                         `json:"mappingGeneration"`
}

type LiveCategoryRow struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Usage    *float64          `json:"usage"`
	Share    *float64          `json:"share"`
	Coverage LiveCoverage      `json:"coverage"`
	Children []LiveCategoryRow `json:"children,omitempty"`
}

type LiveBucketPoint struct {
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Usage    *float64  `json:"usage"`
	Valid    int       `json:"valid"`
	Expected int       `json:"expected"`
}

type LiveOverviewEnergyCard struct {
	Energy            buildingenergy.EnergyType `json:"energy"`
	Unit              string                    `json:"unit"`
	Usage             *float64                  `json:"usage"`
	UnclassifiedUsage *float64                  `json:"unclassifiedUsage"`
	Coverage          LiveCoverage              `json:"coverage"`
	Carbon            *CarbonEstimate           `json:"carbon,omitempty"`
	MeterCount        int                       `json:"meterCount"`
	Categories        []LiveCategoryRow         `json:"categories"`
	Sources           []LiveSourceTrace         `json:"sources"`
}

// F01 综合能耗概览
type LiveOverviewResponse struct {
	DataMode             string                   `json:"dataMode"`
	DataOriginNotice     string                   `json:"dataOriginNotice"`
	ConfigurationState   string                   `json:"configurationState"`
	ConfigurationMessage string                   `json:"configurationMessage,omitempty"`
	BuildingID           string                   `json:"buildingId"`
	BuildingName         string                   `json:"buildingName"`
	BuildingRevision     string                   `json:"buildingRevision"`
	Timezone             string                   `json:"timezone"`
	From                 time.Time                `json:"from"`
	To                   time.Time                `json:"to"`
	AsOf                 time.Time                `json:"asOf"`
	Energies             []LiveOverviewEnergyCard `json:"energies"`
	CarbonSummary        *CarbonSummary           `json:"carbonSummary,omitempty"`
	HourlyTrend          []LiveBucketPoint        `json:"hourlyTrend"`
	Exceptions           []ExceptionSummary       `json:"exceptions"`
}

// F02 能源分类构成 (distinct from F01 KPI summary and F06 single-energy deep audit)
type LiveCompositionResponse struct {
	DataMode             string                   `json:"dataMode"`
	DataOriginNotice     string                   `json:"dataOriginNotice"`
	ConfigurationState   string                   `json:"configurationState"`
	ConfigurationMessage string                   `json:"configurationMessage,omitempty"`
	BuildingID           string                   `json:"buildingId"`
	BuildingName         string                   `json:"buildingName"`
	BuildingRevision     string                   `json:"buildingRevision"`
	Timezone             string                   `json:"timezone"`
	From                 time.Time                `json:"from"`
	To                   time.Time                `json:"to"`
	AsOf                 time.Time                `json:"asOf"`
	Energies             []LiveOverviewEnergyCard `json:"energies"`
	CarbonSummary        *CarbonSummary           `json:"carbonSummary,omitempty"`
	CategoryBindingCount int                      `json:"categoryBindingCount"`
}

// F03 分区分项概览 (building + child space breakdown)
type LiveObjectsOverviewResponse struct {
	DataMode             string                    `json:"dataMode"`
	DataOriginNotice     string                    `json:"dataOriginNotice"`
	ConfigurationState   string                    `json:"configurationState"`
	ConfigurationMessage string                    `json:"configurationMessage,omitempty"`
	BuildingID           string                    `json:"buildingId"`
	BuildingName         string                    `json:"buildingName"`
	Energy               buildingenergy.EnergyType `json:"energy"`
	Unit                 string                    `json:"unit"`
	Timezone             string                    `json:"timezone"`
	From                 time.Time                 `json:"from"`
	To                   time.Time                 `json:"to"`
	AsOf                 time.Time                 `json:"asOf"`
	BuildingTotal        *float64                  `json:"buildingTotal"`
	SpaceRows            []LiveDimensionRow        `json:"spaceRows"`
	OrgRows              []LiveDimensionRow        `json:"orgRows"`
}

// F04 实时与历史趋势 (multi-day load series + telemetry freshness)
type LiveMeterFreshness struct {
	TerminalID   string                `json:"terminalId"`
	TerminalName string                `json:"terminalName"`
	ItemCode     string                `json:"itemCode"`
	SourceTable  pgadapter.SourceTable `json:"sourceTable"`
	Online       bool                  `json:"online"`
	HourRows     int64                 `json:"hourRows"`
	RawRows      int64                 `json:"rawRows"`
	LatestAt     *time.Time            `json:"latestAt,omitempty"`
}

type LiveRealtimeTrendResponse struct {
	DataMode             string                    `json:"dataMode"`
	DataOriginNotice     string                    `json:"dataOriginNotice"`
	ConfigurationState   string                    `json:"configurationState"`
	ConfigurationMessage string                    `json:"configurationMessage,omitempty"`
	BuildingID           string                    `json:"buildingId"`
	BuildingName         string                    `json:"buildingName"`
	Energy               buildingenergy.EnergyType `json:"energy"`
	Unit                 string                    `json:"unit"`
	Timezone             string                    `json:"timezone"`
	From                 time.Time                 `json:"from"`
	To                   time.Time                 `json:"to"`
	AsOf                 time.Time                 `json:"asOf"`
	TotalUsage           *float64                  `json:"totalUsage"`
	Coverage             LiveCoverage              `json:"coverage"`
	Series               []LiveBucketPoint         `json:"series"`
	Freshness            []LiveMeterFreshness      `json:"freshness"`
}

// F06 分类分项统计
type LiveClassificationResponse struct {
	DataMode                  string                    `json:"dataMode"`
	DataOriginNotice          string                    `json:"dataOriginNotice"`
	ConfigurationState        string                    `json:"configurationState"`
	ConfigurationMessage      string                    `json:"configurationMessage,omitempty"`
	BuildingID                string                    `json:"buildingId"`
	BuildingName              string                    `json:"buildingName"`
	BuildingRevision          string                    `json:"buildingRevision"`
	Energy                    buildingenergy.EnergyType `json:"energy"`
	Unit                      string                    `json:"unit"`
	Timezone                  string                    `json:"timezone"`
	From                      time.Time                 `json:"from"`
	To                        time.Time                 `json:"to"`
	AsOf                      time.Time                 `json:"asOf"`
	TotalUsage                *float64                  `json:"totalUsage"`
	TotalCoverage             LiveCoverage              `json:"totalCoverage"`
	UnclassifiedUsage         *float64                  `json:"unclassifiedUsage"`
	Carbon                    *CarbonEstimate           `json:"carbon,omitempty"`
	Categories                []LiveCategoryRow         `json:"categories"`
	Sources                   []LiveSourceTrace         `json:"sources"`
	RuleVersion               string                    `json:"ruleVersion"`
	ClassificationRuleVersion string                    `json:"classificationRuleVersion"`
}

type LiveDimensionRow struct {
	ID       string                     `json:"id"`
	Name     string                     `json:"name"`
	Usage    *float64                   `json:"usage"`
	Coverage LiveCoverage               `json:"coverage"`
	Status   buildingenergy.TrendStatus `json:"status"`
	Rank     *int                       `json:"rank"`
	Reason   string                     `json:"reason,omitempty"`
	Sources  []LiveSourceTrace          `json:"sources"`
}

// F07 多维统计 & F16 高耗能对象识别
type LiveDimensionsResponse struct {
	DataMode                  string                    `json:"dataMode"`
	DataOriginNotice          string                    `json:"dataOriginNotice"`
	ConfigurationState        string                    `json:"configurationState"`
	ConfigurationMessage      string                    `json:"configurationMessage,omitempty"`
	View                      buildingenergy.ObjectKind `json:"view"`
	Energy                    buildingenergy.EnergyType `json:"energy"`
	Unit                      string                    `json:"unit"`
	Timezone                  string                    `json:"timezone"`
	From                      time.Time                 `json:"from"`
	To                        time.Time                 `json:"to"`
	AsOf                      time.Time                 `json:"asOf"`
	RuleVersion               string                    `json:"ruleVersion"`
	ClassificationRuleVersion string                    `json:"classificationRuleVersion"`
	Rows                      []LiveDimensionRow        `json:"rows"`
}

type LiveTrendComparison struct {
	Status       buildingenergy.TrendStatus `json:"status"`
	BaselineFrom time.Time                  `json:"baselineFrom"`
	BaselineTo   time.Time                  `json:"baselineTo"`
	Baseline     *float64                   `json:"baseline"`
	Delta        *float64                   `json:"delta"`
	Rate         *float64                   `json:"rate"`
	Adjusted     bool                       `json:"adjusted"`
}

// F08 趋势、同环比与排名
type LiveTrendResponse struct {
	DataMode             string                     `json:"dataMode"`
	DataOriginNotice     string                     `json:"dataOriginNotice"`
	ConfigurationState   string                     `json:"configurationState"`
	ConfigurationMessage string                     `json:"configurationMessage,omitempty"`
	BuildingID           string                     `json:"buildingId"`
	BuildingName         string                     `json:"buildingName"`
	Energy               buildingenergy.EnergyType  `json:"energy"`
	Unit                 string                     `json:"unit"`
	Timezone             string                     `json:"timezone"`
	Granularity          buildingenergy.Granularity `json:"granularity"`
	From                 time.Time                  `json:"from"`
	To                   time.Time                  `json:"to"`
	AsOf                 time.Time                  `json:"asOf"`
	Current              *float64                   `json:"current"`
	Coverage             LiveCoverage               `json:"coverage"`
	Previous             LiveTrendComparison        `json:"previous"`
	PriorYear            LiveTrendComparison        `json:"priorYear"`
	Series               []LiveBucketPoint          `json:"series"`
	RuleVersion          string                     `json:"ruleVersion"`
}

// F09 能流与总分表平衡
type LiveBalanceResponse struct {
	DataMode           string                        `json:"dataMode"`
	DataOriginNotice   string                        `json:"dataOriginNotice"`
	BuildingID         string                        `json:"buildingId"`
	BuildingName       string                        `json:"buildingName"`
	Energy             buildingenergy.EnergyType     `json:"energy"`
	Timezone           string                        `json:"timezone"`
	From               time.Time                     `json:"from"`
	To                 time.Time                     `json:"to"`
	AsOf               time.Time                     `json:"asOf"`
	TopologyConfigured bool                          `json:"topologyConfigured"`
	TopologyVersion    string                        `json:"topologyVersion,omitempty"`
	Message            string                        `json:"message,omitempty"`
	Report             *buildingenergy.BalanceReport `json:"report,omitempty"`
}

// F11 / F12 / F13 Metrics & Benchmarks
type LiveEfficiencyMetric struct {
	ID                string                              `json:"id"`
	Name              string                              `json:"name"`
	TemplateVersion   string                              `json:"templateVersion"`
	Energy            buildingenergy.EnergyType           `json:"energy"`
	BuildingType      buildingenergy.BuildingType         `json:"buildingType"`
	NumeratorUsage    *float64                            `json:"numeratorUsage"`
	NumeratorUnit     string                              `json:"numeratorUnit"`
	NumeratorCoverage LiveCoverage                        `json:"numeratorCoverage"`
	DenominatorKind   buildingenergy.DenominatorKind      `json:"denominatorKind"`
	DenominatorValue  *float64                            `json:"denominatorValue"`
	DenominatorUnit   string                              `json:"denominatorUnit"`
	DenominatorBasis  string                              `json:"denominatorBasis,omitempty"`
	Value             *float64                            `json:"value"`
	Unit              string                              `json:"unit"`
	Status            buildingenergy.IndustryMetricStatus `json:"status"`
	Reason            string                              `json:"reason,omitempty"`
}

type LiveEfficiencyResponse struct {
	DataMode             string                      `json:"dataMode"`
	DataOriginNotice     string                      `json:"dataOriginNotice"`
	ConfigurationState   string                      `json:"configurationState"`
	ConfigurationMessage string                      `json:"configurationMessage,omitempty"`
	BuildingID           string                      `json:"buildingId"`
	BuildingName         string                      `json:"buildingName"`
	BuildingType         buildingenergy.BuildingType `json:"buildingType"`
	Region               string                      `json:"region,omitempty"`
	BuildingRevision     string                      `json:"buildingRevision"`
	Energy               buildingenergy.EnergyType   `json:"energy"`
	Timezone             string                      `json:"timezone"`
	From                 time.Time                   `json:"from"`
	To                   time.Time                   `json:"to"`
	AsOf                 time.Time                   `json:"asOf"`
	Metrics              []LiveEfficiencyMetric      `json:"metrics"`
	Sources              []LiveSourceTrace           `json:"sources"`
}

type LiveBenchmarkRow struct {
	Metric            LiveEfficiencyMetric           `json:"metric"`
	BenchmarkStatus   buildingenergy.BenchmarkStatus `json:"benchmarkStatus"`
	BenchmarkReason   string                         `json:"benchmarkReason"`
	BenchmarkDelta    *float64                       `json:"benchmarkDelta"`
	RuleID            string                         `json:"ruleId,omitempty"`
	RuleName          string                         `json:"ruleName,omitempty"`
	RuleVersion       string                         `json:"ruleVersion,omitempty"`
	RulePublisher     string                         `json:"rulePublisher,omitempty"`
	RuleKind          buildingenergy.BenchmarkKind   `json:"ruleKind,omitempty"`
	RuleRegion        string                         `json:"ruleRegion,omitempty"`
	RuleMaximum       *float64                       `json:"ruleMaximum,omitempty"`
	RuleNotice        string                         `json:"ruleNotice,omitempty"`
}

type LiveBenchmarksResponse struct {
	DataMode             string                      `json:"dataMode"`
	DataOriginNotice     string                      `json:"dataOriginNotice"`
	ConfigurationState   string                      `json:"configurationState"`
	ConfigurationMessage string                      `json:"configurationMessage,omitempty"`
	BuildingID           string                      `json:"buildingId"`
	BuildingName         string                      `json:"buildingName"`
	BuildingType         buildingenergy.BuildingType `json:"buildingType"`
	Region               string                      `json:"region,omitempty"`
	Energy               buildingenergy.EnergyType   `json:"energy"`
	Timezone             string                      `json:"timezone"`
	From                 time.Time                   `json:"from"`
	To                   time.Time                   `json:"to"`
	AsOf                 time.Time                   `json:"asOf"`
	Rows                 []LiveBenchmarkRow          `json:"rows"`
	RuleVersion          string                      `json:"ruleVersion"`
}

// F05 年度目标摘要 & F14 目标与分期分解
type LiveTargetPeriodRow struct {
	Month       int                         `json:"month"` // 0 = annual, 1..12 = calendar month
	Label       string                      `json:"label"`
	From        time.Time                   `json:"from"`
	To          time.Time                   `json:"to"`
	Target      *float64                    `json:"target"`
	Usage       *float64                    `json:"usage"`
	Delta       *float64                    `json:"delta"`
	Ratio       *float64                    `json:"ratio"`
	Coverage    LiveCoverage                `json:"coverage"`
	Status      buildingenergy.TargetStatus `json:"status"`
}

type LiveTargetResponse struct {
	DataMode             string                      `json:"dataMode"`
	DataOriginNotice     string                      `json:"dataOriginNotice"`
	BuildingID           string                      `json:"buildingId"`
	BuildingName         string                      `json:"buildingName"`
	Energy               buildingenergy.EnergyType   `json:"energy"`
	Unit                 string                      `json:"unit"`
	Year                 int                         `json:"year"`
	Timezone             string                      `json:"timezone"`
	TargetConfigured     bool                        `json:"targetConfigured"`
	TargetVersion        string                      `json:"targetVersion,omitempty"`
	TargetMethod         buildingenergy.TargetMethod `json:"targetMethod,omitempty"`
	RuleVersion          string                      `json:"ruleVersion"`
	Message              string                      `json:"message,omitempty"`
	Annual               LiveTargetPeriodRow         `json:"annual"`
	Months               []LiveTargetPeriodRow       `json:"months"`
}

// F10 异常识别 & F17 节能机会分析
type LiveAnomalyResponse struct {
	DataMode             string                       `json:"dataMode"`
	DataOriginNotice     string                       `json:"dataOriginNotice"`
	BuildingID           string                       `json:"buildingId"`
	BuildingName         string                       `json:"buildingName"`
	Energy               buildingenergy.EnergyType    `json:"energy"`
	Unit                 string                       `json:"unit"`
	Timezone             string                       `json:"timezone"`
	From                 time.Time                    `json:"from"`
	To                   time.Time                    `json:"to"`
	AsOf                 time.Time                    `json:"asOf"`
	RuleConfigured       bool                         `json:"ruleConfigured"`
	RuleVersion          string                       `json:"ruleVersion"`
	ActiveRuleRevision   string                       `json:"activeRuleRevision,omitempty"`
	Status               buildingenergy.AnomalyStatus `json:"status"`
	Reason               buildingenergy.AnomalyReason `json:"reason,omitempty"`
	Current              *float64                     `json:"current"`
	Coverage             LiveCoverage                 `json:"coverage"`
	BaselineCount        int                          `json:"baselineCount"`
	Baseline             *float64                     `json:"baseline"`
	Delta                *float64                     `json:"delta"`
	ChangeRate           *float64                     `json:"changeRate"`
	HistoryDays          []LiveBucketPoint            `json:"historyDays"`
	Exceptions           []ExceptionSummary           `json:"exceptions"`
}

type LiveOpportunityClue struct {
	Kind            buildingenergy.OpportunityKind   `json:"kind"`
	Status          buildingenergy.OpportunityStatus `json:"status"`
	ObjectID        string                           `json:"objectId"`
	ObjectName      string                           `json:"objectName"`
	Usage           *float64                         `json:"usage"`
	Baseline        *float64                         `json:"baseline"`
	ComparablePeers int                              `json:"comparablePeers"`
	Evidence        string                           `json:"evidence"`
	Limitations     []string                         `json:"limitations"`
	Confirm         []string                         `json:"confirm"`
}

type LiveOpportunitiesResponse struct {
	DataMode         string                      `json:"dataMode"`
	DataOriginNotice string                      `json:"dataOriginNotice"`
	BuildingID       string                      `json:"buildingId"`
	BuildingName     string                      `json:"buildingName"`
	Energy           buildingenergy.EnergyType   `json:"energy"`
	Unit             string                      `json:"unit"`
	Timezone         string                      `json:"timezone"`
	From             time.Time                   `json:"from"`
	To               time.Time                   `json:"to"`
	AsOf             time.Time                   `json:"asOf"`
	Anomaly          LiveAnomalyResponse         `json:"anomaly"`
	Clues            []LiveOpportunityClue       `json:"clues"`
	RuleVersion      string                      `json:"ruleVersion"`
}

// F21 / F22 / F25 Reports (identical structure drives preview table AND CSV download)
type LiveReportColumn struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

type LiveReportResponse struct {
	ReportCode       string              `json:"reportCode"` // F21 | F22 | F23 | F24 | F25
	ReportTitle      string              `json:"reportTitle"`
	DataMode         string              `json:"dataMode"`
	DataOriginNotice string              `json:"dataOriginNotice"`
	Dataset          *DatasetMeta        `json:"dataset,omitempty"`
	BuildingID       string              `json:"buildingId"`
	BuildingName     string              `json:"buildingName"`
	Energy           string              `json:"energy"`
	Timezone         string              `json:"timezone"`
	From             time.Time           `json:"from"`
	To               time.Time           `json:"to"`
	AsOf             time.Time           `json:"asOf"`
	GeneratedAt      time.Time           `json:"generatedAt"`
	AllowedFields    []LiveReportColumn  `json:"allowedFields,omitempty"`
	SelectedFields   []string            `json:"selectedFields,omitempty"`
	Columns          []LiveReportColumn  `json:"columns"`
	Rows             []map[string]string `json:"rows"`
	Disclaimer       string              `json:"disclaimer,omitempty"`
}

// ── F15 Organization & Space Assessment (PRD §6.3) ──────────────────────────

type AssessmentRuleRecord struct {
	RuleID          string                    `json:"ruleId"`
	Revision        string                    `json:"revision"`
	EffectiveFrom   time.Time                 `json:"effectiveFrom"`
	EffectiveTo     *time.Time                `json:"effectiveTo,omitempty"`
	ObjectKind      buildingenergy.ObjectKind `json:"objectKind"`
	ObjectID        string                    `json:"objectId"`
	Energy          buildingenergy.EnergyType `json:"energy"`
	MetricLabel     string                    `json:"metricLabel"`
	TargetValue     float64                   `json:"targetValue"`
	Unit            string                    `json:"unit"`
	WeightPct       float64                   `json:"weightPct"`
	PublicSharePct  float64                   `json:"publicSharePct"`
	Exempt          bool                      `json:"exempt"`
	ExemptReason    string                    `json:"exemptReason,omitempty"`
	ScoringFormula  string                    `json:"scoringFormula"`
	MinCoverageRate float64                   `json:"minCoverageRate"`
	ReviewStatus    string                    `json:"reviewStatus"`
	ReviewNote      string                    `json:"reviewNote,omitempty"`
}

type LiveAssessmentRow struct {
	RuleID         string                    `json:"ruleId"`
	ObjectKind     buildingenergy.ObjectKind `json:"objectKind"`
	ObjectID       string                    `json:"objectId"`
	ObjectName     string                    `json:"objectName"`
	Energy         buildingenergy.EnergyType `json:"energy"`
	MetricLabel    string                    `json:"metricLabel"`
	TargetValue    float64                   `json:"targetValue"`
	ActualUsage    *float64                  `json:"actualUsage"`
	Unit           string                    `json:"unit"`
	Deviation      *float64                  `json:"deviation"`
	DeviationRate  *float64                  `json:"deviationRate"`
	WeightPct      float64                   `json:"weightPct"`
	PublicSharePct float64                   `json:"publicSharePct"`
	Exempt         bool                      `json:"exempt"`
	ExemptReason   string                    `json:"exemptReason,omitempty"`
	Coverage       LiveCoverage              `json:"coverage"`
	Score          *float64                  `json:"score"`
	WeightedScore  *float64                  `json:"weightedScore,omitempty"`
	FormulaDetail  string                    `json:"formulaDetail,omitempty"`
	Confirmable    bool                      `json:"confirmable"`
	ReviewStatus   string                    `json:"reviewStatus"`
	ReviewNote     string                    `json:"reviewNote,omitempty"`
	ScoringFormula string                    `json:"scoringFormula"`
}

type LiveAssessmentResponse struct {
	DataMode           string              `json:"dataMode"`
	DataOriginNotice   string              `json:"dataOriginNotice"`
	Dataset            *DatasetMeta        `json:"dataset,omitempty"`
	Timezone           string              `json:"timezone"`
	From               time.Time           `json:"from"`
	To                 time.Time           `json:"to"`
	AsOf               time.Time           `json:"asOf"`
	TotalWeightPct     float64             `json:"totalWeightPct"`
	WeightedTotalScore *float64            `json:"weightedTotalScore,omitempty"`
	SummaryStatus      string              `json:"summaryStatus"` // complete | partial_blocked | empty
	Rows               []LiveAssessmentRow `json:"rows"`
	Disclaimer         string              `json:"disclaimer"`
}

type ReviewAssessmentRequest struct {
	RuleID       string `json:"ruleId"`
	ReviewStatus string `json:"reviewStatus"` // confirmed | pending_review | appealed
	ReviewNote   string `json:"reviewNote"`
}

type UpsertAssessmentRuleRequest struct {
	RuleID          string                    `json:"ruleId"`
	ObjectKind      buildingenergy.ObjectKind `json:"objectKind"`
	ObjectID        string                    `json:"objectId"`
	Energy          buildingenergy.EnergyType `json:"energy"`
	MetricLabel     string                    `json:"metricLabel"`
	TargetValue     float64                   `json:"targetValue"`
	Unit            string                    `json:"unit"`
	WeightPct       float64                   `json:"weightPct"`
	PublicSharePct  float64                   `json:"publicSharePct"`
	Exempt          bool                      `json:"exempt"`
	ExemptReason    string                    `json:"exemptReason"`
	ScoringFormula  string                    `json:"scoringFormula"`
	MinCoverageRate float64                   `json:"minCoverageRate"`
}

// ── F18 / F19 / F20 / F24 Retrofit & M&V (PRD §7.2-§7.3) ───────────────────

type RetrofitBaselineRevision struct {
	BaselineVersion     string    `json:"baselineVersion"`
	BuildingID          string    `json:"buildingId"`
	SpaceID             string    `json:"spaceId,omitempty"`
	Energy              string    `json:"energy"`
	BaselineFrom        time.Time `json:"baselineFrom"`
	BaselineTo          time.Time `json:"baselineTo"`
	BaselineModel       string    `json:"baselineModel"`
	BaselineFactors     string    `json:"baselineFactors"`
	BaselineR2          *float64  `json:"baselineR2,omitempty"`
	BaselineCoverage    float64   `json:"baselineCoverage"`
	BaselineLocked      bool      `json:"baselineLocked"`
	BaselineDailyAvg    float64   `json:"baselineDailyAvg"`
	BaselineActualUsage *float64  `json:"baselineActualUsage,omitempty"`
	ChangeReason        string    `json:"changeReason"`
	ActorSub            string    `json:"actorSub"`
	RecordedAt          time.Time `json:"recordedAt"`
}

type RetrofitVerificationRecord struct {
	ID                     int64     `json:"id"`
	BaselineVersion        string    `json:"baselineVersion"`
	ReportFrom             time.Time `json:"reportFrom"`
	ReportTo               time.Time `json:"reportTo"`
	AdjustedBaselineUse    *float64  `json:"adjustedBaselineUse,omitempty"`
	ReportActualUsage      *float64  `json:"reportActualUsage,omitempty"`
	ReportCoverageRate     *float64  `json:"reportCoverageRate,omitempty"`
	NonRoutineAdjustment   float64   `json:"nonRoutineAdjustment"`
	VerifiedSavings        *float64  `json:"verifiedSavings,omitempty"`
	VerifiedSavingsRate    *float64  `json:"verifiedSavingsRate,omitempty"`
	VerifiedCarbonKgCO2e   *float64  `json:"verifiedCarbonKgCO2e,omitempty"`
	WeatherDataComplete    bool      `json:"weatherDataComplete"`
	MVStatus               string    `json:"mvStatus"`
	MVNote                 string    `json:"mvNote"`
	ActorSub               string    `json:"actorSub"`
	RecordedAt             time.Time `json:"recordedAt"`
}

type RetrofitProjectRecord struct {
	RetrofitID                   string                       `json:"retrofitId"`
	BuildingID                   string                       `json:"buildingId"`
	BuildingName                 string                       `json:"buildingName"`
	SpaceID                      string                       `json:"spaceId,omitempty"`
	SpaceName                    string                       `json:"spaceName,omitempty"`
	Energy                       buildingenergy.EnergyType    `json:"energy"`
	Name                         string                       `json:"name"`
	MeasureSummary               string                       `json:"measureSummary"`
	Unit                         string                       `json:"unit"`
	BaselineVersion              string                       `json:"baselineVersion"`
	BaselineFrom                 time.Time                    `json:"baselineFrom"`
	BaselineTo                   time.Time                    `json:"baselineTo"`
	BaselineModel                string                       `json:"baselineModel"`
	BaselineFactors              string                       `json:"baselineFactors"`
	BaselineR2                   *float64                     `json:"baselineR2,omitempty"`
	BaselineCoverage             float64                      `json:"baselineCoverage"`
	BaselineActualCoverage       LiveCoverage                 `json:"baselineActualCoverage"`
	BaselineLocked               bool                         `json:"baselineLocked"`
	BaselineDailyAvg             float64                      `json:"baselineDailyAvg"`
	BaselineActualUsage          *float64                     `json:"baselineActualUsage,omitempty"`
	AnnualizedBaselineUsage      *float64                     `json:"annualizedBaselineUsage,omitempty"`
	EstSavingsLow                float64                      `json:"estSavingsLow"`
	EstSavingsBase               float64                      `json:"estSavingsBase"`
	EstSavingsHigh               float64                      `json:"estSavingsHigh"`
	EstSavingsRateLow            *float64                     `json:"estSavingsRateLow,omitempty"`
	EstSavingsRateBase           *float64                     `json:"estSavingsRateBase,omitempty"`
	EstSavingsRateHigh           *float64                     `json:"estSavingsRateHigh,omitempty"`
	RefUnitPrice                 float64                      `json:"refUnitPrice"`
	PriceConfigured              bool                         `json:"priceConfigured"`
	EstAnnualCostLow             *float64                     `json:"estAnnualCostLow,omitempty"`
	EstAnnualCostBase            *float64                     `json:"estAnnualCostBase,omitempty"`
	EstAnnualCostHigh            *float64                     `json:"estAnnualCostHigh,omitempty"`
	CapexBudget                  float64                      `json:"capexBudget"`
	StaticPaybackLowYears        *float64                     `json:"staticPaybackLowYears,omitempty"`
	StaticPaybackYears           *float64                     `json:"staticPaybackYears,omitempty"`
	StaticPaybackHighYears       *float64                     `json:"staticPaybackHighYears,omitempty"`
	ReportFrom                   time.Time                    `json:"reportFrom"`
	ReportTo                     time.Time                    `json:"reportTo"`
	AdjustedBaselineUse          *float64                     `json:"adjustedBaselineUse,omitempty"`
	ReportActualUsage            *float64                     `json:"reportActualUsage,omitempty"`
	ReportActualCoverage         LiveCoverage                 `json:"reportActualCoverage"`
	MinReportCoverageRate        float64                      `json:"minReportCoverageRate"`
	NonRoutineAdjustment         float64                      `json:"nonRoutineAdjustment"`
	VerifiedSavings              *float64                     `json:"verifiedSavings,omitempty"`
	VerifiedSavingsRate          *float64                     `json:"verifiedSavingsRate,omitempty"`
	VerifiedCostSavings          *float64                     `json:"verifiedCostSavings,omitempty"`
	VerifiedCarbonReductionKgCO2 *float64                     `json:"verifiedCarbonReductionKgCO2,omitempty"`
	WeatherDataComplete          bool                         `json:"weatherDataComplete"`
	MVStandardRef                string                       `json:"mvStandardRef"`
	MVStatus                     string                       `json:"mvStatus"`
	MVNote                       string                       `json:"mvNote"`
	BaselineHistory              []RetrofitBaselineRevision   `json:"baselineHistory,omitempty"`
	VerificationLogs             []RetrofitVerificationRecord `json:"verificationLogs,omitempty"`
}

type CreateRetrofitRequest struct {
	RetrofitID           string                    `json:"retrofitId"`
	BuildingID           string                    `json:"buildingId"`
	SpaceID              string                    `json:"spaceId"`
	Energy               buildingenergy.EnergyType `json:"energy"`
	Name                 string                    `json:"name"`
	MeasureSummary       string                    `json:"measureSummary"`
	BaselineVersion      string                    `json:"baselineVersion"`
	BaselineFrom         time.Time                 `json:"baselineFrom"`
	BaselineTo           time.Time                 `json:"baselineTo"`
	BaselineModel        string                    `json:"baselineModel"`
	BaselineFactors      string                    `json:"baselineFactors"`
	BaselineR2           *float64                  `json:"baselineR2,omitempty"`
	EstSavingsLow        float64                   `json:"estSavingsLow"`
	EstSavingsBase       float64                   `json:"estSavingsBase"`
	EstSavingsHigh       float64                   `json:"estSavingsHigh"`
	RefUnitPrice         float64                   `json:"refUnitPrice"`
	CapexBudget          float64                   `json:"capexBudget"`
	ReportFrom           *time.Time                `json:"reportFrom,omitempty"`
	ReportTo             *time.Time                `json:"reportTo,omitempty"`
	AdjustedBaselineUse  *float64                  `json:"adjustedBaselineUse,omitempty"`
	NonRoutineAdjustment float64                   `json:"nonRoutineAdjustment"`
	WeatherDataComplete  bool                      `json:"weatherDataComplete"`
	MVNote               string                    `json:"mvNote"`
}

type UpdateRetrofitRequest struct {
	RetrofitID               string                    `json:"retrofitId"`
	BuildingID               string                    `json:"buildingId"`
	SpaceID                  string                    `json:"spaceId"`
	Energy                   buildingenergy.EnergyType `json:"energy"`
	Name                     string                    `json:"name"`
	MeasureSummary           string                    `json:"measureSummary"`
	BaselineFrom             *time.Time                `json:"baselineFrom,omitempty"`
	BaselineTo               *time.Time                `json:"baselineTo,omitempty"`
	BaselineModel            string                    `json:"baselineModel"`
	BaselineFactors          string                    `json:"baselineFactors"`
	BaselineR2               *float64                  `json:"baselineR2,omitempty"`
	CreateNewBaselineVersion bool                      `json:"createNewBaselineVersion"`
	NewBaselineVersion       string                    `json:"newBaselineVersion"`
	ChangeReason             string                    `json:"changeReason"`
	EstSavingsLow            *float64                  `json:"estSavingsLow,omitempty"`
	EstSavingsBase           *float64                  `json:"estSavingsBase,omitempty"`
	EstSavingsHigh           *float64                  `json:"estSavingsHigh,omitempty"`
	RefUnitPrice             *float64                  `json:"refUnitPrice,omitempty"`
	CapexBudget              *float64                  `json:"capexBudget,omitempty"`
}

type LockRetrofitBaselineRequest struct {
	RetrofitID   string `json:"retrofitId"`
	ChangeReason string `json:"changeReason"`
}

type VerifyRetrofitRequest struct {
	RetrofitID           string     `json:"retrofitId"`
	ReportFrom           time.Time  `json:"reportFrom"`
	ReportTo             time.Time  `json:"reportTo"`
	AdjustedBaselineUse  *float64   `json:"adjustedBaselineUse,omitempty"`
	NonRoutineAdjustment float64    `json:"nonRoutineAdjustment"`
	WeatherDataComplete  bool       `json:"weatherDataComplete"`
	MarkVerified         bool       `json:"markVerified"`
	MVNote               string     `json:"mvNote"`
}

type LiveRetrofitResponse struct {
	Section          string                  `json:"section"` // F18 | F19 | F20
	DataMode         string                  `json:"dataMode"`
	DataOriginNotice string                  `json:"dataOriginNotice"`
	Dataset          *DatasetMeta            `json:"dataset,omitempty"`
	Projects         []RetrofitProjectRecord `json:"projects"`
	Disclaimer       string                  `json:"disclaimer"`
}
