import { useCallback, useEffect, useMemo, useState } from 'react';
import { createPortal } from 'react-dom';
import { useLocation, useNavigate, useSearchParams } from 'react-router-dom';
import { Activity, AlertTriangle, ArrowDownRight, ArrowRight, ArrowUpRight, CheckCircle2, KeyRound, Maximize2, Radio, RefreshCw, RotateCcw, Settings2, ShieldAlert, X, ZoomIn, ZoomOut } from 'lucide-react';
import { currentEnergyItem } from '../components/BuildingEnergyNavigation';
import { EnergyManagementDrawer } from '../components/EnergyManagementDrawer';
import { EnergyReportStudio } from '../components/EnergyReportStudio';
import { EnergySankeyChart, EnergyTreemapChart, type SankeyMeterNodeMeta, type TreemapCategoryNode } from '../components/EnergyECharts';
import { RetrofitWorkspace, type RetrofitData } from '../components/RetrofitWorkspace';
import {
  AnomalyDistributionBarChart,
  AssessmentScoreChart,
  BenchmarkBulletChart,
  CHART_PALETTE,
  CumulativeTargetBurnupChart,
  DeviationDivergingChart,
  DiagnosticQuadrantChart,
  DiscreteSampleCompareChart,
  EnergyStructureChart,
  GroupedComparisonBarChart,
  HorizontalRankingChart,
  PeriodVarianceBarChart,
  ScoreDistributionChart,
  ScoreGaugeChart,
  SpatialHierarchyMap,
  StackedCategoryTrendChart,
  TargetComparisonChart,
  TrendAreaChart,
} from '../components/EnergyCharts';
import {
  ChineseDateInput,
  CollapsibleSection,
  DatasetInfoButton,
  fmtShanghaiDate,
  MobileFilterDrawer,
} from '../components/EnergyWorkspaceChrome';
import {
  LIVE_TOKEN_KEY,
  liveGet,
  livePost,
  type BucketPoint,
  type CategoryRow,
  type Coverage,
  type ExceptionRow,
  type InventoryData,
  type LiveReportData,
  type SourceTrace,
} from '../api/energyLive';

export type LiveSection =
  | 'overview'              // F01 综合能耗概览
  | 'composition'           // F02 能源分类构成
  | 'objects'               // F03 分区分项概览
  | 'realtime'              // F04 实时与历史趋势
  | 'target-summary'        // F05 年度目标摘要
  | 'classification'        // F06 分类分项统计
  | 'dimensions'            // F07 多维统计
  | 'trend'                 // F08 趋势、同环比与排名
  | 'balance'               // F09 能流与总分表平衡
  | 'anomalies'             // F10 异常浪费识别
  | 'efficiency'            // F11 通用能效强度
  | 'industry-templates'    // F12 行业指标模板
  | 'industry-benchmarks'   // F13 定额标准对标
  | 'target-periods'        // F14 目标与分期分解
  | 'assessment'            // F15 组织与区域考核
  | 'diagnosis'             // F16 高耗能对象识别
  | 'opportunities'         // F17 节能机会分析
  | 'retrofit-baseline'     // F18 改造前基线
  | 'retrofit-potential'    // F19 潜力与收益测算
  | 'retrofit-verification' // F20 改造后节能核验
  | 'report-consumption'    // F21 综合与分类分项报表
  | 'report-metrics'        // F22 指标与定额报表
  | 'report-assessment'     // F23 组织考核报表
  | 'report-retrofit'       // F24 诊断与改造成效报告
  | 'report-custom';        // F25 行业专项与自定义报表

const numFmt = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 3 });
const pctFmt = new Intl.NumberFormat('zh-CN', { style: 'percent', maximumFractionDigits: 1 });

function fmtNum(v: number | null | undefined) {
  return v == null ? '—' : numFmt.format(v);
}

function fmtPct(v: number | null | undefined) {
  return v == null ? '—' : pctFmt.format(v);
}

const energyLabels: Record<string, string> = {
  electricity: '用电',
  water: '用水',
  gas: '燃气',
  heat: '供热',
  cooling: '供冷',
  steam: '蒸汽',
};

const statusLabels: Record<string, string> = {
  comparable: '同口径可比',
  unequal_periods: '时段不等',
  missing_data: '缺少历史数据',
  insufficient_quality: '覆盖率或质量不足',
  inconsistent_scope: '统计口径变化',
  zero_baseline: '基期为零',
  ready: '可计算',
  unconfigured: '未配置',
  missing_denominator: '缺少业务分母',
  invalid_denominator: '分母无效',
  denominator_out_of_period: '分母区间不匹配',
  missing_usage: '缺少计量数据',
  no_standard: '未配置定额标准',
  inapplicable: '标准不适用',
  not_comparable: '不可对标',
  insufficient_data: '数据不足',
  within_threshold: '低于阈值上限',
  exceeds_threshold: '高于阈值上限',
  usage_candidate: '用能异常候选',
  metering_anomaly: '计量质量异常',
  insufficient_evidence: '证据不足',
  no_candidate: '未触发异常候选',
  missing_rule: '尚未配置异常规则',
  insufficient_history: '同星期历史完整日不足',
  incomplete_period: '周期尚未完结',
  partial_coverage: '覆盖率不足',
  outside_effective_period: '不在目标有效期内',
  pending_manual_review: '待人工核查',
  confirmed: '已确认',
  draft: '草稿待确认',
  pending_review: '待复核确认',
  appealed: '复核申诉中',
  blocked_low_quality: '低质量阻断',
  verified_simulated: '模拟核验完成',
  pending_verification: '待核验',
  estimated_only: '仅模型估算',
};

// ── Section Payload Types ───────────────────────────────────────────────────

type CarbonEstimate = {
  kgCO2e: number | null;
  factor: number;
  factorUnit: string;
  factorVersion: string;
  includedInTotal: boolean;
  status: string;
  note: string;
};

type CarbonSummary = {
  totalKgCO2e: number | null;
  factorVersion: string;
  status: string;
  includedEnergies: string[];
  excludedEnergies: string[];
  missingEnergies?: string[];
  disclaimer: string;
};

type EnergyCard = {
  energy: string;
  unit: string;
  usage: number | null;
  unclassifiedUsage?: number | null;
  coverage: Coverage;
  carbon?: CarbonEstimate;
  meterCount: number;
  categories: CategoryRow[];
  sources: SourceTrace[];
};

type OverviewData = {
  dataOriginNotice: string;
  configurationState: string;
  configurationMessage?: string;
  buildingId: string;
  buildingName: string;
  buildingRevision: string;
  timezone: string;
  from: string;
  to: string;
  asOf: string;
  energies: EnergyCard[];
  carbonSummary?: CarbonSummary;
  hourlyTrend: BucketPoint[];
  exceptions: ExceptionRow[];
};

type CompositionData = {
  dataOriginNotice: string;
  configurationState: string;
  configurationMessage?: string;
  buildingId: string;
  buildingName: string;
  buildingRevision: string;
  timezone: string;
  from: string;
  to: string;
  asOf: string;
  energies: EnergyCard[];
  carbonSummary?: CarbonSummary;
  categoryBindingCount: number;
};

type DimensionRow = {
  id: string;
  name: string;
  usage: number | null;
  coverage: Coverage;
  status: string;
  rank: number | null;
  reason?: string;
  sources: SourceTrace[];
};

type ObjectsOverviewData = {
  dataOriginNotice: string;
  configurationState: string;
  configurationMessage?: string;
  buildingId: string;
  buildingName: string;
  energy: string;
  unit: string;
  timezone: string;
  from: string;
  to: string;
  buildingTotal: number | null;
  spaceRows: DimensionRow[];
  orgRows: DimensionRow[];
};

type RealtimeTrendData = {
  dataOriginNotice: string;
  configurationState: string;
  configurationMessage?: string;
  buildingId: string;
  buildingName: string;
  energy: string;
  unit: string;
  timezone: string;
  from: string;
  to: string;
  asOf: string;
  totalUsage: number | null;
  coverage: Coverage;
  series: BucketPoint[];
  freshness: Array<{
    terminalId: string;
    terminalName: string;
    itemCode: string;
    sourceTable: string;
    online: boolean;
    hourRows: number;
    rawRows: number;
    latestAt?: string;
  }>;
};

type ClassificationData = {
  dataOriginNotice: string;
  configurationState: string;
  configurationMessage?: string;
  buildingId: string;
  buildingName: string;
  buildingRevision: string;
  energy: string;
  unit: string;
  timezone: string;
  from: string;
  to: string;
  asOf: string;
  totalUsage: number | null;
  totalCoverage: Coverage;
  unclassifiedUsage: number | null;
  carbon?: CarbonEstimate;
  categories: CategoryRow[];
  sources: SourceTrace[];
  ruleVersion: string;
  classificationRuleVersion: string;
};

type DimensionsData = {
  dataOriginNotice: string;
  configurationState: string;
  configurationMessage?: string;
  view: string;
  energy: string;
  unit: string;
  timezone: string;
  from: string;
  to: string;
  rows: DimensionRow[];
};

type TrendData = {
  dataOriginNotice: string;
  configurationState: string;
  configurationMessage?: string;
  buildingId: string;
  buildingName: string;
  energy: string;
  unit: string;
  timezone: string;
  from: string;
  to: string;
  asOf: string;
  current: number | null;
  coverage: Coverage;
  previous: { status: string; baselineFrom: string; baselineTo: string; baseline: number | null; delta: number | null; rate: number | null };
  priorYear: { status: string; baselineFrom: string; baselineTo: string; baseline: number | null; delta: number | null; rate: number | null };
  series: BucketPoint[];
  ruleVersion?: string;
};

type BalanceMeterResult = {
  Meter: {
    Name: string;
    Point: { Key: { TerminalID: string; ItemCode: string } };
    Zones?: string[];
  };
  Usage: number | null;
  Status: string;
};

type BalanceData = {
  dataOriginNotice: string;
  buildingId: string;
  buildingName: string;
  energy: string;
  timezone: string;
  from: string;
  to: string;
  topologyConfigured: boolean;
  topologyVersion?: string;
  message?: string;
  report?: {
    Unit: string;
    Status: string;
    Parent?: BalanceMeterResult;
    Children?: BalanceMeterResult[];
    ChildrenUsage: number | null;
    Difference: number | null;
    DifferenceRate: number | null;
  };
};

type EfficiencyMetric = {
  id: string;
  name: string;
  templateVersion: string;
  buildingType: string;
  numeratorUsage: number | null;
  numeratorUnit: string;
  numeratorCoverage: Coverage;
  denominatorKind: string;
  denominatorValue: number | null;
  denominatorUnit: string;
  denominatorBasis?: string;
  value: number | null;
  unit: string;
  status: string;
  reason?: string;
};

type EfficiencyData = {
  dataOriginNotice: string;
  configurationState: string;
  configurationMessage?: string;
  buildingId: string;
  buildingName: string;
  buildingType: string;
  region?: string;
  buildingRevision: string;
  energy: string;
  timezone: string;
  from: string;
  to: string;
  metrics: EfficiencyMetric[];
  sources: SourceTrace[];
};

type BenchmarksData = {
  dataOriginNotice: string;
  configurationState: string;
  configurationMessage?: string;
  buildingId: string;
  buildingName: string;
  buildingType: string;
  region?: string;
  energy: string;
  rows: Array<{
    metric: EfficiencyMetric;
    benchmarkStatus: string;
    benchmarkReason: string;
    benchmarkDelta: number | null;
    ruleId?: string;
    ruleName?: string;
    ruleKind?: string;
    ruleMaximum?: number;
    ruleNotice?: string;
  }>;
};

type TargetPeriodRow = {
  month: number;
  label: string;
  from: string;
  to: string;
  target: number | null;
  usage: number | null;
  delta: number | null;
  ratio: number | null;
  coverage: Coverage;
  status: string;
};

type TargetsData = {
  dataOriginNotice: string;
  buildingId: string;
  buildingName: string;
  energy: string;
  unit: string;
  year: number;
  timezone?: string;
  targetConfigured: boolean;
  targetVersion?: string;
  targetMethod?: string;
  ruleVersion?: string;
  message?: string;
  annual: TargetPeriodRow;
  months: TargetPeriodRow[];
};

type AnomalyData = {
  dataOriginNotice: string;
  buildingId: string;
  buildingName: string;
  energy: string;
  unit: string;
  timezone?: string;
  from: string;
  to: string;
  ruleConfigured: boolean;
  ruleVersion?: string;
  activeRuleRevision?: string;
  status: string;
  reason?: string;
  current: number | null;
  coverage: Coverage;
  baselineCount: number;
  baseline: number | null;
  delta: number | null;
  changeRate: number | null;
  historyDays: BucketPoint[];
  exceptions: ExceptionRow[];
};

type OpportunitiesData = {
  dataOriginNotice: string;
  buildingId: string;
  buildingName: string;
  energy: string;
  unit: string;
  anomaly: AnomalyData;
  clues: Array<{
    kind: string;
    status: string;
    objectId: string;
    objectName: string;
    usage: number | null;
    baseline: number | null;
    comparablePeers: number;
    evidence: string;
    limitations: string[];
    confirm: string[];
  }>;
};

type AssessmentRow = {
  ruleId: string;
  objectKind: string;
  objectId: string;
  objectName: string;
  energy: string;
  metricLabel: string;
  targetValue: number;
  actualUsage: number | null;
  unit: string;
  deviation: number | null;
  deviationRate: number | null;
  weightPct: number;
  publicSharePct: number;
  exempt: boolean;
  exemptReason?: string;
  coverage: Coverage;
  score: number | null;
  weightedScore?: number | null;
  formulaDetail?: string;
  confirmable: boolean;
  reviewStatus: string;
  reviewNote?: string;
  scoringFormula: string;
};

type AssessmentData = {
  dataOriginNotice: string;
  timezone: string;
  from: string;
  to: string;
  asOf: string;
  totalWeightPct?: number;
  weightedTotalScore?: number | null;
  summaryStatus?: string;
  rows: AssessmentRow[];
  disclaimer: string;
};

export default function BuildingEnergyLivePage({ section = 'overview' }: { section?: LiveSection }) {
  const location = useLocation();
  const navigate = useNavigate();
  const item = currentEnergyItem(location.pathname);
  const [params, setParams] = useSearchParams();

  const [token, setToken] = useState<string>(() => sessionStorage.getItem(LIVE_TOKEN_KEY) ?? '');
  const [tokenDraft, setTokenDraft] = useState<string>(token);
  const [devAuthTried, setDevAuthTried] = useState<boolean>(() => Boolean(sessionStorage.getItem(LIVE_TOKEN_KEY)));
  const [isLanReadOnly, setIsLanReadOnly] = useState<boolean>(false);
  const [lanAllowedSubnet, setLanAllowedSubnet] = useState<string>('');
  const [inventory, setInventory] = useState<InventoryData | null>(null);
  const [payload, setPayload] = useState<{ section: LiveSection; data: unknown } | null>(null);
  const [loading, setLoading] = useState<boolean>(false);
  const [authError, setAuthError] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [drawerOpen, setDrawerOpen] = useState<boolean>(false);
  const [refreshTick, setRefreshTick] = useState<number>(0);

  const rawBuilding = params.get('building') ?? '';
  const isLegacySyntheticBuilding =
    rawBuilding === 'office-a' ||
    rawBuilding === 'office-b' ||
    rawBuilding === 'hospital-a' ||
    rawBuilding === 'school-a' ||
    rawBuilding === 'hotel-a' ||
    rawBuilding === 'all';
  const building = isLegacySyntheticBuilding ? '' : rawBuilding;
  const energy = params.get('energy') ?? 'electricity';
  const date = params.get('date') ?? '';
  const year = params.get('year') ?? '2026';
  const days = params.get('days') ?? '7';
  const view = params.get('view') ?? (section === 'diagnosis' ? 'space' : 'building');
  const scope = params.get('scope') ?? 'metrics';
  const fieldsParam = params.get('fields') ?? '';

  // Auto-heal stale/unknown building query params (e.g. legacy "office-a" bookmarks) once inventory loads.
  useEffect(() => {
    if (isLegacySyntheticBuilding) {
      const next = new URLSearchParams(params);
      if (inventory?.buildings?.length) {
        next.set('building', inventory.buildings[0].buildingId);
      } else {
        next.delete('building');
      }
      setParams(next, { replace: true });
      return;
    }
    if (!building || !inventory?.buildings?.length) return;
    if (!inventory.buildings.some((b) => b.buildingId === building)) {
      const next = new URLSearchParams(params);
      next.set('building', inventory.buildings[0].buildingId);
      setParams(next, { replace: true });
    }
  }, [isLegacySyntheticBuilding, building, inventory, params, setParams]);

  const updateParam = useCallback(
    (key: string, value: string) => {
      const next = new URLSearchParams(params);
      if (value) next.set(key, value);
      else next.delete(key);
      setParams(next);
    },
    [params, setParams],
  );

  const applyToken = (e: React.FormEvent) => {
    e.preventDefault();
    const cleaned = tokenDraft.trim();
    if (cleaned) sessionStorage.setItem(LIVE_TOKEN_KEY, cleaned);
    else sessionStorage.removeItem(LIVE_TOKEN_KEY);
    setToken(cleaned);
    setRefreshTick((n) => n + 1);
  };

  const buildEndpoint = useCallback(() => {
    const qs = new URLSearchParams();
    if (building) qs.set('building', building);
    if (energy) qs.set('energy', energy);
    if (date) qs.set('date', date);
    if (section === 'dimensions' || section === 'diagnosis') qs.set('view', view);
    if (section === 'target-summary' || section === 'target-periods') qs.set('year', year);
    if (section === 'realtime') qs.set('days', ['1', '7', '14', '30'].includes(days) ? days : '7');
    if (section === 'report-custom') {
      qs.set('scope', scope);
      const effectiveFields =
        fieldsParam ||
        (scope === 'consumption'
          ? 'category_name,energy,usage,unit,share,coverage_rate,coverage_slots'
          : 'metric_name,building_name,metric_value,metric_unit,benchmark_max,benchmark_status,metric_status');
      qs.set('fields', effectiveFields);
    }
    switch (section) {
      case 'overview': return `/overview?${qs}`;
      case 'composition': return `/composition?${qs}`;
      case 'objects': return `/objects-overview?${qs}`;
      case 'realtime': return `/realtime-trend?${qs}`;
      case 'target-summary':
      case 'target-periods': return `/targets?${qs}`;
      case 'classification': return `/classification?${qs}`;
      case 'dimensions':
      case 'diagnosis': return `/dimensions?${qs}`;
      case 'trend': return `/trend?${qs}`;
      case 'balance': return `/balance?${qs}`;
      case 'anomalies': return `/anomalies?${qs}`;
      case 'efficiency': return `/efficiency?${qs}`;
      case 'industry-templates': return `/industry/templates?${qs}`;
      case 'industry-benchmarks': return `/industry/benchmarks?${qs}`;
      case 'assessment': return `/assessment?${qs}`;
      case 'opportunities': return `/opportunities?${qs}`;
      case 'retrofit-baseline': return `/retrofit/baselines`;
      case 'retrofit-potential': return `/retrofit/potential`;
      case 'retrofit-verification': return `/retrofit/verification`;
      case 'report-consumption': return `/reports/consumption?${qs}`;
      case 'report-metrics': return `/reports/metrics?${qs}`;
      case 'report-assessment': return `/reports/assessment?${qs}`;
      case 'report-retrofit': return `/reports/retrofit`;
      case 'report-custom': return `/reports/custom?${qs}`;
    }
  }, [section, building, energy, date, view, year, days, scope, fieldsParam]);

  useEffect(() => {
    if (token || devAuthTried) return;
    let cancelled = false;
    const host = window.location.hostname;
    const isLoopback = host === '127.0.0.1' || host === 'localhost' || host === '::1';

    // Non-loopback host (trusted LAN gateway): NEVER invoke loopback dev-auth; enter read-only preview directly!
    if (!isLoopback) {
      fetch('/lan-auth/status', { credentials: 'same-origin' })
        .then(async (r) =>
          r.ok
            ? ((await r.json()) as { lanGateway?: boolean; readOnly?: boolean; allowedSubnet?: string })
            : null,
        )
        .then((body) => {
          if (cancelled) return;
          if (body?.lanGateway) {
            setIsLanReadOnly(Boolean(body.readOnly ?? true));
            setLanAllowedSubnet(body.allowedSubnet ?? '192.168.100.0/24');
          }
          setDevAuthTried(true);
        })
        .catch(() => {
          if (!cancelled) setDevAuthTried(true);
        });
      return () => {
        cancelled = true;
      };
    }

    if (!import.meta.env.DEV) {
      setDevAuthTried(true);
      return;
    }
    fetch('/admin/v1/dev-auth/session', { method: 'POST', credentials: 'same-origin' })
      .then(async (r) => (r.ok ? ((await r.json()) as { token?: string }) : null))
      .then((body) => {
        if (cancelled) return;
        if (body?.token) {
          sessionStorage.setItem(LIVE_TOKEN_KEY, body.token);
          setToken(body.token);
          setTokenDraft(body.token);
        }
        setDevAuthTried(true);
      })
      .catch(() => {
        if (!cancelled) setDevAuthTried(true);
      });
    return () => {
      cancelled = true;
    };
  }, [token, devAuthTried]);

  useEffect(() => {
    if (!token && !devAuthTried) return;
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    setAuthError(null);
    const endpoint = buildEndpoint();

    Promise.all([
      liveGet<InventoryData>('/management/inventory', token, controller.signal),
      liveGet<unknown>(endpoint, token, controller.signal),
    ])
      .then(([invRes, dataRes]) => {
        if (controller.signal.aborted) return;
        if (invRes.status === 401 || invRes.status === 403 || dataRes.status === 401 || dataRes.status === 403) {
          setAuthError(dataRes.error ?? invRes.error ?? '正式建筑能耗接口要求有效可信身份凭据（默认拒绝）');
          setInventory(null);
          setPayload(null);
          return;
        }
        if (invRes.data) setInventory(invRes.data);
        if (dataRes.error) {
          setError(dataRes.error);
          setPayload(null);
        } else {
          setPayload(dataRes.data != null ? { section, data: dataRes.data } : null);
        }
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) return;
        if ((err as { name?: string })?.name !== 'AbortError') {
          setError(err instanceof Error ? err.message : '加载数据库数据失败');
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });

    return () => controller.abort();
  }, [buildEndpoint, section, token, devAuthTried, refreshTick]);

  const isRetrofitSection =
    section === 'retrofit-baseline' ||
    section === 'retrofit-potential' ||
    section === 'retrofit-verification' ||
    section === 'report-retrofit';
  const isAssessmentSection = section === 'assessment' || section === 'report-assessment';
  const showBuildingFilter = section !== 'dimensions' && section !== 'diagnosis' && !isAssessmentSection && !isRetrofitSection;
  const showEnergyFilter =
    section !== 'overview' &&
    section !== 'report-consumption' &&
    !isAssessmentSection &&
    !isRetrofitSection;
  const showDateFilter = section !== 'target-summary' && section !== 'target-periods' && !isRetrofitSection;
  const showYearFilter = section === 'target-summary' || section === 'target-periods';
  const showFilterBar = !isRetrofitSection;

  const dataset = inventory?.dataset;
  const isSimulated = Boolean(dataset?.isSimulated);
  const activePayload = payload && payload.section === section ? payload.data : null;

  const originNotice =
    inventory?.dataOriginNotice ||
    (activePayload && typeof activePayload === 'object' && 'dataOriginNotice' in activePayload
      ? String((activePayload as { dataOriginNotice?: string }).dataOriginNotice ?? '')
      : '');

  const activeBldgName = useMemo(() => {
    const targetId = building || inventory?.buildings?.[0]?.buildingId;
    return inventory?.buildings?.find((b) => b.buildingId === targetId)?.name ?? targetId ?? '默认建筑';
  }, [building, inventory]);

  const filterSummaryText = useMemo(() => {
    const parts: string[] = [];
    if (showBuildingFilter) parts.push(activeBldgName);
    if (section === 'dimensions' || section === 'diagnosis') {
      parts.push(view === 'building' ? '建筑对比' : view === 'space' ? '区域对比' : '组织对比');
    }
    if (showEnergyFilter) parts.push(energyLabels[energy] ?? energy);
    if (showDateFilter) parts.push(date || '最新完整日');
    if (showYearFilter) parts.push(`${year} 年`);
    return parts.join(' · ');
  }, [showBuildingFilter, activeBldgName, section, view, showEnergyFilter, energy, showDateFilter, date, showYearFilter, year]);

  const filterControls = (
    <>
      {showBuildingFilter && (
        <label className="flex items-center gap-2 text-[13px] text-fg-2">
          <span className="shrink-0">建筑</span>
          <select
            aria-label="选择建筑"
            value={building || inventory?.buildings?.[0]?.buildingId || ''}
            onChange={(e) => updateParam('building', e.target.value)}
            className="arco-input min-w-[164px] font-normal"
          >
            {(inventory?.buildings ?? []).length === 0 ? (
              <option value="">读取中…</option>
            ) : (
              (inventory?.buildings ?? []).map((b) => (
                <option key={b.buildingId} value={b.buildingId}>
                  {b.name}
                </option>
              ))
            )}
          </select>
        </label>
      )}

      {/* View switcher for F07/F16 lives inside the unified Dimension Workbench header — no duplicate dropdown here. */}

      {showEnergyFilter && (
        <label className="flex items-center gap-2 text-[13px] text-fg-2">
          <span className="shrink-0">能源</span>
          <select
            aria-label="选择能源类型"
            value={energy}
            onChange={(e) => updateParam('energy', e.target.value)}
            className="arco-input min-w-[120px] font-normal"
          >
            <option value="electricity">用电 (kWh)</option>
            <option value="water">用水 (m³)</option>
            <option value="gas">燃气 (Nm³)</option>
            <option value="heat">供热 (GJ)</option>
            <option value="cooling">供冷 (GJ)</option>
          </select>
        </label>
      )}

      {showDateFilter && (
        <label className="flex items-center gap-2 text-[13px] text-fg-2">
          <span className="shrink-0">日期</span>
          <ChineseDateInput
            ariaLabel="统计自然日"
            value={date}
            placeholder="最新完整日"
            allowClear
            onChange={(next) => updateParam('date', next)}
          />
        </label>
      )}

      {showYearFilter && (
        <label className="flex items-center gap-2 text-[13px] text-fg-2">
          <span className="shrink-0">年度</span>
          <input
            aria-label="考核年度"
            type="number"
            min={2020}
            max={2035}
            value={year}
            onChange={(e) => updateParam('year', e.target.value)}
            className="arco-input w-24 tabular"
          />
        </label>
      )}

      {/* Scope switcher for F25 lives inside EnergyReportStudio's builder header — no duplicate dropdown here. */}
    </>
  );

  return (
    <div className="mx-auto w-full max-w-[1720px] space-y-4">
      {/* 语义化 H1 仅对屏幕阅读器可见，避免与左侧选中导航重复显示功能标题与 F 编号 */}
      <h1 className="sr-only">{item ? item.label : '建筑能耗工作台'}</h1>

      {/* 统一操作与筛选工具栏（全量 F01–F25 一致：左侧直接筛选，右侧紧凑模拟标识 + 数据集详情 + 全局操作） */}
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line pb-3">
        <div className="flex min-w-0 flex-wrap items-center gap-3">
          {showFilterBar ? (
            <div aria-label="统计范围筛选" className="hidden flex-wrap items-center gap-3 md:flex">
              {filterControls}
            </div>
          ) : (
            <div className="flex flex-wrap items-center gap-2">
              {isSimulated && (
                <span className="inline-flex h-[20px] items-center rounded-[2px] bg-warning-soft px-1.5 text-[11px] font-medium text-warning">
                  模拟数据
                </span>
              )}
              <DatasetInfoButton dataset={dataset} originNotice={originNotice} />
            </div>
          )}
        </div>

        <div className="flex flex-wrap items-center gap-2">
          {showFilterBar && (
            <>
              {isSimulated && (
                <span className="inline-flex h-[20px] items-center rounded-[2px] bg-warning-soft px-1.5 text-[11px] font-medium text-warning">
                  模拟数据
                </span>
              )}
              {isSimulated && section === 'realtime' && (
                <span className="inline-flex h-[20px] items-center rounded-[2px] bg-primary-soft px-1.5 text-[11px] font-medium text-primary">
                  模拟实时
                </span>
              )}
              <DatasetInfoButton dataset={dataset} originNotice={originNotice} />
              <span className="hidden h-4 w-px bg-line sm:inline-block" aria-hidden="true" />
            </>
          )}
          {isLanReadOnly && (
            <span
              data-lan-readonly-badge
              title={`仅限可信局域网网段 ${lanAllowedSubnet || '192.168.100.0/24'} 免口令只读预览；配置与写入操作受限`}
              className="inline-flex h-[24px] items-center rounded-[2px] border border-primary/25 bg-primary-soft/60 px-2 text-[11px] font-medium text-primary"
            >
              局域网只读预览
            </span>
          )}
          {!isLanReadOnly && (
            <button
              type="button"
              onClick={() => setDrawerOpen(true)}
              className="arco-btn"
            >
              <Settings2 className="h-3.5 w-3.5 text-primary" aria-hidden="true" />
              <span>配置中心</span>
              <span className="hidden text-muted tabular sm:inline">({inventory?.configuredMeterCount ?? 0})</span>
            </button>
          )}
          <button
            type="button"
            onClick={() => setRefreshTick((n) => n + 1)}
            aria-label="刷新当前视图"
            className="arco-btn"
          >
            <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin text-primary' : 'text-fg-2'}`} aria-hidden="true" />
            <span className="hidden sm:inline">刷新</span>
          </button>
        </div>
      </div>

      {/* Mobile Compact Filter Drawer */}
      {showFilterBar && <MobileFilterDrawer summary={filterSummaryText}>{filterControls}</MobileFilterDrawer>}

      {/* Fail-closed Auth Gate (Production / Non-LAN Token Gate only; LAN enters directly in read-only mode) */}
      {authError && (
        <section className="rounded-[4px] border border-warning/40 bg-surface p-5">
          <div className="flex items-start gap-3">
            <KeyRound className="mt-0.5 h-5 w-5 shrink-0 text-warning" aria-hidden="true" />
            <div className="min-w-0 flex-1 space-y-3">
              <div>
                <h2 className="text-sm font-semibold text-fg">需要可信身份凭据（默认拒绝模式）</h2>
                <p className="mt-1 text-xs text-muted">{authError}</p>
              </div>
              <form onSubmit={applyToken} className="flex flex-wrap items-center gap-2">
                <input
                  type="password"
                  value={tokenDraft}
                  onChange={(e) => setTokenDraft(e.target.value)}
                  placeholder="粘贴经服务端签名的 Bearer Token（仅存于当前会话）"
                  aria-label="Bearer Token"
                  className="min-w-[260px] flex-1 rounded-[4px] border border-line bg-surface-2 px-3 py-1.5 text-xs text-fg"
                />
                <button type="submit" className="rounded-[4px] bg-primary px-4 py-1.5 text-xs font-medium text-white">
                  应用凭据并加载
                </button>
              </form>
            </div>
          </div>
        </section>
      )}

      {error && !authError && (
        <div role="alert" className="rounded-[4px] border border-negative/40 bg-negative-soft p-4 text-xs text-negative">
          {error}
        </div>
      )}

      {loading && activePayload == null && !authError && (
        <div role="status" className="rounded-[4px] border border-line bg-surface p-8 text-center text-xs text-muted">
          正在从 PostgreSQL 计算正式能耗结果…
        </div>
      )}

      {!authError && !error && activePayload != null && (
        <>
          {section === 'overview' && (
            <LiveOverviewView
              data={activePayload as OverviewData}
              token={token}
              date={date}
              onOpenConfig={() => setDrawerOpen(true)}
              onNavigateAnomaly={(en) =>
                navigate(
                  `/a/energy/statistics/anomalies?building=${encodeURIComponent((activePayload as OverviewData).buildingId)}&energy=${encodeURIComponent(en)}${date ? `&date=${encodeURIComponent(date)}` : ''}`,
                )
              }
            />
          )}
          {section === 'composition' && (
            <LiveCompositionView
              data={activePayload as CompositionData}
              selectedEnergy={energy}
              token={token}
              inventory={inventory}
              onOpenConfig={() => setDrawerOpen(true)}
              onNavigateClassification={(en) =>
                navigate(
                  `/a/energy/statistics?building=${encodeURIComponent((activePayload as CompositionData).buildingId)}&energy=${encodeURIComponent(en)}${date ? `&date=${encodeURIComponent(date)}` : ''}`,
                )
              }
            />
          )}
          {section === 'objects' && (
            <LiveObjectsOverviewView
              data={activePayload as ObjectsOverviewData}
              token={token}
              inventory={inventory}
              date={date}
              onSelectBuilding={(bid) => updateParam('building', bid)}
            />
          )}
          {section === 'realtime' && (
            <LiveRealtimeTrendView
              data={activePayload as RealtimeTrendData}
              days={days}
              isSimulated={isSimulated}
              onChangeDays={(nextDays) => updateParam('days', nextDays)}
              onManualRefresh={() => setRefreshTick((n) => n + 1)}
            />
          )}
          {section === 'target-summary' && (
            <LiveTargetSummaryView
              data={activePayload as TargetsData}
              token={token}
              inventory={inventory}
              onChangeEnergy={(en) => updateParam('energy', en)}
              onOpenF14={() => {
                const td = activePayload as TargetsData;
                navigate(
                  `/a/energy/efficiency/targets?building=${encodeURIComponent(td.buildingId)}&energy=${encodeURIComponent(td.energy)}&year=${encodeURIComponent(String(td.year))}`,
                );
              }}
            />
          )}
          {section === 'target-periods' && <LiveTargetsView data={activePayload as TargetsData} inventory={inventory} summaryOnly={false} />}
          {section === 'classification' && (
            <LiveClassificationView
              data={activePayload as ClassificationData}
              token={token}
              inventory={inventory}
              onOpenConfig={() => setDrawerOpen(true)}
            />
          )}
          {section === 'dimensions' && (
            <LiveDimensionsView
              data={activePayload as DimensionsData}
              token={token}
              onChangeView={(nextView) => updateParam('view', nextView)}
              onDrillBuilding={(bid) =>
                navigate(`/a/energy/overview?building=${encodeURIComponent(bid)}${date ? `&date=${encodeURIComponent(date)}` : ''}`)
              }
            />
          )}
          {section === 'diagnosis' && (
            <LiveDiagnosisView
              data={activePayload as DimensionsData}
              token={token}
              inventory={inventory}
              date={date}
              onChangeView={(nextView) => updateParam('view', nextView)}
              onNavigateOpportunities={(bid) =>
                navigate(
                  `/a/energy/diagnosis/opportunities?building=${encodeURIComponent(bid)}&energy=${encodeURIComponent(energy)}${date ? `&date=${encodeURIComponent(date)}` : ''}`,
                )
              }
            />
          )}
          {section === 'trend' && <LiveTrendView data={activePayload as TrendData} token={token} />}
          {section === 'balance' && <LiveBalanceView data={activePayload as BalanceData} />}
          {section === 'anomalies' && (
            <LiveAnomalyView
              data={activePayload as AnomalyData}
              token={token}
              inventory={inventory}
            />
          )}
          {section === 'efficiency' && (
            <LiveEfficiencyView
              data={activePayload as EfficiencyData}
              token={token}
              inventory={inventory}
              date={date}
              onSelectBuilding={(bid) => updateParam('building', bid)}
            />
          )}
          {section === 'industry-templates' && (
            <LiveIndustryTemplatesView
              data={activePayload as EfficiencyData}
              token={token}
              inventory={inventory}
              date={date}
              onSelectBuilding={(bid) => updateParam('building', bid)}
            />
          )}
          {section === 'industry-benchmarks' && (
            <LiveBenchmarksView data={activePayload as BenchmarksData} inventory={inventory} />
          )}
          {section === 'assessment' && (
            <LiveAssessmentView
              data={activePayload as AssessmentData}
              inventory={inventory}
              token={token}
              onUpdated={() => setRefreshTick((n) => n + 1)}
            />
          )}
          {section === 'opportunities' && (
            <LiveOpportunitiesView
              data={activePayload as OpportunitiesData}
              token={token}
              inventory={inventory}
              date={date}
              onSelectBuilding={(bid) => updateParam('building', bid)}
              onNavigateRetrofit={(bid, en) =>
                navigate(`/a/energy/diagnosis/baseline?building=${encodeURIComponent(bid)}&energy=${encodeURIComponent(en)}`)
              }
            />
          )}
          {(section === 'retrofit-baseline' || section === 'retrofit-potential' || section === 'retrofit-verification') && (
            <RetrofitWorkspace
              data={activePayload as RetrofitData}
              mode={section}
              token={token}
              inventory={inventory}
              initialBuilding={building}
              initialEnergy={energy}
              onUpdated={() => setRefreshTick((n) => n + 1)}
            />
          )}
          {(section === 'report-consumption' ||
            section === 'report-metrics' ||
            section === 'report-assessment' ||
            section === 'report-retrofit' ||
            section === 'report-custom') && (
            <EnergyReportStudio
              section={section}
              data={activePayload as LiveReportData}
              endpoint={buildEndpoint()}
              token={token}
              inventory={inventory}
              date={date}
              scope={scope}
              onScopeChange={(nextScope) => {
                const next = new URLSearchParams(params);
                next.set('scope', nextScope);
                next.delete('fields');
                setParams(next);
              }}
              onFieldsChange={(nextFields) => updateParam('fields', nextFields.join(','))}
            />
          )}
        </>
      )}

      <EnergyManagementDrawer
        open={drawerOpen}
        onOpenChange={setDrawerOpen}
        token={token}
        inventory={inventory}
        onChanged={() => setRefreshTick((n) => n + 1)}
      />
    </div>
  );
}

// ── Shared Notice & Progressive Disclosure Helpers ──────────────────────────

function UnconfiguredNotice({ message, onOpenConfig }: { message?: string; onOpenConfig?: () => void }) {
  return (
    <section className="rounded-[4px] border border-warning/40 bg-surface p-6">
      <div className="flex items-start gap-3">
        <AlertTriangle className="mt-0.5 h-5 w-5 shrink-0 text-warning" aria-hidden="true" />
        <div className="space-y-2">
          <h2 className="text-sm font-semibold text-fg">计量口径待配置（严禁自动推断入账）</h2>
          <p className="text-xs text-muted">{message || '当前范围尚无已启用的正式计量点配置。'}</p>
          {onOpenConfig && (
            <button type="button" onClick={onOpenConfig} className="rounded-[4px] bg-primary px-3 py-1.5 text-xs font-medium text-white">
              打开配置中心完成配置
            </button>
          )}
        </div>
      </div>
    </section>
  );
}

// ── FLAGSHIP #1 — F01: Overview ─────────────────────────────────────────────

function LiveOverviewView({
  data,
  token,
  date,
  onOpenConfig,
  onNavigateAnomaly,
}: {
  data: OverviewData;
  token: string;
  date: string;
  onOpenConfig: () => void;
  onNavigateAnomaly: (energy: string) => void;
}) {
  const energies = data.energies ?? [];
  const [focusEnergy, setFocusEnergy] = useState<string>(() => energies[0]?.energy ?? 'electricity');
  const [trendMode, setTrendMode] = useState<'hourly' | '7d'>('hourly');

  // Parallel server-computed analytical companions for the active single energy (zero frontend recalculation).
  const [trendExtra, setTrendExtra] = useState<TrendData | null>(null);
  const [recent7d, setRecent7d] = useState<RealtimeTrendData | null>(null);
  const [targetExtra, setTargetExtra] = useState<TargetsData | null>(null);
  const [anomalyExtra, setAnomalyExtra] = useState<AnomalyData | null>(null);

  useEffect(() => {
    if (energies.length > 0 && !energies.some((e) => e.energy === focusEnergy)) {
      setFocusEnergy(energies[0].energy);
    }
  }, [energies, focusEnergy]);

  useEffect(() => {
    if (!data.buildingId || !focusEnergy) return;
    const ctrl = new AbortController();
    const qs = new URLSearchParams();
    qs.set('building', data.buildingId);
    qs.set('energy', focusEnergy);
    if (date) qs.set('date', date);

    const yearStr = (data.from || '2026').slice(0, 4);
    const tQs = new URLSearchParams({ building: data.buildingId, energy: focusEnergy, year: yearStr });
    const rtQs = new URLSearchParams(qs);
    rtQs.set('days', '7');

    Promise.all([
      liveGet<TrendData>(`/trend?${qs}`, token, ctrl.signal).catch(() => ({ data: null })),
      liveGet<RealtimeTrendData>(`/realtime-trend?${rtQs}`, token, ctrl.signal).catch(() => ({ data: null })),
      liveGet<TargetsData>(`/targets?${tQs}`, token, ctrl.signal).catch(() => ({ data: null })),
      liveGet<AnomalyData>(`/anomalies?${qs}`, token, ctrl.signal).catch(() => ({ data: null })),
    ]).then(([tr, rt, tg, an]) => {
      if (ctrl.signal.aborted) return;
      setTrendExtra(tr.data ?? null);
      setRecent7d(rt.data ?? null);
      setTargetExtra(tg.data ?? null);
      setAnomalyExtra(an.data ?? null);
    });

    return () => ctrl.abort();
  }, [data.buildingId, data.from, focusEnergy, date, token]);

  if (data.configurationState === 'unconfigured') {
    return <UnconfiguredNotice message={data.configurationMessage} onOpenConfig={onOpenConfig} />;
  }

  const focusedCard = energies.find((e) => e.energy === focusEnergy) ?? energies[0] ?? null;
  const focusedLabel = focusedCard ? (energyLabels[focusedCard.energy] ?? focusedCard.energy) : '用电';
  const focusedUnit = focusedCard?.unit ?? 'kWh';

  const focusedSlices = focusedCard
    ? [
        ...(focusedCard.categories ?? []).map((c) => ({ id: c.id, name: c.name, usage: c.usage, share: c.share })),
        ...(focusedCard.unclassifiedUsage && focusedCard.unclassifiedUsage > 0
          ? [
              {
                id: '__unclassified__',
                name: '未分项',
                usage: focusedCard.unclassifiedUsage,
                share: focusedCard.usage ? focusedCard.unclassifiedUsage / focusedCard.usage : null,
                isUnclassified: true,
              },
            ]
          : []),
      ]
    : [];

  // Trend series for the selected single energy (strict single-unit fidelity).
  const activeSeries =
    trendMode === '7d'
      ? recent7d?.series ?? []
      : trendExtra?.series?.length
        ? trendExtra.series
        : focusedCard?.energy === 'electricity'
          ? data.hourlyTrend ?? []
          : [];

  // Target execution summary derived strictly from server-computed completed months & annual target.
  const targetMonths = targetExtra?.months ?? [];
  const completedMonths = targetMonths.filter((m) => m.usage != null);
  const ytdUsage = completedMonths.length > 0 ? completedMonths.reduce((acc, m) => acc + (m.usage ?? 0), 0) : null;
  const annualTarget = targetExtra?.annual?.target ?? null;
  const ytdRatio = ytdUsage != null && annualTarget != null && annualTarget > 0 ? ytdUsage / annualTarget : null;
  const overTargetMonths = completedMonths.filter((m) => m.target != null && (m.usage ?? 0) > m.target).length;

  const prev = trendExtra?.previous;
  const prior = trendExtra?.priorYear;
  const allSources = energies.flatMap((e) => e.sources ?? []);
  const combinedExceptions = (anomalyExtra?.exceptions?.length ? anomalyExtra.exceptions : data.exceptions) ?? [];
  const lowCovEnergies = energies.filter((e) => e.coverage.rate != null && e.coverage.rate < 0.999);

  return (
    <div className="space-y-4">
      {/* ── 1. Unified Executive Command Band (Single Energy Switcher + Headline KPIs + YoY/MoM/Target) ── */}
      <section aria-label="核心能耗与对标摘要" className="arco-card overflow-hidden">
        {/* Single-Energy Selector Strip (strict single-unit discipline: never sum across different units) */}
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line bg-fill-1/60 px-4 py-2">
          <div className="flex flex-wrap items-center gap-1" role="tablist" aria-label="选择单一分析能源">
            {energies.map((card) => {
              const active = focusedCard?.energy === card.energy;
              return (
                <button
                  key={card.energy}
                  type="button"
                  role="tab"
                  aria-selected={active}
                  onClick={() => setFocusEnergy(card.energy)}
                  className={`inline-flex h-7 items-center gap-1.5 rounded-[2px] px-3 text-[12px] transition-colors ${
                    active
                      ? 'bg-primary font-medium text-white'
                      : 'text-fg-2 hover:bg-fill-2 hover:text-fg'
                  }`}
                >
                  <span>{energyLabels[card.energy] ?? card.energy}</span>
                  <span className={`text-[11px] tabular ${active ? 'text-white/80' : 'text-muted'}`}>
                    {card.unit}
                  </span>
                </button>
              );
            })}
          </div>
          <span className="text-[11px] text-muted tabular">
            {data.buildingName} · {fmtShanghaiDate(data.from)} · {data.timezone} · 单能源独立分析（严禁跨单位直接相加）
          </span>
        </div>

        {/* 5-Column Hairline-Divided Executive Metric Strip */}
        <div className="grid divide-y divide-line sm:grid-cols-2 lg:grid-cols-5 lg:divide-y-0 lg:divide-x tabular">
          {/* 1) Selected Energy Consumption */}
          <div className="p-4">
            <div className="flex items-center justify-between gap-2 text-[12px]">
              <span className="font-medium text-fg-2">当日{focusedLabel}总量</span>
              <span
                className={`rounded-[2px] px-1.5 py-0 text-[11px] leading-[18px] ${
                  (focusedCard?.coverage.rate ?? 0) >= 0.999
                    ? 'bg-positive-soft text-positive'
                    : 'bg-warning-soft text-warning'
                }`}
              >
                覆盖 {fmtPct(focusedCard?.coverage.rate)}
              </span>
            </div>
            <p className="mt-2 text-[26px] font-semibold leading-[32px] tracking-[-0.02em] text-fg">
              {fmtNum(focusedCard?.usage)}
              <span className="ml-1 text-[12px] font-normal text-muted">{focusedUnit}</span>
            </p>
            <p className="mt-1.5 truncate text-[11px] text-muted">
              {focusedCard?.meterCount ?? 0} 块计量点 · 有效 {focusedCard?.coverage.valid ?? 0}/{focusedCard?.coverage.expected ?? 0} 槽
            </p>
          </div>

          {/* 2) Period-over-Period (环比) */}
          <div className="p-4">
            <div className="flex items-center justify-between gap-2 text-[12px]">
              <span className="font-medium text-fg-2">环比变化（较昨日）</span>
              {prev && (
                <span className="text-[11px] text-muted">{statusLabels[prev.status] ?? prev.status}</span>
              )}
            </div>
            {prev?.status === 'comparable' && prev.rate != null ? (
              <>
                <p
                  className={`mt-2 inline-flex items-baseline gap-1 text-[24px] font-semibold leading-[32px] tracking-[-0.02em] ${
                    prev.rate > 0 ? 'text-negative' : 'text-positive'
                  }`}
                >
                  {prev.rate > 0 ? (
                    <ArrowUpRight className="h-4 w-4 shrink-0 self-center" aria-hidden="true" />
                  ) : (
                    <ArrowDownRight className="h-4 w-4 shrink-0 self-center" aria-hidden="true" />
                  )}
                  <span>
                    {prev.rate > 0 ? '+' : ''}
                    {fmtPct(prev.rate)}
                  </span>
                </p>
                <p className="mt-1.5 truncate text-[11px] text-muted">
                  较昨日 {fmtNum(prev.baseline)} {focusedUnit}（{prev.delta != null && prev.delta > 0 ? '+' : ''}
                  {fmtNum(prev.delta)}）
                </p>
              </>
            ) : (
              <>
                <p className="mt-2 text-[18px] font-medium leading-[32px] text-muted">暂不可比</p>
                <p className="mt-1.5 truncate text-[11px] text-muted">
                  {prev ? `${statusLabels[prev.status] ?? prev.status} · 基期 ${fmtNum(prev.baseline)}` : '读取中…'}
                </p>
              </>
            )}
          </div>

          {/* 3) Year-over-Year (同比) */}
          <div className="p-4">
            <div className="flex items-center justify-between gap-2 text-[12px]">
              <span className="font-medium text-fg-2">同比变化（去年同期）</span>
              {prior && prior.status !== 'comparable' && (
                <span className="rounded-[2px] bg-warning-soft px-1.5 py-0 text-[11px] leading-[18px] text-warning">
                  {statusLabels[prior.status] ?? prior.status}
                </span>
              )}
            </div>
            {prior?.status === 'comparable' && prior.rate != null ? (
              <>
                <p
                  className={`mt-2 inline-flex items-baseline gap-1 text-[24px] font-semibold leading-[32px] tracking-[-0.02em] ${
                    prior.rate > 0 ? 'text-negative' : 'text-positive'
                  }`}
                >
                  {prior.rate > 0 ? (
                    <ArrowUpRight className="h-4 w-4 shrink-0 self-center" aria-hidden="true" />
                  ) : (
                    <ArrowDownRight className="h-4 w-4 shrink-0 self-center" aria-hidden="true" />
                  )}
                  <span>
                    {prior.rate > 0 ? '+' : ''}
                    {fmtPct(prior.rate)}
                  </span>
                </p>
                <p className="mt-1.5 truncate text-[11px] text-muted">
                  同期基期 {fmtNum(prior.baseline)} {focusedUnit}
                </p>
              </>
            ) : (
              <>
                <p className="mt-2 text-[22px] font-semibold leading-[32px] tracking-[-0.02em] text-fg-2">
                  {prior?.baseline != null ? fmtNum(prior.baseline) : '—'}
                  {prior?.baseline != null && (
                    <span className="ml-1 text-[11px] font-normal text-muted">{focusedUnit}</span>
                  )}
                </p>
                <p className="mt-1.5 truncate text-[11px] text-muted">
                  {prior?.baseline != null ? '仅作历史参考，口径不一致不直接出同比差' : '无去年同期完整基期'}
                </p>
              </>
            )}
          </div>

          {/* 4) Annual Target Progress (年度目标进度) */}
          <div className="p-4">
            <div className="flex items-center justify-between gap-2 text-[12px]">
              <span className="font-medium text-fg-2">{targetExtra?.year ?? 2026} 年度目标进度</span>
              <span className="text-[11px] text-muted">已完结 {completedMonths.length}/12 月</span>
            </div>
            {ytdRatio != null && annualTarget != null ? (
              <>
                <p className="mt-2 text-[24px] font-semibold leading-[32px] tracking-[-0.02em] text-fg">
                  {fmtPct(ytdRatio)}
                  <span className="ml-1.5 text-[11px] font-normal text-muted">
                    / {fmtNum(annualTarget)} {focusedUnit}
                  </span>
                </p>
                <div className="mt-2 h-[5px] w-full overflow-hidden rounded-full bg-fill-2">
                  <div
                    className={`h-full rounded-full ${ytdRatio > 1 ? 'bg-negative' : 'bg-primary'}`}
                    style={{ width: `${Math.min(100, ytdRatio * 100)}%` }}
                  />
                </div>
              </>
            ) : (
              <>
                <p className="mt-2 text-[18px] font-medium leading-[32px] text-muted">未配置目标</p>
                <p className="mt-1.5 text-[11px] text-muted">当前能源尚无年度配额目标</p>
              </>
            )}
          </div>

          {/* 5) Reference Carbon (Strictly Separate, Excludes Water) */}
          <div className="bg-fill-1/40 p-4">
            <div className="flex items-center justify-between gap-2 text-[12px]">
              <span className="truncate font-medium text-fg-2">参考碳排（不含水）</span>
              {data.carbonSummary && (
                <span className="shrink-0 rounded-[2px] bg-positive-soft px-1.5 py-0 text-[11px] leading-[18px] text-positive">
                  {data.carbonSummary.status === 'complete' ? '完整' : '部分'}
                </span>
              )}
            </div>
            <p className="mt-2 text-[24px] font-semibold leading-[32px] tracking-[-0.02em] text-fg">
              {fmtNum(data.carbonSummary?.totalKgCO2e)}
              <span className="ml-1 text-[11px] font-normal text-muted">kgCO₂e</span>
            </p>
            <p className="mt-1.5 truncate text-[11px] text-muted" title={data.carbonSummary?.disclaimer}>
              {focusedCard?.carbon?.includedInTotal
                ? `其中${focusedLabel} ${fmtNum(focusedCard.carbon.kgCO2e)} kg`
                : '水资源单列，不计入碳排'}
            </p>
          </div>
        </div>
      </section>

      {/* ── 2. Primary Analytical Stage: Trend Hero (8/9 cols) + Single-Energy Structure & 5-Energy Ledger (4/3 cols) ── */}
      <div className="grid gap-4 lg:grid-cols-12">
        <section className="arco-card flex flex-col justify-between p-5 lg:col-span-8 2xl:col-span-9">
          <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
            <div>
              <h2 className="text-[15px] font-medium leading-[22px] text-fg">
                {focusedLabel}消费趋势分析（{trendMode === 'hourly' ? '当日 24 小时逐时负荷' : '近 7 日消费趋势'}）
              </h2>
              <p className="mt-0.5 text-[12px] text-muted tabular">
                {data.buildingName} · 版本 {data.buildingRevision} · 单位 {focusedUnit}
                {trendMode === '7d' && anomalyExtra?.baseline != null
                  ? ` · 同星期基线参考 ${fmtNum(anomalyExtra.baseline)} ${focusedUnit}`
                  : ''}
              </p>
            </div>
            <div className="inline-flex rounded-[2px] border border-line bg-fill-1 p-0.5 text-[12px]">
              <button
                type="button"
                onClick={() => setTrendMode('hourly')}
                className={`rounded-[2px] px-2.5 py-1 transition-colors ${
                  trendMode === 'hourly' ? 'bg-surface font-medium text-primary shadow-xs' : 'text-fg-2 hover:text-fg'
                }`}
              >
                当日逐时
              </button>
              <button
                type="button"
                onClick={() => setTrendMode('7d')}
                className={`rounded-[2px] px-2.5 py-1 transition-colors ${
                  trendMode === '7d' ? 'bg-surface font-medium text-primary shadow-xs' : 'text-fg-2 hover:text-fg'
                }`}
              >
                近 7 日
              </button>
            </div>
          </div>

          <TrendAreaChart
            points={activeSeries}
            unit={focusedUnit}
            height={304}
            referenceValue={
              trendMode === '7d'
                ? anomalyExtra?.baseline ?? undefined
                : prev?.baseline != null && prev.baseline > 0
                  ? prev.baseline / 24
                  : undefined
            }
            referenceLabel={
              trendMode === '7d'
                ? anomalyExtra?.baseline != null
                  ? `同星期基线 ${fmtNum(anomalyExtra.baseline)}`
                  : undefined
                : prev?.baseline != null && prev.baseline > 0
                  ? `昨日时均 ${fmtNum(prev.baseline / 24)}`
                  : undefined
            }
          />
        </section>

        <section className="arco-card flex flex-col justify-between p-5 lg:col-span-4 2xl:col-span-3">
          <div>
            <div className="mb-3 flex items-baseline justify-between gap-2">
              <div>
                <h2 className="text-[15px] font-medium leading-[22px] text-fg">{focusedLabel}分项结构</h2>
                <p className="mt-0.5 text-[12px] text-muted">仅展示当前单一能源内部分项占比</p>
              </div>
              <span className="text-[13px] font-semibold text-fg tabular">
                {fmtNum(focusedCard?.usage)} <span className="text-[11px] font-normal text-muted">{focusedUnit}</span>
              </span>
            </div>

            {focusedCard && (
              <EnergyStructureChart
                slices={focusedSlices}
                total={focusedCard.usage}
                unit={focusedCard.unit}
                layout="stack"
              />
            )}
          </div>

          {/* Hairline-separated 5-Energy Independent Summary inside the SAME card (no extra card!) */}
          <div className="mt-5 border-t border-line pt-3.5">
            <div className="mb-2 flex items-center justify-between text-[11px] text-muted">
              <span className="font-medium text-fg-2">五能源独立计量总览（按各自单位分列）</span>
              <span>点击切换主图</span>
            </div>
            <div className="divide-y divide-line text-[12px] tabular">
              {energies.map((card) => {
                const isSel = card.energy === focusedCard?.energy;
                return (
                  <button
                    key={card.energy}
                    type="button"
                    onClick={() => setFocusEnergy(card.energy)}
                    className={`flex w-full items-center justify-between gap-2 px-1.5 py-1.5 text-left transition-colors ${
                      isSel ? 'bg-primary-soft/60 font-medium text-primary' : 'text-fg-2 hover:bg-fill-1'
                    }`}
                  >
                    <span className="flex items-center gap-2">
                      <span className={`size-1.5 rounded-full ${isSel ? 'bg-primary' : 'bg-line-strong'}`} />
                      <span>{energyLabels[card.energy] ?? card.energy}</span>
                    </span>
                    <span className="flex items-baseline gap-2">
                      <span className="font-semibold text-fg">
                        {fmtNum(card.usage)} <span className="text-[11px] font-normal text-muted">{card.unit}</span>
                      </span>
                      <span className="w-10 text-right text-[11px] text-muted">{fmtPct(card.coverage.rate)}</span>
                    </span>
                  </button>
                );
              })}
            </div>
          </div>
        </section>
      </div>

      {/* ── 3. Secondary Analytical Stage: Target Execution (7/8 cols) + Dedicated Anomaly & Data Quality Panel (5/4 cols) ── */}
      <div className="grid gap-4 lg:grid-cols-12">
        <section className="arco-card p-5 lg:col-span-7 2xl:col-span-8">
          <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
            <div>
              <h2 className="text-[15px] font-medium leading-[22px] text-fg">
                {targetExtra?.year ?? 2026} 年度{focusedLabel}目标月度执行进度
              </h2>
              <p className="mt-0.5 text-[12px] text-muted tabular">
                {annualTarget != null
                  ? `年度总目标 ${fmtNum(annualTarget)} ${focusedUnit} · 已完结 ${completedMonths.length} 个月累计 ${fmtNum(ytdUsage)} ${focusedUnit}（超目标月份 ${overTargetMonths} 个）`
                  : '当前能源尚未配置年度配额目标'}
              </p>
            </div>
          </div>
          <TargetComparisonChart months={targetMonths} unit={focusedUnit} height={218} />
        </section>

        <section aria-label="异常告警与数据质量独立分析" className="arco-card flex flex-col justify-between p-5 lg:col-span-5 2xl:col-span-4">
          <div className="space-y-4">
            <div className="flex items-center justify-between gap-2">
              <div className="flex items-center gap-2">
                <ShieldAlert className="h-4 w-4 text-warning" aria-hidden="true" />
                <h2 className="text-[15px] font-medium leading-[22px] text-fg">异常告警与数据质量分析</h2>
              </div>
              <button
                type="button"
                onClick={() => onNavigateAnomaly(focusedCard?.energy ?? 'electricity')}
                className="inline-flex items-center gap-1 text-[12px] font-medium text-primary hover:underline"
              >
                进入 F10 诊断
                <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
              </button>
            </div>

            {/* Anomaly Verdict Strip */}
            <div
              className={`rounded-[4px] border px-3.5 py-2.5 text-[12px] tabular ${
                anomalyExtra?.status === 'usage_candidate' || anomalyExtra?.status === 'metering_anomaly'
                  ? 'border-warning/35 bg-warning-soft/70'
                  : 'border-line bg-fill-1/60'
              }`}
            >
              <div className="flex items-center justify-between gap-2">
                <span className="font-medium text-fg">
                  {focusedLabel}异常判定：{anomalyExtra ? (statusLabels[anomalyExtra.status] ?? anomalyExtra.status) : '检测中…'}
                </span>
                {anomalyExtra?.changeRate != null && (
                  <span
                    className={`font-semibold ${
                      anomalyExtra.changeRate > 0 ? 'text-warning' : 'text-positive'
                    }`}
                  >
                    {anomalyExtra.changeRate > 0 ? '+' : ''}
                    {fmtPct(anomalyExtra.changeRate)}
                  </span>
                )}
              </div>
              <p className="mt-1 text-[11px] text-fg-2">
                {anomalyExtra?.baseline != null
                  ? `当日实测 ${fmtNum(anomalyExtra.current)} ${focusedUnit}，较近 ${anomalyExtra.baselineCount} 个同星期完整日基线 ${fmtNum(anomalyExtra.baseline)} ${focusedUnit} 偏差 ${anomalyExtra.delta != null && anomalyExtra.delta > 0 ? '+' : ''}${fmtNum(anomalyExtra.delta)} ${focusedUnit}。`
                  : '当前范围未检出超出规则阈值的用能偏差。'}
              </p>
            </div>

            {/* 5-Energy Data Quality & Coverage Diagnostics */}
            <div>
              <div className="mb-1.5 flex items-center justify-between text-[11px] text-muted">
                <span className="font-medium text-fg-2">五能源数据覆盖率与采集质量</span>
                <span>{lowCovEnergies.length === 0 ? '全能源 100% 完整' : `${lowCovEnergies.length} 项存在缺测`}</span>
              </div>
              <div className="grid grid-cols-5 gap-1.5 tabular">
                {energies.map((e) => {
                  const ok = (e.coverage.rate ?? 0) >= 0.999 && e.coverage.suspect === 0;
                  return (
                    <div
                      key={e.energy}
                      className="rounded-[2px] border border-line bg-fill-1/50 px-2 py-1.5 text-center"
                    >
                      <div className="text-[11px] text-fg-2">{energyLabels[e.energy] ?? e.energy}</div>
                      <div className={`mt-0.5 text-[12px] font-semibold ${ok ? 'text-positive' : 'text-warning'}`}>
                        {fmtPct(e.coverage.rate)}
                      </div>
                      <div className="text-[10px] text-muted">
                        缺 {e.coverage.missing} · 疑 {e.coverage.suspect}
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>

            {/* Metering Exception Events */}
            <div>
              <div className="mb-1.5 flex items-center justify-between text-[11px] text-muted">
                <span className="font-medium text-fg-2">采集与质量异常事件</span>
                <span className="tabular">{combinedExceptions.length} 类终端告警</span>
              </div>
              {combinedExceptions.length === 0 ? (
                <div className="flex items-center gap-2 rounded-[2px] bg-fill-1/60 px-3 py-2 text-[12px] text-positive">
                  <CheckCircle2 className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                  <span>当前统计窗口内无终端掉线、补录或质量规则违例记录</span>
                </div>
              ) : (
                <ul className="divide-y divide-line rounded-[2px] border border-line text-[12px] tabular">
                  {combinedExceptions.slice(0, 4).map((ex, i) => (
                    <li key={`${ex.terminalId}-${ex.errorType}-${i}`} className="flex items-center justify-between gap-2 px-3 py-1.5">
                      <span className="min-w-0 truncate">
                        <span className="font-mono text-[11px] font-medium text-fg">{ex.terminalId}</span>
                        <span className="ml-2 text-[11px] text-muted">{ex.errorType}</span>
                      </span>
                      <span className="shrink-0 rounded-[2px] bg-warning-soft px-1.5 py-0 text-[11px] font-medium text-warning">
                        {ex.count} 次
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </div>
        </section>
      </div>

      {/* ── 4. On-Demand Collapsible Source Trace Evidence ── */}
      <SourceTraceTable sources={allSources} />
    </div>
  );
}

// ── F02: Energy Structure & Contribution Analysis (ECharts Interactive Treemap) ──

function LiveCompositionView({
  data,
  selectedEnergy,
  token,
  inventory,
  onOpenConfig,
  onNavigateClassification,
}: {
  data: CompositionData;
  selectedEnergy: string;
  token: string;
  inventory: InventoryData | null;
  onOpenConfig: () => void;
  onNavigateClassification: (energy: string) => void;
}) {
  const energies = data.energies ?? [];
  const [drilledCatId, setDrilledCatId] = useState<string | null>(null);
  const [sourceDrawerCatId, setSourceDrawerCatId] = useState<string | null>(null);
  const [prevComposition, setPrevComposition] = useState<CompositionData | null>(null);

  const anchorDate = fmtShanghaiDate(data.from);
  const prevDateLabel = shiftShanghaiDate(anchorDate, -1);

  useEffect(() => {
    setDrilledCatId(null);
    setSourceDrawerCatId(null);
  }, [selectedEnergy, data.buildingId, anchorDate]);

  // Fetch previous day's server-computed composition for real structural share change (Δpp) & growth contribution.
  useEffect(() => {
    if (data.configurationState === 'unconfigured' || !data.buildingId || !anchorDate || anchorDate === '—') {
      setPrevComposition(null);
      return;
    }
    const prevDate = shiftShanghaiDate(anchorDate, -1);
    const ctrl = new AbortController();
    const qs = new URLSearchParams({ building: data.buildingId, date: prevDate });
    liveGet<CompositionData>(`/composition?${qs}`, token, ctrl.signal)
      .then((res) => {
        if (!ctrl.signal.aborted) setPrevComposition(res.data ?? null);
      })
      .catch(() => {
        if (!ctrl.signal.aborted) setPrevComposition(null);
      });
    return () => ctrl.abort();
  }, [data.configurationState, data.buildingId, anchorDate, token]);

  if (data.configurationState === 'unconfigured') {
    return <UnconfiguredNotice message={data.configurationMessage} onOpenConfig={onOpenConfig} />;
  }

  const focusedCard = energies.find((e) => e.energy === selectedEnergy) ?? energies[0] ?? null;
  const focusedLabel = focusedCard ? (energyLabels[focusedCard.energy] ?? focusedCard.energy) : '用电';
  const prevFocusedCard = (prevComposition?.energies ?? []).find((e) => e.energy === focusedCard?.energy) ?? null;

  // Map confirmed category bindings to attach ONLY real bound metering points as Treemap children.
  const bindingMap = useMemo(() => {
    const map = new Map<string, Array<{ categoryId: string; weightPpm: number; revision: string }>>();
    for (const b of inventory?.categoryBindings ?? []) {
      map.set(
        `${b.terminalId}::${b.itemCode}`,
        (b.allocations ?? []).map((a) => ({ categoryId: a.categoryId, weightPpm: a.weightPpm, revision: b.revision })),
      );
    }
    return map;
  }, [inventory?.categoryBindings]);

  const categoryRows = useMemo(() => {
    if (!focusedCard) return [];
    const prevCatMap = new Map((prevFocusedCard?.categories ?? []).map((c) => [c.id, c]));
    const sources = focusedCard.sources ?? [];

    const buildBoundLeaves = (catId: string) =>
      sources
        .map((s) => {
          const allocs = bindingMap.get(`${s.terminalId}::${s.itemCode}`) ?? [];
          if (catId === '__unclassified__') {
            const sumPpm = allocs.reduce((acc, a) => acc + a.weightPpm, 0);
            const remPpm = Math.max(0, 1_000_000 - sumPpm);
            if (remPpm <= 0) return null;
            const allocUsage = (s.usage ?? 0) * (remPpm / 1_000_000);
            return {
              id: `${s.terminalId}::${s.itemCode}`,
              name: s.terminalName,
              terminalId: s.terminalId,
              sourceTable: s.sourceTable,
              value: allocUsage,
              weightPct: remPpm / 10_000,
              valid: s.valid,
              expected: s.expected,
            };
          }
          const hit = allocs.find((a) => a.categoryId === catId);
          if (!hit || hit.weightPpm <= 0) return null;
          return {
            id: `${s.terminalId}::${s.itemCode}`,
            name: s.terminalName,
            terminalId: s.terminalId,
            sourceTable: s.sourceTable,
            value: (s.usage ?? 0) * (hit.weightPpm / 1_000_000),
            weightPct: hit.weightPpm / 10_000,
            valid: s.valid,
            expected: s.expected,
          };
        })
        .filter((x): x is NonNullable<typeof x> => x != null && x.value > 0);

    const rows: TreemapCategoryNode[] = (focusedCard.categories ?? []).map((c) => {
      const prev = prevCatMap.get(c.id);
      const shareDiffPp = c.share != null && prev?.share != null ? (c.share - prev.share) * 100 : null;
      const usageDelta = c.usage != null && prev?.usage != null ? c.usage - prev.usage : null;
      return {
        id: c.id,
        name: c.name,
        value: c.usage ?? 0,
        share: c.share,
        shareDiffPp,
        usageDelta,
        coverageRate: c.coverage.rate,
        isUnclassified: false,
        children: buildBoundLeaves(c.id),
      };
    });

    if (focusedCard.unclassifiedUsage && focusedCard.unclassifiedUsage > 0) {
      const share = focusedCard.usage ? focusedCard.unclassifiedUsage / focusedCard.usage : null;
      const prevUnclass = prevFocusedCard?.unclassifiedUsage ?? null;
      const prevShare = prevUnclass != null && prevFocusedCard?.usage ? prevUnclass / prevFocusedCard.usage : null;
      rows.push({
        id: '__unclassified__',
        name: '未分项（待分配）',
        value: focusedCard.unclassifiedUsage,
        share,
        shareDiffPp: share != null && prevShare != null ? (share - prevShare) * 100 : null,
        usageDelta: prevUnclass != null ? focusedCard.unclassifiedUsage - prevUnclass : null,
        coverageRate: focusedCard.coverage.rate,
        isUnclassified: true,
        children: buildBoundLeaves('__unclassified__'),
      });
    }

    return rows.sort((a, b) => (a.isUnclassified ? 1 : b.isUnclassified ? -1 : b.value - a.value));
  }, [focusedCard, prevFocusedCard, bindingMap]);

  const hasComparablePrev = Boolean(
    prevFocusedCard && prevFocusedCard.usage != null && prevFocusedCard.usage > 0,
  );
  const topSystem = categoryRows.find((r) => !r.isUnclassified) ?? categoryRows[0] ?? null;
  const totalDelta =
    hasComparablePrev && focusedCard?.usage != null && prevFocusedCard?.usage != null
      ? focusedCard.usage - prevFocusedCard.usage
      : null;
  const positiveGrowers = hasComparablePrev
    ? categoryRows
        .filter((r) => r.usageDelta != null && r.usageDelta > 0.01)
        .sort((a, b) => (b.usageDelta ?? 0) - (a.usageDelta ?? 0))
    : [];
  const topGrowthDriver = positiveGrowers[0] ?? null;
  const growthContribRatio =
    topGrowthDriver?.usageDelta != null && totalDelta != null && totalDelta > 0
      ? Math.min(1, topGrowthDriver.usageDelta / totalDelta)
      : null;

  const topShareMover = hasComparablePrev
    ? categoryRows
        .filter((r) => r.shareDiffPp != null)
        .sort((a, b) => Math.abs(b.shareDiffPp ?? 0) - Math.abs(a.shareDiffPp ?? 0))[0] ?? null
    : null;

  const drilledNode = drilledCatId ? categoryRows.find((r) => r.id === drilledCatId) ?? null : null;
  const drawerNode = sourceDrawerCatId ? categoryRows.find((r) => r.id === sourceDrawerCatId) ?? null : null;

  // Clicking a category tile drills smoothly into its real bound meters INSIDE the Treemap;
  // only categories with zero confirmed meter bindings open the provenance drawer directly to explain why.
  const handleTreemapClick = (catId: string | null) => {
    if (!catId) {
      setDrilledCatId(null);
      return;
    }
    const target = categoryRows.find((r) => r.id === catId);
    if (!target) return;
    if (target.children.length > 0) {
      setDrilledCatId(catId);
    } else {
      setSourceDrawerCatId(catId);
    }
  };

  return (
    <div className="space-y-3">
      {/* Main Product Workspace: Interactive Treemap (Left 8/9 cols) + Concise 3-Finding Insights (Right 4/3 cols) */}
      <div className="grid items-stretch gap-4 lg:grid-cols-12">
        <section aria-label="单一能源分项构成 Treemap" className="arco-card flex flex-col p-5 lg:col-span-8 xl:col-span-9">
          <div className="mb-3.5 flex flex-wrap items-center justify-between gap-2">
            <div className="flex flex-wrap items-center gap-2 text-[13px] tabular">
              <button
                type="button"
                onClick={() => setDrilledCatId(null)}
                className={`font-semibold transition-colors ${
                  drilledNode ? 'text-primary hover:underline' : 'text-fg'
                }`}
              >
                {data.buildingName} · {focusedLabel}分项构成
              </button>
              {drilledNode && (
                <>
                  <span className="text-muted">/</span>
                  <span className="font-semibold text-fg">{drilledNode.name}（已绑定计量点）</span>
                </>
              )}
              <span className="rounded-[2px] bg-fill-2 px-1.5 py-0 text-[11px] font-medium text-fg-2">
                {fmtNum(focusedCard?.usage)} {focusedCard?.unit}
              </span>
              <span className="text-[11px] text-muted">
                覆盖 {fmtPct(focusedCard?.coverage.rate)} · 自然日 {anchorDate}
              </span>
            </div>

            <div className="flex items-center gap-2 text-[11px]">
              {drilledNode && (
                <>
                  <button
                    type="button"
                    data-f02-open-drilled-source
                    onClick={() => setSourceDrawerCatId(drilledNode.id)}
                    className="rounded-[2px] border border-primary/40 bg-primary-soft/50 px-2.5 py-1 font-medium text-primary hover:bg-primary-soft"
                  >
                    查看溯源明细
                  </button>
                  <button
                    type="button"
                    data-treemap-back
                    onClick={() => setDrilledCatId(null)}
                    className="rounded-[2px] border border-line bg-surface px-2.5 py-1 font-medium text-fg-2 hover:bg-fill-1"
                  >
                    返回全部分项
                  </button>
                </>
              )}
              {!drilledNode && (
                <span className="text-muted">单一物理单位 ({focusedCard?.unit}) 独立构成 · 点击系统块下钻</span>
              )}
            </div>
          </div>

          <div className="flex-1">
            <EnergyTreemapChart
              key={`${focusedCard?.energy ?? selectedEnergy}-${drilledCatId ?? 'root'}`}
              nodes={categoryRows}
              unit={focusedCard?.unit ?? ''}
              energyLabel={focusedLabel}
              focusedCategoryId={drilledCatId}
              onSelectCategory={handleTreemapClick}
              onSelectMeter={(catId) => setSourceDrawerCatId(catId)}
              height={520}
            />
          </div>
        </section>

        {/* Right Streamlined Analytical Insights (Strictly 3 Findings — Zero KPI Grid / Carbon Wall / Bottom Table) */}
        <aside aria-label="构成变化核心发现" className="arco-card flex flex-col justify-between p-5 lg:col-span-4 xl:col-span-3 tabular">
          <div className="space-y-4">
            <div className="border-b border-line pb-2.5">
              <h2 className="text-[14px] font-semibold text-fg">分析发现</h2>
              <p className="mt-0.5 text-[11px] text-muted">
                {hasComparablePrev
                  ? `基于 ${anchorDate} 与前一日 ${prevDateLabel} 实测构成比对`
                  : `${anchorDate} · 无可比前日实测数据`}
              </p>
            </div>

            {/* Finding 1: Dominant System */}
            <div className="rounded-[4px] border border-line bg-fill-1/35 p-3.5">
              <div className="flex items-center justify-between text-[11px] text-muted">
                <span>最大用能系统</span>
                {topSystem?.share != null && (
                  <span className="font-semibold text-primary">占 {fmtPct(topSystem.share)}</span>
                )}
              </div>
              <p className="mt-1.5 text-[15px] font-semibold text-fg">{topSystem?.name ?? '—'}</p>
              <p className="mt-1 text-[12px] text-fg-2">
                {topSystem ? `${fmtNum(topSystem.value)} ${focusedCard?.unit}` : '暂无实测分项'}
              </p>
              {topSystem && (
                <button
                  type="button"
                  onClick={() => setSourceDrawerCatId(topSystem.id)}
                  className="mt-2 inline-flex items-center gap-1 text-[11px] font-medium text-primary hover:underline"
                >
                  查看计量来源
                  <ArrowRight className="h-3 w-3" aria-hidden="true" />
                </button>
              )}
            </div>

            {/* Finding 2: Top Growth Contributor */}
            <div className="rounded-[4px] border border-line bg-fill-1/35 p-3.5">
              <div className="flex items-center justify-between text-[11px] text-muted">
                <span>增长贡献最大分项</span>
                {growthContribRatio != null && (
                  <span className="font-medium text-warning">贡献 {fmtPct(growthContribRatio)}</span>
                )}
              </div>
              {!hasComparablePrev ? (
                <p className="mt-1.5 text-[12px] text-muted">无可比前日实测数据，不输出增长归因</p>
              ) : topGrowthDriver ? (
                <>
                  <p className="mt-1.5 text-[15px] font-semibold text-fg">{topGrowthDriver.name}</p>
                  <p className="mt-1 text-[12px] font-medium text-negative">
                    较前日 +{fmtNum(topGrowthDriver.usageDelta)} {focusedCard?.unit}
                  </p>
                  <button
                    type="button"
                    onClick={() => setSourceDrawerCatId(topGrowthDriver.id)}
                    className="mt-2 inline-flex items-center gap-1 text-[11px] font-medium text-primary hover:underline"
                  >
                    下钻增长计量来源
                    <ArrowRight className="h-3 w-3" aria-hidden="true" />
                  </button>
                </>
              ) : (
                <p className="mt-1.5 text-[12px] text-positive">
                  较前日整体下降或持平，无明显正向增长分项
                </p>
              )}
            </div>

            {/* Finding 3: Largest Structural Share Shift (pp) */}
            <div className="rounded-[4px] border border-line bg-fill-1/35 p-3.5">
              <div className="flex items-center justify-between text-[11px] text-muted">
                <span>结构占比变化最大分项</span>
                <span className="text-[10px]">单位：百分点</span>
              </div>
              {topShareMover && topShareMover.shareDiffPp != null ? (
                <>
                  <p className="mt-1.5 text-[15px] font-semibold text-fg">{topShareMover.name}</p>
                  <p
                    className={`mt-1 text-[14px] font-semibold ${
                      topShareMover.shareDiffPp > 0 ? 'text-warning' : topShareMover.shareDiffPp < 0 ? 'text-positive' : 'text-fg-2'
                    }`}
                  >
                    {topShareMover.shareDiffPp > 0 ? '+' : ''}
                    {topShareMover.shareDiffPp.toFixed(1)} pp
                    <span className="ml-1.5 text-[11px] font-normal text-muted">
                      （现占 {fmtPct(topShareMover.share)}）
                    </span>
                  </p>
                  <button
                    type="button"
                    data-f02-drill-mover
                    onClick={() => setSourceDrawerCatId(topShareMover.id)}
                    className="mt-2 inline-flex items-center gap-1 text-[11px] font-medium text-primary hover:underline"
                  >
                    从此分项下钻计量来源
                    <ArrowRight className="h-3 w-3" aria-hidden="true" />
                  </button>
                </>
              ) : (
                <p className="mt-1.5 text-[12px] text-muted">无可比前日实测数据，不计算结构变动</p>
              )}
            </div>
          </div>

          <div className="mt-4 border-t border-line pt-3">
            <button
              type="button"
              onClick={() => onNavigateClassification(focusedCard?.energy ?? selectedEnergy)}
              className="inline-flex w-full items-center justify-between rounded-[4px] border border-line bg-surface px-3 py-2 text-[12px] font-medium text-fg-2 hover:bg-fill-1 hover:text-primary"
            >
              <span>在 F06 查看深层分类树与权重表</span>
              <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
            </button>
          </div>
        </aside>
      </div>

      {/* On-Demand Right Slide-Over Metering Source Drawer */}
      {drawerNode && focusedCard && createPortal(
        <div className="fixed inset-0 z-50 flex justify-end bg-slate-900/20" onClick={() => setSourceDrawerCatId(null)}>
        <div
          role="dialog"
          aria-label="分项计量来源详情"
          data-f02-source-drawer
          onClick={(e) => e.stopPropagation()}
          className="flex h-full w-full max-w-[420px] flex-col border-l border-line bg-surface shadow-2xl tabular"
        >
          <div className="flex items-start justify-between gap-3 border-b border-line px-5 py-4">
            <div>
              <span className="text-[11px] text-muted">{focusedLabel} · 分项计量溯源</span>
              <h3 className="mt-0.5 text-[15px] font-semibold text-fg">{drawerNode.name}</h3>
            </div>
            <button
              type="button"
              onClick={() => setSourceDrawerCatId(null)}
              aria-label="关闭计量来源抽屉"
              className="rounded-[4px] p-1 text-muted hover:bg-fill-1 hover:text-fg"
            >
              <X className="h-4 w-4" aria-hidden="true" />
            </button>
          </div>

          <div className="flex-1 space-y-4 overflow-y-auto p-5 text-[12px]">
            <div className="grid grid-cols-2 gap-3 rounded-[4px] bg-fill-1/60 p-3.5">
              <div>
                <span className="text-[11px] text-muted">分项实测</span>
                <p className="mt-0.5 text-[16px] font-semibold text-fg">
                  {fmtNum(drawerNode.value)} {focusedCard.unit}
                </p>
              </div>
              <div>
                <span className="text-[11px] text-muted">占比 / 较前日变动</span>
                <p className="mt-0.5 text-[14px] font-semibold text-fg">
                  {fmtPct(drawerNode.share)}
                  {drawerNode.shareDiffPp != null && (
                    <span className={`ml-1.5 text-[12px] ${drawerNode.shareDiffPp > 0 ? 'text-warning' : 'text-positive'}`}>
                      ({drawerNode.shareDiffPp > 0 ? '+' : ''}{drawerNode.shareDiffPp.toFixed(1)} pp)
                    </span>
                  )}
                </p>
              </div>
            </div>

            <div>
              <div className="mb-2 flex items-center justify-between text-[11px] text-muted">
                <span className="font-medium text-fg-2">真实绑定计量点（{drawerNode.children.length} 块）</span>
                <span>按已确认分类权重分摊</span>
              </div>
              {drawerNode.children.length === 0 ? (
                <p className="rounded-[4px] border border-dashed border-line bg-fill-1/40 px-3 py-4 text-center text-[11px] text-muted">
                  当前分项无已确认的下级计量点绑定关系，不可继续下钻。
                </p>
              ) : (
                <ul className="divide-y divide-line rounded-[4px] border border-line">
                  {drawerNode.children.map((leaf) => (
                    <li key={leaf.id} className="px-3.5 py-2.5">
                      <div className="flex items-center justify-between gap-2">
                        <span className="font-medium text-fg">{leaf.name}</span>
                        <span className="font-semibold text-fg">
                          {fmtNum(leaf.value)} {focusedCard.unit}
                        </span>
                      </div>
                      <div className="mt-1 flex items-center justify-between text-[11px] text-muted">
                        <span className="font-mono">{leaf.terminalId} · {leaf.sourceTable}</span>
                        <span>权重 {leaf.weightPct.toFixed(0)}% · 有效 {leaf.valid}/{leaf.expected}</span>
                      </div>
                    </li>
                  ))}
                </ul>
              )}
            </div>
          </div>

          <div className="border-t border-line px-5 py-3">
            <button
              type="button"
              onClick={() => onNavigateClassification(focusedCard.energy)}
              className="inline-flex w-full items-center justify-center gap-1.5 rounded-[4px] bg-primary px-4 py-2 text-[12px] font-medium text-white hover:bg-primary/90"
            >
              <span>前往 F06 查看完整分类树与多日趋势</span>
              <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
            </button>
          </div>
        </div>
        </div>,
        document.body,
      )}
    </div>
  );
}

// ── F03: Space, Organization & Sub-Item Linked Overview ─────────────────────

function LiveObjectsOverviewView({
  data,
  token,
  inventory,
  date,
  onSelectBuilding,
}: {
  data: ObjectsOverviewData;
  token: string;
  inventory: InventoryData | null;
  date: string;
  onSelectBuilding: (buildingId: string) => void;
}) {
  const [activeTab, setActiveTab] = useState<'space' | 'org' | 'building'>('space');
  const [selectedObjId, setSelectedObjId] = useState<string | null>(null);
  const [classificationExtra, setClassificationExtra] = useState<ClassificationData | null>(null);
  const [buildingDimExtra, setBuildingDimExtra] = useState<DimensionsData | null>(null);

  useEffect(() => {
    setSelectedObjId(null);
  }, [data.buildingId, data.energy, activeTab]);

  useEffect(() => {
    if (data.configurationState === 'unconfigured' || !data.buildingId) return;
    const ctrl = new AbortController();
    const qsClass = new URLSearchParams({ building: data.buildingId, energy: data.energy });
    const qsBldg = new URLSearchParams({ view: 'building', energy: data.energy });
    if (date) {
      qsClass.set('date', date);
      qsBldg.set('date', date);
    }
    Promise.all([
      liveGet<ClassificationData>(`/classification?${qsClass}`, token, ctrl.signal).catch(() => ({ data: null })),
      liveGet<DimensionsData>(`/dimensions?${qsBldg}`, token, ctrl.signal).catch(() => ({ data: null })),
    ]).then(([cls, bldg]) => {
      if (ctrl.signal.aborted) return;
      setClassificationExtra(cls.data ?? null);
      setBuildingDimExtra(bldg.data ?? null);
    });
    return () => ctrl.abort();
  }, [data.configurationState, data.buildingId, data.energy, date, token]);

  if (data.configurationState === 'unconfigured') {
    return <UnconfiguredNotice message={data.configurationMessage} />;
  }

  const spaceRows = data.spaceRows ?? [];
  const orgRows = data.orgRows ?? [];

  // Build parent-child spatial hierarchy from inventory.spaces (prevents double-counting parent + child!).
  const spaceProfileMap = useMemo(
    () => new Map((inventory?.spaces ?? []).map((s) => [s.spaceId, s])),
    [inventory?.spaces],
  );

  const spaceHierarchy = useMemo(() => {
    const rowMap = new Map(spaceRows.map((r) => [r.id, r]));
    const childIdsByParent = new Map<string, string[]>();
    for (const r of spaceRows) {
      const parentId = spaceProfileMap.get(r.id)?.parentSpaceId;
      if (parentId && rowMap.has(parentId)) {
        const arr = childIdsByParent.get(parentId) ?? [];
        arr.push(r.id);
        childIdsByParent.set(parentId, arr);
      }
    }
    const topSpaces = spaceRows
      .filter((r) => {
        const pid = spaceProfileMap.get(r.id)?.parentSpaceId;
        return !pid || !rowMap.has(pid);
      })
      .map((r) => {
        const children = (childIdsByParent.get(r.id) ?? [])
          .map((cid) => rowMap.get(cid)!)
          .sort((a, b) => (b.usage ?? 0) - (a.usage ?? 0));
        const directUsage = r.usage ?? 0;
        const childrenUsage = children.reduce((acc, c) => acc + (c.usage ?? 0), 0);
        const subtreeUsage = r.usage != null || children.length > 0 ? directUsage + childrenUsage : null;
        return {
          row: r,
          profile: spaceProfileMap.get(r.id) ?? null,
          children: children.map((c) => ({ row: c, profile: spaceProfileMap.get(c.id) ?? null })),
          subtreeUsage,
        };
      })
      .sort((a, b) => (b.subtreeUsage ?? 0) - (a.subtreeUsage ?? 0));

    return topSpaces;
  }, [spaceRows, spaceProfileMap]);

  const directSpaceSum = spaceRows.reduce((acc, r) => acc + (r.usage ?? 0), 0);
  const unassignedSpaceDiff =
    data.buildingTotal != null ? data.buildingTotal - directSpaceSum : null;
  const hasUnassignedResidual = unassignedSpaceDiff != null && Math.abs(unassignedSpaceDiff) > 0.05;
  const lowCovSpaces = spaceRows.filter((r) => (r.coverage.rate ?? 0) < 0.999 || r.status !== 'comparable');

  // Identify meters belonging to the current building to separate local building orgs from cross-building peer orgs.
  const buildingTerminalSet = useMemo(() => {
    const set = new Set<string>();
    for (const m of inventory?.meters ?? []) {
      if (m.boundBuildingId === data.buildingId) set.add(m.terminalId);
    }
    for (const r of spaceRows) {
      for (const s of r.sources ?? []) set.add(s.terminalId);
    }
    return set;
  }, [inventory?.meters, data.buildingId, spaceRows]);

  const localOrgRows = useMemo(
    () =>
      orgRows.filter((o) =>
        (o.sources ?? []).some((s) => buildingTerminalSet.has(s.terminalId)),
      ),
    [orgRows, buildingTerminalSet],
  );
  const peerOrgRows = useMemo(
    () => orgRows.filter((o) => !localOrgRows.some((lo) => lo.id === o.id)),
    [orgRows, localOrgRows],
  );

  // Category & binding lookup for Space/Org <-> Sub-Item linkage.
  const catNameMap = useMemo(() => {
    const map = new Map<string, string>();
    for (const c of inventory?.categories ?? []) map.set(c.categoryId, c.name);
    for (const c of classificationExtra?.categories ?? []) map.set(c.id, c.name);
    return map;
  }, [inventory?.categories, classificationExtra?.categories]);

  const bindingMap = useMemo(() => {
    const map = new Map<string, Array<{ categoryId: string; weightPpm: number }>>();
    for (const b of inventory?.categoryBindings ?? []) {
      map.set(`${b.terminalId}::${b.itemCode}`, b.allocations ?? []);
    }
    return map;
  }, [inventory?.categoryBindings]);

  const selectedObj: DimensionRow | null = useMemo(() => {
    if (!selectedObjId) return null;
    return (
      spaceRows.find((r) => r.id === selectedObjId) ??
      orgRows.find((r) => r.id === selectedObjId) ??
      null
    );
  }, [selectedObjId, spaceRows, orgRows]);

  // Derive linked sub-item breakdown for the selected space or organization strictly from its bound meters & category weights.
  const selectedObjBreakdown = useMemo(() => {
    if (!selectedObj) return null;
    const catTotals = new Map<string, number>();
    let unallocated = 0;
    let ambiguousWeight = false;
    for (const src of selectedObj.sources ?? []) {
      const u = src.usage ?? 0;
      const allocs = bindingMap.get(`${src.terminalId}::${src.itemCode}`) ?? [];
      const sumPpm = allocs.reduce((acc, a) => acc + a.weightPpm, 0);
      if (allocs.length === 0 || sumPpm === 0) {
        unallocated += u;
        ambiguousWeight = true;
      } else {
        for (const a of allocs) {
          const share = a.weightPpm / 1_000_000;
          catTotals.set(a.categoryId, (catTotals.get(a.categoryId) ?? 0) + u * share);
        }
        if (sumPpm < 1_000_000) {
          unallocated += u * ((1_000_000 - sumPpm) / 1_000_000);
          ambiguousWeight = true;
        }
      }
    }
    const slices = Array.from(catTotals.entries()).map(([cid, usage]) => ({
      id: cid,
      name: catNameMap.get(cid) ?? cid,
      usage,
      share: selectedObj.usage && selectedObj.usage > 0 ? usage / selectedObj.usage : null,
      isUnclassified: false,
    }));
    if (unallocated > 0.001) {
      slices.push({
        id: '__unclassified__',
        name: '未分配分项/权重待明确',
        usage: unallocated,
        share: selectedObj.usage && selectedObj.usage > 0 ? unallocated / selectedObj.usage : null,
        isUnclassified: true,
      });
    }
    return { slices, ambiguousWeight };
  }, [selectedObj, bindingMap, catNameMap]);

  const topSpace = [...spaceRows].sort((a, b) => (b.usage ?? 0) - (a.usage ?? 0))[0] ?? null;

  const buildingSlices = [
    ...(classificationExtra?.categories ?? []).map((c) => ({ id: c.id, name: c.name, usage: c.usage, share: c.share })),
    ...(classificationExtra?.unclassifiedUsage && classificationExtra.unclassifiedUsage > 0
      ? [
          {
            id: '__unclassified__',
            name: '未分项',
            usage: classificationExtra.unclassifiedUsage,
            share: classificationExtra.totalUsage ? classificationExtra.unclassifiedUsage / classificationExtra.totalUsage : null,
            isUnclassified: true,
          },
        ]
      : []),
  ];

  const spatialMapNodes = useMemo(
    () =>
      spaceHierarchy.map(({ row, profile, children, subtreeUsage }) => ({
        id: row.id,
        name: row.name,
        usage: row.usage,
        areaSqm: profile?.areaSqm ?? null,
        coverageRate: row.coverage.rate,
        comparable: row.status === 'comparable' && (row.coverage.rate ?? 0) >= 0.999,
        meterCount: row.sources?.length ?? 0,
        subtreeUsage,
        children: children.map(({ row: c, profile: cp }) => ({
          id: c.id,
          name: c.name,
          usage: c.usage,
          areaSqm: cp?.areaSqm ?? null,
          coverageRate: c.coverage.rate,
          comparable: c.status === 'comparable' && (c.coverage.rate ?? 0) >= 0.999,
          meterCount: c.sources?.length ?? 0,
        })),
      })),
    [spaceHierarchy],
  );

  const allSources = spaceRows.flatMap((r) => r.sources ?? []);

  return (
    <div className="space-y-4">
      {/* ── 1. Compact Spatial Reconciliation Header Bar (Zero 4-Box KPI Repetition) ── */}
      <div className="arco-card flex flex-wrap items-center justify-between gap-3 px-5 py-3 text-[12px] tabular">
        <div className="flex flex-wrap items-center gap-3">
          <span className="font-semibold text-fg">
            {data.buildingName} · {energyLabels[data.energy] ?? data.energy}
          </span>
          <span className="text-muted">自然日 {fmtShanghaiDate(data.from)}</span>
          <span className="inline-flex items-baseline gap-1 rounded-[2px] bg-fill-1 px-2 py-0.5">
            <span className="text-[11px] text-muted">建筑总量</span>
            <strong className="text-[13px] font-semibold text-fg">{fmtNum(data.buildingTotal)}</strong>
            <span className="text-[11px] text-muted">{data.unit}</span>
          </span>
          {topSpace && (
            <span className="text-[11px] text-fg-2">
              最高耗能区：<strong className="font-medium text-fg">{topSpace.name}</strong> ({fmtNum(topSpace.usage)} {data.unit})
            </span>
          )}
          <span
            className={`rounded-[2px] px-1.5 py-0.5 text-[10px] font-medium ${
              !hasUnassignedResidual && lowCovSpaces.length === 0
                ? 'bg-positive-soft text-positive'
                : 'bg-warning-soft text-warning'
            }`}
          >
            {!hasUnassignedResidual && lowCovSpaces.length === 0
              ? `直属归属闭合 (${fmtNum(directSpaceSum)} ${data.unit} · 零重复累计)`
              : hasUnassignedResidual
                ? `未归属差额 ${fmtNum(unassignedSpaceDiff)} ${data.unit}`
                : `${lowCovSpaces.length} 个空间存在低覆盖/不可比`}
          </span>
        </div>

        <div className="inline-flex rounded-[2px] border border-line bg-fill-1 p-0.5 text-[12px]">
          {(
            [
              { id: 'space', label: `空间平面层级 (${spaceRows.length})` },
              { id: 'org', label: `关联组织 (${localOrgRows.length}/${orgRows.length})` },
              { id: 'building', label: `园区建筑 (${buildingDimExtra?.rows?.length ?? 3})` },
            ] as const
          ).map((tab) => (
            <button
              key={tab.id}
              type="button"
              data-objects-tab={tab.id}
              onClick={() => setActiveTab(tab.id)}
              className={`rounded-[2px] px-2.5 py-1 transition-colors ${
                activeTab === tab.id ? 'bg-surface font-medium text-primary shadow-xs' : 'text-fg-2 hover:text-fg'
              }`}
            >
              {tab.label}
            </button>
          ))}
        </div>
      </div>

      {/* ── 2. Full-Width 2D Nested Spatial Blueprint Canvas (Distinct Spatial Hierarchy Archetype) ── */}
      <section className="arco-card p-5">
        <div className="mb-3.5 flex flex-wrap items-baseline justify-between gap-2">
          <div>
            <h2 className="text-[15px] font-medium leading-[22px] text-fg">
              {activeTab === 'space'
                ? '建筑空间嵌套平面图谱（父空间包裹下级子空间 · 严格区分直属表计）'
                : activeTab === 'org'
                  ? '关联组织能耗分布与同口径参考'
                  : '园区建筑横向能耗分布对比'}
            </h2>
            <p className="mt-0.5 text-[12px] text-muted">
              {activeTab === 'space'
                ? '点击任意父空间或嵌套子空间区块，下方同步展开该区域的分项用能构成与直属表计清单'
                : activeTab === 'org'
                  ? '左侧为本建筑关联组织，右侧为园区其他建筑同口径参照（不计入本建筑汇总）'
                  : '点击任意建筑直接切换当前分析对象'}
            </p>
          </div>
          {selectedObj && (
            <button
              type="button"
              onClick={() => setSelectedObjId(null)}
              className="rounded-[2px] border border-line px-2.5 py-1 text-[11px] text-fg-2 hover:bg-fill-1"
            >
              清除选中 · 返回整栋分项
            </button>
          )}
        </div>

        {activeTab === 'space' && (
          <div className="space-y-3">
            <SpatialHierarchyMap
              nodes={spatialMapNodes}
              buildingTotal={data.buildingTotal}
              unit={data.unit}
              selectedId={selectedObjId}
              onSelect={(id) => setSelectedObjId((cur) => (cur === id ? null : id))}
            />

            {hasUnassignedResidual && (
              <div className="flex items-center justify-between rounded-[4px] border border-dashed border-warning/50 bg-warning-soft/40 px-4 py-2.5 text-[12px] tabular">
                <span className="font-medium text-warning">未归属空间 / 待核查差额（建筑总量 − 空间直属合计）</span>
                <span className="font-semibold text-warning">
                  {fmtNum(unassignedSpaceDiff)} {data.unit}
                </span>
              </div>
            )}
          </div>
        )}

        {activeTab === 'org' && (
          <div className="grid gap-5 lg:grid-cols-2">
            <div>
              <div className="mb-2 flex items-center justify-between text-[12px]">
                <span className="font-medium text-fg">本建筑关联组织（{data.buildingName}）</span>
                <span className="text-[11px] text-muted">点击条目下方查看分项构成</span>
              </div>
              <HorizontalRankingChart
                unit={data.unit}
                selectedId={selectedObjId}
                onSelect={(id) => setSelectedObjId((cur) => (cur === id ? null : id))}
                items={(localOrgRows.length > 0 ? localOrgRows : orgRows).map((r) => ({
                  id: r.id,
                  name: r.name,
                  usage: r.usage,
                  rank: r.rank,
                  coverageRate: r.coverage.rate,
                  statusText: r.reason || (statusLabels[r.status] ?? r.status),
                }))}
              />
            </div>

            {peerOrgRows.length > 0 && (
              <div className="border-t border-line pt-4 lg:border-t-0 lg:border-l lg:pt-0 lg:pl-5">
                <div className="mb-2 flex items-center justify-between text-[12px]">
                  <span className="font-medium text-fg-2">园区其他建筑同口径组织参考</span>
                  <span className="text-[11px] text-muted">跨建筑参照，不计入本建筑汇总</span>
                </div>
                <HorizontalRankingChart
                  unit={data.unit}
                  selectedId={selectedObjId}
                  onSelect={(id) => setSelectedObjId((cur) => (cur === id ? null : id))}
                  items={peerOrgRows.map((r) => ({
                    id: r.id,
                    name: r.name,
                    usage: r.usage,
                    rank: r.rank,
                    coverageRate: r.coverage.rate,
                    statusText: r.reason || (statusLabels[r.status] ?? r.status),
                  }))}
                />
              </div>
            )}
          </div>
        )}

        {activeTab === 'building' && (
          <HorizontalRankingChart
            unit={buildingDimExtra?.unit ?? data.unit}
            selectedId={data.buildingId}
            onSelect={(bid) => onSelectBuilding(bid)}
            items={(buildingDimExtra?.rows ?? []).map((r) => ({
              id: r.id,
              name: r.name,
              usage: r.usage,
              rank: r.rank,
              coverageRate: r.coverage.rate,
              statusText: r.reason || (statusLabels[r.status] ?? r.status),
            }))}
          />
        )}
      </section>

      {/* ── 3. Inline Contextual Attribution Bar: Selected Zone Sub-Item Breakdown (5 cols) + Bound Meters Grid (7 cols) ── */}
      <section aria-label="联动分项构成与计量归属" className="arco-card p-5 text-[12px] tabular">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-2 border-b border-line pb-3">
          <div className="flex flex-wrap items-center gap-2.5">
            <span className="rounded-[2px] bg-primary-soft px-2 py-0.5 text-[11px] font-medium text-primary">
              {selectedObj ? '已选空间/组织联动归属' : '整栋建筑分项汇总'}
            </span>
            <h3 className="text-[14px] font-semibold text-fg">
              {selectedObj ? selectedObj.name : `${data.buildingName}（全部直属空间）`}
            </h3>
            {selectedObj && (
              <span className="text-[12px] text-muted">
                直属实测 <strong className="text-fg">{fmtNum(selectedObj.usage)} {data.unit}</strong>
                {' · '}覆盖 {fmtPct(selectedObj.coverage.rate)} · {statusLabels[selectedObj.status] ?? selectedObj.status}
              </span>
            )}
          </div>
          {selectedObj && (
            <button
              type="button"
              onClick={() => setSelectedObjId(null)}
              className="rounded-[2px] border border-line px-2.5 py-1 text-[11px] text-fg-2 hover:bg-fill-1"
            >
              返回整栋
            </button>
          )}
        </div>

        <div className="grid gap-5 lg:grid-cols-12">
          <div className="space-y-3 lg:col-span-5">
            <p className="text-[11px] font-medium text-fg-2">对应分项用能构成</p>
            {selectedObj && selectedObjBreakdown ? (
              <>
                {selectedObjBreakdown.ambiguousWeight && (
                  <div className="rounded-[4px] border border-warning/35 bg-warning-soft/60 px-3 py-2 text-[11px] text-warning">
                    存在未绑定分类或分项权重不足 100% 的计量点，已单列为「未分配分项/权重待明确」。
                  </div>
                )}
                <EnergyStructureChart
                  slices={selectedObjBreakdown.slices}
                  totalUsage={selectedObj.usage}
                  unit={data.unit}
                  layout="stack"
                  emptyLabel="该对象关联表具尚未配置分项权重"
                />
              </>
            ) : (
              <EnergyStructureChart
                slices={buildingSlices}
                totalUsage={classificationExtra?.totalUsage ?? data.buildingTotal}
                unit={data.unit}
                layout="stack"
                emptyLabel="暂无分项构成数据"
              />
            )}
          </div>

          <div className="border-t border-line pt-4 lg:col-span-7 lg:border-t-0 lg:border-l lg:pt-0 lg:pl-5">
            <div className="mb-2.5 flex items-center justify-between text-[11px] text-muted">
              <span className="font-medium text-fg-2">
                {selectedObj
                  ? `归属计量点清单（${selectedObj.sources?.length ?? 0} 块表）`
                  : `建筑直属空间计量汇总（${allSources.length} 条来源）`}
              </span>
              <span>点击上方平面图区块即时切换对应表具</span>
            </div>

            {selectedObj ? (
              <div className="grid gap-2.5 sm:grid-cols-2">
                {(selectedObj.sources ?? []).map((s) => (
                  <div key={`${s.terminalId}-${s.itemCode}`} className="rounded-[4px] border border-line bg-fill-1/30 px-3 py-2">
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate font-medium text-fg">{s.terminalName}</span>
                      <span className="shrink-0 font-semibold text-fg">
                        {fmtNum(s.usage)} {s.unit}
                      </span>
                    </div>
                    <div className="mt-1 flex items-center justify-between text-[10px] text-muted">
                      <span className="font-mono">{s.terminalId} · {s.sourceTable}</span>
                      <span>有效 {s.valid}/{s.expected}</span>
                    </div>
                  </div>
                ))}
              </div>
            ) : (
              <div className="grid gap-2.5 sm:grid-cols-2">
                {allSources.slice(0, 6).map((s, idx) => (
                  <div key={`${s.terminalId}-${s.itemCode}-${idx}`} className="rounded-[4px] border border-line bg-fill-1/30 px-3 py-2">
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate font-medium text-fg">{s.terminalName}</span>
                      <span className="shrink-0 font-semibold text-fg">
                        {fmtNum(s.usage)} {s.unit}
                      </span>
                    </div>
                    <div className="mt-1 flex items-center justify-between text-[10px] text-muted">
                      <span className="font-mono">{s.terminalId} · {s.sourceTable}</span>
                      <span>有效 {s.valid}/{s.expected}</span>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      </section>

      <SourceTraceTable sources={allSources} />
    </div>
  );
}

// ── F04: Simulated-Realtime Telemetry, Peak-Valley & Freshness Monitor ──────

function LiveRealtimeTrendView({
  data,
  days,
  isSimulated,
  onChangeDays,
  onManualRefresh,
}: {
  data: RealtimeTrendData;
  days: string;
  isSimulated: boolean;
  onChangeDays: (days: string) => void;
  onManualRefresh: () => void;
}) {
  const [simAutoRefresh, setSimAutoRefresh] = useState<boolean>(false);
  const [lastPulseAt, setLastPulseAt] = useState<string>(() => new Date().toLocaleTimeString('zh-CN', { hour12: false }));

  // Simulated periodic refresh (explicitly non-hardware replay when enabled by operator).
  useEffect(() => {
    if (!simAutoRefresh) return;
    const id = window.setInterval(() => {
      setLastPulseAt(new Date().toLocaleTimeString('zh-CN', { hour12: false }));
      onManualRefresh();
    }, 15_000);
    return () => window.clearInterval(id);
  }, [simAutoRefresh, onManualRefresh]);

  if (data.configurationState === 'unconfigured') {
    return <UnconfiguredNotice message={data.configurationMessage} />;
  }

  const series = data.series ?? [];
  const freshness = data.freshness ?? [];
  const onlineCount = freshness.filter((f) => f.online).length;
  const offlineCount = freshness.length - onlineCount;
  const isHourly = days === '1';

  const validPoints = series
    .map((p, idx) => ({ p, idx }))
    .filter((x): x is { p: BucketPoint & { usage: number }; idx: number } => x.p.usage != null);

  const peakEntry = validPoints.reduce<{ p: BucketPoint & { usage: number }; idx: number } | null>(
    (best, cur) => (best == null || cur.p.usage > best.p.usage ? cur : best),
    null,
  );
  const valleyEntry = validPoints.reduce<{ p: BucketPoint & { usage: number }; idx: number } | null>(
    (best, cur) => (best == null || cur.p.usage < best.p.usage ? cur : best),
    null,
  );
  const meanVal =
    validPoints.length > 0 ? validPoints.reduce((acc, x) => acc + x.p.usage, 0) / validPoints.length : null;
  const peakValleySpread =
    peakEntry && valleyEntry ? peakEntry.p.usage - valleyEntry.p.usage : null;
  const peakToMean =
    peakEntry && meanVal && meanVal > 0 ? peakEntry.p.usage / meanVal : null;

  // Identify slots with missing or partial coverage (never silently treated as 100% healthy!).
  const partialSlotIndices = series
    .map((p, idx) => (p.usage == null || (p.expected > 0 && p.valid < p.expected) ? idx : -1))
    .filter((i) => i >= 0);

  const latestReadingAt = useMemo(() => {
    const times = freshness.map((f) => f.latestAt).filter((t): t is string => Boolean(t)).sort();
    return times.length ? times[times.length - 1] : null;
  }, [freshness]);

  const formatSlotLabel = (p: BucketPoint) =>
    isHourly ? `${p.from.slice(11, 16)}–${p.to.slice(11, 16)}` : p.from.slice(5, 10);

  const periodLabels: Record<string, string> = {
    '1': '当日逐时 (24h)',
    '7': '近 7 日',
    '14': '近 14 日',
    '30': '近 30 日',
  };

  return (
    <div className="space-y-4">
      {/* ── 1. Unified Full-Width Operational Telemetry Console (Zero 4-Box KPI / 8:4 Template Repetition) ── */}
      <section aria-label="模拟实时状态与负荷遥测主曲线" className="arco-card overflow-hidden">
        {/* Top Telemetry Command Strip */}
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line bg-fill-1/50 px-5 py-2.5 text-[12px] tabular">
          <div className="flex flex-wrap items-center gap-2.5">
            <span className="inline-flex items-center gap-1.5 rounded-[2px] bg-primary-soft px-2 py-0.5 text-[11px] font-medium text-primary">
              <Radio className={`h-3 w-3 ${simAutoRefresh ? 'animate-pulse' : ''}`} aria-hidden="true" />
              {isSimulated ? '模拟实时刷新 · 非真实硬件直采' : '实时监测模式'}
            </span>
            <span className="font-medium text-fg">
              {data.buildingName} · {energyLabels[data.energy] ?? data.energy}
            </span>
            <span className="text-[11px] text-muted">
              窗口 [{fmtShanghaiDate(data.from)}, {fmtShanghaiDate(data.to)}) · 最近刷新 {lastPulseAt}
            </span>
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <button
              type="button"
              data-sim-autorefresh
              onClick={() => {
                setSimAutoRefresh((v) => !v);
                setLastPulseAt(new Date().toLocaleTimeString('zh-CN', { hour12: false }));
              }}
              className={`inline-flex h-7 items-center gap-1.5 rounded-[2px] border px-2.5 text-[11px] font-medium transition-colors ${
                simAutoRefresh
                  ? 'border-primary/40 bg-primary-soft text-primary'
                  : 'border-line bg-surface text-fg-2 hover:bg-fill-1'
              }`}
            >
              <Activity className="h-3 w-3" aria-hidden="true" />
              <span>{simAutoRefresh ? '模拟轮询中 (15s)' : '开启模拟轮询'}</span>
            </button>

            <div className="inline-flex rounded-[2px] border border-line bg-surface p-0.5 text-[11px]" role="group" aria-label="趋势时间周期切换">
              {(['1', '7', '14', '30'] as const).map((d) => (
                <button
                  key={d}
                  type="button"
                  data-rt-days={d}
                  onClick={() => onChangeDays(d)}
                  className={`rounded-[2px] px-2.5 py-0.5 transition-colors ${
                    days === d ? 'bg-primary font-medium text-white' : 'text-fg-2 hover:bg-fill-1 hover:text-fg'
                  }`}
                >
                  {periodLabels[d]}
                </button>
              ))}
            </div>
          </div>
        </div>

        {/* Dense Telemetry Readout Ribbon + Full-Width Primary Load Curve */}
        <div className="space-y-4 p-5 tabular">
          <div className="flex flex-wrap items-end justify-between gap-4 border-b border-line pb-3">
            <div>
              <h2 className="text-[15px] font-medium leading-[22px] text-fg">
                {periodLabels[days] ?? '历史周期'}{energyLabels[data.energy] ?? data.energy}负荷运行遥测曲线
              </h2>
              <p className="mt-0.5 text-[12px] text-muted">
                橙色虚线为周期均值；琥珀色高亮带标示存在缺测槽位的时段（缺测绝不按 0 补齐）
              </p>
            </div>
            <div className="flex flex-wrap items-center gap-4 text-[12px]">
              <div>
                <span className="text-[11px] text-muted">周期累计</span>
                <p className="text-[18px] font-semibold leading-[22px] text-fg">
                  {fmtNum(data.totalUsage)} <span className="text-[11px] font-normal text-muted">{data.unit}</span>
                </p>
              </div>
              <div className="h-7 w-px bg-line" />
              <div>
                <span className="text-[11px] text-muted">数据覆盖</span>
                <p className="text-[16px] font-semibold leading-[22px] text-fg">
                  {fmtPct(data.coverage.rate)}{' '}
                  <span className="text-[11px] font-normal text-muted">
                    ({data.coverage.valid}/{data.coverage.expected})
                  </span>
                </p>
              </div>
              <div className="h-7 w-px bg-line" />
              <div>
                <span className="text-[11px] text-muted">计量在线</span>
                <p className={`text-[16px] font-semibold leading-[22px] ${offlineCount > 0 ? 'text-warning' : 'text-positive'}`}>
                  {onlineCount}/{freshness.length} 块表
                </p>
              </div>
            </div>
          </div>

          <TrendAreaChart
            points={series}
            unit={data.unit}
            wide
            height={326}
            seriesLabel={`${energyLabels[data.energy] ?? data.energy}实测 (${data.unit})`}
            referenceValue={meanVal}
            referenceLabel={meanVal != null ? `周期均值 ${fmtNum(meanVal)}` : undefined}
            highlightIndices={partialSlotIndices}
            highlightLabel="部分缺测时段"
          />

          {/* Docked Slot-Level Completeness Quality Ribbon */}
          {series.length > 0 && (
            <div className="border-t border-line pt-3.5">
              <div className="mb-2 flex flex-wrap items-center justify-between gap-2 text-[11px] text-muted">
                <span className="font-medium text-fg-2">
                  时段采集完整率质量带（每格 = 1 个统计时段 · {partialSlotIndices.length === 0 ? '全槽完整' : `${partialSlotIndices.length} 个时段缺测`}）
                </span>
                <div className="flex items-center gap-3">
                  <span className="inline-flex items-center gap-1">
                    <span className="size-2 rounded-[1px] bg-positive/80" />100% 完整
                  </span>
                  <span className="inline-flex items-center gap-1">
                    <span className="size-2 rounded-[1px] bg-warning" />部分缺测
                  </span>
                  <span className="inline-flex items-center gap-1">
                    <span className="size-2 rounded-[1px] bg-line-strong" />全槽缺测
                  </span>
                </div>
              </div>
              <div className="flex gap-1" role="list" aria-label="各时段采集完整率状态">
                {series.map((pt, idx) => {
                  const full = pt.usage != null && (pt.expected === 0 || pt.valid >= pt.expected);
                  const empty = pt.usage == null || pt.valid === 0;
                  const lbl = formatSlotLabel(pt);
                  return (
                    <div
                      key={`${pt.from}-${idx}`}
                      role="listitem"
                      title={`${lbl}: ${pt.usage != null ? `${fmtNum(pt.usage)} ${data.unit}` : '缺测'} · 覆盖 ${pt.valid}/${pt.expected}`}
                      className={`h-3 flex-1 rounded-[1px] ${
                        empty ? 'bg-line-strong' : full ? 'bg-positive/75' : 'bg-warning'
                      }`}
                    />
                  );
                })}
              </div>
              {partialSlotIndices.length > 0 && (
                <p className="mt-1.5 text-[11px] text-warning">
                  质量提示：
                  {partialSlotIndices
                    .slice(0, 4)
                    .map((i) => `${formatSlotLabel(series[i])} (${series[i].valid}/${series[i].expected} 槽)`)
                    .join('、')}
                  存在缺测记录，分析峰值与累计值时请结合覆盖率核查。
                </p>
              )}
            </div>
          )}
        </div>
      </section>

      {/* ── 2. Compact 2-Col Operational Telemetry Footer: Peak-Valley Envelope (4 cols) + Meter Health Grid (8 cols) ── */}
      <div className="grid items-start gap-4 lg:grid-cols-12 tabular">
        <section aria-label="峰谷负荷包络参数" className="arco-card p-5 text-[12px] lg:col-span-4">
          <div className="mb-3 flex items-center justify-between border-b border-line pb-2">
            <h3 className="text-[14px] font-medium text-fg">峰谷与负荷运行包络</h3>
            {peakToMean != null && (
              <span className="rounded-[2px] bg-fill-2 px-1.5 py-0 text-[11px] font-medium text-fg-2">
                峰均比 {peakToMean.toFixed(2)}×
              </span>
            )}
          </div>
          <dl className="space-y-2 text-[12px]">
            <div className="flex items-center justify-between gap-2 rounded-[2px] bg-fill-1/60 px-3 py-2">
              <dt className="text-muted">最高峰值</dt>
              <dd className="text-right font-semibold text-fg">
                {peakEntry ? `${fmtNum(peakEntry.p.usage)} ${data.unit}` : '—'}
                <span className="ml-1.5 text-[11px] font-normal text-muted">
                  ({peakEntry ? formatSlotLabel(peakEntry.p) : '—'})
                </span>
              </dd>
            </div>
            <div className="flex items-center justify-between gap-2 rounded-[2px] bg-fill-1/60 px-3 py-2">
              <dt className="text-muted">最低谷值</dt>
              <dd className="text-right font-medium text-fg-2">
                {valleyEntry ? `${fmtNum(valleyEntry.p.usage)} ${data.unit}` : '—'}
                <span className="ml-1.5 text-[11px] font-normal text-muted">
                  ({valleyEntry ? formatSlotLabel(valleyEntry.p) : '—'})
                </span>
              </dd>
            </div>
            <div className="flex items-center justify-between gap-2 px-1 pt-1">
              <dt className="text-muted">峰谷绝对差</dt>
              <dd className="font-medium text-fg">
                {peakValleySpread != null ? `${fmtNum(peakValleySpread)} ${data.unit}` : '—'}
              </dd>
            </div>
            <div className="flex items-center justify-between gap-2 px-1">
              <dt className="text-muted">{isHourly ? '单时均值' : '日均消耗'}</dt>
              <dd className="font-medium text-fg-2">{meanVal != null ? `${fmtNum(meanVal)} ${data.unit}` : '—'}</dd>
            </div>
            <div className="flex items-center justify-between gap-2 px-1">
              <dt className="text-muted">最新入库时间</dt>
              <dd className="font-mono text-[11px] text-muted">
                {latestReadingAt ? latestReadingAt.slice(0, 16).replace('T', ' ') : '—'}
              </dd>
            </div>
          </dl>
        </section>

        <section aria-label="计量点在线与采集新鲜度状态格" className="arco-card p-5 text-[12px] lg:col-span-8">
          <div className="mb-3 flex flex-wrap items-center justify-between gap-2 border-b border-line pb-2">
            <h3 className="text-[14px] font-medium text-fg">计量点采集新鲜度监控格</h3>
            <span className="text-[11px] text-muted">
              在线 <strong className="text-positive">{onlineCount}</strong> · 离线{' '}
              <strong className={offlineCount > 0 ? 'text-negative' : 'text-fg-2'}>{offlineCount}</strong> · 共 {freshness.length} 块表
            </span>
          </div>
          <div className="grid gap-2.5 sm:grid-cols-2 xl:grid-cols-3">
            {freshness.map((f) => (
              <div
                key={`${f.terminalId}:${f.itemCode}`}
                className={`rounded-[4px] border px-3 py-2.5 ${
                  f.online ? 'border-line bg-fill-1/25' : 'border-negative/40 bg-negative-soft/30'
                }`}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="flex min-w-0 items-center gap-1.5">
                    <span
                      className={`size-2 shrink-0 rounded-full ${f.online ? 'bg-positive' : 'bg-negative'}`}
                      aria-hidden="true"
                    />
                    <span className="truncate font-medium text-fg">{f.terminalName}</span>
                  </span>
                  <span className={`shrink-0 text-[11px] font-medium ${f.online ? 'text-positive' : 'text-negative'}`}>
                    {f.online ? '在线' : '离线'}
                  </span>
                </div>
                <div className="mt-1.5 flex items-center justify-between gap-2 text-[10px] text-muted">
                  <span className="font-mono">{f.sourceTable} · {f.hourRows} 行</span>
                  <span>{f.latestAt ? f.latestAt.slice(5, 16).replace('T', ' ') : '无读数'}</span>
                </div>
              </div>
            ))}
          </div>
        </section>
      </div>

      {/* ── 3. Collapsible Full Freshness Technical Ledger (Default Collapsed) ── */}
      <CollapsibleSection title="计量点采集新鲜度与来源表明细" badge={`${freshness.length} 块表`} subtitle="查看完整终端编号、小时快照行数与原始流水记录">
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs">
            <thead className="border-b border-line bg-surface-2 text-muted">
              <tr>
                <th className="px-4 py-2">计量点</th>
                <th className="px-4 py-2">来源表</th>
                <th className="px-4 py-2">在线状态</th>
                <th className="px-4 py-2 text-right">小时快照行</th>
                <th className="px-4 py-2 text-right">原始流水行</th>
                <th className="px-4 py-2">最新读数时间</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line tabular">
              {freshness.map((f) => (
                <tr key={`${f.terminalId}:${f.itemCode}`}>
                  <td className="px-4 py-2 font-medium text-fg">
                    {f.terminalName} <span className="font-mono text-muted">({f.terminalId})</span>
                  </td>
                  <td className="px-4 py-2 font-mono text-muted">{f.sourceTable}</td>
                  <td className="px-4 py-2">
                    <span className={f.online ? 'text-positive' : 'text-negative'}>{f.online ? '在线' : '离线'}</span>
                  </td>
                  <td className="px-4 py-2 text-right text-muted">{f.hourRows}</td>
                  <td className="px-4 py-2 text-right text-muted">{f.rawRows}</td>
                  <td className="px-4 py-2 text-muted">{f.latestAt ? f.latestAt.slice(0, 19).replace('T', ' ') : '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </CollapsibleSection>
    </div>
  );
}

// ── F05: Executive Annual Target Risk & Burn-Up Summary (Distinct from F14) ─

function LiveTargetSummaryView({
  data,
  token,
  onChangeEnergy,
  onOpenF14,
}: {
  data: TargetsData;
  token: string;
  inventory?: InventoryData | null;
  onChangeEnergy: (energy: string) => void;
  onOpenF14: () => void;
}) {
  const [multiEnergyTargets, setMultiEnergyTargets] = useState<TargetsData[]>([]);

  // Fetch all 5 energies' annual targets for the active building so F05 acts as a true overview summary.
  useEffect(() => {
    if (!data.buildingId) return;
    const energyList = ['electricity', 'water', 'gas', 'heat', 'cooling'];
    const ctrl = new AbortController();
    Promise.all(
      energyList.map((en) => {
        if (en === data.energy) return Promise.resolve(data);
        const qs = new URLSearchParams({ building: data.buildingId, energy: en, year: String(data.year) });
        return liveGet<TargetsData>(`/targets?${qs}`, token, ctrl.signal)
          .then((r) => r.data ?? null)
          .catch(() => null);
      }),
    ).then((list) => {
      if (ctrl.signal.aborted) return;
      setMultiEnergyTargets(list.filter((x): x is TargetsData => x != null));
    });
    return () => ctrl.abort();
  }, [data, token]);

  const months = data.months ?? [];
  const annualTarget = data.annual.target;
  const completedComparable = months.filter(
    (m) => m.status === 'comparable' && m.usage != null && m.target != null && (m.coverage.rate ?? 0) >= 0.9,
  );
  const incompleteOrLowCov = months.filter(
    (m) => m.status !== 'comparable' || m.usage == null || (m.coverage.rate ?? 0) < 0.9,
  );
  const completedUsageSum =
    completedComparable.length > 0 ? completedComparable.reduce((s, m) => s + (m.usage ?? 0), 0) : null;
  const completedTargetSum =
    completedComparable.length > 0 ? completedComparable.reduce((s, m) => s + (m.target ?? 0), 0) : null;
  const completedPaceRatio =
    completedUsageSum != null && completedTargetSum != null && completedTargetSum > 0
      ? completedUsageSum / completedTargetSum
      : null;
  const ytdAnnualRatio =
    completedUsageSum != null && annualTarget != null && annualTarget > 0
      ? completedUsageSum / annualTarget
      : null;
  const remainingAnnual =
    annualTarget != null && completedUsageSum != null ? annualTarget - completedUsageSum : null;
  const remainingMonthCount = Math.max(0, 12 - completedComparable.length);
  const avgMonthlyActual =
    completedUsageSum != null && completedComparable.length > 0
      ? completedUsageSum / completedComparable.length
      : null;
  const avgRemainingAllowance =
    remainingAnnual != null && remainingMonthCount > 0 ? remainingAnnual / remainingMonthCount : null;
  const projectedAnnualRef =
    completedUsageSum != null && avgMonthlyActual != null
      ? completedUsageSum + avgMonthlyActual * remainingMonthCount
      : null;

  const overTargetMonths = completedComparable.filter((m) => (m.usage ?? 0) > (m.target ?? 0));
  const worstOverMonth = overTargetMonths
    .slice()
    .sort((a, b) => (b.delta ?? 0) - (a.delta ?? 0))[0] ?? null;

  // Determine executive risk posture strictly from server-calculated target vs actuals.
  const riskPosture = useMemo(() => {
    if (!data.targetConfigured || annualTarget == null) {
      return { level: 'unconfigured', label: '未配置年度目标', tone: 'bg-fill-2 text-fg-2', summary: '当前能源尚未配置年度配额目标，无法评估执行风险。' };
    }
    if (remainingAnnual != null && remainingAnnual < 0) {
      return {
        level: 'high',
        label: '高风险 · 已超出全年总配额',
        tone: 'bg-negative-soft text-negative',
        summary: `前 ${completedComparable.length} 个已完结月累计用量已超出全年总目标 ${fmtNum(Math.abs(remainingAnnual))} ${data.unit}，需立即启动节能管控。`,
      };
    }
    if (
      (projectedAnnualRef != null && projectedAnnualRef > annualTarget) ||
      overTargetMonths.length >= 3 ||
      (avgRemainingAllowance != null && avgMonthlyActual != null && avgRemainingAllowance < avgMonthlyActual)
    ) {
      return {
        level: 'warning',
        label: '执行预警 · 存在超配额风险',
        tone: 'bg-warning-soft text-warning',
        summary: `已完结期出现 ${overTargetMonths.length} 个超标月${
          avgRemainingAllowance != null && avgMonthlyActual != null
            ? `，剩余 ${remainingMonthCount} 月月均可用额度 (${fmtNum(avgRemainingAllowance)} ${data.unit}) 低于当前月均实测 (${fmtNum(avgMonthlyActual)} ${data.unit})`
            : ''
        }。`,
      };
    }
    return {
      level: 'controlled',
      label: '执行受控 · 预算留有缓冲',
      tone: 'bg-positive-soft text-positive',
      summary: `已完成 ${completedComparable.length} 个可比月累计用量低于同期规划进度，剩余 ${remainingMonthCount} 个月预算空间充足（未完结月暂不出正式全年完成率）。`,
    };
  }, [
    data.targetConfigured,
    data.unit,
    annualTarget,
    remainingAnnual,
    completedComparable.length,
    projectedAnnualRef,
    overTargetMonths.length,
    avgRemainingAllowance,
    avgMonthlyActual,
    remainingMonthCount,
  ]);

  return (
    <div className="space-y-4">
      {/* ── 1. Compact Target Risk Posture Strip ── */}
      <section aria-label="年度目标执行风险摘要" className="arco-card overflow-hidden">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line bg-fill-1/50 px-5 py-2.5 text-[12px] tabular">
          <div className="flex flex-wrap items-center gap-2">
            <span className={`rounded-[2px] px-2 py-0.5 text-[11px] font-medium ${riskPosture.tone}`}>
              {riskPosture.label}
            </span>
            <span className="font-medium text-fg">
              {data.buildingName} · {data.year} 年度{energyLabels[data.energy] ?? data.energy}目标风险摘要
            </span>
            {data.targetVersion && (
              <span className="font-mono text-[11px] text-muted">版本 {data.targetVersion}</span>
            )}
          </div>
          <button
            type="button"
            data-drill-f14
            onClick={onOpenF14}
            className="inline-flex h-7 items-center gap-1 rounded-[2px] bg-primary px-3 text-[12px] font-medium text-white transition-colors hover:bg-primary/90"
          >
            <span>进入 F14 详细目标分解</span>
            <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
          </button>
        </div>

        <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 px-5 py-3 text-[12px] tabular">
          <div className="flex flex-wrap items-baseline gap-x-6 gap-y-2">
            <div className="flex items-baseline gap-1.5">
              <span className="text-muted">{data.year} 年度目标</span>
              <strong className="text-[16px] font-semibold text-fg">{fmtNum(annualTarget)}</strong>
              <span className="text-[11px] text-muted">{data.unit}</span>
            </div>
            <span className="hidden h-3.5 w-px bg-line sm:inline-block" aria-hidden="true" />
            <div className="flex items-baseline gap-1.5">
              <span className="text-muted">已完结实测</span>
              <strong className="text-[16px] font-semibold text-fg">{fmtNum(completedUsageSum)}</strong>
              <span className="text-[11px] text-muted">{data.unit}（{completedComparable.length}/12 月可比）</span>
            </div>
            <span className="hidden h-3.5 w-px bg-line sm:inline-block" aria-hidden="true" />
            <div className="flex items-baseline gap-1.5">
              <span className="text-muted">占全年配额</span>
              <strong className="text-[16px] font-semibold text-primary">{fmtPct(ytdAnnualRatio)}</strong>
              {completedPaceRatio != null && (
                <span
                  className={`rounded-[2px] px-1.5 py-0 text-[11px] font-medium ${
                    completedPaceRatio > 1 ? 'bg-negative-soft text-negative' : 'bg-positive-soft text-positive'
                  }`}
                >
                  {completedPaceRatio > 1 ? '超前消耗' : '进度受控'} {fmtPct(completedPaceRatio)}
                </span>
              )}
            </div>
            <span className="hidden h-3.5 w-px bg-line sm:inline-block" aria-hidden="true" />
            <div className="flex items-baseline gap-1.5">
              <span className="text-muted">剩余可用</span>
              <strong
                className={`text-[16px] font-semibold ${
                  remainingAnnual != null && remainingAnnual < 0 ? 'text-negative' : 'text-fg'
                }`}
              >
                {fmtNum(remainingAnnual)}
              </strong>
              {remainingAnnual != null && <span className="text-[11px] text-muted">{data.unit}</span>}
              {avgRemainingAllowance != null && avgMonthlyActual != null && (
                <span className="text-[11px] text-muted">
                  （余 {remainingMonthCount} 月 · 均可用 {fmtNum(avgRemainingAllowance)}）
                </span>
              )}
            </div>
          </div>
          {incompleteOrLowCov.length > 0 && (
            <span className="text-[11px] text-muted">
              {incompleteOrLowCov.length} 个月未完结/低覆盖不计入正式完成率
            </span>
          )}
        </div>
      </section>

      {/* ── 2. Primary Stage: Cumulative Burn-Up Risk Trajectory (8 cols) + Risk Diagnosis & 12-Month Status Grid (4 cols) ── */}
      <div className="grid items-start gap-4 lg:grid-cols-12">
        <section className="arco-card p-5 lg:col-span-8">
          <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
            <div>
              <h2 className="text-[15px] font-medium leading-[22px] text-fg">
                {data.year} 年度累计执行轨迹与配额风险推演
              </h2>
              <p className="mt-0.5 text-[12px] text-muted">
                对比已完结期累计实测与累计规划轨迹，直观识别年末配额突破风险；未完结月份以斜纹隔离
              </p>
            </div>
          </div>

          <CumulativeTargetBurnupChart
            months={months.map((m) => ({
              month: m.month,
              label: m.label,
              target: m.target,
              usage: m.usage,
              coverageRate: m.coverage.rate,
              status: m.status,
            }))}
            annualTarget={annualTarget}
            unit={data.unit}
            height={292}
            wide
          />
        </section>

        {/* Right 4 Cols: Risk Diagnostics + Compact 4x3 Monthly Execution Overview */}
        <aside aria-label="目标执行风险诊断与月度概览" className="arco-card space-y-4 p-5 text-[12px] tabular lg:col-span-4">
          <div>
            <h3 className="mb-2 text-[14px] font-medium text-fg">执行风险诊断要点</h3>
            <p className="mb-2.5 rounded-[4px] bg-fill-1/60 px-3 py-2 text-[11px] leading-relaxed text-fg-2">
              {riskPosture.summary}
            </p>
            <dl className="space-y-1.5 text-[11px]">
              <div className="flex items-center justify-between gap-2 rounded-[2px] border border-line px-2.5 py-1.5">
                <dt className="text-muted">已超月度目标月份</dt>
                <dd className={`font-semibold ${overTargetMonths.length > 0 ? 'text-negative' : 'text-positive'}`}>
                  {overTargetMonths.length} / {completedComparable.length} 月
                  {worstOverMonth ? `（峰值 ${worstOverMonth.month}月）` : ''}
                </dd>
              </div>
              <div className="flex items-center justify-between gap-2 rounded-[2px] border border-line px-2.5 py-1.5">
                <dt className="text-muted">按月均速率参考推演</dt>
                <dd
                  className={`font-semibold ${
                    projectedAnnualRef != null && annualTarget != null && projectedAnnualRef > annualTarget
                      ? 'text-negative'
                      : 'text-positive'
                  }`}
                >
                  {projectedAnnualRef != null && annualTarget != null
                    ? `${fmtNum(projectedAnnualRef)} ${data.unit} (${fmtPct(projectedAnnualRef / annualTarget)})`
                    : '—'}
                </dd>
              </div>
              <div className="flex items-center justify-between gap-2 rounded-[2px] border border-line px-2.5 py-1.5">
                <dt className="text-muted">未完结 / 低覆盖月</dt>
                <dd className="font-medium text-warning">
                  {incompleteOrLowCov.length > 0
                    ? `${incompleteOrLowCov.map((m) => `${m.month}月`).join('、')}（不出完成率）`
                    : '无'}
                </dd>
              </div>
            </dl>
          </div>

          <div className="border-t border-line pt-3.5">
            <div className="mb-2 flex items-center justify-between">
              <h3 className="text-[13px] font-medium text-fg">月度执行概览（点击下钻 F14）</h3>
              <span className="text-[10px] text-muted">绿达标 · 红超标 · 灰未完结</span>
            </div>
            <div className="grid grid-cols-4 gap-1.5">
              {months.map((m) => {
                const formalOk = m.status === 'comparable' && m.usage != null && (m.coverage.rate ?? 0) >= 0.9;
                const over = formalOk && m.delta != null && m.delta > 0;
                return (
                  <button
                    key={m.month}
                    type="button"
                    onClick={onOpenF14}
                    title={
                      formalOk
                        ? `${m.label}：实测 ${fmtNum(m.usage)} / 目标 ${fmtNum(m.target)} ${data.unit}（${fmtPct(m.ratio)}）`
                        : `${m.label}：${statusLabels[m.status] ?? m.status}（不出正式完成率）`
                    }
                    className={`rounded-[2px] border px-2 py-1.5 text-left transition-colors ${
                      !formalOk
                        ? 'border-dashed border-line bg-fill-1/60 text-muted hover:bg-fill-2/60'
                        : over
                          ? 'border-negative/35 bg-negative-soft/60 text-negative hover:bg-negative-soft'
                          : 'border-positive/30 bg-positive-soft/45 text-fg hover:bg-positive-soft/75'
                    }`}
                  >
                    <div className="flex items-center justify-between text-[11px]">
                      <span className="font-medium">{m.month}月</span>
                      <span
                        className={`size-1.5 rounded-full ${
                          !formalOk ? 'bg-line-strong' : over ? 'bg-negative' : 'bg-positive'
                        }`}
                      />
                    </div>
                    <div className="mt-0.5 text-[11px] font-semibold">
                      {formalOk ? fmtPct(m.ratio) : '未完结'}
                    </div>
                  </button>
                );
              })}
            </div>
          </div>
        </aside>
      </div>

      {/* ── 3. Multi-Energy Annual Risk Quick Ledger (Native Units Kept Strictly Separate) ── */}
      {multiEnergyTargets.length > 0 && (
        <section aria-label="本建筑五能源年度目标风险速览" className="arco-card p-5">
          <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
            <div>
              <h2 className="text-[15px] font-medium leading-[22px] text-fg">
                {data.buildingName} · {data.year} 年度多能源目标风险速览
              </h2>
              <p className="mt-0.5 text-[12px] text-muted">
                各能源按原生单位独立评估，点击行切换上方轨迹分析；完整月度分期表与权重分解请在 F14 查看
              </p>
            </div>
          </div>

          <div className="overflow-x-auto">
            <table className="w-full text-left text-[12px]">
              <thead className="border-b border-line bg-fill-1 text-fg-2">
                <tr>
                  <th className="px-3 py-2 font-medium">能源品种</th>
                  <th className="px-3 py-2 text-right font-medium">年度总目标</th>
                  <th className="px-3 py-2 text-right font-medium">已完成期累计实测</th>
                  <th className="px-3 py-2 text-right font-medium">占全年配额</th>
                  <th className="px-3 py-2 text-right font-medium">剩余额度</th>
                  <th className="px-3 py-2 text-center font-medium">超标月份</th>
                  <th className="px-3 py-2 font-medium">风险判断</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line tabular">
                {multiEnergyTargets.map((t) => {
                  const isCur = t.energy === data.energy;
                  const comp = (t.months ?? []).filter(
                    (m) => m.status === 'comparable' && m.usage != null && m.target != null && (m.coverage.rate ?? 0) >= 0.9,
                  );
                  const sumU = comp.length > 0 ? comp.reduce((s, m) => s + (m.usage ?? 0), 0) : null;
                  const annT = t.annual.target;
                  const rem = annT != null && sumU != null ? annT - sumU : null;
                  const overCnt = comp.filter((m) => (m.usage ?? 0) > (m.target ?? 0)).length;
                  const ratio = annT && sumU != null ? sumU / annT : null;
                  const riskLabel = !t.targetConfigured
                    ? '未配置'
                    : rem != null && rem < 0
                      ? '已超配额'
                      : overCnt >= 3
                        ? '超标预警'
                        : overCnt > 0
                          ? '局部波动'
                          : '受控';
                  const riskClass = !t.targetConfigured
                    ? 'bg-fill-2 text-muted'
                    : rem != null && rem < 0
                      ? 'bg-negative-soft text-negative'
                      : overCnt >= 3
                        ? 'bg-warning-soft text-warning'
                        : overCnt > 0
                          ? 'bg-warning-soft/70 text-warning'
                          : 'bg-positive-soft text-positive';
                  return (
                    <tr
                      key={t.energy}
                      onClick={() => onChangeEnergy(t.energy)}
                      className={`cursor-pointer transition-colors ${isCur ? 'bg-primary-soft/35' : 'hover:bg-fill-1/60'}`}
                    >
                      <td className="px-3 py-2 font-medium text-fg">
                        {energyLabels[t.energy] ?? t.energy}
                        <span className="ml-1.5 text-[11px] font-normal text-muted">({t.unit})</span>
                      </td>
                      <td className="px-3 py-2 text-right text-fg-2">{fmtNum(annT)}</td>
                      <td className="px-3 py-2 text-right font-semibold text-fg">
                        {fmtNum(sumU)} <span className="text-[11px] font-normal text-muted">({comp.length}/12月)</span>
                      </td>
                      <td className="px-3 py-2 text-right text-fg-2">{fmtPct(ratio)}</td>
                      <td className={`px-3 py-2 text-right font-medium ${rem != null && rem < 0 ? 'text-negative' : 'text-fg'}`}>
                        {fmtNum(rem)}
                      </td>
                      <td className="px-3 py-2 text-center">
                        <span className={overCnt > 0 ? 'font-semibold text-warning' : 'text-muted'}>{overCnt} 个月</span>
                      </td>
                      <td className="px-3 py-2">
                        <span className={`rounded-[2px] px-1.5 py-0 text-[11px] font-medium ${riskClass}`}>{riskLabel}</span>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </section>
      )}
    </div>
  );
}

// ── F14: Annual Target Decomposition & Monthly Execution Studio ─────────────

function LiveTargetsView({
  data,
  inventory,
  summaryOnly,
}: {
  data: TargetsData;
  inventory?: InventoryData | null;
  summaryOnly: boolean;
}) {
  const months = data.months ?? [];
  const annualTarget = data.annual.target;
  const completedComparable = months.filter(
    (m) => m.status === 'comparable' && m.usage != null && m.target != null && (m.coverage.rate ?? 0) >= 0.9,
  );
  const completedUsageSum =
    completedComparable.length > 0 ? completedComparable.reduce((s, m) => s + (m.usage ?? 0), 0) : null;
  const completedTargetSum =
    completedComparable.length > 0 ? completedComparable.reduce((s, m) => s + (m.target ?? 0), 0) : null;
  const completedExecRatio =
    completedUsageSum != null && completedTargetSum != null && completedTargetSum > 0
      ? completedUsageSum / completedTargetSum
      : null;
  const remainingAnnual =
    annualTarget != null && completedUsageSum != null ? annualTarget - completedUsageSum : null;
  const overTargetCount = completedComparable.filter((m) => (m.usage ?? 0) > (m.target ?? 0)).length;

  // Monthly weight shares (demonstrates linear vs seasonal weight distribution transparently).
  const monthWeights = months.map((m) => {
    const share = annualTarget != null && annualTarget > 0 && m.target != null ? m.target / annualTarget : null;
    return { ...m, weightShare: share };
  });
  const maxWeightShare = Math.max(...monthWeights.map((m) => m.weightShare ?? 0), 0.12);
  const isWeighted = data.targetMethod === 'weighted';
  const weightValues = monthWeights.map((m) => m.weightShare).filter((v): v is number => v != null);
  const weightMin = weightValues.length ? Math.min(...weightValues) : null;
  const weightMax = weightValues.length ? Math.max(...weightValues) : null;

  // Variance series for PeriodVarianceBarChart (strictly null for incomplete/low-coverage months!).
  const variancePoints = months.map((m) => {
    const valid = m.status === 'comparable' && m.usage != null && m.target != null && (m.coverage.rate ?? 0) >= 0.9;
    return {
      label: `${m.month}月`,
      current: valid ? m.usage : null,
      baseline: valid ? m.target : null,
    };
  });

  const targetConfig = useMemo(
    () =>
      (inventory?.targets ?? []).find(
        (t) => t.objectId === data.buildingId && t.energy === data.energy && t.year === data.year,
      ) ?? null,
    [inventory?.targets, data.buildingId, data.energy, data.year],
  );

  return (
    <div className="space-y-4">
      {/* ── 1. Unified Annual Target Executive Band + Rule Provenance Strip ── */}
      <section aria-label="年度目标与分期执行摘要" className="arco-card overflow-hidden">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line bg-fill-1/50 px-5 py-2 text-[12px] tabular">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium text-fg">
              {data.buildingName} · {data.year} 年度{energyLabels[data.energy] ?? data.energy}目标分解
            </span>
            <span
              className={`rounded-[2px] px-1.5 py-0 text-[11px] font-medium leading-[18px] ${
                isWeighted ? 'bg-primary-soft text-primary' : 'bg-fill-2 text-fg-2'
              }`}
            >
              {isWeighted ? '季节权重分解' : '月度线性均分'}
            </span>
            {data.targetVersion && (
              <span className="font-mono text-[11px] text-muted">
                目标版本 {data.targetVersion}
                {data.ruleVersion ? ` · 规则 ${data.ruleVersion}` : ''}
              </span>
            )}
          </div>
          <span className="text-[11px] text-muted">
            {data.timezone || 'Asia/Shanghai'} · 未结束或低覆盖月份不出正式完成率
          </span>
        </div>

        <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 px-5 py-3 text-[12px] tabular">
          <div className="flex flex-wrap items-baseline gap-x-6 gap-y-2">
            <div className="flex items-baseline gap-1.5">
              <span className="text-muted">{data.year} 年度目标</span>
              <strong className="text-[16px] font-semibold text-fg">{fmtNum(annualTarget)}</strong>
              <span className="text-[11px] text-muted">{data.unit}</span>
            </div>
            <span className="hidden h-3.5 w-px bg-line sm:inline-block" aria-hidden="true" />
            <div className="flex items-baseline gap-1.5">
              <span className="text-muted">已完结实测</span>
              <strong className="text-[16px] font-semibold text-fg">{fmtNum(data.annual.usage ?? completedUsageSum)}</strong>
              <span className="text-[11px] text-muted">{data.unit}（{completedComparable.length}/12 月可比）</span>
            </div>
            <span className="hidden h-3.5 w-px bg-line sm:inline-block" aria-hidden="true" />
            <div className="flex items-baseline gap-1.5">
              <span className="text-muted">执行比</span>
              <strong className="text-[16px] font-semibold text-primary">
                {fmtPct(data.annual.status === 'comparable' ? data.annual.ratio : completedExecRatio)}
              </strong>
              <span
                className={`rounded-[2px] px-1.5 py-0 text-[11px] leading-[18px] ${
                  overTargetCount > 0 ? 'bg-warning-soft text-warning' : 'bg-positive-soft text-positive'
                }`}
              >
                {overTargetCount > 0 ? `超标 ${overTargetCount} 个月` : '全部受控'}
              </span>
            </div>
            <span className="hidden h-3.5 w-px bg-line sm:inline-block" aria-hidden="true" />
            <div className="flex items-baseline gap-1.5">
              <span className="text-muted">年度剩余</span>
              <strong
                className={`text-[16px] font-semibold ${
                  remainingAnnual != null && remainingAnnual < 0 ? 'text-negative' : 'text-fg'
                }`}
              >
                {fmtNum(remainingAnnual)}
              </strong>
              {remainingAnnual != null && <span className="text-[11px] text-muted">{data.unit}</span>}
            </div>
          </div>
          {weightMin != null && weightMax != null && (
            <span className="text-[11px] text-muted">
              {isWeighted ? `月权重 ${fmtPct(weightMin)}–${fmtPct(weightMax)}` : `等权均分 ${fmtPct(weightMin)}/月`}
            </span>
          )}
        </div>
        {data.message && (
          <p className="border-t border-line bg-warning-soft/50 px-5 py-2 text-[12px] text-warning">{data.message}</p>
        )}
      </section>

      {/* ── 2. Primary Stage: Monthly Target vs Actual + Variance (8 cols) + Seasonal Weight Distribution & Provenance (4 cols) ── */}
      {months.length > 0 && (
        <div className="grid items-start gap-4 lg:grid-cols-12">
          <section className="arco-card space-y-5 p-5 lg:col-span-8">
            <div>
              <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
                <div>
                  <h2 className="text-[15px] font-medium leading-[22px] text-fg">
                    {data.year} 年月度目标与实际执行对比
                  </h2>
                  <p className="mt-0.5 text-[12px] text-muted">
                    橙色横杠标记各月目标上限；未结束或低覆盖月份以斜纹占位，不按 0 补齐也不出正式完成率
                  </p>
                </div>
              </div>
              <TargetComparisonChart months={months} unit={data.unit} height={248} wide />
            </div>

            <div className="border-t border-line pt-4">
              <h3 className="mb-2 text-[13px] font-medium text-fg">
                月度执行偏差分布（实际用量 − 月度目标）
              </h3>
              <PeriodVarianceBarChart
                points={variancePoints}
                unit={data.unit}
                wide
                increaseLabel="超出月度目标"
                decreaseLabel="低于月度目标（节余）"
                formulaLabel={`偏差 = 实际用量 − 月度目标（${data.unit}）`}
              />
            </div>
          </section>

          {/* Right 4 Cols: Seasonal Weight Breakdown & Rule Source */}
          <aside aria-label="分期权重分布与目标规则来源" className="arco-card flex flex-col justify-between p-5 lg:col-span-4">
            <div className="space-y-3 tabular">
              <div className="border-b border-line pb-2.5">
                <h2 className="text-[15px] font-medium text-fg">月度目标权重与季节差异</h2>
                <p className="mt-0.5 text-[12px] text-muted">
                  {isWeighted
                    ? '按季节负荷系数分配各月目标占比，夏冬峰季权重更高'
                    : '当前版本采用 12 个月等权线性分解（每月 8.3%）；可在配置中心切换为季节权重'}
                </p>
              </div>

              <div className="space-y-1.5">
                {monthWeights.map((m) => {
                  const pct = m.weightShare != null ? (m.weightShare / maxWeightShare) * 100 : 0;
                  const done = m.status === 'comparable' && m.usage != null && (m.coverage.rate ?? 0) >= 0.9;
                  const over = done && m.target != null && m.usage! > m.target;
                  return (
                    <div key={m.month} className="grid grid-cols-[36px_1fr_84px_46px] items-center gap-2 text-[11px]">
                      <span className="font-medium text-fg-2">{m.month}月</span>
                      <div className="h-[7px] overflow-hidden rounded-[2px] bg-fill-2">
                        <div
                          className="h-full rounded-[2px]"
                          style={{
                            width: `${Math.max(4, pct)}%`,
                            backgroundColor: !done ? '#C9CDD4' : over ? '#F53F3F' : '#165DFF',
                          }}
                        />
                      </div>
                      <span className="text-right text-fg">{fmtNum(m.target)}</span>
                      <span className="text-right text-muted">{fmtPct(m.weightShare)}</span>
                    </div>
                  );
                })}
              </div>
            </div>

            <dl className="mt-4 space-y-1.5 border-t border-line pt-3 text-[11px] tabular">
              <div className="flex justify-between gap-2">
                <dt className="text-muted">目标版本</dt>
                <dd className="font-mono text-fg">{data.targetVersion || '未配置'}</dd>
              </div>
              <div className="flex justify-between gap-2">
                <dt className="text-muted">分解规则</dt>
                <dd className="text-fg">{isWeighted ? '季节权重分解 (weighted)' : '线性均分 (linear)'}</dd>
              </div>
              {targetConfig && (
                <div className="flex justify-between gap-2">
                  <dt className="text-muted">配置来源</dt>
                  <dd className="font-mono text-fg">
                    {targetConfig.objectKind}:{targetConfig.objectId} · {targetConfig.revision}
                  </dd>
                </div>
              )}
            </dl>
          </aside>
        </div>
      )}

      {/* ── 3. Detailed 12-Month Breakdown Ledger (F14 full mode) ── */}
      {!summaryOnly && (
        <CollapsibleSection title="12 个月分期目标与正式完成率明细表" badge={`${months.length} 月`} defaultOpen>
          <div className="overflow-x-auto">
            <table className="w-full text-left text-[12px]">
              <thead className="border-b border-line bg-fill-1 text-fg-2">
                <tr>
                  <th className="whitespace-nowrap px-4 py-2.5 font-medium">月份</th>
                  <th className="whitespace-nowrap px-4 py-2.5 text-right font-medium">月权重</th>
                  <th className="whitespace-nowrap px-4 py-2.5 text-right font-medium">分期目标 ({data.unit})</th>
                  <th className="whitespace-nowrap px-4 py-2.5 text-right font-medium">实际用量 ({data.unit})</th>
                  <th className="whitespace-nowrap px-4 py-2.5 text-right font-medium">偏差量</th>
                  <th className="whitespace-nowrap px-4 py-2.5 text-right font-medium">数据覆盖率</th>
                  <th className="whitespace-nowrap px-4 py-2.5 text-right font-medium">正式完成率</th>
                  <th className="whitespace-nowrap px-4 py-2.5 font-medium">比对状态</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line tabular">
                {monthWeights.map((m) => {
                  const formalOk = m.status === 'comparable' && m.usage != null && (m.coverage.rate ?? 0) >= 0.9;
                  const over = formalOk && m.delta != null && m.delta > 0;
                  return (
                    <tr key={m.month} className="hover:bg-fill-1/50">
                      <td className="whitespace-nowrap px-4 py-2 font-medium text-fg">{m.label}</td>
                      <td className="whitespace-nowrap px-4 py-2 text-right text-muted">{fmtPct(m.weightShare)}</td>
                      <td className="whitespace-nowrap px-4 py-2 text-right text-fg-2">{fmtNum(m.target)}</td>
                      <td className="whitespace-nowrap px-4 py-2 text-right font-medium text-fg">
                        {m.usage != null ? fmtNum(m.usage) : '—'}
                      </td>
                      <td
                        className={`whitespace-nowrap px-4 py-2 text-right font-medium ${
                          !formalOk ? 'text-muted' : over ? 'text-negative' : 'text-positive'
                        }`}
                      >
                        {formalOk && m.delta != null ? `${m.delta > 0 ? '+' : ''}${fmtNum(m.delta)}` : '—'}
                      </td>
                      <td className="whitespace-nowrap px-4 py-2 text-right text-muted">
                        {m.coverage.rate != null ? fmtPct(m.coverage.rate) : '—'}
                      </td>
                      <td className="whitespace-nowrap px-4 py-2 text-right">
                        {formalOk ? (
                          <span className={`font-semibold ${over ? 'text-negative' : 'text-fg'}`}>
                            {fmtPct(m.ratio)}
                          </span>
                        ) : (
                          <span className="rounded-[2px] bg-fill-2 px-1.5 py-0 text-[11px] text-muted">不出完成率</span>
                        )}
                      </td>
                      <td className="whitespace-nowrap px-4 py-2">
                        <span
                          className={`rounded-[2px] px-1.5 py-0 text-[11px] ${
                            formalOk
                              ? over
                                ? 'bg-negative-soft text-negative'
                                : 'bg-positive-soft text-positive'
                              : 'bg-warning-soft text-warning'
                          }`}
                        >
                          {formalOk
                            ? over
                              ? '超月度目标'
                              : '达标受控'
                            : statusLabels[m.status] ?? m.status}
                        </span>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </CollapsibleSection>
      )}
    </div>
  );
}

// ── F06: Classification & Sub-Item Analysis Workbench ───────────────────────

function shiftShanghaiDate(ymd: string, deltaDays: number): string {
  const [y, m, d] = ymd.split('-').map(Number);
  if (!y || !m || !d) return ymd;
  const dt = new Date(Date.UTC(y, m - 1, d + deltaDays));
  const yy = dt.getUTCFullYear();
  const mm = String(dt.getUTCMonth() + 1).padStart(2, '0');
  const dd = String(dt.getUTCDate()).padStart(2, '0');
  return `${yy}-${mm}-${dd}`;
}

function LiveClassificationView({
  data,
  token,
  inventory,
  onOpenConfig,
}: {
  data: ClassificationData;
  token: string;
  inventory: InventoryData | null;
  onOpenConfig: () => void;
}) {
  const [selectedCatId, setSelectedCatId] = useState<string | null>(null);
  const [trendMode, setTrendMode] = useState<'value' | 'share'>('value');
  const [historyDays, setHistoryDays] = useState<Array<{ date: string; payload: ClassificationData }>>([]);
  const [historyLoading, setHistoryLoading] = useState(false);

  const anchorDate = fmtShanghaiDate(data.from);

  // Reset selection when switching building/energy if category no longer exists.
  useEffect(() => {
    if (!selectedCatId) return;
    if (selectedCatId === '__unclassified__') {
      if (!(data.unclassifiedUsage && data.unclassifiedUsage > 0)) setSelectedCatId(null);
      return;
    }
    if (!(data.categories ?? []).some((c) => c.id === selectedCatId)) {
      setSelectedCatId(null);
    }
  }, [data.categories, data.unclassifiedUsage, selectedCatId]);

  // Fetch real 7-day daily classification snapshots [D-6 .. D] for trend & share evolution (zero synthetic data).
  useEffect(() => {
    if (data.configurationState === 'unconfigured' || !data.buildingId || !anchorDate || anchorDate === '—') {
      setHistoryDays([]);
      return;
    }
    const ctrl = new AbortController();
    setHistoryLoading(true);
    const prevDates = [-6, -5, -4, -3, -2, -1].map((off) => shiftShanghaiDate(anchorDate, off));

    Promise.all(
      prevDates.map((d) =>
        liveGet<ClassificationData>(
          `/classification?building=${encodeURIComponent(data.buildingId)}&energy=${encodeURIComponent(data.energy)}&date=${encodeURIComponent(d)}`,
          token,
          ctrl.signal,
        )
          .then((res) => (res.data ? { date: d, payload: res.data } : null))
          .catch(() => null),
      ),
    )
      .then((results) => {
        if (ctrl.signal.aborted) return;
        const valid = results.filter((x): x is { date: string; payload: ClassificationData } => x != null);
        setHistoryDays([...valid, { date: anchorDate, payload: data }]);
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setHistoryLoading(false);
      });

    return () => ctrl.abort();
  }, [data, anchorDate, token]);

  if (data.configurationState === 'unconfigured') {
    return <UnconfiguredNotice message={data.configurationMessage} onOpenConfig={onOpenConfig} />;
  }

  const categories = useMemo(
    () => [...(data.categories ?? [])].sort((a, b) => (b.usage ?? 0) - (a.usage ?? 0)),
    [data.categories],
  );
  const hasUnclassified = data.unclassifiedUsage != null && data.unclassifiedUsage > 0;
  const classifiedSum = categories.reduce((acc, c) => acc + (c.usage ?? 0), 0);
  const classifiedShare = data.totalUsage && data.totalUsage > 0 ? classifiedSum / data.totalUsage : null;
  const topCategory = categories[0] ?? null;

  const seriesDefs = useMemo(() => {
    const base = categories.map((c, idx) => ({
      id: c.id,
      name: c.name,
      color: CHART_PALETTE[idx % (CHART_PALETTE.length - 1)],
    }));
    if (hasUnclassified) {
      base.push({ id: '__unclassified__', name: '未分项', color: '#C9CDD4' });
    }
    return base;
  }, [categories, hasUnclassified]);

  const colorMap = useMemo(() => new Map(seriesDefs.map((s) => [s.id, s.color])), [seriesDefs]);

  const slices = [
    ...categories.map((c) => ({ id: c.id, name: c.name, usage: c.usage, share: c.share })),
    ...(hasUnclassified
      ? [
          {
            id: '__unclassified__',
            name: '未分项',
            usage: data.unclassifiedUsage!,
            share: data.totalUsage ? data.unclassifiedUsage! / data.totalUsage : null,
            isUnclassified: true,
          },
        ]
      : []),
  ];

  const trendDays = useMemo(() => {
    const list = historyDays.length > 0 ? historyDays : [{ date: anchorDate, payload: data }];
    return list.map(({ date: d, payload: p }) => {
      const values: Record<string, number | null> = {};
      for (const c of p.categories ?? []) {
        values[c.id] = c.usage;
      }
      if (p.unclassifiedUsage != null && p.unclassifiedUsage > 0) {
        values.__unclassified__ = p.unclassifiedUsage;
      }
      return { date: d, total: p.totalUsage, values };
    });
  }, [historyDays, anchorDate, data]);

  // Build weight-traceable allocation rows joining data.sources with inventory.categoryBindings (exact weightPpm).
  const allocationRows = useMemo(() => {
    const bindings = inventory?.categoryBindings ?? [];
    const catNameMap = new Map(categories.map((c) => [c.id, c.name]));
    const rows: Array<{
      key: string;
      terminalId: string;
      terminalName: string;
      itemCode: string;
      categoryId: string;
      categoryName: string;
      weightPct: number | null;
      meterUsage: number | null;
      allocatedUsage: number | null;
      bindingRevision: string;
      sourceTable: string;
      multiplier: number;
      coverageRate: number | null;
    }> = [];

    for (const src of data.sources ?? []) {
      const binding =
        bindings.find(
          (b) =>
            b.terminalId === src.terminalId &&
            b.itemCode === src.itemCode &&
            (!src.categoryBindingVersion || b.revision === src.categoryBindingVersion),
        ) ?? bindings.find((b) => b.terminalId === src.terminalId && b.itemCode === src.itemCode);

      const covRate = src.expected > 0 ? src.valid / src.expected : null;
      if (binding && binding.allocations.length > 0) {
        const totalPpm = binding.allocations.reduce((s, a) => s + a.weightPpm, 0);
        for (const alloc of binding.allocations) {
          const ratio = alloc.weightPpm / 1_000_000;
          rows.push({
            key: `${src.terminalId}:${src.itemCode}:${alloc.categoryId}`,
            terminalId: src.terminalId,
            terminalName: src.terminalName,
            itemCode: src.itemCode,
            categoryId: alloc.categoryId,
            categoryName: catNameMap.get(alloc.categoryId) ?? alloc.categoryId,
            weightPct: ratio * 100,
            meterUsage: src.usage,
            allocatedUsage: src.usage != null ? src.usage * ratio : null,
            bindingRevision: binding.revision,
            sourceTable: src.sourceTable,
            multiplier: src.multiplier,
            coverageRate: covRate,
          });
        }
        if (totalPpm < 1_000_000) {
          const remRatio = (1_000_000 - totalPpm) / 1_000_000;
          rows.push({
            key: `${src.terminalId}:${src.itemCode}:__unclassified__`,
            terminalId: src.terminalId,
            terminalName: src.terminalName,
            itemCode: src.itemCode,
            categoryId: '__unclassified__',
            categoryName: '未分项（剩余未分配权重）',
            weightPct: remRatio * 100,
            meterUsage: src.usage,
            allocatedUsage: src.usage != null ? src.usage * remRatio : null,
            bindingRevision: binding.revision,
            sourceTable: src.sourceTable,
            multiplier: src.multiplier,
            coverageRate: covRate,
          });
        }
      } else {
        rows.push({
          key: `${src.terminalId}:${src.itemCode}:__unclassified__`,
          terminalId: src.terminalId,
          terminalName: src.terminalName,
          itemCode: src.itemCode,
          categoryId: '__unclassified__',
          categoryName: '未分项（无正式分类绑定）',
          weightPct: 100,
          meterUsage: src.usage,
          allocatedUsage: src.usage,
          bindingRevision: src.categoryBindingVersion || '未绑定',
          sourceTable: src.sourceTable,
          multiplier: src.multiplier,
          coverageRate: covRate,
        });
      }
    }
    return rows;
  }, [data.sources, inventory?.categoryBindings, categories]);

  const filteredAllocRows = useMemo(
    () => (selectedCatId ? allocationRows.filter((r) => r.categoryId === selectedCatId) : allocationRows),
    [allocationRows, selectedCatId],
  );

  const selectedCatObj = selectedCatId === '__unclassified__'
    ? { id: '__unclassified__', name: '未分项', usage: data.unclassifiedUsage, share: data.totalUsage && data.unclassifiedUsage ? data.unclassifiedUsage / data.totalUsage : null }
    : categories.find((c) => c.id === selectedCatId) ?? null;

  const toggleSelect = (id: string) => setSelectedCatId((cur) => (cur === id ? null : id));

  return (
    <div className="space-y-4">
      {/* ── 1. Compact Classification Context Strip (Parent vs Sub-Items, Unclassified Check, No 4-KPI Wall) ── */}
      <section aria-label="分类分项汇总摘要" className="arco-card px-5 py-3 tabular">
        <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 text-[12px]">
          <div className="flex flex-wrap items-baseline gap-x-5 gap-y-1.5">
            <div className="flex items-baseline gap-1.5">
              <span className="text-muted">{energyLabels[data.energy] ?? data.energy}父级总计</span>
              <strong className="text-[16px] font-semibold text-fg">{fmtNum(data.totalUsage)}</strong>
              <span className="text-[11px] text-muted">{data.unit}</span>
              <span className="rounded-[2px] bg-primary-soft px-1.5 py-0 text-[11px] text-primary">
                覆盖 {fmtPct(data.totalCoverage.rate)}
              </span>
            </div>
            <span className="hidden h-3.5 w-px bg-line sm:inline-block" aria-hidden="true" />
            <div className="flex items-baseline gap-1.5">
              <span className="text-muted">已分项</span>
              <strong className="font-semibold text-fg">{fmtPct(classifiedShare)}</strong>
              <span className="text-[11px] text-muted">（{fmtNum(classifiedSum)} {data.unit}）</span>
              <span className={`text-[11px] ${hasUnclassified ? 'font-medium text-warning' : 'text-positive'}`}>
                {hasUnclassified ? `未分项 ${fmtNum(data.unclassifiedUsage ?? 0)} ${data.unit}` : '100% 已分项'}
              </span>
            </div>
            {topCategory && (
              <>
                <span className="hidden h-3.5 w-px bg-line sm:inline-block" aria-hidden="true" />
                <div className="flex items-baseline gap-1.5">
                  <span className="text-muted">最大子分项</span>
                  <strong className="font-semibold text-fg">{topCategory.name}</strong>
                  <span className="font-medium text-primary">{fmtPct(topCategory.share)}</span>
                </div>
              </>
            )}
          </div>
          <div className="flex flex-wrap items-center gap-3 text-[11px] text-muted">
            {data.carbon?.includedInTotal ? (
              <span>参考碳排 {fmtNum(data.carbon.kgCO2e)} kgCO₂e</span>
            ) : (
              <span>水资源单列不计碳排</span>
            )}
            <span className="font-mono">
              {data.buildingName} · {anchorDate} · {data.classificationRuleVersion}
            </span>
          </div>
        </div>
      </section>

      {/* ── 2. Split Analytical Workbench: Left Category Tree (3 cols) + Right Charts & Weight Trace (9 cols) ── */}
      <div className="grid gap-4 lg:grid-cols-12">
        {/* Left 3 Cols: Hierarchical Energy Classification Tree */}
        <aside aria-label="能源分类树" className="arco-card flex flex-col justify-between p-4 lg:col-span-3">
          <div className="space-y-3">
            <div className="flex items-center justify-between gap-2 border-b border-line pb-2.5">
              <div>
                <h2 className="text-[14px] font-medium text-fg">能源分类树与层级汇总</h2>
                <p className="mt-0.5 text-[11px] text-muted">点击节点联动趋势、构成与权重明细</p>
              </div>
              {selectedCatId && (
                <button
                  type="button"
                  onClick={() => setSelectedCatId(null)}
                  className="shrink-0 text-[11px] font-medium text-primary hover:underline"
                >
                  看全部
                </button>
              )}
            </div>

            {/* Parent Summary Node (Root) */}
            <div className="space-y-1 tabular">
              <button
                type="button"
                onClick={() => setSelectedCatId(null)}
                aria-current={selectedCatId == null ? 'true' : undefined}
                className={`w-full rounded-[4px] border px-3 py-2.5 text-left transition-colors ${
                  selectedCatId == null
                    ? 'border-primary/40 bg-primary-soft/60'
                    : 'border-line bg-fill-1/50 hover:bg-fill-1'
                }`}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="text-[12px] font-semibold text-fg">
                    {energyLabels[data.energy] ?? data.energy}正式总计
                  </span>
                  <span className="rounded-[2px] bg-primary/12 px-1.5 py-0 text-[10px] font-medium text-primary">
                    父级汇总
                  </span>
                </div>
                <div className="mt-1 flex items-baseline justify-between gap-2">
                  <span className="text-[16px] font-semibold text-fg">
                    {fmtNum(data.totalUsage)} <span className="text-[11px] font-normal text-muted">{data.unit}</span>
                  </span>
                  <span className="text-[11px] text-muted">100%</span>
                </div>
              </button>

              {/* Child Category Nodes (Indented cleanly to visually separate from parent total) */}
              <div className="ml-3 space-y-1 border-l border-line pl-2.5 pt-1">
                {categories.map((cat) => {
                  const isSel = selectedCatId === cat.id;
                  const color = colorMap.get(cat.id) ?? '#165DFF';
                  return (
                    <button
                      key={cat.id}
                      type="button"
                      data-cat-node={cat.id}
                      onClick={() => toggleSelect(cat.id)}
                      aria-current={isSel ? 'true' : undefined}
                      className={`w-full rounded-[4px] border px-2.5 py-2 text-left transition-colors ${
                        isSel
                          ? 'border-primary/40 bg-primary-soft/55'
                          : 'border-transparent hover:bg-fill-1'
                      }`}
                    >
                      <div className="flex items-center justify-between gap-2 text-[12px]">
                        <span className="flex min-w-0 items-center gap-1.5">
                          <span className="size-2 shrink-0 rounded-[2px]" style={{ backgroundColor: color }} />
                          <span className={`truncate ${isSel ? 'font-semibold text-primary' : 'font-medium text-fg'}`}>
                            {cat.name}
                          </span>
                        </span>
                        <span className="shrink-0 text-[11px] font-medium text-fg-2">{fmtPct(cat.share)}</span>
                      </div>
                      <div className="mt-1 flex items-baseline justify-between gap-2 text-[11px] text-muted">
                        <span className="font-medium text-fg">
                          {fmtNum(cat.usage)} {data.unit}
                        </span>
                        <span>覆盖 {fmtPct(cat.coverage.rate)}</span>
                      </div>
                      <div className="mt-1 h-[4px] w-full overflow-hidden rounded-full bg-fill-2">
                        <div
                          className="h-full rounded-full"
                          style={{ width: `${Math.max(2, (cat.share ?? 0) * 100)}%`, backgroundColor: color }}
                        />
                      </div>
                    </button>
                  );
                })}

                {hasUnclassified && (
                  <button
                    type="button"
                    data-cat-node="__unclassified__"
                    onClick={() => toggleSelect('__unclassified__')}
                    aria-current={selectedCatId === '__unclassified__' ? 'true' : undefined}
                    className={`w-full rounded-[4px] border px-2.5 py-2 text-left transition-colors ${
                      selectedCatId === '__unclassified__'
                        ? 'border-warning/50 bg-warning-soft/60'
                        : 'border-transparent hover:bg-fill-1'
                    }`}
                  >
                    <div className="flex items-center justify-between gap-2 text-[12px]">
                      <span className="flex items-center gap-1.5 font-medium text-warning">
                        <span className="size-2 rounded-[2px] bg-[#C9CDD4]" />
                        未分项（待绑定）
                      </span>
                      <span className="text-[11px] text-warning">
                        {fmtPct(data.totalUsage ? data.unclassifiedUsage! / data.totalUsage : null)}
                      </span>
                    </div>
                    <div className="mt-1 text-[11px] font-medium text-fg">
                      {fmtNum(data.unclassifiedUsage)} {data.unit}
                    </div>
                  </button>
                )}
              </div>
            </div>
          </div>

          <div className="mt-4 rounded-[2px] bg-fill-1/60 px-2.5 py-2 text-[11px] leading-relaxed text-muted">
            口径说明：父级总计按有效表计独立聚合，子分项按权重分配；二者分开展示，避免父子叠加造成重复计量。
          </div>
        </aside>

        {/* Right 9 Cols: Visual Stage + Synchronized Weight Allocation Traceability */}
        <div className="space-y-4 lg:col-span-9">
          {/* Top Visual Pair: 7-Day Multi-Category Trend (7 cols) + Interactive Structure Donut (5 cols) */}
          <div className="grid gap-4 lg:grid-cols-12">
            <section className="arco-card p-5 lg:col-span-7 2xl:col-span-8">
              <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
                <div>
                  <h2 className="text-[15px] font-medium leading-[22px] text-fg">
                    {selectedCatObj ? `${selectedCatObj.name} · 近 7 日趋势与占比变化` : '各分项近 7 日时间趋势与占比变化'}
                  </h2>
                  <p className="mt-0.5 text-[12px] text-muted tabular">
                    {historyLoading ? '正在加载近 7 日自然日分类数据…' : `自然日区间 ${trendDays[0]?.date ?? anchorDate} ~ ${anchorDate} · ${data.timezone}`}
                  </p>
                </div>
                <div className="inline-flex rounded-[2px] border border-line bg-fill-1 p-0.5 text-[12px]">
                  <button
                    type="button"
                    onClick={() => setTrendMode('value')}
                    className={`rounded-[2px] px-2.5 py-1 transition-colors ${
                      trendMode === 'value' ? 'bg-surface font-medium text-primary shadow-xs' : 'text-fg-2 hover:text-fg'
                    }`}
                  >
                    用量趋势
                  </button>
                  <button
                    type="button"
                    onClick={() => setTrendMode('share')}
                    className={`rounded-[2px] px-2.5 py-1 transition-colors ${
                      trendMode === 'share' ? 'bg-surface font-medium text-primary shadow-xs' : 'text-fg-2 hover:text-fg'
                    }`}
                  >
                    占比变化
                  </button>
                </div>
              </div>

              <StackedCategoryTrendChart
                days={trendDays}
                series={seriesDefs}
                unit={data.unit}
                mode={trendMode}
                selectedId={selectedCatId}
                onSelectCategory={toggleSelect}
              />
            </section>

            <section className="arco-card p-5 lg:col-span-5 2xl:col-span-4">
              <div className="mb-3 flex items-baseline justify-between gap-2">
                <div>
                  <h2 className="text-[15px] font-medium leading-[22px] text-fg">当日分项构成比例</h2>
                  <p className="mt-0.5 text-[12px] text-muted">点击扇区或条目与左侧分类树同步联动</p>
                </div>
                <span className="text-[12px] font-semibold text-fg tabular">
                  {fmtNum(data.totalUsage)} {data.unit}
                </span>
              </div>
              <EnergyStructureChart
                slices={slices}
                total={data.totalUsage}
                unit={data.unit}
                layout="stack"
                selectedId={selectedCatId}
                onSelectSlice={toggleSelect}
              />
            </section>
          </div>

          {/* Synchronized Category Weight & Meter Allocation Traceability Table */}
          <section className="arco-card overflow-hidden">
            <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line px-5 py-3">
              <div>
                <h2 className="text-[15px] font-medium text-fg">
                  {selectedCatObj
                    ? `${selectedCatObj.name} · 分类权重与计量来源追溯（${filteredAllocRows.length} 条映射）`
                    : `全部分项权重分配与计量点追溯明细（${filteredAllocRows.length} 条映射）`}
                </h2>
                <p className="mt-0.5 text-[11px] text-muted">
                  精确展示每块表计的分类分摊权重（weightPpm）、原始用量与归属分项用量，支持多分类拆分核验
                </p>
              </div>
              {hasUnclassified && (
                <button type="button" onClick={onOpenConfig} className="arco-btn">
                  去配置中心补充绑定
                </button>
              )}
            </div>
            <div className="overflow-x-auto">
              <table className="w-full text-left text-[12px]">
                <thead className="border-b border-line bg-fill-1 text-fg-2">
                  <tr>
                    <th className="whitespace-nowrap px-4 py-2.5 font-medium">归属分项</th>
                    <th className="whitespace-nowrap px-4 py-2.5 font-medium">计量点 / 终端</th>
                    <th className="whitespace-nowrap px-4 py-2.5 text-right font-medium">分摊权重</th>
                    <th className="whitespace-nowrap px-4 py-2.5 text-right font-medium">表计原始用量 ({data.unit})</th>
                    <th className="whitespace-nowrap px-4 py-2.5 text-right font-medium">分项归属用量 ({data.unit})</th>
                    <th className="whitespace-nowrap px-4 py-2.5 text-right font-medium">覆盖率</th>
                    <th className="whitespace-nowrap px-4 py-2.5 font-medium">绑定版本 / 来源表</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-line tabular">
                  {filteredAllocRows.length === 0 ? (
                    <tr>
                      <td colSpan={7} className="px-4 py-6 text-center text-muted">
                        当前选中分类暂无对应表计映射记录
                      </td>
                    </tr>
                  ) : (
                    filteredAllocRows.map((row) => (
                      <tr key={row.key} className="hover:bg-fill-1/60">
                        <td className="whitespace-nowrap px-4 py-2.5">
                          <span className="inline-flex items-center gap-1.5 font-medium text-fg">
                            <span
                              className="size-2 rounded-[2px]"
                              style={{ backgroundColor: colorMap.get(row.categoryId) ?? '#C9CDD4' }}
                            />
                            {row.categoryName}
                          </span>
                        </td>
                        <td className="min-w-[210px] px-4 py-2.5">
                          <div className="font-medium text-fg">{row.terminalName}</div>
                          <div className="font-mono text-[11px] text-muted">
                            {row.terminalId} / {row.itemCode} · ×{row.multiplier}
                          </div>
                        </td>
                        <td className="whitespace-nowrap px-4 py-2.5 text-right">
                          <span className="rounded-[2px] bg-fill-2 px-1.5 py-0.5 font-medium text-fg">
                            {row.weightPct != null ? `${row.weightPct.toFixed(1).replace(/\.0$/, '')}%` : '—'}
                          </span>
                        </td>
                        <td className="whitespace-nowrap px-4 py-2.5 text-right text-fg-2">{fmtNum(row.meterUsage)}</td>
                        <td className="whitespace-nowrap px-4 py-2.5 text-right font-semibold text-fg">
                          {fmtNum(row.allocatedUsage)}
                        </td>
                        <td className="whitespace-nowrap px-4 py-2.5 text-right text-muted">
                          {fmtPct(row.coverageRate)}
                        </td>
                        <td className="whitespace-nowrap px-4 py-2.5 font-mono text-[11px] text-muted">
                          <div>{row.bindingRevision}</div>
                          <div className="text-[10px] opacity-80">{row.sourceTable}</div>
                        </td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
          </section>
        </div>
      </div>
    </div>
  );
}

// ── F07 & F16: Unified Multi-Dimensional Analysis & Ranking Workbench ───────

function LiveDimensionsView({
  data,
  token,
  onChangeView,
  onDrillBuilding,
}: {
  data: DimensionsData;
  token: string;
  onChangeView: (view: string) => void;
  onDrillBuilding: (buildingId: string) => void;
}) {
  const rows = data.rows ?? [];
  const [selectedId, setSelectedId] = useState<string | null>(() => rows[0]?.id ?? null);
  const [prevDimData, setPrevDimData] = useState<DimensionsData | null>(null);
  const anchorDate = fmtShanghaiDate(data.from);
  const prevDate = anchorDate && anchorDate !== '—' ? shiftShanghaiDate(anchorDate, -1) : '';

  // Fetch previous natural day's exact same dimension + energy for true side-by-side grouped bar comparison.
  useEffect(() => {
    if (data.configurationState === 'unconfigured' || !prevDate) {
      setPrevDimData(null);
      return;
    }
    const ctrl = new AbortController();
    const qs = new URLSearchParams({ view: data.view, energy: data.energy, date: prevDate });
    liveGet<DimensionsData>(`/dimensions?${qs}`, token, ctrl.signal)
      .then((res) => {
        if (!ctrl.signal.aborted) setPrevDimData(res.data ?? null);
      })
      .catch(() => {
        if (!ctrl.signal.aborted) setPrevDimData(null);
      });
    return () => ctrl.abort();
  }, [data.configurationState, data.view, data.energy, prevDate, token]);

  useEffect(() => {
    if (rows.length === 0) {
      setSelectedId(null);
      return;
    }
    if (!selectedId || !rows.some((r) => r.id === selectedId)) {
      const firstRanked = rows.find((r) => r.rank != null) ?? rows[0];
      setSelectedId(firstRanked.id);
    }
  }, [rows, selectedId]);

  if (data.configurationState === 'unconfigured') {
    return <UnconfiguredNotice message={data.configurationMessage} />;
  }

  const canDrill = data.view === 'building';
  const prevRowMap = useMemo(
    () => new Map((prevDimData?.rows ?? []).map((r) => [r.id, r])),
    [prevDimData?.rows],
  );
  const rankedRows = useMemo(
    () => rows.filter((r) => r.rank != null && r.usage != null).sort((a, b) => (a.rank ?? 999) - (b.rank ?? 999)),
    [rows],
  );
  const unrankedRows = useMemo(() => rows.filter((r) => r.rank == null || r.usage == null), [rows]);

  const comparableTotal = rankedRows.reduce((acc, r) => acc + (r.usage ?? 0), 0);
  const comparableAvg = rankedRows.length > 0 ? comparableTotal / rankedRows.length : null;
  const highest = rankedRows[0] ?? null;
  const lowest = rankedRows.length > 0 ? rankedRows[rankedRows.length - 1] : null;
  const spreadAbs =
    highest?.usage != null && lowest?.usage != null ? highest.usage - lowest.usage : null;
  const spreadRatio =
    highest?.usage != null && lowest?.usage != null && lowest.usage > 0
      ? highest.usage / lowest.usage
      : null;

  const selectedRow = rows.find((r) => r.id === selectedId) ?? rows[0] ?? null;
  const selectedDiffPct =
    selectedRow?.usage != null && comparableAvg != null && comparableAvg > 0
      ? (selectedRow.usage - comparableAvg) / comparableAvg
      : null;
  const viewLabel = data.view === 'building' ? '建筑' : data.view === 'space' ? '区域空间' : '组织部门';

  const groupedItems = useMemo(
    () =>
      rows.map((r) => ({
        id: r.id,
        label: r.name,
        shortLabel: r.name.replace(/^[A-Z0-9]+\s*[·・]\s*/, ''),
        current: r.usage,
        baseline: prevRowMap.get(r.id)?.usage ?? null,
        comparable: r.rank != null,
        reason: r.reason || (statusLabels[r.status] ?? r.status),
      })),
    [rows, prevRowMap],
  );

  return (
    <div className="space-y-4">
      {/* ── 1. Compact Dimension Switcher & Dispersion Command Bar (Zero 4-Box KPI Repetition) ── */}
      <section aria-label="多维统计口径与极值差异摘要" className="arco-card flex flex-wrap items-center justify-between gap-3 px-5 py-3 text-[12px] tabular">
        <div className="flex flex-wrap items-center gap-3">
          <div className="inline-flex items-center gap-1 rounded-[2px] border border-line bg-fill-1 p-0.5" role="tablist" aria-label="切换统计维度">
            {[
              { id: 'building', label: '建筑对比' },
              { id: 'space', label: '区域空间' },
              { id: 'organization', label: '组织部门' },
            ].map((tab) => {
              const active = data.view === tab.id;
              return (
                <button
                  key={tab.id}
                  type="button"
                  role="tab"
                  aria-selected={active}
                  onClick={() => onChangeView(tab.id)}
                  className={`inline-flex h-7 items-center rounded-[2px] px-3 text-[12px] transition-colors ${
                    active ? 'bg-primary font-medium text-white' : 'text-fg-2 hover:bg-fill-2 hover:text-fg'
                  }`}
                >
                  {tab.label}
                </button>
              );
            })}
          </div>
          <span className="text-muted">
            {anchorDate} vs {prevDate} · {energyLabels[data.energy] ?? data.energy} ({data.unit})
          </span>
        </div>

        <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[12px]">
          {highest && (
            <span>
              <span className="text-muted">#1 最高：</span>
              <strong className="font-semibold text-fg">{highest.name}</strong> ({fmtNum(highest.usage)} {data.unit})
            </span>
          )}
          {spreadRatio != null && (
            <span>
              <span className="text-muted">极差：</span>
              <strong className="font-semibold text-warning">{spreadRatio.toFixed(2)}×</strong> ({fmtNum(spreadAbs)} {data.unit})
            </span>
          )}
          <span>
            <span className="text-muted">可比参与：</span>
            <strong className="font-semibold text-fg">{rankedRows.length}/{rows.length}</strong>
            {unrankedRows.length > 0 && <span className="ml-1 text-[11px] text-warning">({unrankedRows.length} 项未排名)</span>}
          </span>
        </div>
      </section>

      {/* ── 2. Primary Comparison Stage: Grouped Side-by-Side Column Chart + Horizontal Ranking Chart (8 cols) + Linked Meter Inspector (4 cols) ── */}
      <div className="grid items-start gap-4 lg:grid-cols-12">
        <div className="space-y-4 lg:col-span-8">
          {/* A. Side-by-Side Grouped Comparison Column Chart (Same Energy, Same Window, Same Unit) */}
          <section className="arco-card p-5">
            <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
              <div>
                <h2 className="text-[15px] font-medium leading-[22px] text-fg">
                  {viewLabel}分组对比柱状图（本期 {anchorDate} vs 前一日 {prevDate}）
                </h2>
                <p className="mt-0.5 text-[12px] text-muted">
                  同一能源（{energyLabels[data.energy] ?? data.energy}）、同一单位（{data.unit}）下各对象同期并列对比，点击柱组联动右侧计量溯源
                </p>
              </div>
              <span className="text-[11px] text-muted tabular">均值参考 {fmtNum(comparableAvg)} {data.unit}</span>
            </div>
            <GroupedComparisonBarChart
              items={groupedItems}
              unit={data.unit}
              currentLabel={`本期 (${anchorDate})`}
              baselineLabel={`前一日 (${prevDate})`}
              averageValue={comparableAvg}
              selectedId={selectedRow?.id ?? null}
              onSelect={(id) => setSelectedId(id)}
              height={246}
            />
          </section>

          {/* B. Horizontal Ranking Bar Chart with Mean Deviation */}
          <section className="arco-card p-5">
            <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
              <div>
                <h2 className="text-[15px] font-medium leading-[22px] text-fg">
                  {viewLabel}同口径横向排名柱状图与均值偏离（{energyLabels[data.energy] ?? data.energy}）
                </h2>
                <p className="mt-0.5 text-[12px] text-muted">
                  低覆盖或不可比对象保留实测值并置空名次；右侧百分比标示相对同口径均值的偏离幅度
                </p>
              </div>
            </div>
            <HorizontalRankingChart
              unit={data.unit}
              averageValue={comparableAvg}
              selectedId={selectedRow?.id ?? null}
              onSelect={(id) => setSelectedId(id)}
              items={rows.map((r) => ({
                id: r.id,
                name: r.name,
                usage: r.usage,
                rank: r.rank,
                coverageRate: r.coverage.rate,
                reason: r.reason || (statusLabels[r.status] ?? r.status),
              }))}
            />
          </section>
        </div>

        {/* Right 4 Cols: Linked Object Details + Metering Source Traceability */}
        <aside aria-label="选中对象详情与计量来源" className="arco-card flex flex-col justify-between p-5 lg:col-span-4">
          {selectedRow ? (
            <div className="space-y-4 tabular">
              <div className="flex items-start justify-between gap-2 border-b border-line pb-3">
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <span
                      className={`inline-flex h-5 items-center rounded-[2px] px-1.5 text-[11px] font-medium ${
                        selectedRow.rank != null ? 'bg-primary text-white' : 'bg-warning-soft text-warning'
                      }`}
                    >
                      {selectedRow.rank != null ? `第 ${selectedRow.rank} 名` : '未参与正式排名'}
                    </span>
                    <span className="truncate font-mono text-[11px] text-muted">{selectedRow.id}</span>
                  </div>
                  <h3 className="mt-1.5 text-[15px] font-medium text-fg">{selectedRow.name}</h3>
                </div>
                {canDrill && (
                  <button
                    type="button"
                    onClick={() => onDrillBuilding(selectedRow.id)}
                    className="arco-btn arco-btn-primary shrink-0 !h-7 !px-2.5 !text-[12px]"
                  >
                    <span>下钻总览</span>
                    <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
                  </button>
                )}
              </div>

              <div className="grid grid-cols-2 gap-3 rounded-[4px] bg-fill-1/60 p-3">
                <div>
                  <p className="text-[11px] text-muted">统计用量</p>
                  <p className="mt-0.5 text-[20px] font-semibold text-fg">
                    {fmtNum(selectedRow.usage)}{' '}
                    <span className="text-[11px] font-normal text-muted">{data.unit}</span>
                  </p>
                </div>
                <div>
                  <p className="text-[11px] text-muted">较同口径均值</p>
                  <p
                    className={`mt-0.5 text-[18px] font-semibold ${
                      selectedDiffPct == null
                        ? 'text-muted'
                        : selectedDiffPct > 0
                          ? 'text-warning'
                          : 'text-positive'
                    }`}
                  >
                    {selectedDiffPct != null
                      ? `${selectedDiffPct > 0 ? '+' : ''}${fmtPct(selectedDiffPct)}`
                      : '不可比'}
                  </p>
                </div>
                <div>
                  <p className="text-[11px] text-muted">数据覆盖率</p>
                  <p className="mt-0.5 text-[13px] font-medium text-fg">
                    {fmtPct(selectedRow.coverage.rate)}{' '}
                    <span className="text-[11px] font-normal text-muted">
                      ({selectedRow.coverage.valid}/{selectedRow.coverage.expected})
                    </span>
                  </p>
                </div>
                <div>
                  <p className="text-[11px] text-muted">可比状态</p>
                  <p className={`mt-0.5 text-[12px] font-medium ${selectedRow.rank != null ? 'text-positive' : 'text-warning'}`}>
                    {selectedRow.reason || (statusLabels[selectedRow.status] ?? selectedRow.status)}
                  </p>
                </div>
              </div>

              <div>
                <div className="mb-2 flex items-center justify-between text-[12px]">
                  <span className="font-medium text-fg">关联计量来源（{selectedRow.sources?.length ?? 0} 块表）</span>
                  <span className="text-[11px] text-muted">可溯源至表计与绑定版本</span>
                </div>
                {(selectedRow.sources ?? []).length === 0 ? (
                  <p className="rounded-[2px] bg-fill-1/60 px-3 py-2 text-[12px] text-muted">
                    当前对象暂无有效绑定表计来源
                  </p>
                ) : (
                  <ul className="divide-y divide-line rounded-[4px] border border-line text-[12px]">
                    {(selectedRow.sources ?? []).map((s, idx) => (
                      <li key={`${s.terminalId}-${s.itemCode}-${idx}`} className="px-3 py-2">
                        <div className="flex items-baseline justify-between gap-2">
                          <span className="truncate font-medium text-fg">{s.terminalName}</span>
                          <span className="shrink-0 font-semibold text-fg">
                            {fmtNum(s.usage)} <span className="text-[11px] font-normal text-muted">{s.unit}</span>
                          </span>
                        </div>
                        <div className="mt-0.5 flex flex-wrap items-center justify-between gap-2 font-mono text-[11px] text-muted">
                          <span>
                            {s.terminalId}/{s.itemCode} · ×{s.multiplier}
                          </span>
                          <span>
                            {s.valid}/{s.expected} · {s.objectBindingVersion || s.sourceTable}
                          </span>
                        </div>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            </div>
          ) : (
            <div className="flex h-full items-center justify-center text-[12px] text-muted">
              选择左侧排名对象查看明细与计量来源
            </div>
          )}
        </aside>
      </div>

      {/* On-Demand Collapsible Full Comparison Ledger (does not duplicate chart space) */}
      <CollapsibleSection
        title="全部对象同口径汇总明细表"
        badge={`${rows.length} 项`}
        subtitle="按需展开查看完整表格与状态说明"
      >
        <div className="overflow-x-auto">
          <table className="w-full text-left text-[12px]">
            <thead className="border-b border-line bg-fill-1 text-fg-2">
              <tr>
                <th className="px-4 py-2 font-medium">名次</th>
                <th className="px-4 py-2 font-medium">对象</th>
                <th className="px-4 py-2 text-right font-medium">用量 ({data.unit})</th>
                <th className="px-4 py-2 text-right font-medium">较均值偏差</th>
                <th className="px-4 py-2 text-right font-medium">覆盖率</th>
                <th className="px-4 py-2 font-medium">可比状态与说明</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line tabular">
              {rows.map((r) => {
                const dp =
                  r.rank != null && r.usage != null && comparableAvg != null && comparableAvg > 0
                    ? (r.usage - comparableAvg) / comparableAvg
                    : null;
                return (
                  <tr
                    key={r.id}
                    onClick={() => setSelectedId(r.id)}
                    className={`cursor-pointer hover:bg-fill-1/60 ${selectedRow?.id === r.id ? 'bg-primary-soft/40' : ''}`}
                  >
                    <td className="px-4 py-2 font-semibold text-fg">{r.rank != null ? `#${r.rank}` : '未排名'}</td>
                    <td className="px-4 py-2 font-medium text-fg">
                      {r.name} <span className="font-mono text-[11px] font-normal text-muted">({r.id})</span>
                    </td>
                    <td className="px-4 py-2 text-right font-medium text-fg">{fmtNum(r.usage)}</td>
                    <td className="px-4 py-2 text-right text-muted">
                      {dp != null ? `${dp > 0 ? '+' : ''}${fmtPct(dp)}` : '—'}
                    </td>
                    <td className="px-4 py-2 text-right text-muted">{fmtPct(r.coverage.rate)}</td>
                    <td className="px-4 py-2 text-muted">{r.reason || (statusLabels[r.status] ?? r.status)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </CollapsibleSection>
    </div>
  );
}

// ── F08: Unified Multi-Granularity Trend, YoY/MoM & Variance Analysis ───────

function LiveTrendView({ data, token }: { data: TrendData; token: string }) {
  const [granularity, setGranularity] = useState<'hour' | 'day' | 'month'>('hour');
  const [monthYear, setMonthYear] = useState<number>(() => {
    const parsed = Number((data.from || '2026').slice(0, 4));
    return Number.isFinite(parsed) && parsed >= 2020 ? parsed : 2026;
  });

  // Companion server-computed comparison series (zero frontend recalculation).
  const [prevDayTrend, setPrevDayTrend] = useState<TrendData | null>(null);
  const [daily14, setDaily14] = useState<RealtimeTrendData | null>(null);
  const [monthlyCur, setMonthlyCur] = useState<TargetsData | null>(null);
  const [monthlyPrev, setMonthlyPrev] = useState<TargetsData | null>(null);
  const [auxLoading, setAuxLoading] = useState(false);

  const anchorDate = fmtShanghaiDate(data.from);
  const prevDate = fmtShanghaiDate(data.previous.baselineFrom);

  useEffect(() => {
    if (data.configurationState === 'unconfigured' || !data.buildingId) return;
    const ctrl = new AbortController();
    setAuxLoading(true);

    if (granularity === 'hour') {
      if (data.previous.status === 'comparable' && prevDate && prevDate !== '—') {
        liveGet<TrendData>(
          `/trend?building=${encodeURIComponent(data.buildingId)}&energy=${encodeURIComponent(data.energy)}&date=${encodeURIComponent(prevDate)}`,
          token,
          ctrl.signal,
        )
          .then((res) => {
            if (!ctrl.signal.aborted) setPrevDayTrend(res.data ?? null);
          })
          .catch(() => {
            if (!ctrl.signal.aborted) setPrevDayTrend(null);
          })
          .finally(() => {
            if (!ctrl.signal.aborted) setAuxLoading(false);
          });
      } else {
        setPrevDayTrend(null);
        setAuxLoading(false);
      }
    } else if (granularity === 'day') {
      const qs = new URLSearchParams({ building: data.buildingId, energy: data.energy, days: '14' });
      if (anchorDate && anchorDate !== '—') qs.set('date', anchorDate);
      liveGet<RealtimeTrendData>(`/realtime-trend?${qs}`, token, ctrl.signal)
        .then((res) => {
          if (!ctrl.signal.aborted) setDaily14(res.data ?? null);
        })
        .catch(() => {
          if (!ctrl.signal.aborted) setDaily14(null);
        })
        .finally(() => {
          if (!ctrl.signal.aborted) setAuxLoading(false);
        });
    } else {
      const qCur = new URLSearchParams({ building: data.buildingId, energy: data.energy, year: String(monthYear) });
      const qPrev = new URLSearchParams({ building: data.buildingId, energy: data.energy, year: String(monthYear - 1) });
      Promise.all([
        liveGet<TargetsData>(`/targets?${qCur}`, token, ctrl.signal).catch(() => ({ data: null })),
        liveGet<TargetsData>(`/targets?${qPrev}`, token, ctrl.signal).catch(() => ({ data: null })),
      ])
        .then(([cur, prev]) => {
          if (ctrl.signal.aborted) return;
          setMonthlyCur(cur.data ?? null);
          setMonthlyPrev(prev.data ?? null);
        })
        .finally(() => {
          if (!ctrl.signal.aborted) setAuxLoading(false);
        });
    }

    return () => ctrl.abort();
  }, [data.configurationState, data.buildingId, data.energy, data.previous.status, anchorDate, prevDate, granularity, monthYear, token]);

  if (data.configurationState === 'unconfigured') {
    return <UnconfiguredNotice message={data.configurationMessage} />;
  }

  // Derive unified primary & comparable-prior series for the active granularity.
  const chartModel = useMemo(() => {
    if (granularity === 'hour') {
      const curPts = data.series ?? [];
      const cmpPts = data.previous.status === 'comparable' ? (prevDayTrend?.series ?? []) : [];
      const variance = curPts.map((pt, i) => ({
        label: pt.from.slice(11, 16),
        current: pt.usage,
        baseline: cmpPts[i]?.usage ?? null,
      }));
      return {
        title: `${data.buildingName} · ${energyLabels[data.energy] ?? data.energy}逐时双线趋势对照`,
        subtitle: `本期自然日 ${anchorDate}${cmpPts.length ? ` vs 可比上期 ${prevDate}` : ''} · ${data.timezone} [start, end)`,
        points: curPts,
        comparisonPoints: cmpPts,
        seriesLabel: `本期 (${anchorDate})`,
        comparisonLabel: `上期 (${prevDate})`,
        variance,
        xFormatter: undefined as ((pt: { from: string }, idx: number) => string) | undefined,
      };
    }
    if (granularity === 'day') {
      const all14 = daily14?.series ?? [];
      const half = Math.floor(all14.length / 2);
      const cmpPts = half > 0 ? all14.slice(0, half) : [];
      const curPts = half > 0 ? all14.slice(half) : all14;
      const variance = curPts.map((pt, i) => ({
        label: pt.from.slice(5, 10),
        current: pt.usage,
        baseline: cmpPts[i]?.usage ?? null,
      }));
      return {
        title: `${data.buildingName} · ${energyLabels[data.energy] ?? data.energy}逐日双周趋势对照`,
        subtitle: `近 ${curPts.length} 自然日 vs 前 ${cmpPts.length} 自然日对照 · ${data.timezone}`,
        points: curPts,
        comparisonPoints: cmpPts,
        seriesLabel: `近 ${curPts.length} 日实测`,
        comparisonLabel: `前 ${cmpPts.length} 日对照`,
        variance,
        xFormatter: undefined as ((pt: { from: string }, idx: number) => string) | undefined,
      };
    }
    // Month granularity: 12 natural months of monthYear vs (monthYear - 1)
    const curMonths = monthlyCur?.months ?? [];
    const prevMonths = monthlyPrev?.months ?? [];
    const curPts: BucketPoint[] = curMonths.map((m) => ({
      from: m.from,
      to: m.to,
      usage: m.usage,
      valid: m.coverage.valid,
      expected: m.coverage.expected,
    }));
    const cmpPts: BucketPoint[] = prevMonths.map((m) => ({
      from: m.from,
      to: m.to,
      usage: m.usage,
      valid: m.coverage.valid,
      expected: m.coverage.expected,
    }));
    const variance = curMonths.map((m, i) => ({
      label: `${m.month}月`,
      current: m.usage,
      baseline: prevMonths[i]?.usage ?? null,
    }));
    return {
      title: `${data.buildingName} · ${energyLabels[data.energy] ?? data.energy}逐月年度双线对照（${monthYear} vs ${monthYear - 1}）`,
      subtitle: `自然月边界（${monthYear}-01 ~ ${monthYear}-12）· 未完结月份标注缺测，严禁按 0 补齐 · ${data.timezone}`,
      points: curPts,
      comparisonPoints: cmpPts,
      seriesLabel: `${monthYear} 年实测`,
      comparisonLabel: `${monthYear - 1} 年同期`,
      variance,
      xFormatter: (pt: { from: string }, idx: number) => `${curMonths[idx]?.month ?? Number(pt.from.slice(5, 7))}月`,
    };
  }, [granularity, data, prevDayTrend, daily14, monthlyCur, monthlyPrev, anchorDate, prevDate, monthYear]);

  // Visual comparison helper for YoY/MoM magnitude bars.
  const prevMax = Math.max(data.current ?? 0, data.previous.baseline ?? 0, 1);
  const priorMax = Math.max(data.current ?? 0, data.priorYear.baseline ?? 0, 1);

  return (
    <div className="space-y-4">
      {/* ── 1. Unified Granularity Bar + Explainable YoY / MoM Change Region ── */}
      <section aria-label="趋势粒度与同环比变化分析" className="arco-card overflow-hidden">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line bg-fill-1/60 px-4 py-2">
          <div className="flex flex-wrap items-center gap-2">
            <div className="flex items-center gap-1" role="tablist" aria-label="切换时间粒度">
              {[
                { id: 'hour' as const, label: '逐时 (24h)' },
                { id: 'day' as const, label: '逐日 (近14日)' },
                { id: 'month' as const, label: '逐月 (年度12月)' },
              ].map((g) => {
                const active = granularity === g.id;
                return (
                  <button
                    key={g.id}
                    type="button"
                    role="tab"
                    aria-selected={active}
                    onClick={() => setGranularity(g.id)}
                    className={`inline-flex h-7 items-center rounded-[2px] px-3 text-[12px] transition-colors ${
                      active ? 'bg-primary font-medium text-white' : 'text-fg-2 hover:bg-fill-2 hover:text-fg'
                    }`}
                  >
                    {g.label}
                  </button>
                );
              })}
            </div>

            {granularity === 'month' && (
              <div className="inline-flex items-center gap-1.5 border-l border-line pl-2.5 text-[12px] text-fg-2">
                <span>对照年度</span>
                <select
                  aria-label="逐月对照年度"
                  value={monthYear}
                  onChange={(e) => setMonthYear(Number(e.target.value))}
                  className="arco-input !h-7 min-w-[84px] tabular"
                >
                  <option value={2026}>2026 年</option>
                  <option value={2025}>2025 年</option>
                </select>
              </div>
            )}
          </div>

          <span className="text-[11px] text-muted tabular">
            自然边界口径 · {data.timezone} · 覆盖 {fmtPct(data.coverage.rate)} ({data.coverage.valid}/{data.coverage.expected})
          </span>
        </div>

        {/* 3-Column Explainable Comparison Band (Current + MoM Magnitude + YoY Magnitude) */}
        <div className="grid divide-y divide-line lg:grid-cols-3 lg:divide-y-0 lg:divide-x tabular">
          {/* Current Period */}
          <div className="p-4 lg:p-5">
            <div className="flex items-center justify-between gap-2 text-[12px]">
              <span className="font-medium text-fg-2">本期统计总量（{anchorDate}）</span>
              <span className="rounded-[2px] bg-positive-soft px-1.5 py-0 text-[11px] leading-[18px] text-positive">
                覆盖 {fmtPct(data.coverage.rate)}
              </span>
            </div>
            <p className="mt-2 text-[28px] font-semibold leading-[34px] tracking-[-0.02em] text-fg">
              {fmtNum(data.current)}
              <span className="ml-1 text-[12px] font-normal text-muted">{data.unit}</span>
            </p>
            <p className="mt-1.5 text-[11px] text-muted">
              {data.buildingName} · {energyLabels[data.energy] ?? data.energy} · 规则 {data.ruleVersion || 'f08-v1'}
            </p>
          </div>

          {/* MoM / Period-over-Period with Visual Amplitude Bar */}
          <div className="p-4 lg:p-5">
            <div className="flex items-center justify-between gap-2 text-[12px]">
              <span className="font-medium text-fg-2">环比变化（较 {prevDate}）</span>
              <span
                className={`rounded-[2px] px-1.5 py-0 text-[11px] leading-[18px] ${
                  data.previous.status === 'comparable'
                    ? 'bg-primary-soft text-primary'
                    : 'bg-warning-soft text-warning'
                }`}
              >
                {statusLabels[data.previous.status] ?? data.previous.status}
              </span>
            </div>

            {data.previous.status === 'comparable' && data.previous.rate != null ? (
              <>
                <div className="mt-2 flex items-baseline gap-2.5">
                  <span
                    className={`inline-flex items-baseline gap-0.5 text-[24px] font-semibold leading-[30px] ${
                      data.previous.rate > 0 ? 'text-negative' : 'text-positive'
                    }`}
                  >
                    {data.previous.rate > 0 ? (
                      <ArrowUpRight className="h-4 w-4 self-center" aria-hidden="true" />
                    ) : (
                      <ArrowDownRight className="h-4 w-4 self-center" aria-hidden="true" />
                    )}
                    {data.previous.rate > 0 ? '+' : ''}
                    {fmtPct(data.previous.rate)}
                  </span>
                  <span className="text-[12px] font-medium text-fg-2">
                    {data.previous.delta != null && data.previous.delta > 0 ? '+' : ''}
                    {fmtNum(data.previous.delta)} {data.unit}
                  </span>
                </div>
                <div className="mt-2 space-y-1">
                  <div className="flex items-center gap-2 text-[11px]">
                    <span className="w-8 shrink-0 text-muted">本期</span>
                    <div className="h-[6px] flex-1 overflow-hidden rounded-[2px] bg-fill-2">
                      <div className="h-full rounded-[2px] bg-primary" style={{ width: `${((data.current ?? 0) / prevMax) * 100}%` }} />
                    </div>
                    <span className="w-20 text-right text-fg">{fmtNum(data.current)}</span>
                  </div>
                  <div className="flex items-center gap-2 text-[11px]">
                    <span className="w-8 shrink-0 text-muted">上期</span>
                    <div className="h-[6px] flex-1 overflow-hidden rounded-[2px] bg-fill-2">
                      <div className="h-full rounded-[2px] bg-[#0FC6C2]" style={{ width: `${((data.previous.baseline ?? 0) / prevMax) * 100}%` }} />
                    </div>
                    <span className="w-20 text-right text-muted">{fmtNum(data.previous.baseline)}</span>
                  </div>
                </div>
              </>
            ) : (
              <div className="mt-2 space-y-1">
                <p className="text-[18px] font-medium text-muted">不可比（不强行输出环比）</p>
                <p className="text-[11px] text-muted">
                  原因：{statusLabels[data.previous.status] ?? data.previous.status} · 环期参考值 {fmtNum(data.previous.baseline)} {data.unit}
                </p>
              </div>
            )}
          </div>

          {/* YoY with Explicit Comparability / Zero-Baseline / Scope Honesty */}
          <div className="bg-fill-1/30 p-4 lg:p-5">
            <div className="flex items-center justify-between gap-2 text-[12px]">
              <span className="font-medium text-fg-2">同比变化（去年同期 {fmtShanghaiDate(data.priorYear.baselineFrom)}）</span>
              <span
                className={`rounded-[2px] px-1.5 py-0 text-[11px] leading-[18px] ${
                  data.priorYear.status === 'comparable'
                    ? 'bg-primary-soft text-primary'
                    : 'bg-warning-soft text-warning'
                }`}
              >
                {statusLabels[data.priorYear.status] ?? data.priorYear.status}
              </span>
            </div>

            {data.priorYear.status === 'comparable' && data.priorYear.rate != null ? (
              <>
                <div className="mt-2 flex items-baseline gap-2.5">
                  <span
                    className={`inline-flex items-baseline gap-0.5 text-[24px] font-semibold leading-[30px] ${
                      data.priorYear.rate > 0 ? 'text-negative' : 'text-positive'
                    }`}
                  >
                    {data.priorYear.rate > 0 ? (
                      <ArrowUpRight className="h-4 w-4 self-center" aria-hidden="true" />
                    ) : (
                      <ArrowDownRight className="h-4 w-4 self-center" aria-hidden="true" />
                    )}
                    {data.priorYear.rate > 0 ? '+' : ''}
                    {fmtPct(data.priorYear.rate)}
                  </span>
                  <span className="text-[12px] font-medium text-fg-2">
                    {data.priorYear.delta != null && data.priorYear.delta > 0 ? '+' : ''}
                    {fmtNum(data.priorYear.delta)} {data.unit}
                  </span>
                </div>
                <div className="mt-2 space-y-1">
                  <div className="flex items-center gap-2 text-[11px]">
                    <span className="w-8 shrink-0 text-muted">本期</span>
                    <div className="h-[6px] flex-1 overflow-hidden rounded-[2px] bg-fill-2">
                      <div className="h-full rounded-[2px] bg-primary" style={{ width: `${((data.current ?? 0) / priorMax) * 100}%` }} />
                    </div>
                    <span className="w-20 text-right text-fg">{fmtNum(data.current)}</span>
                  </div>
                  <div className="flex items-center gap-2 text-[11px]">
                    <span className="w-8 shrink-0 text-muted">同期</span>
                    <div className="h-[6px] flex-1 overflow-hidden rounded-[2px] bg-fill-2">
                      <div className="h-full rounded-[2px] bg-[#86909C]" style={{ width: `${((data.priorYear.baseline ?? 0) / priorMax) * 100}%` }} />
                    </div>
                    <span className="w-20 text-right text-muted">{fmtNum(data.priorYear.baseline)}</span>
                  </div>
                </div>
              </>
            ) : (
              <div className="mt-2 space-y-1.5">
                <p className="text-[18px] font-semibold text-fg-2">
                  同期参考 {fmtNum(data.priorYear.baseline)}{' '}
                  <span className="text-[11px] font-normal text-muted">{data.unit}</span>
                </p>
                <p className="text-[11px] leading-relaxed text-muted">
                  因「{statusLabels[data.priorYear.status] ?? data.priorYear.status}」，仅保留去年同期量值参考，不强行输出同比涨跌幅。
                </p>
              </div>
            )}
          </div>
        </div>
      </section>

      {/* ── 2. Primary Comparison Stage: Side-by-Side Period Grouped Column Chart + Signed Delta Column Chart ── */}
      <section className="arco-card space-y-5 p-5">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <div>
            <h2 className="text-[15px] font-medium leading-[22px] text-fg">
              {chartModel.title.replace('双线趋势对照', '并列柱状对比与正负差额分析')}
            </h2>
            <p className="mt-0.5 text-[12px] text-muted tabular">
              {auxLoading ? '正在加载可比上期分组对比数据…' : `${chartModel.subtitle} · 主图采用本期/上期并列柱状图与正负差额柱状图`}
            </p>
          </div>
          <span className="text-[11px] text-muted">悬停任意时段查看本期、上期、绝对差额与增减率</span>
        </div>

        <GroupedComparisonBarChart
          items={chartModel.variance.map((v, idx) => ({
            id: `${v.label}-${idx}`,
            label: v.label,
            shortLabel: v.label,
            current: v.current,
            baseline: v.baseline,
          }))}
          unit={data.unit}
          currentLabel={chartModel.seriesLabel}
          baselineLabel={chartModel.comparisonLabel}
          height={252}
          wide
        />

        {chartModel.comparisonPoints.length > 0 && (
          <div className="border-t border-line pt-4">
            <h3 className="mb-2 text-[13px] font-medium text-fg">正负差额柱状图（本期 − 可比上期，红色上升 / 绿色下降）</h3>
            <PeriodVarianceBarChart points={chartModel.variance} unit={data.unit} wide />
          </div>
        )}
      </section>

      {/* ── 3. Auxiliary Continuous Dual-Line Trend Curve ── */}
      <section className="arco-card p-5">
        <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
          <div>
            <h2 className="text-[14px] font-medium text-fg">辅助趋势折线对照（连续负荷形态观察）</h2>
            <p className="mt-0.5 text-[12px] text-muted">辅助核查周期内峰值漂移与缺测断档（缺测时段绝不按 0 连线）</p>
          </div>
        </div>
        <TrendAreaChart
          points={chartModel.points}
          comparisonPoints={chartModel.comparisonPoints}
          seriesLabel={chartModel.seriesLabel}
          comparisonLabel={chartModel.comparisonLabel}
          unit={data.unit}
          height={224}
          wide
          xLabelFormatter={chartModel.xFormatter}
        />
      </section>
    </div>
  );
}

// ── F09: Sankey Energy Flow & Meter Balance ─────────────────────────────────

const balanceStatusLabels: Record<string, string> = {
  balanced: '总分表平衡',
  complete: '完整可读',
  unallocated: '存在未分配/待核查差额',
  negative_difference: '负差额（待核查表计或倍率）',
  missing_data: '缺测无法闭合',
  partial: '部分覆盖',
  suspect_data: '含可疑读数',
  meter_change: '区间存在换表',
  time_mismatch: '时间窗口未对齐',
};

function LiveBalanceView({ data }: { data: BalanceData }) {
  const [zoom, setZoom] = useState<number>(1);
  const [selectedNode, setSelectedNode] = useState<SankeyMeterNodeMeta | null>(null);

  useEffect(() => {
    setSelectedNode(null);
    setZoom(1);
  }, [data.buildingId, data.energy, data.from]);

  if (!data.topologyConfigured || !data.report) {
    return (
      <section
        aria-label="未配置物理拓扑提示"
        className="arco-card flex min-h-[480px] flex-col items-center justify-center gap-2 p-8 text-center"
      >
        <h2 className="text-[15px] font-semibold text-fg">当前对象尚未配置物理总分表拓扑</h2>
        <p className="max-w-md text-[12px] leading-relaxed text-muted">
          {data.message || '未检测到已确认的父总表与直接分表绑定关系，系统严禁生成虚假能源流向。'}
        </p>
      </section>
    );
  }

  const rep = data.report;
  const unit = rep.Unit;
  const parentUsage = rep.Parent?.Usage ?? null;
  const highImbalance = rep.DifferenceRate != null && Math.abs(rep.DifferenceRate) >= 0.05;

  const stripBuildingPrefix = (rawName: string): string => {
    const prefix = `${data.buildingName} · `;
    return rawName.startsWith(prefix) ? rawName.slice(prefix.length) : rawName;
  };

  const parentMeta: SankeyMeterNodeMeta = {
    id: rep.Parent
      ? `parent::${rep.Parent.Meter.Point.Key.TerminalID}:${rep.Parent.Meter.Point.Key.ItemCode}`
      : 'parent::main',
    name: rep.Parent?.Meter?.Name || '父总表',
    displayName: stripBuildingPrefix(rep.Parent?.Meter?.Name || '父总表'),
    role: 'parent',
    terminalId: rep.Parent?.Meter.Point.Key.TerminalID ?? '—',
    itemCode: rep.Parent?.Meter.Point.Key.ItemCode ?? '—',
    zone: (rep.Parent?.Meter.Zones ?? []).join(', ') || '全建筑总进线',
    usage: parentUsage,
    share: 1,
    status: rep.Parent?.Status ?? rep.Status,
    statusLabel: balanceStatusLabels[rep.Parent?.Status ?? rep.Status] ?? (rep.Parent?.Status ?? rep.Status),
    topologyVersion: data.topologyVersion,
    note: '物理总进线计量点，不与下级直接分表重复相加。',
  };

  const childMetas: SankeyMeterNodeMeta[] = (rep.Children ?? []).map((ch) => ({
    id: `child::${ch.Meter.Point.Key.TerminalID}:${ch.Meter.Point.Key.ItemCode}`,
    name: ch.Meter.Name,
    displayName: stripBuildingPrefix(ch.Meter.Name),
    role: 'child',
    terminalId: ch.Meter.Point.Key.TerminalID,
    itemCode: ch.Meter.Point.Key.ItemCode,
    zone: (ch.Meter.Zones ?? []).join(', ') || '—',
    usage: ch.Usage,
    share: parentUsage && parentUsage > 0 && ch.Usage != null ? ch.Usage / parentUsage : null,
    status: ch.Status,
    statusLabel: balanceStatusLabels[ch.Status] ?? ch.Status,
    topologyVersion: data.topologyVersion,
  }));

  const diffMeta: SankeyMeterNodeMeta | null =
    rep.Difference != null && Math.abs(rep.Difference) > 0.001
      ? {
          id: 'diff::unallocated',
          name: '未分配 / 待核查差额',
          role: 'diff',
          terminalId: 'DIFF (父总表 − 分表合计)',
          itemCode: 'unallocated_balance_diff',
          zone: '待核查边界',
          usage: rep.Difference,
          share: rep.DifferenceRate,
          status: rep.Status,
          statusLabel: balanceStatusLabels[rep.Status] ?? rep.Status,
          topologyVersion: data.topologyVersion,
          note: '按 PRD 统计口径，总分表差额标示为「未分配 / 待核查差额」，不可直接定性为线损，需排查未挂表回路、倍率或时钟对齐。',
        }
      : null;

  return (
    <div className="relative">
      {/* Full-Canvas Studio Sankey Workspace (Zero 4-KPI Grid / Zero Bottom Table / Zero Boilerplate) */}
      <section aria-label="物理能流与总分表平衡画布" className="arco-card overflow-hidden">
        {/* Compact Canvas Toolbar: Inline Flow Telemetry + Zoom / Fit / Reset Controls */}
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line bg-fill-1/40 px-5 py-2.5 text-[12px] tabular">
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
            <span className="font-semibold text-fg">
              {data.buildingName} · {energyLabels[data.energy] ?? data.energy}物理能流 ({unit})
            </span>
            <span className="text-muted">
              总表 <strong className="text-fg">{fmtNum(parentUsage)}</strong> · 分表合计{' '}
              <strong className="text-fg">{fmtNum(rep.ChildrenUsage)}</strong> ({childMetas.length} 块)
            </span>
            {rep.Difference != null && (
              <span className={highImbalance ? 'font-medium text-warning' : 'text-fg-2'}>
                未分配差额 <strong>{fmtNum(rep.Difference)} {unit}</strong> ({fmtPct(rep.DifferenceRate)})
              </span>
            )}
            <span
              className={`rounded-[2px] px-1.5 py-0 text-[10px] font-medium leading-[18px] ${
                rep.Status === 'balanced'
                  ? 'bg-positive-soft text-positive'
                  : highImbalance
                    ? 'bg-warning-soft text-warning'
                    : 'bg-fill-2 text-fg-2'
              }`}
            >
              {balanceStatusLabels[rep.Status] ?? rep.Status}
            </span>
          </div>

          <div className="flex items-center gap-1" role="toolbar" aria-label="能流画布视图控制">
            <button
              type="button"
              data-sankey-zoom="in"
              onClick={() => setZoom((z) => Math.min(1.4, Number((z + 0.15).toFixed(2))))}
              title="放大视图"
              className="inline-flex h-7 items-center gap-1 rounded-[2px] border border-line bg-surface px-2 text-[11px] text-fg-2 hover:bg-fill-1 hover:text-fg"
            >
              <ZoomIn className="h-3.5 w-3.5" aria-hidden="true" />
              <span>放大</span>
            </button>
            <button
              type="button"
              data-sankey-zoom="out"
              onClick={() => setZoom((z) => Math.max(0.75, Number((z - 0.15).toFixed(2))))}
              title="缩小视图"
              className="inline-flex h-7 items-center gap-1 rounded-[2px] border border-line bg-surface px-2 text-[11px] text-fg-2 hover:bg-fill-1 hover:text-fg"
            >
              <ZoomOut className="h-3.5 w-3.5" aria-hidden="true" />
              <span>缩小</span>
            </button>
            <button
              type="button"
              data-sankey-zoom="fit"
              onClick={() => setZoom(1)}
              title="适应画布"
              className="inline-flex h-7 items-center gap-1 rounded-[2px] border border-line bg-surface px-2 text-[11px] text-fg-2 hover:bg-fill-1 hover:text-fg"
            >
              <Maximize2 className="h-3.5 w-3.5" aria-hidden="true" />
              <span>适应画布</span>
            </button>
            <button
              type="button"
              data-sankey-zoom="reset"
              onClick={() => {
                setZoom(1);
                setSelectedNode(null);
              }}
              title="重置视图与选中状态"
              className="inline-flex h-7 items-center gap-1 rounded-[2px] border border-line bg-surface px-2 text-[11px] text-fg-2 hover:bg-fill-1 hover:text-fg"
            >
              <RotateCcw className="h-3.5 w-3.5" aria-hidden="true" />
              <span>重置</span>
            </button>
          </div>
        </div>

        {/* Dedicated Full-Viewport Sankey Canvas */}
        <div className="bg-surface px-4 py-3">
          <EnergySankeyChart
            parentNode={parentMeta}
            childNodes={childMetas}
            diffNode={diffMeta}
            unit={unit}
            zoom={zoom}
            selectedNodeId={selectedNode?.id ?? null}
            onSelectNode={(node) => setSelectedNode(node)}
            height="clamp(540px, calc(100vh - 185px), 780px)"
          />
        </div>
      </section>

      {/* On-Demand Right Slide-Over Node Inspector Drawer */}
      {selectedNode && createPortal(
        <div className="fixed inset-0 z-50 flex justify-end bg-slate-900/20" onClick={() => setSelectedNode(null)}>
        <aside
          role="dialog"
          aria-label="能流节点计量溯源详情"
          data-f09-node-drawer
          onClick={(e) => e.stopPropagation()}
          className="flex h-full w-full max-w-[390px] flex-col border-l border-line bg-surface shadow-2xl tabular"
        >
          <div className="flex items-start justify-between gap-3 border-b border-line px-5 py-4">
            <div>
              <span className="rounded-[2px] bg-primary-soft px-1.5 py-0.5 text-[10px] font-medium text-primary">
                {selectedNode.role === 'parent' ? '父总表节点' : selectedNode.role === 'diff' ? '总分表平衡差额' : '直接分表节点'}
              </span>
              <h3 className="mt-1.5 text-[15px] font-semibold text-fg">{selectedNode.name}</h3>
            </div>
            <button
              type="button"
              onClick={() => setSelectedNode(null)}
              aria-label="关闭节点详情抽屉"
              className="rounded-[4px] p-1 text-muted hover:bg-fill-1 hover:text-fg"
            >
              <X className="h-4 w-4" aria-hidden="true" />
            </button>
          </div>

          <div className="flex-1 space-y-4 overflow-y-auto p-5 text-[12px]">
            <div className="grid grid-cols-2 gap-3 rounded-[4px] bg-fill-1/60 p-3.5">
              <div>
                <span className="text-[11px] text-muted">实测用量</span>
                <p className="mt-0.5 text-[18px] font-semibold text-fg">
                  {fmtNum(selectedNode.usage)} <span className="text-[11px] font-normal text-muted">{unit}</span>
                </p>
              </div>
              <div>
                <span className="text-[11px] text-muted">占父总表比例</span>
                <p className="mt-0.5 text-[16px] font-semibold text-primary">
                  {selectedNode.share != null ? fmtPct(Math.abs(selectedNode.share)) : '—'}
                </p>
              </div>
            </div>

            <dl className="divide-y divide-line rounded-[4px] border border-line text-[12px]">
              <div className="flex justify-between gap-3 px-3.5 py-2.5">
                <dt className="text-muted">计量点编号</dt>
                <dd className="font-mono text-right text-fg">{selectedNode.terminalId}</dd>
              </div>
              <div className="flex justify-between gap-3 px-3.5 py-2.5">
                <dt className="text-muted">采集项编码</dt>
                <dd className="font-mono text-right text-fg">{selectedNode.itemCode}</dd>
              </div>
              <div className="flex justify-between gap-3 px-3.5 py-2.5">
                <dt className="text-muted">归属区域</dt>
                <dd className="text-right text-fg">{selectedNode.zone}</dd>
              </div>
              <div className="flex justify-between gap-3 px-3.5 py-2.5">
                <dt className="text-muted">平衡与质量状态</dt>
                <dd className="text-right font-medium text-fg">{selectedNode.statusLabel}</dd>
              </div>
              <div className="flex justify-between gap-3 px-3.5 py-2.5">
                <dt className="text-muted">物理拓扑版本</dt>
                <dd className="font-mono text-right text-muted">{selectedNode.topologyVersion || '—'}</dd>
              </div>
              <div className="flex justify-between gap-3 px-3.5 py-2.5">
                <dt className="text-muted">统计自然窗口</dt>
                <dd className="text-right text-muted">
                  {fmtShanghaiDate(data.from)} ~ {fmtShanghaiDate(data.to)}
                </dd>
              </div>
            </dl>

            {selectedNode.note && (
              <p className="rounded-[4px] border border-warning/30 bg-warning-soft/40 px-3.5 py-2.5 text-[11px] leading-relaxed text-fg-2">
                {selectedNode.note}
              </p>
            )}
          </div>
        </aside>
        </div>,
        document.body,
      )}
    </div>
  );
}

// ── F10: Anomaly & Waste Candidate Diagnostic Workbench ─────────────────────

function LiveAnomalyView({
  data,
  token = '',
  inventory = null,
}: {
  data: AnomalyData;
  token?: string;
  inventory?: InventoryData | null;
}) {
  const [chartView, setChartView] = useState<'history' | 'hourly'>('history');
  const [dayTrend, setDayTrend] = useState<TrendData | null>(null);
  const [daySources, setDaySources] = useState<SourceTrace[]>([]);
  const [selectedEventKey, setSelectedEventKey] = useState<string>('__usage__');

  const anchorDate = fmtShanghaiDate(data.from);

  // Fetch observation-day 24h load profile & metering sources for evidence drilldown (zero invented rules).
  useEffect(() => {
    if (!data.buildingId || !anchorDate || anchorDate === '—') return;
    const ctrl = new AbortController();
    const qs = new URLSearchParams({ building: data.buildingId, energy: data.energy, date: anchorDate });

    Promise.all([
      liveGet<TrendData>(`/trend?${qs}`, token, ctrl.signal).catch(() => ({ data: null })),
      liveGet<ClassificationData>(`/classification?${qs}`, token, ctrl.signal).catch(() => ({ data: null })),
    ]).then(([tr, cl]) => {
      if (ctrl.signal.aborted) return;
      setDayTrend(tr.data ?? null);
      setDaySources(cl.data?.sources ?? []);
    });

    return () => ctrl.abort();
  }, [data.buildingId, data.energy, anchorDate, token]);

  const isUsageCandidate = data.status === 'usage_candidate';
  const isMeteringAnomaly = data.status === 'metering_anomaly';
  const isInsufficient = data.status === 'insufficient_evidence';

  // Matched server rule definition from inventory.anomalyRules (exact threshold traceability).
  const matchedRule = useMemo(() => {
    const rules = inventory?.anomalyRules ?? [];
    return (
      rules.find((r) => r.revision === data.activeRuleRevision) ??
      rules.find((r) => r.objectId === data.buildingId && r.energy === data.energy) ??
      null
    );
  }, [inventory?.anomalyRules, data.activeRuleRevision, data.buildingId, data.energy]);

  // Chronological timeline: oldest history day -> newest history day -> observation day.
  const historySeries = useMemo(() => {
    const sortedHist = [...(data.historyDays ?? [])].sort((a, b) => a.from.localeCompare(b.from));
    return [
      ...sortedHist,
      {
        from: data.from,
        to: data.to,
        usage: data.current,
        valid: data.coverage.valid,
        expected: data.coverage.expected,
      },
    ];
  }, [data.historyDays, data.from, data.to, data.current, data.coverage]);

  const anomalyHitIndices = useMemo(() => {
    if (!isUsageCandidate && !isMeteringAnomaly) return [];
    return historySeries.length > 0 ? [historySeries.length - 1] : [];
  }, [isUsageCandidate, isMeteringAnomaly, historySeries.length]);

  // Hourly profile spike indices: ONLY highlight when backend itself flagged usage_candidate (not inventing new rule!).
  const hourlySeries = dayTrend?.series ?? [];
  const baselineHourlyRef = data.baseline != null ? data.baseline / 24 : null;
  const hourlyHighlightIndices = useMemo(() => {
    if (!isUsageCandidate || baselineHourlyRef == null || hourlySeries.length === 0) return [];
    // Mark hours exceeding 1.25x baseline hourly average ONLY for visual localization of the server-flagged candidate day.
    const hits: number[] = [];
    hourlySeries.forEach((pt, idx) => {
      if (pt.usage != null && pt.usage > baselineHourlyRef * 1.25) hits.push(idx);
    });
    return hits.slice(0, 8);
  }, [isUsageCandidate, baselineHourlyRef, hourlySeries]);

  const exceptions = data.exceptions ?? [];

  // Unified clickable diagnostic items (Usage Candidate + Metering Quality Exceptions).
  const diagnosticItems = useMemo(() => {
    const list: Array<{
      key: string;
      kind: 'usage_candidate' | 'metering_quality' | 'insufficient_or_normal';
      title: string;
      badge: string;
      summary: string;
      timeRange: string;
      ruleInfo: string;
      terminalId?: string;
      errorType?: string;
      count?: number;
    }> = [];

    list.push({
      key: '__usage__',
      kind: isUsageCandidate
        ? 'usage_candidate'
        : isMeteringAnomaly
          ? 'metering_quality'
          : 'insufficient_or_normal',
      title: isUsageCandidate
        ? `${data.buildingName} · ${energyLabels[data.energy] ?? data.energy}用能异常候选`
        : isMeteringAnomaly
          ? `${data.buildingName} · 计量质量异常`
          : `${data.buildingName} · ${statusLabels[data.status] ?? data.status}`,
      badge: isUsageCandidate
        ? '用能异常候选（待核查）'
        : isMeteringAnomaly
          ? '计量质量问题'
          : isInsufficient
            ? '证据不足'
            : '未触发候选',
      summary:
        data.delta != null && data.changeRate != null
          ? `偏差 ${data.delta > 0 ? '+' : ''}${fmtNum(data.delta)} ${data.unit}（${data.changeRate > 0 ? '+' : ''}${fmtPct(data.changeRate)}）`
          : data.reason
            ? (statusLabels[data.reason] ?? data.reason)
            : '当前样本未命中异常阈值',
      timeRange: `${fmtShanghaiDate(data.from)} 00:00 ~ ${fmtShanghaiDate(data.to)} 00:00 (${data.timezone})`,
      ruleInfo: `${data.activeRuleRevision || '未配置'} · 引擎 ${data.ruleVersion || 'f10-f17-v1'}`,
    });

    exceptions.forEach((ex, i) => {
      list.push({
        key: `ex-${ex.terminalId}-${ex.errorType}-${i}`,
        kind: 'metering_quality',
        title: `终端 ${ex.terminalId} 采集质量异常`,
        badge: '计量质量问题',
        summary: `${ex.errorType} · 累计 ${ex.count} 次`,
        timeRange: `观测窗口 ${fmtShanghaiDate(data.from)} (${data.timezone})`,
        ruleInfo: '底层采集质量与快照完整性规则',
        terminalId: ex.terminalId,
        errorType: ex.errorType,
        count: ex.count,
      });
    });

    return list;
  }, [data, isUsageCandidate, isMeteringAnomaly, isInsufficient, exceptions]);

  useEffect(() => {
    if (!diagnosticItems.some((it) => it.key === selectedEventKey)) {
      setSelectedEventKey('__usage__');
    }
  }, [diagnosticItems, selectedEventKey]);

  const activeItem = diagnosticItems.find((it) => it.key === selectedEventKey) ?? diagnosticItems[0];
  const matchedMeterInfo = useMemo(() => {
    if (!activeItem?.terminalId) return null;
    return (inventory?.meters ?? []).find((m) => m.terminalId === activeItem.terminalId) ?? null;
  }, [activeItem, inventory?.meters]);

  const verdictTone = isUsageCandidate
    ? 'text-warning'
    : isMeteringAnomaly
      ? 'text-negative'
      : isInsufficient
        ? 'text-fg-2'
        : 'text-positive';

  const verdictBorder = isUsageCandidate
    ? 'border-l-warning'
    : isMeteringAnomaly
      ? 'border-l-negative'
      : isInsufficient
        ? 'border-l-line-strong'
        : 'border-l-positive';

  return (
    <div className="space-y-4">
      {/* ── 1. Compact Rule Verdict & Threshold Command Bar (Zero 4-Box KPI Repetition) ── */}
      <section
        aria-label="异常识别判定与规则门禁命令条"
        className={`arco-card flex flex-wrap items-center justify-between gap-3 border-l-[3px] px-5 py-3 text-[12px] tabular ${verdictBorder}`}
      >
        <div className="flex flex-wrap items-center gap-2.5">
          <span
            className={`rounded-[2px] px-2 py-0.5 text-[11px] font-semibold ${
              isUsageCandidate
                ? 'bg-warning-soft text-warning'
                : isMeteringAnomaly
                  ? 'bg-negative-soft text-negative'
                  : isInsufficient
                    ? 'bg-fill-2 text-fg-2'
                    : 'bg-positive-soft text-positive'
            }`}
          >
            {statusLabels[data.status] ?? data.status}
          </span>
          <span className="font-medium text-fg">
            {data.buildingName} · {energyLabels[data.energy] ?? data.energy} ({anchorDate})
          </span>
          <span className="text-[11px] text-muted">
            实测 <strong className="text-fg">{fmtNum(data.current)}</strong> vs 基线{' '}
            <strong className="text-fg">{fmtNum(data.baseline)}</strong> {data.unit} ({data.baselineCount} 天样本)
          </span>
          {data.changeRate != null && (
            <span className={`font-semibold ${verdictTone}`}>
              偏差 {data.changeRate > 0 ? '+' : ''}
              {fmtPct(data.changeRate)}
              {data.delta != null ? ` (${data.delta > 0 ? '+' : ''}${fmtNum(data.delta)} ${data.unit})` : ''}
            </span>
          )}
        </div>

        <div className="flex flex-wrap items-center gap-3 text-[11px] text-muted">
          <span>
            覆盖 <strong className="text-fg-2">{fmtPct(data.coverage.rate)}</strong> ({data.coverage.valid}/{data.coverage.expected})
          </span>
          <span>
            规则 <strong className="font-mono text-fg-2">{data.activeRuleRevision || '未配置'}</strong>
          </span>
          {matchedRule && (
            <span>
              门禁 ≥ +{fmtNum(matchedRule.minimumAbsoluteIncrease)} {data.unit} 且 ≥ +{fmtPct(matchedRule.minimumRelativeIncrease)}
            </span>
          )}
        </div>
      </section>

      {/* ── 2. Reversed Triage-First Diagnostic Split: Left 4-Col Event Queue -> Right 8-Col Evidence & Meter Verification Stage ── */}
      <div className="grid items-start gap-4 lg:grid-cols-12">
        {/* Left 4 Cols: Anomaly Event Triage Queue + Selected Event Boundary Spec */}
        <aside aria-label="异常分类分诊队列与规则边界" className="arco-card flex flex-col gap-4 p-5 lg:col-span-4">
          <div>
            <div className="mb-2.5 flex items-center justify-between gap-2 border-b border-line pb-2">
              <div>
                <h2 className="text-[14px] font-semibold text-fg">异常分诊队列（先定性、再溯源）</h2>
                <p className="mt-0.5 text-[11px] text-muted">严格区分用能异常候选与采集质量异常</p>
              </div>
              <span className="rounded-[2px] bg-fill-2 px-1.5 py-0.5 text-[11px] font-medium text-fg-2 tabular">
                {diagnosticItems.length} 项
              </span>
            </div>

            <div className="space-y-2">
              {diagnosticItems.map((it) => {
                const isSel = activeItem?.key === it.key;
                const badgeClass =
                  it.kind === 'usage_candidate'
                    ? 'bg-warning-soft text-warning'
                    : it.kind === 'metering_quality'
                      ? 'bg-negative-soft text-negative'
                      : 'bg-fill-2 text-fg-2';
                const accentBar =
                  it.kind === 'usage_candidate'
                    ? 'border-l-warning'
                    : it.kind === 'metering_quality'
                      ? 'border-l-negative'
                      : 'border-l-line-strong';
                return (
                  <button
                    key={it.key}
                    type="button"
                    data-anomaly-item={it.key}
                    onClick={() => setSelectedEventKey(it.key)}
                    className={`w-full rounded-[4px] border border-l-[3px] px-3 py-2.5 text-left transition-colors ${accentBar} ${
                      isSel
                        ? 'border-primary/50 bg-primary-soft/45'
                        : 'border-line bg-surface hover:bg-fill-1'
                    }`}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate text-[12px] font-medium text-fg">{it.title}</span>
                      <span className={`shrink-0 rounded-[2px] px-1.5 py-0 text-[10px] font-medium leading-[16px] ${badgeClass}`}>
                        {it.badge}
                      </span>
                    </div>
                    <p className="mt-1 truncate text-[11px] text-muted tabular">{it.summary}</p>
                  </button>
                );
              })}
            </div>
          </div>

          {/* Embedded Boundary & Verdict Spec for the Selected Queue Event */}
          {activeItem && (
            <div className="border-t border-line pt-3.5 text-[12px] tabular">
              <h3 className="mb-2 text-[13px] font-medium text-fg">当前事件判定边界与规则依据</h3>
              <dl className="space-y-1.5 rounded-[4px] bg-fill-1/60 p-3 text-[11px]">
                <div className="flex justify-between gap-2">
                  <dt className="shrink-0 text-muted">定性分类</dt>
                  <dd className="text-right font-medium text-fg">{activeItem.badge}</dd>
                </div>
                <div className="flex justify-between gap-2">
                  <dt className="shrink-0 text-muted">观测窗口</dt>
                  <dd className="text-right text-fg">{activeItem.timeRange}</dd>
                </div>
                <div className="flex justify-between gap-2">
                  <dt className="shrink-0 text-muted">规则版本</dt>
                  <dd className="text-right font-mono text-fg">{activeItem.ruleInfo}</dd>
                </div>
                {activeItem.key === '__usage__' && matchedRule && (
                  <>
                    <div className="flex justify-between gap-2">
                      <dt className="shrink-0 text-muted">触发阈值</dt>
                      <dd className="text-right text-fg">
                        ≥ +{fmtNum(matchedRule.minimumAbsoluteIncrease)} {data.unit} 且 ≥ +{fmtPct(matchedRule.minimumRelativeIncrease)}
                      </dd>
                    </div>
                    <div className="flex justify-between gap-2">
                      <dt className="shrink-0 text-muted">历史样本门禁</dt>
                      <dd className="text-right text-fg">
                        {data.baselineCount} / {matchedRule.minimumHistory} 个同星期完整日
                      </dd>
                    </div>
                  </>
                )}
                {activeItem.terminalId && (
                  <div className="flex justify-between gap-2">
                    <dt className="shrink-0 text-muted">异常终端</dt>
                    <dd className="text-right font-mono text-fg">
                      {activeItem.terminalId} ({activeItem.errorType} × {activeItem.count})
                    </dd>
                  </div>
                )}
                {matchedMeterInfo && (
                  <div className="flex justify-between gap-2">
                    <dt className="shrink-0 text-muted">数据来源</dt>
                    <dd className="text-right font-mono text-fg">
                      {matchedMeterInfo.sourceTable} · {matchedMeterInfo.itemCode}
                    </dd>
                  </div>
                )}
              </dl>
              <p className="mt-2 text-[11px] leading-relaxed text-muted">
                {data.reason
                  ? `服务端备注：${statusLabels[data.reason] ?? data.reason}`
                  : isUsageCandidate
                    ? '说明：用能异常候选需结合运行班次、气候与现场设备核查，不可直接定性为已确认浪费。'
                    : '严格遵循服务端规则引擎判定结果与覆盖率约束。'}
              </p>
            </div>
          )}
        </aside>

        {/* Right 8 Cols: Deviation Evidence Chart + Docked Root-Cause Metering Point Checklist */}
        <section className="arco-card space-y-5 p-5 lg:col-span-8">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div>
              <h2 className="text-[15px] font-medium leading-[22px] text-fg">
                {chartView === 'history'
                  ? '同星期参考基线对照与规则命中证据'
                  : `观测日（${anchorDate}）24 小时负荷曲线与基线时均对照`}
              </h2>
              <p className="mt-0.5 text-[12px] text-muted tabular">
                {chartView === 'history'
                  ? '琥珀色标记服务端规则命中的异常候选日；结合下方关联表计清单定位具体排查对象'
                  : '展示观测日逐时负荷波动与历史基线时均对照，用于现场锁定异常升高时段'}
              </p>
            </div>
            <div className="inline-flex rounded-[2px] border border-line bg-fill-1 p-0.5 text-[12px]">
              <button
                type="button"
                onClick={() => setChartView('history')}
                className={`rounded-[2px] px-2.5 py-1 transition-colors ${
                  chartView === 'history' ? 'bg-surface font-medium text-primary shadow-xs' : 'text-fg-2 hover:text-fg'
                }`}
              >
                同星期基线对照
              </button>
              <button
                type="button"
                onClick={() => setChartView('hourly')}
                className={`rounded-[2px] px-2.5 py-1 transition-colors ${
                  chartView === 'hourly' ? 'bg-surface font-medium text-primary shadow-xs' : 'text-fg-2 hover:text-fg'
                }`}
              >
                观测日 24h 溯源
              </button>
            </div>
          </div>

          {chartView === 'history' ? (
            historySeries.length <= 8 ? (
              <DiscreteSampleCompareChart
                points={historySeries}
                unit={data.unit}
                baseline={data.baseline}
                baselineLabel="同星期中位基线"
                highlightIndex={anomalyHitIndices[0] ?? null}
                highlightLabel="规则命中候选日"
                onBarClick={() => setSelectedEventKey('__usage__')}
              />
            ) : (
              <TrendAreaChart
                points={historySeries}
                unit={data.unit}
                seriesLabel={`同星期样本与观测日 (${data.unit})`}
                referenceValue={data.baseline}
                referenceLabel={data.baseline != null ? `同星期中位基线 ${fmtNum(data.baseline)}` : undefined}
                highlightIndices={anomalyHitIndices}
                highlightLabel="规则命中候选日"
                onPointClick={() => setSelectedEventKey('__usage__')}
                height={304}
              />
            )
          ) : (
            <TrendAreaChart
              points={hourlySeries}
              unit={data.unit}
              seriesLabel={`观测日逐时负荷 (${data.unit})`}
              referenceValue={baselineHourlyRef}
              referenceLabel={baselineHourlyRef != null ? `基线日均时负荷 ${fmtNum(baselineHourlyRef)}` : undefined}
              highlightIndices={hourlyHighlightIndices}
              highlightLabel="高于基线时均时段"
              height={304}
            />
          )}

          {/* Anomaly Event Frequency Distribution Bar Chart + Timeline Summary */}
          <div className="grid gap-4 border-t border-line pt-4 lg:grid-cols-12 tabular">
            <div className="lg:col-span-7">
              <h3 className="mb-2 text-[13px] font-medium text-fg">异常类型与终端频次分布柱状图</h3>
              <AnomalyDistributionBarChart
                items={[
                  ...(isUsageCandidate
                    ? [{ key: '__usage_hit__', label: '用能异常候选命中', count: 1, kind: 'usage_candidate' as const }]
                    : []),
                  ...exceptions.map((ex, i) => ({
                    key: `${ex.terminalId}-${ex.errorType}-${i}`,
                    label: `${ex.terminalId} (${ex.errorType})`,
                    count: ex.count,
                    kind: 'metering_quality' as const,
                  })),
                ]}
              />
            </div>

            <div className="border-t border-line pt-3 lg:col-span-5 lg:border-t-0 lg:border-l lg:pt-0 lg:pl-4">
              <h3 className="mb-2 text-[13px] font-medium text-fg">异常诊断事件时间轴</h3>
              <ol className="relative space-y-2.5 border-l border-line pl-3.5 text-[11px]">
                {historySeries.map((pt, idx) => {
                  const isObs = idx === historySeries.length - 1;
                  const hit = isObs && (isUsageCandidate || isMeteringAnomaly);
                  return (
                    <li key={`${pt.from}-${idx}`} className="relative">
                      <span
                        className={`absolute -left-[19px] top-1 size-2 rounded-full ${
                          hit ? 'bg-warning ring-2 ring-warning/30' : 'bg-primary/60'
                        }`}
                      />
                      <div className="flex items-center justify-between gap-2">
                        <span className={hit ? 'font-semibold text-warning' : 'font-medium text-fg'}>
                          {fmtShanghaiDate(pt.from)} {isObs ? '（观测日）' : '（同星期样本）'}
                        </span>
                        <span className="font-semibold text-fg">
                          {pt.usage != null ? `${fmtNum(pt.usage)} ${data.unit}` : '缺测'}
                        </span>
                      </div>
                      <p className="text-[10px] text-muted">
                        有效 {pt.valid}/{pt.expected} 槽{hit ? ` · 命中 ${data.activeRuleRevision || '异常规则'}` : ''}
                      </p>
                    </li>
                  );
                })}
              </ol>
            </div>
          </div>

          {/* Docked Root-Cause Metering Point Verification Checklist */}
          <div className="border-t border-line pt-4 tabular">
            <div className="mb-2.5 flex flex-wrap items-center justify-between gap-2 text-[12px]">
              <h3 className="font-medium text-fg">
                关联计量点现场核查清单（{daySources.length} 块表 · {data.buildingName}）
              </h3>
              <span className="text-[11px] text-muted">
                底层计量质量异常计数：{exceptions.length} 条 · 缺测 {data.coverage.missing} · 可疑 {data.coverage.suspect}
              </span>
            </div>
            {daySources.length === 0 ? (
              <p className="rounded-[4px] bg-fill-1/50 px-3 py-2.5 text-[11px] text-muted">读取关联计量点中…</p>
            ) : (
              <div className="grid gap-2.5 sm:grid-cols-2">
                {daySources.map((s, idx) => (
                  <div
                    key={`${s.terminalId}-${s.itemCode}-${idx}`}
                    className="rounded-[4px] border border-line bg-fill-1/30 px-3 py-2 text-[11px]"
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate font-medium text-fg">{s.terminalName}</span>
                      <span className="shrink-0 font-semibold text-fg">
                        {fmtNum(s.usage)} {s.unit}
                      </span>
                    </div>
                    <div className="mt-1 flex items-center justify-between gap-2 text-[10px] text-muted">
                      <span className="font-mono">{s.terminalId}/{s.itemCode} · {s.sourceTable}</span>
                      <span>有效 {s.valid}/{s.expected}</span>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </section>
      </div>
    </div>
  );
}

// ── Shared Industry & Denominator Labels (F11 / F12 / F13) ─────────────────

const buildingTypeLabels: Record<string, string> = {
  office: '办公建筑',
  commercial: '商业综合体',
  school: '学校 / 教育园区',
  hospital: '医院建筑',
  hotel: '酒店建筑',
  mixed: '综合园区',
};

const denominatorKindLabels: Record<string, string> = {
  area: '建筑面积',
  people: '常驻人数',
  students: '在校学生人数',
  revenue: '业务收入（万元）',
  bed_days: '实际床日',
  room_nights: '实际间夜',
};

// ── F11: General Energy Efficiency Intensity Workbench ──────────────────────

function LiveEfficiencyView({
  data,
  token,
  inventory,
  date,
  onSelectBuilding,
}: {
  data: EfficiencyData;
  token: string;
  inventory: InventoryData | null;
  date: string;
  onSelectBuilding: (buildingId: string) => void;
}) {
  const [focusMetricKind, setFocusMetricKind] = useState<'area' | 'people'>('area');
  const [peerData, setPeerData] = useState<EfficiencyData[]>([]);
  const [inspectedBuildingId, setInspectedBuildingId] = useState<string>(data.buildingId);

  useEffect(() => {
    setInspectedBuildingId(data.buildingId);
  }, [data.buildingId]);

  // Fetch efficiency across all inventory buildings for true cross-building intensity comparison (real server data).
  useEffect(() => {
    const bldgs = inventory?.buildings ?? [];
    if (bldgs.length === 0) {
      setPeerData([data]);
      return;
    }
    const ctrl = new AbortController();
    Promise.all(
      bldgs.map((b) => {
        if (b.buildingId === data.buildingId) return Promise.resolve(data);
        const qs = new URLSearchParams({ building: b.buildingId, energy: data.energy });
        if (date) qs.set('date', date);
        return liveGet<EfficiencyData>(`/efficiency?${qs}`, token, ctrl.signal)
          .then((r) => r.data ?? null)
          .catch(() => null);
      }),
    ).then((list) => {
      if (ctrl.signal.aborted) return;
      const valid = list.filter((x): x is EfficiencyData => x != null);
      setPeerData(valid.length > 0 ? valid : [data]);
    });
    return () => ctrl.abort();
  }, [data, inventory?.buildings, date, token]);

  if (data.configurationState === 'unconfigured') {
    return <UnconfiguredNotice message={data.configurationMessage} />;
  }

  const areaMetric = (data.metrics ?? []).find((m) => m.denominatorKind === 'area') ?? data.metrics?.[0] ?? null;
  const peopleMetric = (data.metrics ?? []).find((m) => m.denominatorKind === 'people') ?? data.metrics?.[1] ?? null;

  // Cross-building intensity ranking for the active denominator kind (strictly same unit!).
  const peerRows = useMemo(() => {
    const raw = (peerData.length ? peerData : [data]).map((b) => {
      const m = (b.metrics ?? []).find((x) => x.denominatorKind === focusMetricKind) ?? null;
      const valid = m != null && m.status === 'ready' && m.value != null;
      return {
        buildingId: b.buildingId,
        buildingName: b.buildingName,
        buildingType: b.buildingType,
        buildingRevision: b.buildingRevision,
        region: b.region,
        metric: m,
        value: valid ? m.value : null,
        unit: m?.unit ?? (focusMetricKind === 'area' ? `${data.metrics?.[0]?.numeratorUnit ?? 'kWh'}/m²` : `${data.metrics?.[0]?.numeratorUnit ?? 'kWh'}/人`),
        coverageRate: m?.numeratorCoverage.rate ?? null,
        status: m?.status ?? 'missing_denominator',
        reason: m?.reason || (m ? statusLabels[m.status] ?? m.status : '缺少对应分母指标'),
        valid,
        sources: b.sources ?? [],
      };
    });

    const rankable = raw.filter((r) => r.valid && r.value != null).sort((a, b) => (b.value ?? 0) - (a.value ?? 0));
    const rankMap = new Map(rankable.map((r, i) => [r.buildingId, i + 1]));
    return raw.map((r) => ({ ...r, rank: rankMap.get(r.buildingId) ?? null }));
  }, [peerData, data, focusMetricKind]);

  const validPeerVals = peerRows.filter((r) => r.valid && r.value != null).map((r) => r.value!);
  const peerAvg = validPeerVals.length > 0 ? validPeerVals.reduce((a, b) => a + b, 0) / validPeerVals.length : null;
  const activeUnit = peerRows[0]?.unit ?? (areaMetric?.unit || 'kWh/m²');

  const inspected = peerRows.find((r) => r.buildingId === inspectedBuildingId) ?? peerRows[0] ?? null;
  const inspectedBldgArchive = (inventory?.buildings ?? []).find((b) => b.buildingId === inspected?.buildingId);
  const inspectedDenomRec = (inventory?.denominators ?? []).find(
    (d) => d.objectId === inspected?.buildingId && d.denominatorKind === focusMetricKind,
  );

  return (
    <div className="space-y-4">
      {/* ── 1. Unified Core Intensity Formula Strip (Area & Per-Capita Intensity with Explicit Numerator / Denominator) ── */}
      <section aria-label="通用能效强度核心指标" className="arco-card overflow-hidden">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line bg-fill-1/50 px-5 py-2 text-[12px] tabular">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium text-fg">{data.buildingName}</span>
            <span className="rounded-[2px] bg-fill-2 px-1.5 py-0 text-[11px] text-fg-2">
              {buildingTypeLabels[data.buildingType] ?? data.buildingType}
            </span>
            <span className="text-muted">
              统计自然日 {fmtShanghaiDate(data.from)} · 能源 {energyLabels[data.energy] ?? data.energy}
            </span>
          </div>
          <span className="font-mono text-[11px] text-muted">
            建筑档案版本 {data.buildingRevision} · {data.timezone}
          </span>
        </div>

        <div className="grid divide-y divide-line md:grid-cols-2 md:divide-y-0 md:divide-x tabular">
          {[
            { kind: 'area' as const, title: '单位建筑面积能耗强度', metric: areaMetric },
            { kind: 'people' as const, title: '人均能耗强度', metric: peopleMetric },
          ].map(({ kind, title, metric }) => {
            const ready = metric?.status === 'ready' && metric.value != null;
            const isFocus = focusMetricKind === kind;
            return (
              <button
                key={kind}
                type="button"
                onClick={() => setFocusMetricKind(kind)}
                className={`relative p-5 text-left transition-colors ${
                  isFocus
                    ? 'bg-primary-soft/30 after:absolute after:inset-x-0 after:top-0 after:h-[2px] after:bg-primary'
                    : 'hover:bg-fill-1/60'
                }`}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className={`text-[13px] font-medium ${isFocus ? 'text-primary' : 'text-fg-2'}`}>{title}</span>
                  <span
                    className={`rounded-[2px] px-1.5 py-0 text-[11px] leading-[18px] ${
                      ready ? 'bg-positive-soft text-positive' : 'bg-warning-soft text-warning'
                    }`}
                  >
                    {metric ? statusLabels[metric.status] ?? metric.status : '未配置'}
                  </span>
                </div>

                <div className="mt-2.5 flex flex-wrap items-baseline justify-between gap-3">
                  <p className="text-[28px] font-semibold leading-[34px] tracking-[-0.02em] text-fg">
                    {ready ? fmtNum(metric!.value) : '不可计算'}
                    {ready && <span className="ml-1.5 text-[13px] font-normal text-muted">{metric!.unit}</span>}
                  </p>
                  {metric && (
                    <span className="rounded-[2px] bg-fill-1 px-2 py-1 text-[11px] text-fg-2">
                      分子 <strong className="font-medium text-fg">{fmtNum(metric.numeratorUsage)}</strong> {metric.numeratorUnit}
                      {' ÷ '}
                      分母{' '}
                      <strong className="font-medium text-fg">
                        {metric.denominatorValue != null ? `${fmtNum(metric.denominatorValue)} ${metric.denominatorUnit}` : '缺失'}
                      </strong>
                    </span>
                  )}
                </div>

                <p className="mt-2 text-[11px] text-muted">
                  {ready
                    ? `覆盖率 ${fmtPct(metric!.numeratorCoverage.rate)} · 依据：${metric!.denominatorBasis || '建筑权威档案'}（点击下方切换横向对比维度）`
                    : metric?.reason || '缺少有效面积/人数分母或历史版本不匹配，禁止输出有效强度'}
                </p>
              </button>
            );
          })}
        </div>
      </section>

      {/* ── 2. Primary Stage: Cross-Building Intensity Ranking (8 cols) + Denominator & Formula Inspector (4 cols) ── */}
      <div className="grid items-start gap-4 lg:grid-cols-12">
        <section className="arco-card p-5 lg:col-span-8">
          <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
            <div>
              <h2 className="text-[15px] font-medium leading-[22px] text-fg">
                建筑间{focusMetricKind === 'area' ? '单位面积' : '人均'}能效强度横向对比（{activeUnit}）
              </h2>
              <p className="mt-0.5 text-[12px] text-muted">
                按归一化强度而非能耗总量排序；缺失面积/人数或档案版本不匹配的建筑保留状态、不参与强度排名
              </p>
            </div>
            <div className="inline-flex rounded-[2px] border border-line bg-fill-1 p-0.5 text-[12px]">
              <button
                type="button"
                onClick={() => setFocusMetricKind('area')}
                className={`rounded-[2px] px-2.5 py-1 transition-colors ${
                  focusMetricKind === 'area' ? 'bg-surface font-medium text-primary shadow-xs' : 'text-fg-2 hover:text-fg'
                }`}
              >
                单位面积强度
              </button>
              <button
                type="button"
                onClick={() => setFocusMetricKind('people')}
                className={`rounded-[2px] px-2.5 py-1 transition-colors ${
                  focusMetricKind === 'people' ? 'bg-surface font-medium text-primary shadow-xs' : 'text-fg-2 hover:text-fg'
                }`}
              >
                人均强度
              </button>
            </div>
          </div>

          <HorizontalRankingChart
            unit={activeUnit}
            averageValue={peerAvg}
            selectedId={inspected?.buildingId ?? null}
            onSelect={(bid) => setInspectedBuildingId(bid)}
            items={peerRows.map((r) => ({
              id: r.buildingId,
              name: `${r.buildingName}（${buildingTypeLabels[r.buildingType] ?? r.buildingType}）`,
              value: r.value,
              rank: r.rank,
              coverageRate: r.coverageRate,
              reason: r.reason,
            }))}
          />
        </section>

        {/* Right 4 Cols: Selected Building Denominator Provenance & Calculation Basis */}
        <aside aria-label="分母来源与计算依据" className="arco-card space-y-3.5 p-5 text-[12px] tabular lg:col-span-4">
          {inspected ? (
            <>
              <div className="flex items-start justify-between gap-2 border-b border-line pb-3">
                <div className="min-w-0">
                  <span className="text-[11px] text-muted">选中建筑计算依据</span>
                  <h3 className="mt-0.5 truncate text-[15px] font-medium text-fg">{inspected.buildingName}</h3>
                </div>
                {inspected.buildingId !== data.buildingId && (
                  <button
                    type="button"
                    onClick={() => onSelectBuilding(inspected.buildingId)}
                    className="arco-btn !h-7 shrink-0 !px-2.5 !text-[12px]"
                  >
                    切换为主建筑
                  </button>
                )}
              </div>

              <div className="rounded-[4px] bg-fill-1/60 p-3 space-y-2">
                <div className="flex items-baseline justify-between">
                  <span className="text-[11px] text-muted">
                    {focusMetricKind === 'area' ? '单位面积强度' : '人均能耗强度'}
                  </span>
                  <span className="text-[18px] font-semibold text-fg">
                    {inspected.valid ? `${fmtNum(inspected.value)} ${inspected.unit}` : '不可计算'}
                  </span>
                </div>
                <div className="flex items-center justify-between border-t border-line pt-2 text-[11px]">
                  <span className="text-muted">计算式</span>
                  <span className="text-fg-2">
                    {fmtNum(inspected.metric?.numeratorUsage)} {inspected.metric?.numeratorUnit} ÷{' '}
                    {inspected.metric?.denominatorValue != null
                      ? `${fmtNum(inspected.metric.denominatorValue)} ${inspected.metric.denominatorUnit}`
                      : '缺失'}
                  </span>
                </div>
              </div>

              <dl className="divide-y divide-line text-[12px]">
                <div className="flex justify-between gap-3 py-2">
                  <dt className="text-muted">分母类型</dt>
                  <dd className="font-medium text-fg">
                    {denominatorKindLabels[focusMetricKind] ?? focusMetricKind}
                  </dd>
                </div>
                <div className="flex justify-between gap-3 py-2">
                  <dt className="text-muted">分母来源依据</dt>
                  <dd className="text-right text-fg">
                    {inspected.metric?.denominatorBasis || inspectedDenomRec?.basis || '建筑权威目录档案'}
                  </dd>
                </div>
                <div className="flex justify-between gap-3 py-2">
                  <dt className="text-muted">建筑档案版本</dt>
                  <dd className="font-mono text-[11px] text-fg">{inspected.buildingRevision}</dd>
                </div>
                {inspectedDenomRec && (
                  <div className="flex justify-between gap-3 py-2">
                    <dt className="text-muted">分母生效区间</dt>
                    <dd className="text-fg">
                      {fmtShanghaiDate(inspectedDenomRec.periodFrom)} ~ {fmtShanghaiDate(inspectedDenomRec.periodTo)}
                    </dd>
                  </div>
                )}
                {inspectedBldgArchive && (
                  <div className="flex justify-between gap-3 py-2">
                    <dt className="text-muted">总面积 / 常驻人数</dt>
                    <dd className="text-fg">
                      {fmtNum(inspectedBldgArchive.grossAreaSqm)} m² · {fmtNum(inspectedBldgArchive.occupiedHeadcount)} 人
                    </dd>
                  </div>
                )}
                <div className="flex justify-between gap-3 py-2">
                  <dt className="text-muted">分子覆盖率</dt>
                  <dd className="text-fg">
                    {fmtPct(inspected.coverageRate)}（{inspected.metric?.numeratorCoverage.valid ?? 0}/
                    {inspected.metric?.numeratorCoverage.expected ?? 0} 槽）
                  </dd>
                </div>
              </dl>
            </>
          ) : null}
        </aside>
      </div>

      {/* Low-Priority Collapsible Source Trace */}
      <SourceTraceTable sources={data.sources ?? []} />
    </div>
  );
}

// ── F12: Unified Industry Indicator Template Framework ──────────────────────

function LiveIndustryTemplatesView({
  data,
  token,
  inventory,
  date,
  onSelectBuilding,
}: {
  data: EfficiencyData;
  token: string;
  inventory: InventoryData | null;
  date: string;
  onSelectBuilding: (buildingId: string) => void;
}) {
  const metrics = data.metrics ?? [];
  const [selectedMetricId, setSelectedMetricId] = useState<string>(() => metrics[0]?.id ?? '');
  const [allTemplates, setAllTemplates] = useState<EfficiencyData[]>([]);

  useEffect(() => {
    if (metrics.length > 0 && !metrics.some((m) => m.id === selectedMetricId)) {
      setSelectedMetricId(metrics[0].id);
    }
  }, [metrics, selectedMetricId]);

  useEffect(() => {
    const bldgs = inventory?.buildings ?? [];
    if (bldgs.length === 0) {
      setAllTemplates([data]);
      return;
    }
    const ctrl = new AbortController();
    Promise.all(
      bldgs.map((b) => {
        if (b.buildingId === data.buildingId) return Promise.resolve(data);
        const qs = new URLSearchParams({ building: b.buildingId, energy: data.energy });
        if (date) qs.set('date', date);
        return liveGet<EfficiencyData>(`/industry/templates?${qs}`, token, ctrl.signal)
          .then((r) => r.data ?? null)
          .catch(() => null);
      }),
    ).then((list) => {
      if (ctrl.signal.aborted) return;
      const valid = list.filter((x): x is EfficiencyData => x != null);
      setAllTemplates(valid.length > 0 ? valid : [data]);
    });
    return () => ctrl.abort();
  }, [data, inventory?.buildings, date, token]);

  if (data.configurationState === 'unconfigured') {
    return <UnconfiguredNotice message={data.configurationMessage} />;
  }

  const activeMetric = metrics.find((m) => m.id === selectedMetricId) ?? metrics[0] ?? null;
  const templateVer = activeMetric?.templateVersion || metrics[0]?.templateVersion || '—';
  const sameMetricPeers = useMemo(() => {
    if (!activeMetric) return [];
    return allTemplates
      .filter((t) => t.buildingType === data.buildingType)
      .map((t) => ({
        buildingId: t.buildingId,
        buildingName: t.buildingName,
        metric: (t.metrics ?? []).find((m) => m.id === activeMetric.id) ?? null,
      }))
      .filter((x) => x.metric != null);
  }, [allTemplates, activeMetric, data.buildingType]);

  const matchedDenominator = useMemo(() => {
    if (!activeMetric) return null;
    return (
      (inventory?.denominators ?? []).find(
        (d) => d.objectId === data.buildingId && d.denominatorKind === activeMetric.denominatorKind,
      ) ?? null
    );
  }, [inventory?.denominators, data.buildingId, activeMetric]);

  const supportedTaxonomy = [
    { type: 'office', label: '办公建筑', denoms: '建筑面积 · 常驻人数 · 万元营收' },
    { type: 'commercial', label: '商业综合体', denoms: '建筑面积 · 万元营收 · 人流/人数' },
    { type: 'school', label: '学校 / 教育', denoms: '在校学生人数 · 建筑面积' },
    { type: 'hospital', label: '医院建筑', denoms: '实际床日 · 建筑面积' },
    { type: 'hotel', label: '酒店建筑', denoms: '实际间夜 · 建筑面积' },
  ];

  return (
    <div className="space-y-4">
      {/* ── 1. Unified Industry Template Framework Strip ── */}
      <section aria-label="行业指标模板与适用范围" className="arco-card overflow-hidden">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line bg-fill-1/50 px-5 py-2.5 text-[12px]">
          <div className="flex flex-wrap items-center gap-2">
            <span className="rounded-[2px] bg-primary-soft px-2 py-0.5 text-[11px] font-medium text-primary">
              {buildingTypeLabels[data.buildingType] ?? data.buildingType}模板
            </span>
            <span className="font-mono text-[11px] text-fg">{templateVer}</span>
            <span className="text-muted">·</span>
            <span className="text-muted">不同单位与业务分母指标独立计算，严禁跨口径混排</span>
          </div>

          {/* Quick Industry Building Switcher */}
          {(inventory?.buildings ?? []).length > 1 && (
            <div className="flex flex-wrap items-center gap-1" role="tablist" aria-label="切换业态建筑样本">
              {(inventory?.buildings ?? []).map((b) => {
                const isSel = b.buildingId === data.buildingId;
                return (
                  <button
                    key={b.buildingId}
                    type="button"
                    role="tab"
                    aria-selected={isSel}
                    onClick={() => onSelectBuilding(b.buildingId)}
                    className={`inline-flex h-6 items-center rounded-[2px] px-2.5 text-[11px] transition-colors ${
                      isSel ? 'bg-primary font-medium text-white' : 'text-fg-2 hover:bg-fill-2 hover:text-fg'
                    }`}
                  >
                    {buildingTypeLabels[b.buildingType] ?? b.buildingType} · {b.name.split('·')[0].trim()}
                  </button>
                );
              })}
            </div>
          )}
        </div>

        {/* Supported Industry Taxonomy Bar (Unified Framework Proof) */}
        <div className="grid divide-y divide-line sm:grid-cols-5 sm:divide-y-0 sm:divide-x text-[11px]">
          {supportedTaxonomy.map((tx) => {
            const isActiveType = tx.type === data.buildingType;
            return (
              <div
                key={tx.type}
                className={`px-4 py-2.5 ${isActiveType ? 'bg-primary-soft/35' : 'bg-surface'}`}
              >
                <div className="flex items-center justify-between gap-1">
                  <span className={`font-medium ${isActiveType ? 'text-primary' : 'text-fg-2'}`}>{tx.label}</span>
                  {isActiveType && (
                    <span className="rounded-[2px] bg-primary/12 px-1 text-[10px] text-primary">当前适用</span>
                  )}
                </div>
                <p className="mt-0.5 truncate text-muted" title={tx.denoms}>
                  {tx.denoms}
                </p>
              </div>
            );
          })}
        </div>
      </section>

      {/* ── 2. Primary Stage: Industry Metric Matrix (7 cols) + Denominator & Template Drilldown (5 cols) ── */}
      <div className="grid items-start gap-4 lg:grid-cols-12">
        <section className="arco-card overflow-hidden lg:col-span-7">
          <div className="flex items-center justify-between border-b border-line px-5 py-3">
            <div>
              <h2 className="text-[15px] font-medium text-fg">
                {data.buildingName} · 行业专属指标矩阵（{metrics.length} 项）
              </h2>
              <p className="mt-0.5 text-[12px] text-muted">
                点击任意指标下钻查看业务分母版本、生效区间与同类对象可比状态
              </p>
            </div>
            <span className="text-[11px] text-muted tabular">{fmtShanghaiDate(data.from)}</span>
          </div>

          <div className="divide-y divide-line tabular">
            {metrics.map((m) => {
              const isSel = activeMetric?.id === m.id;
              const ready = m.status === 'ready' && m.value != null;
              return (
                <button
                  key={m.id}
                  type="button"
                  data-template-metric={m.id}
                  onClick={() => setSelectedMetricId(m.id)}
                  className={`flex w-full flex-wrap items-center justify-between gap-3 px-5 py-4 text-left transition-colors ${
                    isSel ? 'bg-primary-soft/45' : 'hover:bg-fill-1/60'
                  }`}
                >
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className={`text-[14px] font-medium ${isSel ? 'text-primary' : 'text-fg'}`}>{m.name}</span>
                      <span className="rounded-[2px] bg-fill-2 px-1.5 py-0 text-[10px] text-fg-2">
                        分母：{denominatorKindLabels[m.denominatorKind] ?? m.denominatorKind}
                      </span>
                      <span
                        className={`rounded-[2px] px-1.5 py-0 text-[10px] font-medium leading-[17px] ${
                          ready ? 'bg-positive-soft text-positive' : 'bg-warning-soft text-warning'
                        }`}
                      >
                        {statusLabels[m.status] ?? m.status}
                      </span>
                    </div>
                    <p className="mt-1 text-[12px] text-muted">
                      分子 {fmtNum(m.numeratorUsage)} {m.numeratorUnit}（覆盖 {fmtPct(m.numeratorCoverage.rate)}） · 分母{' '}
                      {m.denominatorValue != null ? `${fmtNum(m.denominatorValue)} ${m.denominatorUnit}` : '未配置该业务分母'}
                    </p>
                    {m.reason && (
                      <p className={`mt-0.5 text-[11px] ${ready ? 'text-muted' : 'text-warning'}`}>{m.reason}</p>
                    )}
                  </div>

                  <div className="shrink-0 text-right">
                    <div className={`text-[22px] font-semibold leading-tight ${ready ? 'text-fg' : 'text-muted'}`}>
                      {ready ? fmtNum(m.value) : '—'}
                    </div>
                    <div className="mt-0.5 text-[11px] text-muted">{m.unit}</div>
                  </div>
                </button>
              );
            })}
          </div>
        </section>

        {/* Right 5 Cols: Denominator Drilldown, Template Scope & Same-Industry Peer Check */}
        <aside aria-label="业务分母与模板版本下钻" className="arco-card space-y-4 p-5 text-[12px] tabular lg:col-span-5">
          {activeMetric ? (
            <>
              <div className="border-b border-line pb-3">
                <span className="text-[11px] text-muted">指标与业务分母下钻详情</span>
                <h3 className="mt-0.5 text-[15px] font-medium text-fg">{activeMetric.name}</h3>
                <p className="mt-0.5 font-mono text-[11px] text-muted">
                  指标 ID {activeMetric.id} · 模板 {activeMetric.templateVersion}
                </p>
              </div>

              <dl className="divide-y divide-line">
                <div className="flex justify-between gap-3 py-2">
                  <dt className="text-muted">适用业态与地区</dt>
                  <dd className="text-fg">
                    {buildingTypeLabels[activeMetric.buildingType] ?? activeMetric.buildingType} · {data.region || '通用'}
                  </dd>
                </div>
                <div className="flex justify-between gap-3 py-2">
                  <dt className="text-muted">业务分母类型</dt>
                  <dd className="font-medium text-fg">
                    {denominatorKindLabels[activeMetric.denominatorKind] ?? activeMetric.denominatorKind}
                  </dd>
                </div>
                <div className="flex justify-between gap-3 py-2">
                  <dt className="text-muted">分母实值与单位</dt>
                  <dd className="font-semibold text-fg">
                    {activeMetric.denominatorValue != null
                      ? `${fmtNum(activeMetric.denominatorValue)} ${activeMetric.denominatorUnit}`
                      : '缺失（禁止计算）'}
                  </dd>
                </div>
                <div className="flex justify-between gap-3 py-2">
                  <dt className="text-muted">分母版本与依据</dt>
                  <dd className="text-right text-fg">
                    {matchedDenominator
                      ? `${matchedDenominator.revision} · ${matchedDenominator.basis}`
                      : activeMetric.denominatorBasis || '尚未录入分母台账'}
                  </dd>
                </div>
                {matchedDenominator && (
                  <div className="flex justify-between gap-3 py-2">
                    <dt className="text-muted">分母有效周期</dt>
                    <dd className="text-fg">
                      {fmtShanghaiDate(matchedDenominator.periodFrom)} ~ {fmtShanghaiDate(matchedDenominator.periodTo)}
                    </dd>
                  </div>
                )}
                <div className="flex justify-between gap-3 py-2">
                  <dt className="text-muted">建筑档案版本</dt>
                  <dd className="font-mono text-[11px] text-fg">{data.buildingRevision}</dd>
                </div>
              </dl>

              {/* Same-Industry Peer Comparability Panel */}
              <div className="rounded-[4px] bg-fill-1/60 p-3">
                <div className="flex items-center justify-between text-[12px]">
                  <span className="font-medium text-fg">同类业态对象比较（同单位 {activeMetric.unit}）</span>
                  <span className="text-[11px] text-muted">{sameMetricPeers.length} 栋同业态</span>
                </div>
                {sameMetricPeers.length <= 1 ? (
                  <p className="mt-1.5 text-[11px] leading-relaxed text-muted">
                    当前园区内仅 1 栋「{buildingTypeLabels[data.buildingType] ?? data.buildingType}」建筑；为遵循同口径原则，不与其他业态或不同单位指标混合排名。
                  </p>
                ) : (
                  <ul className="mt-2 divide-y divide-line text-[12px]">
                    {sameMetricPeers.map((p) => (
                      <li key={p.buildingId} className="flex justify-between py-1.5">
                        <span className="text-fg">{p.buildingName}</span>
                        <span className="font-semibold text-fg">
                          {p.metric?.value != null ? `${fmtNum(p.metric.value)} ${p.metric.unit}` : '不可计算'}
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            </>
          ) : null}
        </aside>
      </div>

      {/* Low-Priority Collapsible Cross-Building Template Overview (strictly no cross-unit ranking) */}
      <CollapsibleSection
        title="全园区各建筑行业模板与业务分母配置总览"
        badge={`${allTemplates.length} 栋建筑`}
        subtitle="跨业态分列展示，不进行跨单位混合排名"
      >
        <div className="overflow-x-auto">
          <table className="w-full text-left text-[12px]">
            <thead className="border-b border-line bg-fill-1 text-fg-2">
              <tr>
                <th className="px-4 py-2 font-medium">建筑与业态</th>
                <th className="px-4 py-2 font-medium">模板指标</th>
                <th className="px-4 py-2 font-medium">业务分母</th>
                <th className="px-4 py-2 text-right font-medium">计算值</th>
                <th className="px-4 py-2 font-medium">状态</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line tabular">
              {allTemplates.flatMap((t) =>
                (t.metrics ?? []).map((m) => (
                  <tr key={`${t.buildingId}-${m.id}`} className="hover:bg-fill-1/50">
                    <td className="whitespace-nowrap px-4 py-2 font-medium text-fg">
                      {t.buildingName}{' '}
                      <span className="font-normal text-muted">({buildingTypeLabels[t.buildingType] ?? t.buildingType})</span>
                    </td>
                    <td className="px-4 py-2 text-fg-2">{m.name}</td>
                    <td className="whitespace-nowrap px-4 py-2 text-muted">
                      {denominatorKindLabels[m.denominatorKind] ?? m.denominatorKind}
                      {m.denominatorValue != null ? ` (${fmtNum(m.denominatorValue)} ${m.denominatorUnit})` : ' (缺失)'}
                    </td>
                    <td className="whitespace-nowrap px-4 py-2 text-right font-semibold text-fg">
                      {m.value != null ? `${fmtNum(m.value)} ${m.unit}` : '—'}
                    </td>
                    <td className="whitespace-nowrap px-4 py-2 text-muted">{statusLabels[m.status] ?? m.status}</td>
                  </tr>
                )),
              )}
            </tbody>
          </table>
        </div>
      </CollapsibleSection>
    </div>
  );
}

// ── F13: Quota & Standard Benchmarking Workbench ────────────────────────────

function LiveBenchmarksView({
  data,
  inventory,
}: {
  data: BenchmarksData;
  inventory: InventoryData | null;
}) {
  const rows = data.rows ?? [];
  const [selectedMetricId, setSelectedMetricId] = useState<string>(() => rows[0]?.metric.id ?? '');

  useEffect(() => {
    if (rows.length > 0 && !rows.some((r) => r.metric.id === selectedMetricId)) {
      setSelectedMetricId(rows[0].metric.id);
    }
  }, [rows, selectedMetricId]);

  if (data.configurationState === 'unconfigured') {
    return <UnconfiguredNotice message={data.configurationMessage} />;
  }

  const matchedCount = rows.filter((r) => r.ruleMaximum != null).length;
  const withinCount = rows.filter((r) => r.benchmarkStatus === 'within_threshold').length;
  const exceedsCount = rows.filter((r) => r.benchmarkStatus === 'exceeds_threshold').length;
  const uncomparableCount = rows.filter(
    (r) => r.benchmarkStatus !== 'within_threshold' && r.benchmarkStatus !== 'exceeds_threshold',
  ).length;
  const hasDemoRule = rows.some((r) => r.ruleKind === 'demo');

  const selectedRow = rows.find((r) => r.metric.id === selectedMetricId) ?? rows[0] ?? null;
  const matchedRuleConfig = useMemo(() => {
    if (!selectedRow?.ruleId) return null;
    return (inventory?.benchmarks ?? []).find((b) => b.benchmarkId === selectedRow.ruleId) ?? null;
  }, [inventory?.benchmarks, selectedRow?.ruleId]);

  return (
    <div className="space-y-4">
      {/* ── 1. Standard Governance Tier Banner + Benchmarking Counters ── */}
      <section aria-label="定额标准分级与对标摘要" className="arco-card overflow-hidden">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line bg-fill-1/50 px-5 py-2 text-[12px]">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium text-fg">
              {data.buildingName}（{buildingTypeLabels[data.buildingType] ?? data.buildingType}）· {energyLabels[data.energy] ?? data.energy}定额对标
            </span>
            {hasDemoRule && (
              <span className="rounded-[2px] bg-warning-soft px-2 py-0.5 text-[11px] font-medium text-warning">
                含演示规则 · 仅作参考，不构成正式达标认证
              </span>
            )}
          </div>
          <div className="flex flex-wrap items-center gap-2 text-[11px]">
            <span className="rounded-[2px] bg-warning-soft px-1.5 py-0 text-warning">演示规则</span>
            <span className="rounded-[2px] bg-primary-soft px-1.5 py-0 text-primary">内部目标</span>
            <span className="rounded-[2px] bg-positive-soft px-1.5 py-0 text-positive">权威外部标准</span>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-x-6 gap-y-1.5 px-5 py-2.5 text-[12px] tabular">
          <div className="flex items-baseline gap-1.5">
            <span className="text-muted">已匹配规则</span>
            <strong className="text-[15px] font-semibold text-fg">{matchedCount}</strong>
            <span className="text-[11px] text-muted">/ {rows.length} 项</span>
          </div>
          <span className="hidden h-3.5 w-px bg-line sm:inline-block" aria-hidden="true" />
          <div className="flex items-baseline gap-1.5">
            <span className="text-muted">低于上限</span>
            <strong className="text-[15px] font-semibold text-positive">{withinCount}</strong>
          </div>
          <span className="hidden h-3.5 w-px bg-line sm:inline-block" aria-hidden="true" />
          <div className="flex items-baseline gap-1.5">
            <span className="text-muted">超出上限</span>
            <strong className={`text-[15px] font-semibold ${exceedsCount > 0 ? 'text-negative' : 'text-fg'}`}>
              {exceedsCount}
            </strong>
          </div>
          <span className="hidden h-3.5 w-px bg-line sm:inline-block" aria-hidden="true" />
          <div className="flex items-baseline gap-1.5">
            <span className="text-muted">无标准/不可对标</span>
            <strong className="text-[15px] font-semibold text-fg-2">{uncomparableCount}</strong>
          </div>
        </div>
      </section>

      {/* ── 2. Primary Stage: Horizontal Bullet Range Chart (8 cols) + Selected Rule Provenance (4 cols) ── */}
      <div className="grid items-start gap-4 lg:grid-cols-12">
        <section className="arco-card p-5 lg:col-span-8">
          <div className="mb-3">
            <h2 className="text-[15px] font-medium leading-[22px] text-fg">定额阈值区间对照与偏差分析</h2>
            <p className="mt-0.5 text-[12px] text-muted">
              点击任意指标项查看对应定额来源、适用口径与合规声明；未配置标准或低覆盖项保留不可比状态
            </p>
          </div>

          <BenchmarkBulletChart
            selectedId={selectedRow?.metric.id ?? null}
            onSelect={(id) => setSelectedMetricId(id)}
            items={rows.map((r) => ({
              id: r.metric.id,
              metricName: r.metric.name,
              unit: r.metric.unit,
              actual: r.metric.value,
              maximum: r.ruleMaximum ?? null,
              status: r.benchmarkStatus,
              statusLabel: statusLabels[r.benchmarkStatus] ?? r.benchmarkStatus,
              ruleKind: r.ruleKind,
              ruleName: r.ruleName,
              ruleNotice: r.ruleNotice || r.benchmarkReason,
              coverageRate: r.metric.numeratorCoverage.rate,
            }))}
          />
        </section>

        {/* Right 4 Cols: Selected Benchmark Standard & Compliance Details */}
        <aside aria-label="定额标准来源与适用说明" className="arco-card space-y-3.5 p-5 text-[12px] tabular lg:col-span-4">
          {selectedRow ? (
            <>
              <div className="border-b border-line pb-3">
                <span className="text-[11px] text-muted">选中对标规则与证据</span>
                <h3 className="mt-0.5 text-[15px] font-medium text-fg">{selectedRow.metric.name}</h3>
              </div>

              <dl className="divide-y divide-line">
                <div className="flex justify-between gap-3 py-2">
                  <dt className="text-muted">标准层级</dt>
                  <dd className="font-medium text-fg">
                    {selectedRow.ruleKind === 'demo'
                      ? '演示规则（模拟参考阈值）'
                      : selectedRow.ruleKind === 'external'
                        ? '外部权威标准'
                        : selectedRow.ruleKind === 'internal'
                          ? '内部目标定额'
                          : '未配置标准'}
                  </dd>
                </div>
                <div className="flex justify-between gap-3 py-2">
                  <dt className="text-muted">实测指标值</dt>
                  <dd className="font-semibold text-fg">
                    {selectedRow.metric.value != null
                      ? `${fmtNum(selectedRow.metric.value)} ${selectedRow.metric.unit}`
                      : '不可计算'}
                  </dd>
                </div>
                <div className="flex justify-between gap-3 py-2">
                  <dt className="text-muted">阈值上限</dt>
                  <dd className="text-fg">
                    {selectedRow.ruleMaximum != null
                      ? `${fmtNum(selectedRow.ruleMaximum)} ${selectedRow.metric.unit}`
                      : '—'}
                  </dd>
                </div>
                <div className="flex justify-between gap-3 py-2">
                  <dt className="text-muted">与上限差额</dt>
                  <dd
                    className={`font-semibold ${
                      selectedRow.benchmarkDelta == null
                        ? 'text-muted'
                        : selectedRow.benchmarkDelta > 0
                          ? 'text-negative'
                          : 'text-positive'
                    }`}
                  >
                    {selectedRow.benchmarkDelta != null
                      ? `${selectedRow.benchmarkDelta > 0 ? '+' : ''}${fmtNum(selectedRow.benchmarkDelta)} ${selectedRow.metric.unit}`
                      : '—'}
                  </dd>
                </div>
                <div className="flex justify-between gap-3 py-2">
                  <dt className="text-muted">分子 / 分母</dt>
                  <dd className="text-right text-fg">
                    {fmtNum(selectedRow.metric.numeratorUsage)} {selectedRow.metric.numeratorUnit} ÷{' '}
                    {selectedRow.metric.denominatorValue != null
                      ? `${fmtNum(selectedRow.metric.denominatorValue)} ${selectedRow.metric.denominatorUnit}`
                      : '缺失'}
                  </dd>
                </div>
                {matchedRuleConfig && (
                  <>
                    <div className="flex justify-between gap-3 py-2">
                      <dt className="text-muted">发布方 / 版本</dt>
                      <dd className="text-right text-fg">
                        {matchedRuleConfig.publisher} · <span className="font-mono">{matchedRuleConfig.revision}</span>
                      </dd>
                    </div>
                    <div className="flex justify-between gap-3 py-2">
                      <dt className="text-muted">适用范围</dt>
                      <dd className="text-right text-fg">
                        {buildingTypeLabels[matchedRuleConfig.buildingType] ?? matchedRuleConfig.buildingType} · {matchedRuleConfig.region || '通用'} · {matchedRuleConfig.periodGranularity}
                      </dd>
                    </div>
                  </>
                )}
              </dl>

              <div
                className={`rounded-[4px] px-3 py-2.5 text-[11px] leading-relaxed ${
                  selectedRow.ruleKind === 'demo'
                    ? 'border border-warning/35 bg-warning-soft/60 text-warning'
                    : 'bg-fill-1 text-fg-2'
                }`}
              >
                {selectedRow.ruleNotice || selectedRow.benchmarkReason || '严格按已发布定额版本对标，无适用标准时不生成正式达标认证。'}
              </div>
            </>
          ) : null}
        </aside>
      </div>
    </div>
  );
}

// ── FLAGSHIP #2 — F15: Organization & Space Assessment ──────────────────────

function LiveAssessmentView({
  data,
  inventory,
  token,
  onUpdated,
}: {
  data: AssessmentData;
  inventory: InventoryData | null;
  token: string;
  onUpdated: () => void;
}) {
  const rows = data.rows ?? [];
  const confirmedCount = rows.filter((r) => r.reviewStatus === 'confirmed').length;
  const blockedCount = rows.filter((r) => r.reviewStatus === 'blocked_low_quality').length;
  const withinTargetCount = rows.filter((r) => !r.exempt && r.deviationRate != null && r.deviationRate <= 0).length;
  const [busyRule, setBusyRule] = useState<string | null>(null);
  const [reviewMsg, setReviewMsg] = useState<string | null>(null);

  const handleReview = async (ruleId: string, reviewStatus: 'confirmed' | 'appealed' | 'pending_review', note: string) => {
    setBusyRule(ruleId);
    setReviewMsg(null);
    try {
      const res = await livePost('/management/assessments/review', token, { ruleId, reviewStatus, reviewNote: note });
      if (res.error) setReviewMsg(`复核操作失败：${res.error}`);
      else {
        setReviewMsg(`规则 ${ruleId} 状态已更新为 ${statusLabels[reviewStatus] ?? reviewStatus}`);
        onUpdated();
      }
    } finally {
      setBusyRule(null);
    }
  };

  const chartRows = rows.map((r) => ({
    ruleId: r.ruleId,
    objectName: r.objectName,
    energy: energyLabels[r.energy] ?? r.energy,
    score: r.score,
    weightedScore: r.weightedScore,
    weightPct: r.weightPct,
    deviationRate: r.deviationRate,
    targetValue: r.targetValue,
    actualUsage: r.actualUsage,
    unit: r.unit,
    exempt: r.exempt,
    reviewStatus: r.reviewStatus,
  }));

  const activeObjCount = rows.filter((r) => !r.exempt).length;
  const exemptObjCount = rows.filter((r) => r.exempt).length;
  const orgCount = rows.filter((r) => r.objectKind === 'organization').length;
  const spaceCount = rows.filter((r) => r.objectKind !== 'organization').length;
  const energyList = Array.from(new Set(rows.map((r) => energyLabels[r.energy] ?? r.energy)));
  const buildingCount = inventory?.buildings?.length ?? 0;

  const gaugeStatus =
    data.summaryStatus === 'partial_blocked'
      ? '存在低质量阻断'
      : data.weightedTotalScore == null
        ? '暂无总分'
        : data.weightedTotalScore >= 85
          ? '综合评级 · 优秀'
          : data.weightedTotalScore >= 70
            ? '综合评级 · 良好'
            : data.weightedTotalScore >= 60
              ? '综合评级 · 合格'
              : '综合评级 · 待整改';

  return (
    <div className="space-y-4">
      {/* ── 1. Hero Executive Band: Compact Scope Strip + Weighted Total Score Gauge + Portfolio Counters ── */}
      <section aria-label="考核范围与加权总分摘要" className="arco-card overflow-hidden">
        {/* Compact Assessment Scope Bar (Project, Object Scope, Assessment Period — no repeated feature title) */}
        <div className="flex flex-wrap items-center gap-x-5 gap-y-1.5 border-b border-line bg-fill-1/50 px-5 py-2.5 text-[12px] tabular">
          <div className="flex items-center gap-1.5">
            <span className="text-muted">统计项目</span>
            <span className="font-medium text-fg">
              {inventory?.dataset?.datasetName
                ? `${inventory.dataset.datasetName}${buildingCount > 0 ? `（${buildingCount} 栋建筑）` : ''}`
                : `园区建筑能耗考核（${buildingCount || 5} 栋建筑）`}
            </span>
          </div>
          <span className="hidden h-3.5 w-px bg-line sm:inline-block" aria-hidden="true" />
          <div className="flex items-center gap-1.5">
            <span className="text-muted">对象范围</span>
            <span className="font-medium text-fg">
              共 {rows.length} 项（{orgCount} 个组织{spaceCount > 0 ? ` + ${spaceCount} 个区域` : ''}
              {exemptObjCount > 0 ? `，含 ${exemptObjCount} 项豁免` : ''} · 覆盖 {energyList.join(' / ') || '多能源'}）
            </span>
          </div>
          <span className="hidden h-3.5 w-px bg-line sm:inline-block" aria-hidden="true" />
          <div className="flex items-center gap-1.5">
            <span className="text-muted">考核周期</span>
            <span className="font-medium text-fg">
              自然日 {fmtShanghaiDate(data.from)}（{data.timezone}）
            </span>
          </div>
        </div>

        <div className="grid divide-y divide-line lg:grid-cols-12 lg:divide-y-0 lg:divide-x tabular">
          <div className="flex flex-col items-center justify-center gap-1 bg-fill-1/35 px-5 py-4 sm:flex-row sm:justify-start sm:gap-5 lg:col-span-5">
            <ScoreGaugeChart
              score={data.weightedTotalScore}
              label="加权综合总分"
              statusLabel={gaugeStatus}
            />
            <div className="min-w-0 text-center sm:text-left">
              <p className="text-[12px] font-medium text-fg-2">加权综合总分（核心指标）</p>
              <p className="mt-1 text-[12px] text-muted">
                考核日期：{fmtShanghaiDate(data.from)}
              </p>
              <p className="mt-0.5 text-[12px] text-muted">
                {data.summaryStatus === 'partial_blocked'
                  ? '存在低质量阻断，禁止输出正式总分'
                  : `有效权重合计 ${data.totalWeightPct ?? 0}% · 满分 100 分`}
              </p>
            </div>
          </div>

          <div className="grid grid-cols-3 divide-x divide-line lg:col-span-7">
            <div className="flex flex-col justify-center p-4 lg:p-5">
              <p className="text-[12px] text-fg-2">达标组织占比</p>
              <p className="mt-2 text-[26px] font-semibold leading-[32px] text-positive">
                {withinTargetCount}
                <span className="ml-1 text-[15px] font-normal text-muted">/ {activeObjCount}</span>
              </p>
              <p className="mt-1.5 text-[11px] text-muted">实际用量 ≤ 考核目标</p>
            </div>

            <div className="flex flex-col justify-center p-4 lg:p-5">
              <p className="text-[12px] text-fg-2">复核确认进度</p>
              <p className="mt-2 text-[26px] font-semibold leading-[32px] text-fg">
                {confirmedCount}
                <span className="ml-1 text-[15px] font-normal text-muted">/ {rows.length}</span>
              </p>
              <p className="mt-1.5 text-[11px] text-muted">含豁免项 · 复用 F07 实算</p>
            </div>

            <div className="flex flex-col justify-center p-4 lg:p-5">
              <p className="text-[12px] text-fg-2">低质量阻断项</p>
              <p
                className={`mt-2 text-[26px] font-semibold leading-[32px] ${
                  blockedCount > 0 ? 'text-negative' : 'text-fg'
                }`}
              >
                {blockedCount}
              </p>
              <p className="mt-1.5 text-[11px] text-muted">覆盖率不足禁止出分与确认</p>
            </div>
          </div>
        </div>
      </section>

      {reviewMsg && (
        <div role="status" className="rounded-[4px] border border-primary/30 bg-primary-soft/50 px-4 py-2 text-[12px] text-fg">
          {reviewMsg}
        </div>
      )}

      {/* ── 2. Primary Visual Stage: Horizontal Organization Ranking (7/8 cols) + Score Tier Distribution (5/4 cols) ── */}
      <div className="grid gap-4 lg:grid-cols-12">
        <section className="arco-card p-5 lg:col-span-7 2xl:col-span-8">
          <div className="mb-3 flex items-baseline justify-between gap-2">
            <div>
              <h2 className="text-[15px] font-medium leading-[22px] text-fg">组织考核横向排名</h2>
              <p className="mt-0.5 text-[12px] text-muted">按得分降序排列，标注权重与加权得分贡献（豁免及低质量项置底）</p>
            </div>
          </div>
          <AssessmentScoreChart rows={chartRows} />
        </section>

        <section className="arco-card p-5 lg:col-span-5 2xl:col-span-4">
          <div className="mb-3">
            <h2 className="text-[15px] font-medium leading-[22px] text-fg">考核得分分档分布</h2>
            <p className="mt-0.5 text-[12px] text-muted">按优秀 / 良好 / 合格 / 待整改 / 豁免阻断五档统计组织分布</p>
          </div>
          <ScoreDistributionChart rows={chartRows} />
        </section>
      </div>

      {/* ── 3. Secondary Visual Stage: Target Deviation Diverging Bar Chart ── */}
      <section className="arco-card p-5">
        <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
          <div>
            <h2 className="text-[15px] font-medium leading-[22px] text-fg">目标偏差率对照可视化（%）</h2>
            <p className="mt-0.5 text-[12px] text-muted">以 0% 考核目标为中轴，左侧绿色表示低于目标（节约），右侧红色表示超额扣分</p>
          </div>
        </div>
        <DeviationDivergingChart rows={chartRows} />
      </section>

      {/* ── 4. Actionable Assessment Detail Ledger (Below Charts) ── */}
      <section className="arco-card overflow-x-auto">
        <div className="flex items-center justify-between border-b border-line px-5 py-3">
          <h2 className="text-[15px] font-medium text-fg">组织与区域考核明细表与复核操作</h2>
          <span className="text-[12px] text-muted">支持在线确认与发起复核申诉</span>
        </div>
        <table className="w-full text-left text-xs">
          <thead className="border-b border-line bg-surface-2 text-muted">
            <tr>
              <th className="whitespace-nowrap px-4 py-2.5">考核对象</th>
              <th className="whitespace-nowrap px-4 py-2.5">能源 / 指标</th>
              <th className="whitespace-nowrap px-4 py-2.5 text-right">目标 / 实际</th>
              <th className="whitespace-nowrap px-4 py-2.5 text-right">偏差率</th>
              <th className="whitespace-nowrap px-4 py-2.5 text-right">权重 / 覆盖</th>
              <th className="whitespace-nowrap px-4 py-2.5 text-right">得分 / 加权</th>
              <th className="whitespace-nowrap px-4 py-2.5">状态与复核操作</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line tabular">
            {rows.map((r) => (
              <tr key={r.ruleId}>
                <td className="whitespace-nowrap px-4 py-2.5">
                  <div className="font-medium text-fg">{r.objectName}</div>
                  <div className="text-[11px] text-muted">{r.objectId}</div>
                </td>
                <td className="whitespace-nowrap px-4 py-2.5 text-muted">
                  <span className="font-medium text-fg">{energyLabels[r.energy] ?? r.energy}</span> · {r.metricLabel}
                </td>
                <td className="whitespace-nowrap px-4 py-2.5 text-right">
                  <div className="font-medium text-fg">{fmtNum(r.actualUsage)} {r.unit}</div>
                  <div className="text-[11px] text-muted">目标 {fmtNum(r.targetValue)}</div>
                </td>
                <td className={`whitespace-nowrap px-4 py-2.5 text-right font-medium ${r.deviationRate != null && r.deviationRate > 0 ? 'text-negative' : 'text-positive'}`}>
                  {r.exempt ? '豁免' : fmtPct(r.deviationRate)}
                </td>
                <td className="whitespace-nowrap px-4 py-2.5 text-right text-muted">
                  {r.weightPct}% · {fmtPct(r.coverage.rate)}
                </td>
                <td className="whitespace-nowrap px-4 py-2.5 text-right">
                  <div className="font-semibold text-fg">{r.exempt ? '豁免' : r.score != null ? `${r.score} 分` : '—'}</div>
                  {r.weightedScore != null && <div className="text-[11px] text-muted">加权 {r.weightedScore}</div>}
                </td>
                <td className="min-w-[240px] px-4 py-2.5">
                  <div className="flex flex-wrap items-center gap-1.5">
                    <span
                      className={`inline-block rounded-full px-2 py-0.5 text-[11px] font-medium ${
                        r.reviewStatus === 'confirmed'
                          ? 'bg-positive-soft text-positive'
                          : r.reviewStatus === 'blocked_low_quality'
                          ? 'bg-negative-soft text-negative'
                          : 'bg-warning-soft text-warning'
                      }`}
                    >
                      {r.exempt ? '豁免·已确认' : (statusLabels[r.reviewStatus] ?? r.reviewStatus)}
                    </span>
                    {!r.exempt && r.confirmable && (
                      <>
                        {r.reviewStatus !== 'confirmed' && (
                          <button
                            type="button"
                            disabled={busyRule === r.ruleId}
                            onClick={() => handleReview(r.ruleId, 'confirmed', '人工复核覆盖率与指标无误后确认（模拟）')}
                            className="rounded border border-positive/40 px-1.5 py-0.5 text-[11px] text-positive hover:bg-positive-soft"
                          >
                            确认
                          </button>
                        )}
                        {r.reviewStatus !== 'appealed' && (
                          <button
                            type="button"
                            disabled={busyRule === r.ruleId}
                            onClick={() => handleReview(r.ruleId, 'appealed', '发起考核复核申诉（模拟）')}
                            className="rounded border border-warning/40 px-1.5 py-0.5 text-[11px] text-warning hover:bg-warning-soft"
                          >
                            申诉
                          </button>
                        )}
                      </>
                    )}
                  </div>
                  {r.formulaDetail && <div className="mt-1 text-[11px] text-muted">{r.formulaDetail}</div>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
    </div>
  );
}

// ── F16: High-Consumer Diagnostic Workbench (Reuses F07 / F11 / F06 / F10) ──

function LiveDiagnosisView({
  data,
  token,
  inventory,
  date,
  onChangeView,
  onNavigateOpportunities,
}: {
  data: DimensionsData;
  token: string;
  inventory: InventoryData | null;
  date: string;
  onChangeView: (view: string) => void;
  onNavigateOpportunities: (buildingId: string) => void;
}) {
  const [diagScope, setDiagScope] = useState<'space' | 'building' | 'system'>(() =>
    data.view === 'building' ? 'building' : 'space',
  );
  const [metricLens, setMetricLens] = useState<'usage' | 'intensity'>('usage');
  const [selectedId, setSelectedId] = useState<string | null>(null);

  // Reused server-computed companion datasets (zero duplicate frontend statistical engines).
  const [bldgDims, setBldgDims] = useState<DimensionsData | null>(null);
  const [spaceDims, setSpaceDims] = useState<DimensionsData | null>(null);
  const [effByBldg, setEffByBldg] = useState<Record<string, EfficiencyData>>({});
  const [anomByBldg, setAnomByBldg] = useState<Record<string, AnomalyData>>({});
  const [classByBldg, setClassByBldg] = useState<Record<string, ClassificationData>>({});
  const [trend7d, setTrend7d] = useState<RealtimeTrendData | null>(null);

  useEffect(() => {
    if (data.view === 'building') setBldgDims(data);
    if (data.view === 'space') setSpaceDims(data);
  }, [data]);

  useEffect(() => {
    const bldgs = inventory?.buildings ?? [];
    if (data.configurationState === 'unconfigured') return;
    const ctrl = new AbortController();

    const qsB = new URLSearchParams({ view: 'building', energy: data.energy });
    const qsS = new URLSearchParams({ view: 'space', energy: data.energy });
    if (date) {
      qsB.set('date', date);
      qsS.set('date', date);
    }

    Promise.all([
      data.view === 'building'
        ? Promise.resolve({ data })
        : liveGet<DimensionsData>(`/dimensions?${qsB}`, token, ctrl.signal).catch(() => ({ data: null })),
      data.view === 'space'
        ? Promise.resolve({ data })
        : liveGet<DimensionsData>(`/dimensions?${qsS}`, token, ctrl.signal).catch(() => ({ data: null })),
      ...bldgs.map((b) => {
        const qs = new URLSearchParams({ building: b.buildingId, energy: data.energy });
        if (date) qs.set('date', date);
        return Promise.all([
          liveGet<EfficiencyData>(`/efficiency?${qs}`, token, ctrl.signal).catch(() => ({ data: null })),
          liveGet<AnomalyData>(`/anomalies?${qs}`, token, ctrl.signal).catch(() => ({ data: null })),
          liveGet<ClassificationData>(`/classification?${qs}`, token, ctrl.signal).catch(() => ({ data: null })),
        ]).then(([e, a, c]) => ({ id: b.buildingId, eff: e.data ?? null, anom: a.data ?? null, cls: c.data ?? null }));
      }),
    ]).then(([bRes, sRes, ...perBldg]) => {
      if (ctrl.signal.aborted) return;
      if (bRes?.data) setBldgDims(bRes.data);
      if (sRes?.data) setSpaceDims(sRes.data);
      const effMap: Record<string, EfficiencyData> = {};
      const anomMap: Record<string, AnomalyData> = {};
      const clsMap: Record<string, ClassificationData> = {};
      for (const item of perBldg) {
        if (!item) continue;
        if (item.eff) effMap[item.id] = item.eff;
        if (item.anom) anomMap[item.id] = item.anom;
        if (item.cls) clsMap[item.id] = item.cls;
      }
      setEffByBldg(effMap);
      setAnomByBldg(anomMap);
      setClassByBldg(clsMap);
    });

    return () => ctrl.abort();
  }, [data, inventory?.buildings, date, token]);

  const spaceProfileMap = useMemo(
    () => new Map((inventory?.spaces ?? []).map((s) => [s.spaceId, s])),
    [inventory?.spaces],
  );
  const bldgProfileMap = useMemo(
    () => new Map((inventory?.buildings ?? []).map((b) => [b.buildingId, b])),
    [inventory?.buildings],
  );

  // Build diagnostic records across Building / Space / System levels strictly from F07/F11/F06/F10 results.
  type DiagRecord = {
    id: string;
    name: string;
    buildingId: string;
    buildingName: string;
    usage: number | null;
    share: number | null;
    intensity: number | null;
    intensityUnit: string;
    intensityReason: string;
    coverageRate: number | null;
    comparable: boolean;
    statusText: string;
    anomalyRate: number | null;
    isAnomalyCandidate: boolean;
    rank: number | null;
    sources: SourceTrace[];
  };

  const bldgRecords: DiagRecord[] = useMemo(() => {
    const rows = bldgDims?.rows ?? [];
    const total = rows.reduce((s, r) => s + (r.usage ?? 0), 0);
    return rows.map((r) => {
      const eff = effByBldg[r.id];
      const areaMetric = (eff?.metrics ?? []).find((m) => m.denominatorKind === 'area') ?? null;
      const anom = anomByBldg[r.id];
      const hasInt = areaMetric?.status === 'ready' && areaMetric.value != null;
      return {
        id: r.id,
        name: r.name,
        buildingId: r.id,
        buildingName: r.name,
        usage: r.usage,
        share: total > 0 && r.usage != null ? r.usage / total : null,
        intensity: hasInt ? areaMetric!.value : null,
        intensityUnit: areaMetric?.unit ?? `${data.unit}/m²`,
        intensityReason: hasInt
          ? `F11 实算（面积 ${fmtNum(areaMetric?.denominatorValue)} m²）`
          : areaMetric?.reason || '缺少有效建筑面积或覆盖率不足',
        coverageRate: r.coverage.rate,
        comparable: r.status === 'comparable' && (r.coverage.rate ?? 0) >= 0.9,
        statusText: r.reason || (statusLabels[r.status] ?? r.status),
        anomalyRate: anom?.changeRate ?? null,
        isAnomalyCandidate: anom?.status === 'usage_candidate',
        rank: r.rank,
        sources: r.sources ?? [],
      };
    });
  }, [bldgDims, effByBldg, anomByBldg, data.unit]);

  const spaceRecords: DiagRecord[] = useMemo(() => {
    const rows = spaceDims?.rows ?? [];
    const total = rows.reduce((s, r) => s + (r.usage ?? 0), 0);
    return rows.map((r) => {
      const sp = spaceProfileMap.get(r.id);
      const bId = sp?.buildingId ?? '';
      const bName = bldgProfileMap.get(bId)?.name ?? bId;
      const anom = bId ? anomByBldg[bId] : undefined;
      const covOk = r.status === 'comparable' && (r.coverage.rate ?? 0) >= 0.9;
      const hasArea = sp?.areaSqm != null && sp.areaSqm > 0;
      const intensity = covOk && hasArea && r.usage != null ? r.usage / sp!.areaSqm! : null;
      return {
        id: r.id,
        name: r.name,
        buildingId: bId,
        buildingName: bName,
        usage: r.usage,
        share: total > 0 && r.usage != null ? r.usage / total : null,
        intensity,
        intensityUnit: `${data.unit}/m²`,
        intensityReason: !covOk
          ? `覆盖或状态不可比（${statusLabels[r.status] ?? r.status}）`
          : !hasArea
            ? '空间档案缺少有效面积，不生成强度'
            : `面积 ${fmtNum(sp!.areaSqm)} m²`,
        coverageRate: r.coverage.rate,
        comparable: covOk,
        statusText: r.reason || (statusLabels[r.status] ?? r.status),
        anomalyRate: anom?.changeRate ?? null,
        isAnomalyCandidate: Boolean(anom?.status === 'usage_candidate'),
        rank: r.rank,
        sources: r.sources ?? [],
      };
    });
  }, [spaceDims, spaceProfileMap, bldgProfileMap, anomByBldg, data.unit]);

  const systemRecords: DiagRecord[] = useMemo(() => {
    const agg = new Map<
      string,
      { id: string; name: string; usage: number; expected: number; valid: number; sources: SourceTrace[]; leadBldgId: string; leadBldgName: string; leadUsage: number }
    >();
    for (const [bId, cls] of Object.entries(classByBldg)) {
      const bName = bldgProfileMap.get(bId)?.name ?? bId;
      for (const c of cls.categories ?? []) {
        const cur = agg.get(c.id) ?? {
          id: c.id,
          name: c.name,
          usage: 0,
          expected: 0,
          valid: 0,
          sources: [],
          leadBldgId: bId,
          leadBldgName: bName,
          leadUsage: -1,
        };
        const u = c.usage ?? 0;
        cur.usage += u;
        cur.expected += c.coverage.expected;
        cur.valid += c.coverage.valid;
        if (u > cur.leadUsage) {
          cur.leadUsage = u;
          cur.leadBldgId = bId;
          cur.leadBldgName = bName;
        }
        agg.set(c.id, cur);
      }
    }
    const list = Array.from(agg.values()).sort((a, b) => b.usage - a.usage);
    const total = list.reduce((s, x) => s + x.usage, 0);
    return list.map((x, idx) => {
      const covRate = x.expected > 0 ? x.valid / x.expected : null;
      return {
        id: x.id,
        name: x.name,
        buildingId: x.leadBldgId,
        buildingName: `主力来源：${x.leadBldgName}`,
        usage: x.usage,
        share: total > 0 ? x.usage / total : null,
        intensity: null,
        intensityUnit: `${data.unit}/m²`,
        intensityReason: '用能系统跨空间聚合，不按单建筑总面积折算强度',
        coverageRate: covRate,
        comparable: covRate != null && covRate >= 0.9,
        statusText: covRate != null && covRate >= 0.9 ? '同口径可比' : '覆盖率不足',
        anomalyRate: null,
        isAnomalyCandidate: false,
        rank: covRate != null && covRate >= 0.9 ? idx + 1 : null,
        sources: (classByBldg[x.leadBldgId]?.sources ?? []),
      };
    });
  }, [classByBldg, bldgProfileMap, data.unit]);

  const activeRecords =
    diagScope === 'building' ? bldgRecords : diagScope === 'system' ? systemRecords : spaceRecords;

  const effectiveLens = diagScope === 'system' ? 'usage' : metricLens;

  const sortedRecords = useMemo(() => {
    const copy = [...activeRecords];
    if (effectiveLens === 'intensity') {
      copy.sort((a, b) => (b.intensity ?? -1) - (a.intensity ?? -1));
      let rk = 1;
      return copy.map((r) => ({ ...r, displayRank: r.intensity != null && r.comparable ? rk++ : null }));
    }
    copy.sort((a, b) => (b.usage ?? -1) - (a.usage ?? -1));
    let rk = 1;
    return copy.map((r) => ({ ...r, displayRank: r.usage != null && r.comparable ? rk++ : null }));
  }, [activeRecords, effectiveLens]);

  const selectedRecord = useMemo(
    () => sortedRecords.find((r) => r.id === selectedId) ?? sortedRecords[0] ?? null,
    [sortedRecords, selectedId],
  );

  // Fetch 7-day trend for the selected object's building so right panel shows real trend context.
  useEffect(() => {
    const targetBldg = selectedRecord?.buildingId;
    if (!targetBldg) {
      setTrend7d(null);
      return;
    }
    const ctrl = new AbortController();
    const qs = new URLSearchParams({ building: targetBldg, energy: data.energy, days: '7' });
    if (date) qs.set('date', date);
    liveGet<RealtimeTrendData>(`/realtime-trend?${qs}`, token, ctrl.signal)
      .then((res) => {
        if (!ctrl.signal.aborted) setTrend7d(res.data ?? null);
      })
      .catch(() => {
        if (!ctrl.signal.aborted) setTrend7d(null);
      });
    return () => ctrl.abort();
  }, [selectedRecord?.buildingId, data.energy, date, token]);

  if (data.configurationState === 'unconfigured') {
    return <UnconfiguredNotice message={data.configurationMessage} />;
  }

  // Distinct 3-Lens Leaders (Volume vs Intensity vs Anomaly Growth — never conflated!).
  const topVolumeBldg = [...bldgRecords].sort((a, b) => (b.usage ?? -1) - (a.usage ?? -1))[0] ?? null;
  const topIntensityBldg = [...bldgRecords].filter((r) => r.intensity != null).sort((a, b) => (b.intensity ?? -1) - (a.intensity ?? -1))[0] ?? null;
  const anomalyCandidates = bldgRecords.filter((r) => r.isAnomalyCandidate);
  const topAnomalyBldg = anomalyCandidates.sort((a, b) => (b.anomalyRate ?? -1) - (a.anomalyRate ?? -1))[0] ?? null;
  const topSystem = systemRecords[0] ?? null;

  const selectedBldgClass = selectedRecord?.buildingId ? classByBldg[selectedRecord.buildingId] ?? null : null;
  const selectedSlices = (selectedBldgClass?.categories ?? []).map((c) => ({
    id: c.id,
    name: c.name,
    usage: c.usage,
    share: c.share,
  }));
  const allSources = activeRecords.flatMap((r) => r.sources ?? []);

  return (
    <div className="space-y-4">
      {/* ── 1. Compact Three-Lens Distinction Bar (Volume ≠ Intensity ≠ Anomaly Growth — Zero 4-Box KPI Repetition) ── */}
      <section aria-label="高耗能三维度诊断区分" className="arco-card flex flex-wrap items-center justify-between gap-3 px-5 py-3 text-[12px] tabular">
        <div className="flex flex-wrap items-center gap-2.5">
          <span className="font-semibold text-fg">
            {energyLabels[data.energy] ?? data.energy}高耗能二维诊断
          </span>
          <span className="text-[11px] text-muted">自然日 {fmtShanghaiDate(data.from)}</span>
          <span className="inline-flex items-center gap-1 rounded-[2px] bg-primary-soft/60 px-2 py-0.5 text-[11px]">
            <span className="font-medium text-primary">高用量 F07</span>
            <strong className="text-fg">{topVolumeBldg?.name ?? '—'}</strong>
            {topVolumeBldg && <span className="text-muted">({fmtNum(topVolumeBldg.usage)} {data.unit})</span>}
          </span>
          <span className="inline-flex items-center gap-1 rounded-[2px] bg-[#E8FFFB] px-2 py-0.5 text-[11px]">
            <span className="font-medium text-[#0AA5A2]">高强度 F11</span>
            <strong className="text-fg">{topIntensityBldg?.name ?? '不可比'}</strong>
            {topIntensityBldg && <span className="text-muted">({fmtNum(topIntensityBldg.intensity)} {topIntensityBldg.intensityUnit})</span>}
          </span>
          <span
            className={`inline-flex items-center gap-1 rounded-[2px] px-2 py-0.5 text-[11px] ${
              topAnomalyBldg ? 'bg-warning-soft text-warning' : 'bg-fill-2 text-fg-2'
            }`}
          >
            <span className="font-medium">异常增长 F10</span>
            <strong>{topAnomalyBldg ? `${topAnomalyBldg.name} (+${fmtPct(topAnomalyBldg.anomalyRate)})` : '无超阈值候选'}</strong>
          </span>
          {topSystem && (
            <span className="inline-flex items-center gap-1 rounded-[2px] bg-fill-1 px-2 py-0.5 text-[11px] text-fg-2">
              <span>主力系统</span>
              <strong className="text-fg">{topSystem.name}</strong> ({fmtPct(topSystem.share)})
            </span>
          )}
        </div>
        <span className="text-[11px] text-muted">复用 F07 / F11 / F10 实算口径 · 不混为一谈</span>
      </section>

      {/* ── 2. Primary Diagnostic Stage: 2D Volume×Intensity Quadrant Matrix (8 cols) + Selected Object Linked Inspector (4 cols) ── */}
      <div className="grid items-start gap-4 lg:grid-cols-12">
        <section className="arco-card space-y-4 p-5 lg:col-span-8">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div>
              <h2 className="text-[15px] font-medium leading-[22px] text-fg">
                {diagScope === 'space'
                  ? '区域「用能总量 × 面积能耗强度」二维诊断象限'
                  : diagScope === 'building'
                    ? '建筑「用能总量 × 面积能耗强度」二维诊断象限'
                    : '园区用能系统贡献占比与主力建筑分布'}
              </h2>
              <p className="mt-0.5 text-[12px] text-muted">
                {diagScope === 'system'
                  ? '用能系统跨空间聚合，按实测用量与占比贡献排序；点击系统查看主力建筑趋势与来源'
                  : '同时定位「体量大」「强度高」「叠加异常增长」三类对象，区别于 F07 单维排名；点击气泡或表格行联动右侧'}
              </p>
            </div>

            <div className="flex flex-wrap items-center gap-2">
              <div className="inline-flex rounded-[2px] border border-line bg-fill-1 p-0.5 text-[12px]">
                {(
                  [
                    { id: 'space', label: '区域' },
                    { id: 'building', label: '建筑' },
                    { id: 'system', label: '用能系统' },
                  ] as const
                ).map((t) => (
                  <button
                    key={t.id}
                    type="button"
                    data-diag-scope={t.id}
                    onClick={() => {
                      setDiagScope(t.id);
                      setSelectedId(null);
                      if (t.id === 'building' || t.id === 'space') onChangeView(t.id);
                    }}
                    className={`rounded-[2px] px-2.5 py-1 transition-colors ${
                      diagScope === t.id ? 'bg-surface font-medium text-primary shadow-xs' : 'text-fg-2 hover:text-fg'
                    }`}
                  >
                    {t.label}
                  </button>
                ))}
              </div>

              {diagScope !== 'system' && (
                <div className="inline-flex rounded-[2px] border border-line bg-fill-1 p-0.5 text-[12px]">
                  <button
                    type="button"
                    data-diag-lens="usage"
                    onClick={() => setMetricLens('usage')}
                    className={`rounded-[2px] px-2.5 py-1 transition-colors ${
                      metricLens === 'usage' ? 'bg-primary font-medium text-white' : 'text-fg-2 hover:text-fg'
                    }`}
                  >
                    按用量排序
                  </button>
                  <button
                    type="button"
                    data-diag-lens="intensity"
                    onClick={() => setMetricLens('intensity')}
                    className={`rounded-[2px] px-2.5 py-1 transition-colors ${
                      metricLens === 'intensity' ? 'bg-primary font-medium text-white' : 'text-fg-2 hover:text-fg'
                    }`}
                  >
                    按强度排序
                  </button>
                </div>
              )}
            </div>
          </div>

          {diagScope !== 'system' ? (
            <DiagnosticQuadrantChart
              unit={data.unit}
              intensityUnit={`${data.unit}/m²`}
              selectedId={selectedRecord?.id ?? null}
              onSelect={(id) => setSelectedId(id)}
              points={sortedRecords.map((r) => ({
                id: r.id,
                name: r.name,
                subLabel: r.buildingName !== r.name ? r.buildingName : undefined,
                usage: r.usage,
                intensity: r.intensity,
                comparable: r.comparable,
                isAnomalyCandidate: r.isAnomalyCandidate,
                anomalyRate: r.anomalyRate,
                reason: r.intensity == null ? r.intensityReason : r.statusText,
              }))}
            />
          ) : (
            <HorizontalRankingChart
              unit={data.unit}
              selectedId={selectedRecord?.id ?? null}
              onSelect={(id) => setSelectedId(id)}
              items={sortedRecords.map((r) => ({
                id: r.id,
                name: r.name,
                usage: r.usage,
                rank: r.displayRank,
                coverageRate: r.coverageRate,
                statusText: r.statusText,
              }))}
            />
          )}

          {/* Compact Multi-Lens Diagnostic Table (Shows Volume, Share, Intensity & Anomaly Tag Side-by-Side) */}
          <div className="border-t border-line pt-4">
            <div className="mb-2 flex items-center justify-between text-[12px]">
              <h3 className="font-medium text-fg">对象多维诊断明细（用量 / 占比 / 强度 / 增长状态对照）</h3>
              <span className="text-[11px] text-muted">点击行联动右侧溯源</span>
            </div>
            <div className="overflow-x-auto">
              <table className="w-full text-left text-[12px]">
                <thead className="border-b border-line bg-fill-1 text-fg-2">
                  <tr>
                    <th className="px-3 py-2 font-medium">诊断对象</th>
                    <th className="px-3 py-2 text-right font-medium">用量 ({data.unit})</th>
                    <th className="px-3 py-2 text-right font-medium">贡献占比</th>
                    <th className="px-3 py-2 text-right font-medium">面积强度 ({data.unit}/m²)</th>
                    <th className="px-3 py-2 text-right font-medium">覆盖率</th>
                    <th className="px-3 py-2 font-medium">诊断特征区分</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-line tabular">
                  {sortedRecords.map((r) => {
                    const isSel = selectedRecord?.id === r.id;
                    return (
                      <tr
                        key={r.id}
                        data-diag-row={r.id}
                        onClick={() => setSelectedId(r.id)}
                        className={`cursor-pointer transition-colors ${isSel ? 'bg-primary-soft/40' : 'hover:bg-fill-1/60'}`}
                      >
                        <td className="px-3 py-2">
                          <div className="font-medium text-fg">{r.name}</div>
                          {r.buildingName && r.buildingName !== r.name && (
                            <div className="text-[11px] text-muted">{r.buildingName}</div>
                          )}
                        </td>
                        <td className="px-3 py-2 text-right font-semibold text-fg">{fmtNum(r.usage)}</td>
                        <td className="px-3 py-2 text-right text-fg-2">{fmtPct(r.share)}</td>
                        <td className="px-3 py-2 text-right">
                          {r.intensity != null ? (
                            <span className="font-medium text-fg">{fmtNum(r.intensity)}</span>
                          ) : (
                            <span className="text-[11px] text-muted" title={r.intensityReason}>
                              不可算
                            </span>
                          )}
                        </td>
                        <td className="px-3 py-2 text-right text-muted">{fmtPct(r.coverageRate)}</td>
                        <td className="px-3 py-2">
                          <div className="flex flex-wrap gap-1">
                            {!r.comparable && (
                              <span className="rounded-[2px] bg-warning-soft px-1.5 py-0 text-[10px] text-warning">
                                不可比/低覆盖
                              </span>
                            )}
                            {r.comparable && r.share != null && r.share >= 0.25 && (
                              <span className="rounded-[2px] bg-primary-soft px-1.5 py-0 text-[10px] text-primary">
                                高用量贡献
                              </span>
                            )}
                            {r.intensity != null &&
                              topIntensityBldg?.intensity != null &&
                              r.intensity >= topIntensityBldg.intensity * 0.95 && (
                                <span className="rounded-[2px] bg-[#E8FFFB] px-1.5 py-0 text-[10px] text-[#0AA5A2]">
                                  高强度
                                </span>
                              )}
                            {r.isAnomalyCandidate && (
                              <span className="rounded-[2px] bg-warning-soft px-1.5 py-0 text-[10px] font-medium text-warning">
                                异常增长 +{fmtPct(r.anomalyRate)}
                              </span>
                            )}
                            {r.comparable &&
                              !r.isAnomalyCandidate &&
                              (r.share == null || r.share < 0.25) &&
                              !(
                                r.intensity != null &&
                                topIntensityBldg?.intensity != null &&
                                r.intensity >= topIntensityBldg.intensity * 0.95
                              ) && (
                                <span className="text-[11px] text-muted">常态运行</span>
                              )}
                          </div>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </div>
        </section>

        {/* Right 4 Cols: Linked Object Trend + Sub-Item Breakdown + Sources */}
        <aside aria-label="选中高耗能对象诊断详情" className="arco-card space-y-4 p-5 text-[12px] tabular lg:col-span-4">
          {selectedRecord ? (
            <>
              <div className="flex items-start justify-between gap-2 border-b border-line pb-3">
                <div className="min-w-0">
                  <span className="text-[11px] text-muted">选中诊断对象联动分析</span>
                  <h3 className="mt-0.5 truncate text-[15px] font-medium text-fg">{selectedRecord.name}</h3>
                </div>
                {selectedRecord.buildingId && (
                  <button
                    type="button"
                    onClick={() => onNavigateOpportunities(selectedRecord.buildingId)}
                    className="shrink-0 inline-flex items-center gap-1 rounded-[2px] border border-primary/35 bg-primary-soft/50 px-2 py-1 text-[11px] font-medium text-primary hover:bg-primary-soft"
                  >
                    F17 机会线索
                    <ArrowRight className="h-3 w-3" aria-hidden="true" />
                  </button>
                )}
              </div>

              <div className="grid grid-cols-2 gap-2 rounded-[4px] bg-fill-1/60 p-3 text-[11px]">
                <div>
                  <span className="text-muted">实测用量（贡献占比）</span>
                  <p className="mt-0.5 text-[14px] font-semibold text-fg">
                    {fmtNum(selectedRecord.usage)} {data.unit}
                    {selectedRecord.share != null && (
                      <span className="ml-1 text-[11px] font-normal text-muted">({fmtPct(selectedRecord.share)})</span>
                    )}
                  </p>
                </div>
                <div>
                  <span className="text-muted">面积能耗强度</span>
                  <p className="mt-0.5 text-[14px] font-semibold text-fg">
                    {selectedRecord.intensity != null
                      ? `${fmtNum(selectedRecord.intensity)} ${selectedRecord.intensityUnit}`
                      : '不可计算'}
                  </p>
                </div>
              </div>

              <p className="text-[11px] text-muted">口径依据：{selectedRecord.intensityReason}</p>

              {/* 7-Day Trend for Selected Building */}
              {trend7d && (trend7d.series?.length ?? 0) > 0 && (
                <div className="border-t border-line pt-3">
                  <div className="mb-1.5 flex items-center justify-between text-[11px]">
                    <span className="font-medium text-fg-2">所属建筑近 7 日负荷趋势</span>
                    <span className="text-muted">{trend7d.buildingName}</span>
                  </div>
                  <TrendAreaChart
                    points={trend7d.series}
                    unit={trend7d.unit}
                    height={168}
                    highlightPeak={false}
                  />
                </div>
              )}

              {/* Building Sub-Item Composition */}
              {selectedSlices.length > 0 && (
                <div className="border-t border-line pt-3">
                  <div className="mb-2 flex items-center justify-between text-[11px]">
                    <span className="font-medium text-fg-2">所属建筑分项构成</span>
                    <span className="text-muted">{fmtNum(selectedBldgClass?.totalUsage)} {data.unit}</span>
                  </div>
                  <EnergyStructureChart
                    slices={selectedSlices}
                    totalUsage={selectedBldgClass?.totalUsage}
                    unit={data.unit}
                    layout="stack"
                  />
                </div>
              )}

              {/* Metering Sources */}
              <div className="border-t border-line pt-3">
                <div className="mb-1.5 flex items-center justify-between text-[11px] text-muted">
                  <span className="font-medium text-fg-2">关联计量来源（{selectedRecord.sources.length} 块）</span>
                  <span>覆盖 {fmtPct(selectedRecord.coverageRate)}</span>
                </div>
                {selectedRecord.sources.length === 0 ? (
                  <p className="text-[11px] text-muted">当前对象暂无直接关联表具记录。</p>
                ) : (
                  <ul className="divide-y divide-line rounded-[4px] border border-line text-[11px]">
                    {selectedRecord.sources.slice(0, 5).map((s) => (
                      <li key={`${s.terminalId}-${s.itemCode}`} className="flex items-center justify-between gap-2 px-2.5 py-1.5">
                        <span className="min-w-0 truncate">
                          <span className="font-medium text-fg">{s.terminalName}</span>
                          <span className="ml-1.5 font-mono text-[10px] text-muted">{s.terminalId}</span>
                        </span>
                        <span className="shrink-0 font-semibold text-fg">
                          {fmtNum(s.usage)} {s.unit}
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            </>
          ) : null}
        </aside>
      </div>

      <SourceTraceTable sources={allSources} />
    </div>
  );
}

// ── F17: Energy-Saving Opportunity Diagnostic Workbench ─────────────────────

function LiveOpportunitiesView({
  data,
  token,
  inventory,
  date,
  onSelectBuilding,
  onNavigateRetrofit,
}: {
  data: OpportunitiesData;
  token: string;
  inventory: InventoryData | null;
  date: string;
  onSelectBuilding: (buildingId: string) => void;
  onNavigateRetrofit: (buildingId: string, energy: string) => void;
}) {
  const [portfolioOpps, setPortfolioOpps] = useState<OpportunitiesData[]>([]);
  const [statusFilter, setStatusFilter] = useState<'all' | 'pending_manual_review' | 'confirmed' | 'insufficient'>('all');
  const [kindFilter, setKindFilter] = useState<'all' | 'baseline_increase' | 'high_usage_observation'>('all');
  const [selectedKey, setSelectedKey] = useState<string>('');
  // Operator human-in-the-loop review state (strictly distinguishes pending review vs manually confirmed — never auto-labels as waste).
  const [manualConfirmed, setManualConfirmed] = useState<Record<string, boolean>>({});
  const [peerSpaces, setPeerSpaces] = useState<DimensionsData | null>(null);
  const [clueSources, setClueSources] = useState<SourceTrace[]>([]);

  // Aggregate opportunities across all inventory buildings for a unified portfolio opportunity workbench.
  useEffect(() => {
    const bldgs = inventory?.buildings ?? [];
    if (bldgs.length === 0) {
      setPortfolioOpps([data]);
      return;
    }
    const ctrl = new AbortController();
    Promise.all(
      bldgs.map((b) => {
        if (b.buildingId === data.buildingId) return Promise.resolve(data);
        const qs = new URLSearchParams({ building: b.buildingId, energy: data.energy });
        if (date) qs.set('date', date);
        return liveGet<OpportunitiesData>(`/opportunities?${qs}`, token, ctrl.signal)
          .then((r) => r.data ?? null)
          .catch(() => null);
      }),
    ).then((list) => {
      if (ctrl.signal.aborted) return;
      const valid = list.filter((x): x is OpportunitiesData => x != null);
      setPortfolioOpps(valid.length > 0 ? valid : [data]);
    });
    return () => ctrl.abort();
  }, [data, inventory?.buildings, date, token]);

  type OpportunityItem = {
    key: string;
    buildingId: string;
    buildingName: string;
    energy: string;
    unit: string;
    objectId: string;
    objectName: string;
    kind: string;
    rawStatus: string;
    reviewState: 'pending_manual_review' | 'confirmed' | 'insufficient';
    usage: number | null;
    baseline: number | null;
    delta: number | null;
    changeRate: number | null;
    comparablePeers: number;
    evidence: string;
    limitations: string[];
    confirm: string[];
    coverageRate: number | null;
    anomaly: AnomalyData;
  };

  const allItems: OpportunityItem[] = useMemo(() => {
    const items: OpportunityItem[] = [];
    for (const op of portfolioOpps.length ? portfolioOpps : [data]) {
      const clues = op.clues ?? [];
      if (clues.length === 0) {
        items.push({
          key: `${op.buildingId}::none`,
          buildingId: op.buildingId,
          buildingName: op.buildingName,
          energy: op.energy,
          unit: op.unit,
          objectId: op.buildingId,
          objectName: op.buildingName,
          kind: 'insufficient_clue',
          rawStatus: op.anomaly.status,
          reviewState: 'insufficient',
          usage: op.anomaly.current,
          baseline: op.anomaly.baseline,
          delta: op.anomaly.delta,
          changeRate: op.anomaly.changeRate,
          comparablePeers: 0,
          evidence:
            op.anomaly.status === 'no_candidate'
              ? '当前观测日实测用量未达到同星期基线异常增幅门槛，未产生高置信节能机会线索。'
              : `当前数据质量或基线样本不足（${statusLabels[op.anomaly.status] ?? op.anomaly.status}），严禁生成节能量估算。`,
          limitations: ['无异常候选或缺少足够对照样本时不输出节能量或收益预测。'],
          confirm: ['保持例行监测，待完整周期或对比样本齐备后复查。'],
          coverageRate: op.anomaly.coverage.rate,
          anomaly: op.anomaly,
        });
      } else {
        clues.forEach((c, idx) => {
          const k = `${op.buildingId}::${c.objectId}::${c.kind}::${idx}`;
          const isConfirmed = Boolean(manualConfirmed[k]);
          const delta = c.usage != null && c.baseline != null ? c.usage - c.baseline : null;
          const rate = delta != null && c.baseline && c.baseline > 0 ? delta / c.baseline : null;
          items.push({
            key: k,
            buildingId: op.buildingId,
            buildingName: op.buildingName,
            energy: op.energy,
            unit: op.unit,
            objectId: c.objectId,
            objectName: c.objectName,
            kind: c.kind,
            rawStatus: c.status,
            reviewState: isConfirmed ? 'confirmed' : 'pending_manual_review',
            usage: c.usage,
            baseline: c.baseline,
            delta,
            changeRate: rate,
            comparablePeers: c.comparablePeers,
            evidence: c.evidence,
            limitations: c.limitations ?? [],
            confirm: c.confirm ?? [],
            coverageRate: op.anomaly.coverage.rate,
            anomaly: op.anomaly,
          });
        });
      }
    }
    return items;
  }, [portfolioOpps, data, manualConfirmed]);

  const filteredItems = useMemo(
    () =>
      allItems.filter((it) => {
        if (statusFilter !== 'all' && it.reviewState !== statusFilter) return false;
        if (kindFilter !== 'all' && it.kind !== kindFilter) return false;
        return true;
      }),
    [allItems, statusFilter, kindFilter],
  );

  const selected = useMemo(
    () => filteredItems.find((i) => i.key === selectedKey) ?? filteredItems[0] ?? allItems[0] ?? null,
    [filteredItems, allItems, selectedKey],
  );

  // Fetch metering sources & peer spaces for the selected opportunity clue.
  useEffect(() => {
    if (!selected) return;
    const ctrl = new AbortController();
    const qsCls = new URLSearchParams({ building: selected.buildingId, energy: selected.energy });
    const qsSp = new URLSearchParams({ view: 'space', energy: selected.energy });
    if (date) {
      qsCls.set('date', date);
      qsSp.set('date', date);
    }
    Promise.all([
      liveGet<ClassificationData>(`/classification?${qsCls}`, token, ctrl.signal).catch(() => ({ data: null })),
      liveGet<DimensionsData>(`/dimensions?${qsSp}`, token, ctrl.signal).catch(() => ({ data: null })),
    ]).then(([clsRes, spRes]) => {
      if (ctrl.signal.aborted) return;
      const spData = spRes.data ?? null;
      setPeerSpaces(spData);
      const matchedSpace = (spData?.rows ?? []).find((r) => r.id === selected.objectId);
      setClueSources(matchedSpace?.sources?.length ? matchedSpace.sources : clsRes.data?.sources ?? []);
    });
    return () => ctrl.abort();
  }, [selected?.key, selected?.buildingId, selected?.objectId, selected?.energy, date, token]);

  const pendingCount = allItems.filter((i) => i.reviewState === 'pending_manual_review').length;
  const confirmedCount = allItems.filter((i) => i.reviewState === 'confirmed').length;
  const insufficientCount = allItems.filter((i) => i.reviewState === 'insufficient').length;

  const kindLabels: Record<string, string> = {
    baseline_increase: '同星期基线异常增长',
    high_usage_observation: '同建筑高耗能区域对照',
    insufficient_clue: '无候选 / 样本不足',
  };

  // Build history series for baseline_increase clue (reusing DiscreteSampleCompareChart).
  const selectedHistoryPoints: BucketPoint[] = useMemo(() => {
    if (!selected) return [];
    const hist = [...(selected.anomaly.historyDays ?? [])].sort((a, b) => a.from.localeCompare(b.from));
    if (selected.anomaly.current != null) {
      hist.push({
        from: selected.anomaly.from,
        to: selected.anomaly.to,
        usage: selected.anomaly.current,
        valid: selected.anomaly.coverage.valid,
        expected: selected.anomaly.coverage.expected,
      });
    }
    return hist;
  }, [selected]);

  // Peer spaces belonging to selected building for high_usage_observation visual evidence.
  const sameBldgSpaces = useMemo(() => {
    if (!selected || !peerSpaces) return [];
    const bSpIds = new Set((inventory?.spaces ?? []).filter((s) => s.buildingId === selected.buildingId).map((s) => s.spaceId));
    return (peerSpaces.rows ?? []).filter((r) => bSpIds.has(r.id));
  }, [selected, peerSpaces, inventory?.spaces]);

  return (
    <div className="space-y-4">
      {/* ── 1. Opportunity Governance Banner + 4-Col Clue Status Strip ── */}
      <section aria-label="节能机会线索状态摘要" className="arco-card overflow-hidden">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line bg-fill-1/50 px-5 py-2.5 text-[12px] tabular">
          <div className="flex flex-wrap items-center gap-2">
            <span className="rounded-[2px] bg-warning-soft px-2 py-0.5 text-[11px] font-medium text-warning">
              人工核查门禁 · 异常候选 ≠ 已确认浪费
            </span>
            <span className="text-muted">
              当前能源：{energyLabels[data.energy] ?? data.energy}（{data.unit}） · 仅在有明确基线依据时展示参考差额，无改造模型前不生成节能量或收益
            </span>
          </div>
          <button
            type="button"
            data-opp-goto-f18
            onClick={() => onNavigateRetrofit(selected?.buildingId ?? data.buildingId, data.energy)}
            className="inline-flex h-7 items-center gap-1 rounded-[2px] border border-primary/40 bg-primary-soft px-3 text-[12px] font-medium text-primary hover:bg-primary-soft/80"
          >
            <span>进入 F18 改造项目管理</span>
            <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
          </button>
        </div>

        <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-1.5 px-5 py-2.5 text-[12px] tabular">
          <div className="flex flex-wrap items-baseline gap-x-6 gap-y-1.5">
            <div className="flex items-baseline gap-1.5">
              <span className="text-muted">已识别线索</span>
              <strong className="text-[15px] font-semibold text-fg">{pendingCount + confirmedCount}</strong>
              <span className="text-[11px] text-muted">条（覆盖 {portfolioOpps.length || 1} 栋建筑）</span>
            </div>
            <span className="hidden h-3.5 w-px bg-line sm:inline-block" aria-hidden="true" />
            <div className="flex items-baseline gap-1.5">
              <span className="text-muted">待人工核查</span>
              <strong className="text-[15px] font-semibold text-warning">{pendingCount}</strong>
            </div>
            <span className="hidden h-3.5 w-px bg-line sm:inline-block" aria-hidden="true" />
            <div className="flex items-baseline gap-1.5">
              <span className="text-muted">已人工确认</span>
              <strong className="text-[15px] font-semibold text-positive">{confirmedCount}</strong>
            </div>
            <span className="hidden h-3.5 w-px bg-line sm:inline-block" aria-hidden="true" />
            <div className="flex items-baseline gap-1.5">
              <span className="text-muted">无候选/数据不足</span>
              <strong className="text-[15px] font-semibold text-fg-2">{insufficientCount}</strong>
            </div>
          </div>
          <span className="text-[11px] text-muted">确认后在 F18 手动立项，不满足门槛不伪造潜力</span>
        </div>
      </section>

      {/* ── 2. Primary Stage: Filterable Clue Queue + Visual Evidence (8 cols) + Review & Source Drilldown (4 cols) ── */}
      <div className="grid items-start gap-4 lg:grid-cols-12">
        <section className="arco-card space-y-4 p-5 lg:col-span-8">
          {/* Filters */}
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex flex-wrap items-center gap-1.5 text-[12px]" role="group" aria-label="按核查状态筛选机会线索">
              {(
                [
                  { id: 'all', label: `全部 (${allItems.length})` },
                  { id: 'pending_manual_review', label: `待核查 (${pendingCount})` },
                  { id: 'confirmed', label: `已人工确认 (${confirmedCount})` },
                  { id: 'insufficient', label: `无候选/数据不足 (${insufficientCount})` },
                ] as const
              ).map((f) => (
                <button
                  key={f.id}
                  type="button"
                  data-opp-filter={f.id}
                  onClick={() => setStatusFilter(f.id)}
                  className={`rounded-[2px] border px-2.5 py-1 transition-colors ${
                    statusFilter === f.id
                      ? 'border-primary bg-primary-soft font-medium text-primary'
                      : 'border-line bg-surface text-fg-2 hover:bg-fill-1'
                  }`}
                >
                  {f.label}
                </button>
              ))}
            </div>

            <select
              aria-label="按机会线索类型筛选"
              value={kindFilter}
              onChange={(e) => setKindFilter(e.target.value as typeof kindFilter)}
              className="arco-input !h-7 text-[12px]"
            >
              <option value="all">全部线索类型</option>
              <option value="baseline_increase">基线异常增长</option>
              <option value="high_usage_observation">同口径高耗能对照</option>
            </select>
          </div>

          {/* Clue List */}
          <div className="space-y-2 tabular">
            {filteredItems.length === 0 ? (
              <div className="rounded-[4px] border border-dashed border-line bg-fill-1/40 p-6 text-center text-[12px] text-muted">
                当前筛选条件下暂无匹配的节能机会线索。
              </div>
            ) : (
              filteredItems.map((it) => {
                const isSel = selected?.key === it.key;
                const badgeClass =
                  it.reviewState === 'confirmed'
                    ? 'bg-positive-soft text-positive'
                    : it.reviewState === 'pending_manual_review'
                      ? 'bg-warning-soft text-warning'
                      : 'bg-fill-2 text-fg-2';
                const badgeText =
                  it.reviewState === 'confirmed'
                    ? '已人工确认'
                    : it.reviewState === 'pending_manual_review'
                      ? '待人工核查'
                      : '无候选/数据不足';
                return (
                  <button
                    key={it.key}
                    type="button"
                    data-opp-item={it.objectId}
                    onClick={() => {
                      setSelectedKey(it.key);
                      if (it.buildingId !== data.buildingId) onSelectBuilding(it.buildingId);
                    }}
                    className={`w-full rounded-[4px] border p-3.5 text-left transition-colors ${
                      isSel
                        ? 'border-primary/50 bg-primary-soft/35'
                        : 'border-line bg-surface hover:bg-fill-1/60'
                    }`}
                  >
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="text-[14px] font-medium text-fg">{it.objectName}</span>
                        <span className="rounded-[2px] bg-fill-2 px-1.5 py-0 text-[10px] text-fg-2">
                          {it.buildingName} · {energyLabels[it.energy] ?? it.energy}
                        </span>
                        <span className="rounded-[2px] border border-line bg-surface px-1.5 py-0 text-[10px] text-muted">
                          {kindLabels[it.kind] ?? it.kind}
                        </span>
                      </div>
                      <span className={`rounded-[2px] px-2 py-0.5 text-[11px] font-medium ${badgeClass}`}>
                        {badgeText}
                      </span>
                    </div>

                    <p className="mt-1.5 text-[12px] text-fg-2">{it.evidence}</p>

                    <div className="mt-2 flex flex-wrap items-center justify-between gap-2 text-[11px] text-muted">
                      <span>
                        观测实测 <strong className="font-semibold text-fg">{fmtNum(it.usage)} {it.unit}</strong>
                        {it.baseline != null
                          ? ` · 同星期基线 ${fmtNum(it.baseline)} ${it.unit}（参考差额 ${it.delta != null && it.delta > 0 ? '+' : ''}${fmtNum(it.delta)} ${it.unit} / ${it.changeRate != null ? `${it.changeRate > 0 ? '+' : ''}${fmtPct(it.changeRate)}` : '—'}）`
                          : it.comparablePeers > 0
                            ? ` · 同期可比对照区域 ${it.comparablePeers} 个（无基线模型，不估算节能量）`
                            : ' · 暂无参考超额差值'}
                      </span>
                      <span>覆盖率 {fmtPct(it.coverageRate)}</span>
                    </div>
                  </button>
                );
              })
            )}
          </div>

          {/* Visual Evidence Chart for Selected Opportunity Clue */}
          {selected && (
            <div className="border-t border-line pt-4">
              <div className="mb-2.5 flex flex-wrap items-baseline justify-between gap-2">
                <h3 className="text-[14px] font-medium text-fg">
                  {selected.kind === 'high_usage_observation'
                    ? `同建筑区域对照证据（${selected.buildingName}）`
                    : `历史同星期基线对照证据（${selected.objectName}）`}
                </h3>
                <span className="text-[11px] text-muted">
                  数据覆盖率 {fmtPct(selected.coverageRate)} · 规则 {selected.anomaly.ruleVersion || 'f10-f17-v1'}
                </span>
              </div>

              {selected.kind === 'high_usage_observation' && sameBldgSpaces.length > 0 ? (
                <HorizontalRankingChart
                  unit={selected.unit}
                  selectedId={selected.objectId}
                  items={sameBldgSpaces.map((r) => ({
                    id: r.id,
                    name: r.name,
                    usage: r.usage,
                    rank: r.rank,
                    coverageRate: r.coverage.rate,
                    statusText: r.reason || (statusLabels[r.status] ?? r.status),
                  }))}
                />
              ) : (
                <DiscreteSampleCompareChart
                  points={selectedHistoryPoints}
                  unit={selected.unit}
                  baseline={selected.baseline}
                  baselineLabel="同星期中位基线"
                  highlightIndex={selected.reviewState !== 'insufficient' ? selectedHistoryPoints.length - 1 : null}
                  highlightLabel="异常候选观测日"
                />
              )}
            </div>
          )}
        </section>

        {/* Right 4 Cols: Selected Opportunity Review, Potential Scope & Metering Source Drilldown */}
        <aside aria-label="选中机会核查与来源下钻" className="arco-card space-y-4 p-5 text-[12px] tabular lg:col-span-4">
          {selected ? (
            <>
              <div className="border-b border-line pb-3">
                <span className="text-[11px] text-muted">选中机会线索详情</span>
                <h3 className="mt-0.5 text-[15px] font-medium text-fg">{selected.objectName}</h3>
                <p className="mt-0.5 text-[11px] text-muted">
                  {selected.buildingName} · {kindLabels[selected.kind] ?? selected.kind}
                </p>
              </div>

              {/* Potential Impact Reference (Strict Honesty Guard) */}
              <div className="rounded-[4px] border border-line bg-fill-1/50 p-3 space-y-1.5">
                <div className="flex items-center justify-between text-[11px]">
                  <span className="font-medium text-fg-2">潜在影响参考范围</span>
                  <span className="text-muted">非改造核验节能量</span>
                </div>
                {selected.delta != null && selected.delta > 0 ? (
                  <>
                    <p className="text-[18px] font-semibold text-warning">
                      +{fmtNum(selected.delta)} <span className="text-[11px] font-normal text-muted">{selected.unit}/日（超基线参考差）</span>
                    </p>
                    <p className="text-[11px] leading-relaxed text-muted">
                      仅代表观测日高于同星期基线的实测差额，尚未建立改造工程模型，不直接折算为年度节能收益。
                    </p>
                  </>
                ) : (
                  <p className="text-[11px] leading-relaxed text-muted">
                    当前线索缺少基线模型依据，严禁凭空生成节能量或费用节省预测；需人工核查后在 F18/F19 建立基线与情景模型。
                  </p>
                )}
              </div>

              {/* Human-in-the-loop Confirmation Action */}
              {selected.reviewState !== 'insufficient' && (
                <div className="space-y-2">
                  <button
                    type="button"
                    data-opp-toggle-confirm
                    onClick={() =>
                      setManualConfirmed((prev) => ({ ...prev, [selected.key]: !prev[selected.key] }))
                    }
                    className={`w-full rounded-[4px] px-3 py-2 text-[12px] font-medium transition-colors ${
                      selected.reviewState === 'confirmed'
                        ? 'border border-line bg-surface text-fg-2 hover:bg-fill-1'
                        : 'bg-primary text-white hover:bg-primary/90'
                    }`}
                  >
                    {selected.reviewState === 'confirmed'
                      ? '撤销人工确认（恢复待核查状态）'
                      : '标记为「已人工确认线索」'}
                  </button>
                  <button
                    type="button"
                    onClick={() => onNavigateRetrofit(selected.buildingId, selected.energy)}
                    className="w-full rounded-[4px] border border-primary/40 bg-primary-soft/40 px-3 py-1.5 text-[12px] font-medium text-primary hover:bg-primary-soft"
                  >
                    关联前往 F18 评估立项（不自动创建项目）
                  </button>
                </div>
              )}

              {/* Limitations & Verification Checklist */}
              <div className="space-y-2.5 border-t border-line pt-3 text-[11px]">
                <div>
                  <h4 className="mb-1 font-medium text-fg-2">数据局限与口径边界</h4>
                  <ul className="list-disc space-y-1 pl-4 text-muted">
                    {selected.limitations.map((lim, i) => (
                      <li key={i}>{lim}</li>
                    ))}
                  </ul>
                </div>
                <div>
                  <h4 className="mb-1 font-medium text-fg-2">建议人工核查步骤</h4>
                  <ul className="list-disc space-y-1 pl-4 text-fg-2">
                    {selected.confirm.map((step, i) => (
                      <li key={i}>{step}</li>
                    ))}
                  </ul>
                </div>
              </div>

              {/* Linked Metering Sources */}
              <div className="border-t border-line pt-3">
                <div className="mb-1.5 flex items-center justify-between text-[11px] text-muted">
                  <span className="font-medium text-fg-2">关联计量来源（{clueSources.length} 块）</span>
                  <span>{selected.objectId}</span>
                </div>
                {clueSources.length === 0 ? (
                  <p className="text-[11px] text-muted">读取关联表具中…</p>
                ) : (
                  <ul className="divide-y divide-line rounded-[4px] border border-line text-[11px]">
                    {clueSources.slice(0, 5).map((s) => (
                      <li key={`${s.terminalId}-${s.itemCode}`} className="flex items-center justify-between gap-2 px-2.5 py-1.5">
                        <span className="min-w-0 truncate">
                          <span className="font-medium text-fg">{s.terminalName}</span>
                          <span className="ml-1.5 font-mono text-[10px] text-muted">{s.terminalId}</span>
                        </span>
                        <span className="shrink-0 font-semibold text-fg">
                          {fmtNum(s.usage)} {s.unit}
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            </>
          ) : null}
        </aside>
      </div>
    </div>
  );
}

// ── Collapsible Shared Source Trace Evidence ────────────────────────────────

function SourceTraceTable({ sources }: { sources: SourceTrace[] }) {
  if (!sources.length) return null;
  return (
    <CollapsibleSection
      title="计量来源与溯源证据"
      badge={`${sources.length} 块表`}
      subtitle="按需展开查看终端编码、倍率与口径版本"
    >
      <div className="divide-y divide-line md:hidden">
        {sources.map((s) => (
          <div key={`${s.terminalId}:${s.itemCode}`} className="space-y-1 px-4 py-3 text-xs tabular">
            <div className="flex items-center justify-between gap-2">
              <span className="font-medium text-fg">{s.terminalName}</span>
              <span className="font-semibold text-fg">{fmtNum(s.usage)} {s.unit}</span>
            </div>
            <div className="flex flex-wrap items-center justify-between gap-2 text-muted">
              <span>{s.terminalId} / {s.itemCode}</span>
              <span>{s.sourceTable} · {s.multiplier}× · g{s.mappingGeneration} · {s.valid}/{s.expected}</span>
            </div>
          </div>
        ))}
      </div>
      <div className="hidden overflow-x-auto md:block">
        <table className="w-full text-left text-xs">
          <thead className="border-b border-line bg-surface-2 text-muted">
            <tr>
              <th className="px-4 py-2">计量点</th>
              <th className="px-4 py-2">终端 / 项编码</th>
              <th className="px-4 py-2">来源表 / 方式</th>
              <th className="px-4 py-2 text-right">倍率</th>
              <th className="px-4 py-2 text-right">用量</th>
              <th className="px-4 py-2 text-right">有效时段</th>
              <th className="px-4 py-2">口径版本</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line tabular">
            {sources.map((s) => (
              <tr key={`${s.terminalId}:${s.itemCode}`}>
                <td className="px-4 py-2 font-medium text-fg">{s.terminalName}</td>
                <td className="px-4 py-2 text-muted">{s.terminalId} / {s.itemCode}</td>
                <td className="px-4 py-2 text-muted">{s.sourceTable} · {s.measurementKind}</td>
                <td className="px-4 py-2 text-right text-muted">{s.multiplier}×</td>
                <td className="px-4 py-2 text-right font-medium text-fg">{fmtNum(s.usage)} {s.unit}</td>
                <td className="px-4 py-2 text-right text-muted">{s.valid}/{s.expected}</td>
                <td className="px-4 py-2 text-muted">g{s.mappingGeneration} / {s.profileVersion}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </CollapsibleSection>
  );
}
