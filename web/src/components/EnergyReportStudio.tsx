import { useEffect, useMemo, useRef, useState } from 'react';
import {
  AlertTriangle,
  ArrowUpDown,
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  Download,
  FileSpreadsheet,
  Printer,
  ShieldAlert,
  SlidersHorizontal,
} from 'lucide-react';
import {
  downloadLiveCsv,
  liveGet,
  type InventoryData,
  type LiveReportData,
  type ReportColumn,
  type SourceTrace,
} from '../api/energyLive';
import { CHART_PALETTE } from './EnergyCharts';
import { fmtShanghaiDate } from './EnergyWorkspaceChrome';

export type ReportSection =
  | 'report-consumption'
  | 'report-metrics'
  | 'report-assessment'
  | 'report-retrofit'
  | 'report-custom';

const energyLabels: Record<string, string> = {
  electricity: '用电',
  water: '用水',
  gas: '燃气',
  heat: '供热',
  cooling: '供冷',
  steam: '蒸汽',
};

const statusBadges: Record<string, { label: string; cls: string }> = {
  ready: { label: '可计算', cls: 'bg-positive-soft text-positive' },
  missing_denominator: { label: '缺少业务分母', cls: 'bg-warning-soft text-warning' },
  invalid_denominator: { label: '分母无效', cls: 'bg-warning-soft text-warning' },
  denominator_out_of_period: { label: '分母周期不匹配', cls: 'bg-warning-soft text-warning' },
  within_threshold: { label: '低于阈值上限', cls: 'bg-positive-soft text-positive' },
  exceeds_threshold: { label: '超出阈值上限', cls: 'bg-negative-soft text-negative' },
  no_standard: { label: '未配置定额', cls: 'bg-fill-2 text-fg-2' },
  inapplicable: { label: '标准不适用', cls: 'bg-fill-2 text-fg-2' },
  not_comparable: { label: '不可对标', cls: 'bg-warning-soft text-warning' },
  demo: { label: '演示标准（非正式）', cls: 'bg-warning-soft text-warning' },
  internal: { label: '内部目标', cls: 'bg-primary-soft text-primary' },
  external: { label: '外部权威标准', cls: 'bg-positive-soft text-positive' },
  confirmed: { label: '已确认', cls: 'bg-positive-soft text-positive' },
  pending_review: { label: '待复核（非最终成绩）', cls: 'bg-warning-soft text-warning' },
  appealed: { label: '复核申诉中', cls: 'bg-negative-soft text-negative' },
  blocked_low_quality: { label: '低质量阻断', cls: 'bg-negative-soft text-negative' },
  verified_simulated: { label: '模拟核验完成', cls: 'bg-positive-soft text-positive' },
  pending_verification: { label: '待核验（未达门禁）', cls: 'bg-warning-soft text-warning' },
  estimated_only: { label: '仅模型估算', cls: 'bg-fill-2 text-fg-2' },
};

const NUMERIC_KEYS = new Set([
  'usage',
  'share',
  'coverage_rate',
  'carbon_kgco2e',
  'numerator_usage',
  'denominator_value',
  'metric_value',
  'benchmark_max',
  'target_value',
  'actual_usage',
  'deviation',
  'weight_pct',
  'public_share_pct',
  'score',
  'weighted_score',
  'est_savings_base',
  'static_payback_years',
  'adjusted_baseline_use',
  'report_actual_usage',
  'report_coverage_rate',
  'verified_savings',
  'verified_savings_rate',
  'verified_carbon_kgco2e',
]);

const STATUS_KEYS = new Set([
  'metric_status',
  'benchmark_status',
  'benchmark_kind',
  'review_status',
  'mv_status',
]);

function parseNum(v: string | undefined): number | null {
  if (!v) return null;
  const cleaned = v.replace(/%$/, '').replace(/,/g, '').trim();
  if (!cleaned) return null;
  const n = Number(cleaned);
  return Number.isFinite(n) ? n : null;
}

const numFmt = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 3 });

function fmtNum(v: number | null | undefined) {
  return v == null ? '—' : numFmt.format(v);
}

type AssessmentGroup = 'all' | 'excellent' | 'qualified' | 'rectify' | 'exempt' | 'blocked';

function classifyAssessmentRow(row: Record<string, string>): Exclude<AssessmentGroup, 'all'> {
  if (row.review_status === 'blocked_low_quality') return 'blocked';
  const sc = parseNum(row.score);
  if (sc == null || (row.review_note && row.review_note.includes('豁免'))) return 'exempt';
  if (sc >= 90) return 'excellent';
  if (sc >= 70) return 'qualified';
  return 'rectify';
}

const assessmentGroupLabels: Record<Exclude<AssessmentGroup, 'all'>, { label: string; cls: string; dot: string }> = {
  excellent: { label: '优秀 (≥90)', cls: 'bg-positive-soft text-positive', dot: '#00B42A' },
  qualified: { label: '达标 (70–89)', cls: 'bg-primary-soft text-primary', dot: '#165DFF' },
  rectify: { label: '待整改 (<70)', cls: 'bg-negative-soft text-negative', dot: '#F53F3F' },
  exempt: { label: '豁免对象', cls: 'bg-fill-2 text-fg-2', dot: '#86909C' },
  blocked: { label: '低质量阻断', cls: 'bg-warning-soft text-warning', dot: '#FF7D00' },
};

type CompositionDrillPayload = {
  energies?: Array<{
    energy: string;
    unit: string;
    sources?: SourceTrace[];
  }>;
};

export function EnergyReportStudio({
  section,
  data,
  endpoint,
  token,
  inventory,
  date,
  scope,
  onScopeChange,
  onFieldsChange,
}: {
  section: ReportSection;
  data: LiveReportData;
  endpoint: string;
  token: string;
  inventory: InventoryData | null;
  date: string;
  scope: string;
  onScopeChange?: (nextScope: string) => void;
  onFieldsChange?: (fields: string[]) => void;
}) {
  const [csvErr, setCsvErr] = useState<string | null>(null);
  const [downloading, setDownloading] = useState<boolean>(false);
  const [sortKey, setSortKey] = useState<string | null>(null);
  const [sortAsc, setSortAsc] = useState<boolean>(false);
  const [pageIdx, setPageIdx] = useState<number>(0);
  const [pageSize, setPageSize] = useState<number>(20);

  // F21 energy view tab & category -> metering source drilldown
  const [f21EnergyTab, setF21EnergyTab] = useState<string>('all');
  const [f21DrillCat, setF21DrillCat] = useState<{ energy: string; categoryId: string; categoryName: string } | null>(null);
  const [f21CompSources, setF21CompSources] = useState<Record<string, SourceTrace[]>>({});

  // F23 assessment grouping filter
  const [f23Group, setF23Group] = useState<AssessmentGroup>('all');

  // F24 verification status filter + per-project baseline/report M&V period lookup
  const [f24StatusFilter, setF24StatusFilter] = useState<string>('all');
  const [f24Periods, setF24Periods] = useState<
    Record<string, { baselineFrom: string; baselineTo: string; reportFrom: string; reportTo: string }>
  >({});

  // Wide-table horizontal scroll affordance (F24 / F22 / F25)
  const tableWrapRef = useRef<HTMLDivElement | null>(null);
  const [canScrollH, setCanScrollH] = useState<boolean>(false);

  useEffect(() => {
    setPageIdx(0);
    setSortKey(null);
    setF21EnergyTab('all');
    setF21DrillCat(null);
    setF23Group('all');
    setF24StatusFilter('all');
    setCsvErr(null);
  }, [section, data.buildingId, data.energy, scope]);

  // F24: Fetch exact per-project baseline & M&V verification windows so they are never confused with report query date.
  useEffect(() => {
    if (section !== 'report-retrofit') {
      setF24Periods({});
      return;
    }
    const ctrl = new AbortController();
    liveGet<{
      projects?: Array<{
        retrofitId: string;
        baselineFrom: string;
        baselineTo: string;
        reportFrom: string;
        reportTo: string;
      }>;
    }>('/retrofit/verification', token, ctrl.signal)
      .then((res) => {
        if (ctrl.signal.aborted) return;
        const map: Record<string, { baselineFrom: string; baselineTo: string; reportFrom: string; reportTo: string }> = {};
        for (const p of res.data?.projects ?? []) {
          map[p.retrofitId] = {
            baselineFrom: fmtShanghaiDate(p.baselineFrom),
            baselineTo: fmtShanghaiDate(p.baselineTo),
            reportFrom: fmtShanghaiDate(p.reportFrom),
            reportTo: fmtShanghaiDate(p.reportTo),
          };
        }
        setF24Periods(map);
      })
      .catch(() => {
        if (!ctrl.signal.aborted) setF24Periods({});
      });
    return () => ctrl.abort();
  }, [section, token]);

  useEffect(() => {
    const el = tableWrapRef.current;
    if (!el) return;
    const measure = () => setCanScrollH(el.scrollWidth - el.clientWidth > 8);
    measure();
    window.addEventListener('resize', measure);
    return () => window.removeEventListener('resize', measure);
  }, [data.columns, data.rows, section, scope]);

  // F21: Fetch real metering sources from /composition so clicking any category row drills down to exact meters.
  useEffect(() => {
    if (section !== 'report-consumption' || !data.buildingId) {
      setF21CompSources({});
      return;
    }
    const ctrl = new AbortController();
    const qs = new URLSearchParams({ building: data.buildingId });
    if (date) qs.set('date', date);
    liveGet<CompositionDrillPayload>(`/composition?${qs}`, token, ctrl.signal)
      .then((res) => {
        if (ctrl.signal.aborted) return;
        const map: Record<string, SourceTrace[]> = {};
        for (const e of res.data?.energies ?? []) {
          map[e.energy] = e.sources ?? [];
        }
        setF21CompSources(map);
      })
      .catch(() => {
        if (!ctrl.signal.aborted) setF21CompSources({});
      });
    return () => ctrl.abort();
  }, [section, data.buildingId, date, token]);

  const rawRows = data.rows ?? [];
  const columns = data.columns ?? [];
  const allowedFields = data.allowedFields ?? [];
  const allowedFieldKeys = useMemo(() => new Set(allowedFields.map((f) => f.key)), [allowedFields]);
  const selectedFields = useMemo(() => {
    const base = data.selectedFields ?? columns.map((c) => c.key);
    if (section !== 'report-custom' || allowedFieldKeys.size === 0) return base;
    const valid = base.filter((k) => allowedFieldKeys.has(k));
    return valid.length > 0 ? valid : allowedFields.map((f) => f.key);
  }, [data.selectedFields, columns, section, allowedFieldKeys, allowedFields]);

  // Guarantee CSV export endpoint on F25 strictly carries the sanitized selectedFields & scope (never stale columns).
  const effectiveExportEndpoint = useMemo(() => {
    if (section !== 'report-custom') return endpoint;
    const [basePath, qsRaw = ''] = endpoint.split('?');
    const qs = new URLSearchParams(qsRaw);
    qs.set('scope', scope);
    if (selectedFields.length > 0) qs.set('fields', selectedFields.join(','));
    else qs.delete('fields');
    return `${basePath}?${qs.toString()}`;
  }, [section, endpoint, scope, selectedFields]);

  // Map category bindings for F21 source drilldown
  const bindingMap = useMemo(() => {
    const map = new Map<string, Array<{ categoryId: string; weightPpm: number }>>();
    for (const b of inventory?.categoryBindings ?? []) {
      map.set(`${b.terminalId}::${b.itemCode}`, b.allocations ?? []);
    }
    return map;
  }, [inventory?.categoryBindings]);

  // ── F21 Summaries (Strictly Separate Native Physical Units — Never Summed Across Energies!) ──
  const f21EnergySummaries = useMemo(() => {
    if (section !== 'report-consumption' && !(section === 'report-custom' && scope === 'consumption')) return [];
    const order = ['electricity', 'water', 'gas', 'heat', 'cooling', 'steam'];
    const byEnergy = new Map<
      string,
      {
        energy: string;
        unit: string;
        totalRow: Record<string, string> | null;
        subRows: Array<Record<string, string>>;
      }
    >();
    for (const r of rawRows) {
      const en = r.energy || 'unknown';
      const entry = byEnergy.get(en) ?? { energy: en, unit: r.unit || '', totalRow: null, subRows: [] };
      if (!entry.unit && r.unit) entry.unit = r.unit;
      if (r.category_id === 'TOTAL') entry.totalRow = r;
      else entry.subRows.push(r);
      byEnergy.set(en, entry);
    }
    return Array.from(byEnergy.values()).sort(
      (a, b) => (order.indexOf(a.energy) === -1 ? 99 : order.indexOf(a.energy)) - (order.indexOf(b.energy) === -1 ? 99 : order.indexOf(b.energy)),
    );
  }, [section, scope, rawRows]);

  const f21DrilledSources = useMemo(() => {
    if (!f21DrillCat) return [];
    const list = f21CompSources[f21DrillCat.energy] ?? [];
    if (f21DrillCat.categoryId === 'TOTAL') {
      return list.map((s) => ({ source: s, weightPct: 100 }));
    }
    if (f21DrillCat.categoryId === 'UNCLASSIFIED') {
      return list
        .map((s) => {
          const allocs = bindingMap.get(`${s.terminalId}::${s.itemCode}`) ?? [];
          const sumPpm = allocs.reduce((a, x) => a + x.weightPpm, 0);
          return { source: s, weightPct: Math.max(0, (1_000_000 - sumPpm) / 10_000) };
        })
        .filter((x) => x.weightPct > 0);
    }
    return list
      .map((s) => {
        const allocs = bindingMap.get(`${s.terminalId}::${s.itemCode}`) ?? [];
        const hit = allocs.find((a) => a.categoryId === f21DrillCat.categoryId);
        return hit ? { source: s, weightPct: hit.weightPpm / 10_000 } : null;
      })
      .filter((x): x is { source: SourceTrace; weightPct: number } => x != null);
  }, [f21DrillCat, f21CompSources, bindingMap]);

  // ── F22 Metrics Summary & Rule Provenance ──
  const f22Summary = useMemo(() => {
    if (section !== 'report-metrics') return null;
    const ready = rawRows.filter((r) => r.metric_status === 'ready').length;
    const missingDenom = rawRows.filter((r) => r.metric_status !== 'ready').length;
    const within = rawRows.filter((r) => r.benchmark_status === 'within_threshold').length;
    const exceeds = rawRows.filter((r) => r.benchmark_status === 'exceeds_threshold').length;
    const uncomparable = rawRows.filter(
      (r) => r.benchmark_status !== 'within_threshold' && r.benchmark_status !== 'exceeds_threshold',
    ).length;
    const hasDemo = rawRows.some((r) => r.benchmark_kind === 'demo');
    const matchedRules = (inventory?.benchmarks ?? []).filter((b) =>
      rawRows.some((r) => r.metric_id === b.metricId),
    );
    return { ready, missingDenom, within, exceeds, uncomparable, hasDemo, matchedRules };
  }, [section, rawRows, inventory?.benchmarks]);

  // ── F23 Assessment Tier Summary ──
  const f23Summary = useMemo(() => {
    if (section !== 'report-assessment') return null;
    const counts: Record<Exclude<AssessmentGroup, 'all'>, number> = {
      excellent: 0,
      qualified: 0,
      rectify: 0,
      exempt: 0,
      blocked: 0,
    };
    let confirmed = 0;
    let pending = 0;
    let appealed = 0;
    let weightedSum = 0;
    let weightedCount = 0;

    for (const r of rawRows) {
      counts[classifyAssessmentRow(r)] += 1;
      if (r.review_status === 'confirmed') confirmed += 1;
      else if (r.review_status === 'appealed') appealed += 1;
      else if (r.review_status === 'pending_review') pending += 1;
      const ws = parseNum(r.weighted_score);
      if (ws != null) {
        weightedSum += ws;
        weightedCount += 1;
      }
    }
    return { counts, confirmed, pending, appealed, weightedSum, weightedCount };
  }, [section, rawRows]);

  // ── F24 Retrofit & M&V Summary ──
  const f24Summary = useMemo(() => {
    if (section !== 'report-retrofit') return null;
    const verified = rawRows.filter((r) => r.mv_status === 'verified_simulated');
    const pending = rawRows.filter((r) => r.mv_status === 'pending_verification');
    const estimatedOnly = rawRows.filter((r) => r.mv_status === 'estimated_only');
    const totalVerifiedCarbon = verified.reduce((s, r) => s + (parseNum(r.verified_carbon_kgco2e) ?? 0), 0);
    return { verified, pending, estimatedOnly, totalVerifiedCarbon };
  }, [section, rawRows]);

  // ── Filtered + Sorted + Paginated Table Rows (Zero Mutated Calculations!) ──
  const filteredRows = useMemo(() => {
    if (section === 'report-consumption' && f21EnergyTab !== 'all') {
      return rawRows.filter((r) => r.energy === f21EnergyTab);
    }
    if (section === 'report-assessment' && f23Group !== 'all') {
      return rawRows.filter((r) => classifyAssessmentRow(r) === f23Group);
    }
    if (section === 'report-retrofit' && f24StatusFilter !== 'all') {
      return rawRows.filter((r) => r.mv_status === f24StatusFilter);
    }
    return rawRows;
  }, [section, rawRows, f21EnergyTab, f23Group, f24StatusFilter]);

  const sortedRows = useMemo(() => {
    const copy = [...filteredRows];
    if (!sortKey) return copy;
    return copy.sort((a, b) => {
      const av = a[sortKey] ?? '';
      const bv = b[sortKey] ?? '';
      const an = parseNum(av);
      const bn = parseNum(bv);
      const cmp = an != null && bn != null ? an - bn : av.localeCompare(bv, 'zh-CN');
      return sortAsc ? cmp : -cmp;
    });
  }, [filteredRows, sortKey, sortAsc]);

  const totalPages = Math.max(1, Math.ceil(sortedRows.length / pageSize));
  const safePageIdx = Math.min(pageIdx, totalPages - 1);
  const pagedRows = useMemo(
    () => sortedRows.slice(safePageIdx * pageSize, safePageIdx * pageSize + pageSize),
    [sortedRows, safePageIdx, pageSize],
  );

  const handleSort = (key: string) => {
    if (sortKey === key) setSortAsc((v) => !v);
    else {
      setSortKey(key);
      setSortAsc(false);
    }
  };

  const handleDownload = async () => {
    setCsvErr(null);
    setDownloading(true);
    try {
      const err = await downloadLiveCsv(effectiveExportEndpoint, token, `energy-report-${data.reportCode.toLowerCase()}.csv`);
      if (err) setCsvErr(err);
    } finally {
      setDownloading(false);
    }
  };

  const toggleCustomField = (key: string) => {
    if (!onFieldsChange) return;
    const exists = selectedFields.includes(key);
    const next = exists ? selectedFields.filter((k) => k !== key) : [...selectedFields, key];
    if (next.length > 0) onFieldsChange(next);
  };

  const applyCustomPreset = (presetKeys: string[]) => {
    if (!onFieldsChange) return;
    const allowedSet = new Set(allowedFields.map((f) => f.key));
    const filtered = presetKeys.filter((k) => allowedSet.has(k));
    if (filtered.length > 0) onFieldsChange(filtered);
  };

  const formatCell = (col: ReportColumn, row: Record<string, string>) => {
    const raw = row[col.key] ?? '';
    if (!raw) {
      if (col.key === 'metric_value' && row.metric_status === 'missing_denominator') {
        return <span className="text-[11px] text-warning">缺分母不可算</span>;
      }
      if (col.key === 'verified_savings' && row.mv_status && row.mv_status !== 'verified_simulated') {
        return <span className="text-[11px] text-muted">未核验</span>;
      }
      if (col.key === 'score' && row.review_note?.includes('豁免')) {
        return <span className="text-[11px] text-muted">豁免</span>;
      }
      return <span className="text-muted">—</span>;
    }

    if (col.key === 'energy') {
      return <span className="font-medium text-fg">{energyLabels[raw] ?? raw}</span>;
    }

    if (STATUS_KEYS.has(col.key)) {
      const badge = statusBadges[raw];
      if (badge) {
        return (
          <span className={`inline-block rounded-[2px] px-1.5 py-0 text-[11px] font-medium leading-[18px] ${badge.cls}`}>
            {badge.label}
          </span>
        );
      }
    }

    if (col.key === 'category_name' && row.category_id === 'TOTAL') {
      return (
        <span className="inline-flex items-center gap-1.5 font-semibold text-fg">
          <span className="rounded-[2px] bg-primary-soft px-1 py-0 text-[10px] font-medium text-primary">汇总</span>
          <span>{energyLabels[row.energy] ?? row.energy}总计</span>
        </span>
      );
    }

    if (col.key === 'category_name' && row.category_id && row.category_id !== 'TOTAL') {
      return <span className="pl-3 text-fg-2">└ {raw}</span>;
    }

    if (col.key === 'deviation') {
      const n = parseNum(raw);
      return (
        <span className={n != null && n > 0 ? 'font-medium text-negative' : 'font-medium text-positive'}>
          {n != null && n > 0 ? `+${raw}` : raw}
        </span>
      );
    }

    if (col.key === 'window_from' || col.key === 'window_to') {
      return <span className="text-muted">{fmtShanghaiDate(raw)}</span>;
    }

    if (col.key === 'data_nature') {
      return (
        <span
          title={raw}
          className="inline-flex items-center rounded-[2px] bg-warning-soft/75 px-1.5 py-0 text-[10px] font-medium text-warning"
        >
          模拟数据
        </span>
      );
    }

    return raw;
  };

  // Hide raw technical data_nature column inside dense preview ONLY? NO — keep 100% parity with CSV columns,
  // or place a visible compact badge in data_nature so preview columns == CSV columns 1:1!
  const windowRangeText =
    data.from && data.to
      ? `${fmtShanghaiDate(data.from)} ~ ${fmtShanghaiDate(data.to)}`
      : fmtShanghaiDate(data.asOf || data.generatedAt);

  const isFilteredLocally = filteredRows.length !== rawRows.length;

  return (
    <div className="space-y-4">
      {/* ── 1. Formal Report Header, Scope Metadata & Primary CSV Export Bar ── */}
      <section aria-label="报表统计范围与导出操作" className="arco-card overflow-hidden">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line bg-fill-1/50 px-5 py-3 tabular">
          <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1.5 text-[12px]">
            <span className="inline-flex items-center gap-1.5 font-medium text-fg">
              <FileSpreadsheet className="h-4 w-4 text-primary" aria-hidden="true" />
              <span>正式报表预览</span>
            </span>
            <span className="rounded-[2px] bg-warning-soft px-1.5 py-0 text-[11px] font-medium text-warning">
              模拟数据报表（预览与导出均含标注）
            </span>
            <span className="text-muted">
              范围：<strong className="font-medium text-fg-2">{data.buildingName || '全部授权范围'}</strong>
            </span>
            {section === 'report-retrofit' ? (
              <span className="text-muted">
                报表快照：<strong className="font-medium text-fg-2">{fmtShanghaiDate(data.asOf || data.generatedAt)}</strong>
                <span className="ml-1.5 text-[11px]">（各改造项目按独立基线期与 M&amp;V 报告期核算，不与导出日期混同）</span>
              </span>
            ) : (
              <span className="text-muted">
                周期：<strong className="font-medium text-fg-2">{windowRangeText}</strong>（{data.timezone || 'Asia/Shanghai'}）
              </span>
            )}
            <span className="text-muted">
              记录 <strong className="font-medium text-fg">{rawRows.length}</strong> 行 · 字段 <strong className="font-medium text-fg">{columns.length}</strong> 列
            </span>
          </div>

          <div className="flex flex-wrap items-center gap-2">
            {section === 'report-retrofit' && (
              <button
                type="button"
                onClick={() => window.print()}
                className="arco-btn !h-8 text-[12px]"
              >
                <Printer className="h-3.5 w-3.5 text-fg-2" aria-hidden="true" />
                <span>打印预览</span>
              </button>
            )}
            <button
              type="button"
              data-report-export-csv
              disabled={downloading}
              onClick={handleDownload}
              className="inline-flex h-8 items-center gap-1.5 rounded-[2px] bg-primary px-3.5 text-[12px] font-medium text-white transition-colors hover:bg-primary/90 disabled:opacity-50"
            >
              <Download className="h-3.5 w-3.5" aria-hidden="true" />
              <span>{downloading ? '导出中…' : '导出 CSV（与预览逐列一致）'}</span>
            </button>
          </div>
        </div>

        {csvErr && (
          <div role="alert" className="border-b border-negative/30 bg-negative-soft px-5 py-2.5 text-[12px] text-negative">
            {csvErr}
          </div>
        )}

        {/* ── 2. Tailored Core Summary & Necessary Compact Visual per Report (F21–F25) ── */}

        {/* F21: 5-Energy Independent Out-of-Table Switcher & Composition Bar */}
        {section === 'report-consumption' && f21EnergySummaries.length > 0 && (
          <div className="space-y-4 p-5 tabular">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="flex flex-wrap items-center gap-1.5 text-[12px]" role="tablist" aria-label="按能源分别出表预览">
                <button
                  type="button"
                  role="tab"
                  aria-selected={f21EnergyTab === 'all'}
                  data-f21-energy="all"
                  onClick={() => {
                    setF21EnergyTab('all');
                    setPageIdx(0);
                  }}
                  className={`rounded-[2px] border px-2.5 py-1 transition-colors ${
                    f21EnergyTab === 'all'
                      ? 'border-primary bg-primary font-medium text-white'
                      : 'border-line bg-surface text-fg-2 hover:bg-fill-1'
                  }`}
                >
                  全部五能源分列 ({rawRows.length} 行)
                </button>
                {f21EnergySummaries.map((es) => (
                  <button
                    key={es.energy}
                    type="button"
                    role="tab"
                    aria-selected={f21EnergyTab === es.energy}
                    data-f21-energy={es.energy}
                    onClick={() => {
                      setF21EnergyTab(es.energy);
                      setPageIdx(0);
                    }}
                    className={`rounded-[2px] border px-2.5 py-1 transition-colors ${
                      f21EnergyTab === es.energy
                        ? 'border-primary bg-primary font-medium text-white'
                        : 'border-line bg-surface text-fg-2 hover:bg-fill-1'
                    }`}
                  >
                    {energyLabels[es.energy] ?? es.energy} ({es.unit})
                  </button>
                ))}
              </div>
              <span className="text-[11px] text-muted">
                父汇总行（TOTAL）与子分项行已视觉分离，严禁跨物理单位相加或父子重复累计
              </span>
            </div>

            {/* 5-Energy Native-Unit Summary Cards */}
            <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
              {f21EnergySummaries.map((es) => {
                const isSel = f21EnergyTab === es.energy;
                const totalUsage = es.totalRow?.usage ?? '—';
                const cov = es.totalRow?.coverage_rate ?? '—';
                const topSub = [...es.subRows].sort((a, b) => (parseNum(b.usage) ?? 0) - (parseNum(a.usage) ?? 0))[0];
                return (
                  <button
                    key={es.energy}
                    type="button"
                    onClick={() => setF21EnergyTab((cur) => (cur === es.energy ? 'all' : es.energy))}
                    className={`rounded-[4px] border p-3.5 text-left transition-colors ${
                      isSel ? 'border-primary/50 bg-primary-soft/30' : 'border-line bg-surface hover:bg-fill-1/60'
                    }`}
                  >
                    <div className="flex items-center justify-between text-[12px]">
                      <span className="font-medium text-fg-2">{energyLabels[es.energy] ?? es.energy}</span>
                      <span className="rounded-[2px] bg-positive-soft px-1.5 py-0 text-[10px] text-positive">{cov}</span>
                    </div>
                    <p className="mt-1.5 text-[20px] font-semibold leading-[26px] text-fg">
                      {totalUsage} <span className="text-[11px] font-normal text-muted">{es.unit}</span>
                    </p>
                    {/* Proportional native-unit micro stacked bar */}
                    <div className="mt-2 flex h-[6px] w-full overflow-hidden rounded-[2px] bg-fill-2">
                      {es.subRows.map((sr, si) => {
                        const pct = parseNum(sr.share) ?? 0;
                        return (
                          <div
                            key={sr.category_id}
                            title={`${sr.category_name}: ${sr.usage} ${sr.unit} (${sr.share})`}
                            style={{
                              width: `${Math.max(2, pct)}%`,
                              backgroundColor:
                                sr.category_id === 'UNCLASSIFIED'
                                  ? '#C9CDD4'
                                  : CHART_PALETTE[si % (CHART_PALETTE.length - 1)],
                            }}
                          />
                        );
                      })}
                    </div>
                    <p className="mt-1.5 truncate text-[11px] text-muted">
                      {topSub ? `主力：${topSub.category_name} (${topSub.share})` : `${es.subRows.length} 个分项`}
                    </p>
                  </button>
                );
              })}
            </div>

            {/* On-demand Category -> Metering Source Drilldown Drawer (when user clicks a category row in table) */}
            {f21DrillCat && (
              <div className="rounded-[4px] border border-primary/35 bg-primary-soft/20 p-4 text-[12px]">
                <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
                  <div>
                    <span className="font-medium text-fg">
                      分类计量来源下钻：{energyLabels[f21DrillCat.energy] ?? f21DrillCat.energy} · {f21DrillCat.categoryName}
                    </span>
                    <span className="ml-2 font-mono text-[11px] text-muted">({f21DrillCat.categoryId})</span>
                  </div>
                  <button
                    type="button"
                    onClick={() => setF21DrillCat(null)}
                    className="rounded-[2px] border border-line bg-surface px-2 py-0.5 text-[11px] text-fg-2 hover:bg-fill-1"
                  >
                    收起下钻
                  </button>
                </div>
                {f21DrilledSources.length === 0 ? (
                  <p className="text-[11px] text-muted">当前分类暂无匹配的已绑定计量点，请在配置中心核查分类权重绑定。</p>
                ) : (
                  <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
                    {f21DrilledSources.map(({ source, weightPct }) => (
                      <div key={`${source.terminalId}-${source.itemCode}`} className="rounded-[4px] border border-line bg-surface px-3 py-2 text-[11px]">
                        <div className="truncate font-medium text-fg">{source.terminalName}</div>
                        <div className="mt-0.5 flex items-center justify-between text-muted">
                          <span className="font-mono">{source.terminalId}</span>
                          <span className="font-semibold text-fg">
                            {fmtNum(source.usage)} {source.unit}
                          </span>
                        </div>
                        <div className="mt-0.5 flex items-center justify-between text-[10px] text-muted">
                          <span>权重 {weightPct.toFixed(0)}% · {source.sourceTable}</span>
                          <span>有效 {source.valid}/{source.expected}</span>
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            )}
          </div>
        )}

        {/* F22: Metrics & Quota Report Summary + Compact Inline Benchmarking Visual */}
        {section === 'report-metrics' && f22Summary && (
          <div className="space-y-4 p-5 tabular">
            <div className="grid divide-y divide-line rounded-[4px] border border-line sm:grid-cols-2 lg:grid-cols-4 lg:divide-y-0 lg:divide-x">
              <div className="p-3.5">
                <p className="text-[12px] text-fg-2">已计算指标 / 缺失分母</p>
                <p className="mt-1 text-[22px] font-semibold text-fg">
                  {f22Summary.ready}
                  <span className="mx-1 text-[13px] font-normal text-muted">/</span>
                  <span className={f22Summary.missingDenom > 0 ? 'text-warning' : 'text-fg-2'}>{f22Summary.missingDenom}</span>
                </p>
                <p className="mt-0.5 text-[11px] text-muted">缺分母项保留空值，绝不伪造指标</p>
              </div>
              <div className="p-3.5">
                <p className="text-[12px] text-fg-2">低于阈值上限</p>
                <p className="mt-1 text-[22px] font-semibold text-positive">{f22Summary.within}</p>
                <p className="mt-0.5 text-[11px] text-muted">满足当前已匹配标准上限</p>
              </div>
              <div className="p-3.5">
                <p className="text-[12px] text-fg-2">超出阈值上限</p>
                <p className={`mt-1 text-[22px] font-semibold ${f22Summary.exceeds > 0 ? 'text-negative' : 'text-fg'}`}>
                  {f22Summary.exceeds}
                </p>
                <p className="mt-0.5 text-[11px] text-muted">需核查用能强度超限原因</p>
              </div>
              <div className="bg-fill-1/35 p-3.5">
                <p className="text-[12px] text-fg-2">无标准 / 不可对标</p>
                <p className="mt-1 text-[22px] font-semibold text-fg-2">{f22Summary.uncomparable}</p>
                <p className="mt-0.5 text-[11px] text-muted">
                  {f22Summary.hasDemo ? '含演示标准 · 不构成正式达标认证' : '严格按已发布规则对标'}
                </p>
              </div>
            </div>

            {/* Compact Inline Benchmark Comparison Bars + Rule Version Provenance */}
            <div className="grid gap-4 lg:grid-cols-12">
              <div className="space-y-2.5 rounded-[4px] border border-line bg-surface p-4 lg:col-span-8">
                <div className="flex flex-wrap items-center justify-between gap-2 text-[12px]">
                  <span className="font-medium text-fg">紧凑指标对标辅助视图（各自原生单位，不跨单位排名）</span>
                  <div className="flex items-center gap-2 text-[10px]">
                    <span className="rounded-[2px] bg-warning-soft px-1.5 py-0 text-warning">演示标准</span>
                    <span className="rounded-[2px] bg-primary-soft px-1.5 py-0 text-primary">内部目标</span>
                    <span className="rounded-[2px] bg-positive-soft px-1.5 py-0 text-positive">外部权威标准</span>
                  </div>
                </div>
                <div className="space-y-2.5">
                  {rawRows.map((r) => {
                    const actual = parseNum(r.metric_value);
                    const max = parseNum(r.benchmark_max);
                    const pct = actual != null && max != null && max > 0 ? Math.min(125, (actual / max) * 100) : null;
                    const over = actual != null && max != null && actual > max;
                    const kindBadge = statusBadges[r.benchmark_kind];
                    const bStatusBadge = statusBadges[r.benchmark_status];
                    return (
                      <div key={r.metric_id} className="space-y-1 text-[11px]">
                        <div className="flex flex-wrap items-center justify-between gap-2">
                          <div className="flex flex-wrap items-center gap-1.5">
                            <span className="font-medium text-fg">{r.metric_name}</span>
                            {kindBadge && (
                              <span className={`rounded-[2px] px-1.5 py-0 text-[10px] ${kindBadge.cls}`}>{kindBadge.label}</span>
                            )}
                          </div>
                          <div className="flex items-center gap-2">
                            <span className="font-semibold text-fg">
                              {actual != null ? `${r.metric_value} ${r.metric_unit}` : '缺分母不可算'}
                            </span>
                            <span className="text-muted">
                              {max != null ? `/ 上限 ${r.benchmark_max}` : '/ 无定额上限'}
                            </span>
                            {bStatusBadge && (
                              <span className={`rounded-[2px] px-1.5 py-0 text-[10px] ${bStatusBadge.cls}`}>
                                {bStatusBadge.label}
                              </span>
                            )}
                          </div>
                        </div>
                        <div className="relative h-[8px] w-full overflow-hidden rounded-[2px] bg-fill-2">
                          {pct != null ? (
                            <>
                              <div
                                className="h-full rounded-[2px]"
                                style={{
                                  width: `${Math.min(100, (pct / 125) * 100)}%`,
                                  backgroundColor: over ? '#F53F3F' : '#165DFF',
                                }}
                              />
                              <div
                                title={`阈值上限 ${r.benchmark_max} ${r.metric_unit}`}
                                className="absolute inset-y-0 w-[2px] bg-warning"
                                style={{ left: `${(100 / 125) * 100}%` }}
                              />
                            </>
                          ) : (
                            <div className="h-full w-full bg-[repeating-linear-gradient(45deg,transparent,transparent_4px,rgba(134,144,156,0.22)_4px,rgba(134,144,156,0.22)_8px)]" />
                          )}
                        </div>
                      </div>
                    );
                  })}
                </div>
              </div>

              <div className="rounded-[4px] border border-line bg-fill-1/40 p-4 text-[11px] lg:col-span-4">
                <h3 className="mb-2 text-[13px] font-medium text-fg">指标与定额规则版本来源</h3>
                {f22Summary.matchedRules.length === 0 ? (
                  <p className="text-muted">当前报表指标暂无外部权威定额规则记录，仅展示已计算指标与口径说明。</p>
                ) : (
                  <ul className="space-y-2">
                    {f22Summary.matchedRules.map((b) => (
                      <li key={`${b.benchmarkId}-${b.revision}`} className="rounded-[2px] border border-line bg-surface p-2.5">
                        <div className="flex items-center justify-between gap-1">
                          <span className="font-medium text-fg">{b.name}</span>
                          <span className="font-mono text-[10px] text-muted">{b.revision}</span>
                        </div>
                        <p className="mt-1 text-muted">
                          发布方：{b.publisher} · 适用：{b.buildingType} / {b.region} · 模板 {b.templateVersion}
                        </p>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            </div>
          </div>
        )}

        {/* F23: Assessment Report Tier Grouping & Unconfirmed Warning */}
        {section === 'report-assessment' && f23Summary && (
          <div className="space-y-3.5 p-5 tabular">
            {(f23Summary.pending > 0 || f23Summary.appealed > 0) && (
              <div className="flex flex-wrap items-center gap-2 rounded-[4px] border border-warning/35 bg-warning-soft/55 px-3.5 py-2 text-[12px] text-warning">
                <AlertTriangle className="h-4 w-4 shrink-0" aria-hidden="true" />
                <span>
                  当前报表含 <strong>{f23Summary.pending}</strong> 项「待复核」与 <strong>{f23Summary.appealed}</strong> 项「复核申诉中」对象；复核未完成前不得作为最终确认考核成绩发布。
                </span>
              </div>
            )}

            {/* Compact Score Tier Distribution Bar + Interactive Group Filter */}
            <div className="grid gap-4 lg:grid-cols-12">
              <div className="rounded-[4px] border border-line bg-surface p-4 lg:col-span-8">
                <div className="mb-2.5 flex flex-wrap items-center justify-between gap-2 text-[12px]">
                  <span className="font-medium text-fg">考核对象分档分布摘要（点击分档筛选明细表）</span>
                  <span className="text-[11px] text-muted">
                    已确认 {f23Summary.confirmed} · 待复核 {f23Summary.pending} · 申诉中 {f23Summary.appealed}
                  </span>
                </div>

                {/* Single clean proportional bar across the 5 assessment groups */}
                <div className="mb-3 flex h-[12px] w-full overflow-hidden rounded-[2px] bg-fill-2">
                  {(['excellent', 'qualified', 'rectify', 'exempt', 'blocked'] as const).map((g) => {
                    const cnt = f23Summary.counts[g];
                    if (cnt <= 0 || rawRows.length === 0) return null;
                    return (
                      <div
                        key={g}
                        title={`${assessmentGroupLabels[g].label}: ${cnt} 项`}
                        style={{
                          width: `${(cnt / rawRows.length) * 100}%`,
                          backgroundColor: assessmentGroupLabels[g].dot,
                        }}
                      />
                    );
                  })}
                </div>

                <div className="flex flex-wrap items-center gap-1.5 text-[11px]">
                  <button
                    type="button"
                    data-f23-group="all"
                    onClick={() => {
                      setF23Group('all');
                      setPageIdx(0);
                    }}
                    className={`rounded-[2px] border px-2.5 py-1 transition-colors ${
                      f23Group === 'all' ? 'border-primary bg-primary font-medium text-white' : 'border-line bg-surface text-fg-2 hover:bg-fill-1'
                    }`}
                  >
                    全部对象 ({rawRows.length})
                  </button>
                  {(['excellent', 'qualified', 'rectify', 'exempt', 'blocked'] as const).map((g) => (
                    <button
                      key={g}
                      type="button"
                      data-f23-group={g}
                      onClick={() => {
                        setF23Group(g);
                        setPageIdx(0);
                      }}
                      className={`inline-flex items-center gap-1.5 rounded-[2px] border px-2.5 py-1 transition-colors ${
                        f23Group === g ? 'border-primary bg-primary-soft font-medium text-primary' : 'border-line bg-surface text-fg-2 hover:bg-fill-1'
                      }`}
                    >
                      <span className="size-2 rounded-full" style={{ backgroundColor: assessmentGroupLabels[g].dot }} />
                      <span>{assessmentGroupLabels[g].label}</span>
                      <span className="font-semibold text-fg">{f23Summary.counts[g]}</span>
                    </button>
                  ))}
                </div>
              </div>

              <div className="flex flex-col justify-between rounded-[4px] border border-line bg-fill-1/40 p-4 text-[12px] lg:col-span-4">
                <div className="flex items-center justify-between">
                  <span className="text-fg-2">复核完成进度</span>
                  <span className="font-semibold text-fg">
                    {f23Summary.confirmed} / {rawRows.length} 项已确认
                  </span>
                </div>
                <div className="my-2 h-[6px] w-full overflow-hidden rounded-full bg-fill-2">
                  <div
                    className="h-full rounded-full bg-positive"
                    style={{ width: `${rawRows.length > 0 ? (f23Summary.confirmed / rawRows.length) * 100 : 0}%` }}
                  />
                </div>
                <p className="text-[11px] text-muted">
                  包含确认说明与复核申诉完整追溯记录；低覆盖阻断项禁止出分与确认。
                </p>
              </div>
            </div>
          </div>
        )}

        {/* F24: Retrofit & M&V Print-Friendly Executive Report Cards */}
        {section === 'report-retrofit' && f24Summary && (
          <div className="space-y-4 p-5 tabular">
            <div className="grid divide-y divide-line rounded-[4px] border border-line sm:grid-cols-2 lg:grid-cols-4 lg:divide-y-0 lg:divide-x">
              <div className="p-3.5">
                <p className="text-[12px] text-fg-2">纳入报表改造项目</p>
                <p className="mt-1 text-[22px] font-semibold text-fg">{rawRows.length} 项</p>
                <p className="mt-0.5 text-[11px] text-muted">严格区分模型预计收益与已核验成果</p>
              </div>
              <div className="p-3.5">
                <p className="text-[12px] text-fg-2">M&amp;V 模拟核验完成</p>
                <p className="mt-1 text-[22px] font-semibold text-positive">{f24Summary.verified.length} 项</p>
                <p className="mt-0.5 text-[11px] text-muted">均满足基线锁定与 ≥90% 覆盖门禁</p>
              </div>
              <div className="p-3.5">
                <p className="text-[12px] text-fg-2">待核验 / 仅模型估算</p>
                <p className="mt-1 text-[22px] font-semibold text-warning">
                  {f24Summary.pending.length} / {f24Summary.estimatedOnly.length}
                </p>
                <p className="mt-0.5 text-[11px] text-muted">未达核验门禁不生成已核验节能量</p>
              </div>
              <div className="bg-fill-1/35 p-3.5">
                <p className="text-[12px] text-fg-2">已核验参考减碳合计</p>
                <p className="mt-1 text-[22px] font-semibold text-fg">
                  {fmtNum(f24Summary.totalVerifiedCarbon)} <span className="text-[11px] font-normal text-muted">kgCO₂e</span>
                </p>
                <p className="mt-0.5 text-[11px] text-muted">仅统计已核验项目（非履约凭证）</p>
              </div>
            </div>

            {/* Print-Friendly Per-Project Comparison Cards (Separating Estimated vs Verified Visually) */}
            <div className="flex flex-wrap items-center justify-between gap-2 text-[12px]">
              <span className="font-medium text-fg">项目改造测算与 M&amp;V 核验对照卡</span>
              <div className="flex items-center gap-1.5 text-[11px]">
                {[
                  { id: 'all', label: `全部 (${rawRows.length})` },
                  { id: 'verified_simulated', label: `已核验 (${f24Summary.verified.length})` },
                  { id: 'pending_verification', label: `待核验 (${f24Summary.pending.length})` },
                  { id: 'estimated_only', label: `仅估算 (${f24Summary.estimatedOnly.length})` },
                ].map((f) => (
                  <button
                    key={f.id}
                    type="button"
                    data-f24-status={f.id}
                    onClick={() => {
                      setF24StatusFilter(f.id);
                      setPageIdx(0);
                    }}
                    className={`rounded-[2px] border px-2.5 py-0.5 transition-colors ${
                      f24StatusFilter === f.id
                        ? 'border-primary bg-primary-soft font-medium text-primary'
                        : 'border-line bg-surface text-fg-2 hover:bg-fill-1'
                    }`}
                  >
                    {f.label}
                  </button>
                ))}
              </div>
            </div>

            <div className="grid gap-3 lg:grid-cols-3">
              {rawRows.map((r) => {
                const isVer = r.mv_status === 'verified_simulated';
                const badge = statusBadges[r.mv_status];
                const per = f24Periods[r.retrofit_id];
                return (
                  <article
                    key={r.retrofit_id}
                    className={`rounded-[4px] border p-4 text-[12px] ${
                      isVer ? 'border-positive/35 bg-positive-soft/15' : 'border-line bg-surface'
                    }`}
                  >
                    <div className="flex items-start justify-between gap-2">
                      <div className="min-w-0">
                        <h3 className="truncate font-semibold text-fg" title={r.name}>{r.name}</h3>
                        <p className="mt-0.5 text-[11px] text-muted">
                          {r.building_name} · {energyLabels[r.energy] ?? r.energy} ({r.unit}) · 基线 <span className="font-mono">{r.baseline_version}</span>
                        </p>
                      </div>
                      {badge && (
                        <span className={`shrink-0 rounded-[2px] px-1.5 py-0 text-[10px] font-medium leading-[18px] ${badge.cls}`}>
                          {badge.label}
                        </span>
                      )}
                    </div>

                    {per && (
                      <div className="mt-2 flex flex-wrap gap-x-3 gap-y-0.5 rounded-[2px] bg-fill-1/50 px-2 py-1 text-[10px] text-muted">
                        <span>基线期：{per.baselineFrom} ~ {per.baselineTo}</span>
                        <span>M&amp;V 报告期：{per.reportFrom} ~ {per.reportTo}</span>
                      </div>
                    )}

                    <div className="mt-3 grid grid-cols-2 gap-2 border-t border-line pt-2.5 text-[11px]">
                      <div className="rounded-[2px] bg-fill-1/60 p-2">
                        <span className="text-muted">F19 模型预计（年化）</span>
                        <p className="mt-0.5 font-semibold text-fg">
                          {r.est_savings_base || '—'} {r.unit}
                        </p>
                        <p className="text-[10px] text-muted">回收期 {r.static_payback_years || '—'} 年</p>
                      </div>
                      <div className={`rounded-[2px] p-2 ${isVer ? 'bg-positive-soft/50' : 'bg-fill-1/60'}`}>
                        <span className="text-muted">F20 M&amp;V 核验结论</span>
                        <p className={`mt-0.5 font-semibold ${isVer ? 'text-positive' : 'text-warning'}`}>
                          {isVer && r.verified_savings ? `${r.verified_savings} ${r.unit} (${r.verified_savings_rate})` : '未生成已核验结论'}
                        </p>
                        <p className="text-[10px] text-muted">
                          报告覆盖 {r.report_coverage_rate || '—'}
                          {isVer && r.verified_carbon_kgco2e ? ` · 减碳 ${r.verified_carbon_kgco2e} kg` : ''}
                        </p>
                      </div>
                    </div>

                    <p className="mt-2 text-[11px] leading-relaxed text-muted">{r.mv_note}</p>
                  </article>
                );
              })}
            </div>
          </div>
        )}

        {/* F25: Industry Special & Restricted Custom Field Builder */}
        {section === 'report-custom' && (
          <div className="space-y-3.5 p-5 text-[12px]">
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-[4px] border border-line bg-fill-1/40 px-4 py-3">
              <div className="flex flex-wrap items-center gap-2">
                <SlidersHorizontal className="h-4 w-4 text-primary" aria-hidden="true" />
                <span className="font-medium text-fg">行业专项数据域与白名单字段配置</span>
                {onScopeChange && (
                  <div className="ml-2 inline-flex rounded-[2px] border border-line bg-surface p-0.5">
                    <button
                      type="button"
                      data-f25-scope="metrics"
                      onClick={() => onScopeChange('metrics')}
                      className={`rounded-[2px] px-2.5 py-0.5 text-[11px] transition-colors ${
                        scope === 'metrics' ? 'bg-primary font-medium text-white' : 'text-fg-2 hover:text-fg'
                      }`}
                    >
                      行业指标与定额域 (F11–F13)
                    </button>
                    <button
                      type="button"
                      data-f25-scope="consumption"
                      onClick={() => onScopeChange('consumption')}
                      className={`rounded-[2px] px-2.5 py-0.5 text-[11px] transition-colors ${
                        scope === 'consumption' ? 'bg-primary font-medium text-white' : 'text-fg-2 hover:text-fg'
                      }`}
                    >
                      综合能耗与分项域 (F01/F06)
                    </button>
                  </div>
                )}
              </div>

              <div className="flex flex-wrap items-center gap-1.5 text-[11px]">
                <span className="text-muted">快捷模板：</span>
                {scope === 'metrics' ? (
                  <>
                    <button
                      type="button"
                      data-f25-preset="exec"
                      onClick={() =>
                        applyCustomPreset([
                          'data_nature',
                          'building_name',
                          'metric_name',
                          'metric_value',
                          'metric_unit',
                          'benchmark_max',
                          'benchmark_status',
                        ])
                      }
                      className="rounded-[2px] border border-line bg-surface px-2 py-0.5 text-fg-2 hover:bg-fill-1"
                    >
                      精简对标摘要 (7列)
                    </button>
                    <button
                      type="button"
                      data-f25-preset="all"
                      onClick={() => applyCustomPreset(allowedFields.map((f) => f.key))}
                      className="rounded-[2px] border border-line bg-surface px-2 py-0.5 text-fg-2 hover:bg-fill-1"
                    >
                      完整审计全字段 ({allowedFields.length}列)
                    </button>
                  </>
                ) : (
                  <>
                    <button
                      type="button"
                      data-f25-preset="exec"
                      onClick={() =>
                        applyCustomPreset([
                          'data_nature',
                          'building_name',
                          'energy',
                          'category_name',
                          'usage',
                          'unit',
                          'share',
                          'coverage_rate',
                        ])
                      }
                      className="rounded-[2px] border border-line bg-surface px-2 py-0.5 text-fg-2 hover:bg-fill-1"
                    >
                      精简分项汇总 (8列)
                    </button>
                    <button
                      type="button"
                      data-f25-preset="all"
                      onClick={() => applyCustomPreset(allowedFields.map((f) => f.key))}
                      className="rounded-[2px] border border-line bg-surface px-2 py-0.5 text-fg-2 hover:bg-fill-1"
                    >
                      完整审计全字段 ({allowedFields.length}列)
                    </button>
                  </>
                )}
              </div>
            </div>

            {/* Scope Incompatibility Guard Notice */}
            <div className="flex items-start gap-2 rounded-[4px] border border-primary/25 bg-primary-soft/25 px-3.5 py-2 text-[11px] text-fg-2">
              <ShieldAlert className="mt-0.5 h-3.5 w-3.5 shrink-0 text-primary" aria-hidden="true" />
              <span>
                口径隔离限制：指标定额域（多单位强度/阈值）与能耗分项域（物理计量明细）统计口径不兼容，系统禁止跨域混排与前端二次拼接求和；勾选字段即时同步至预览表与 CSV 导出参数。
              </span>
            </div>

            {allowedFields.length > 0 && (
              <div>
                <div className="mb-2 flex items-center justify-between text-[11px] text-muted">
                  <span>受限白名单字段选择（已选 {selectedFields.length} / {allowedFields.length} 列，至少保留 1 列）</span>
                  <span>导出 CSV 将严格按已选列顺序生成</span>
                </div>
                <div className="flex flex-wrap gap-1.5">
                  {allowedFields.map((f) => {
                    const checked = selectedFields.includes(f.key);
                    return (
                      <label
                        key={f.key}
                        data-f25-field={f.key}
                        className={`inline-flex cursor-pointer items-center gap-1.5 rounded-[2px] border px-2.5 py-1 text-[11px] transition-colors ${
                          checked
                            ? 'border-primary bg-primary-soft font-medium text-primary'
                            : 'border-line bg-surface text-fg-2 hover:bg-fill-1'
                        }`}
                      >
                        <input
                          type="checkbox"
                          checked={checked}
                          onChange={() => toggleCustomField(f.key)}
                          className="sr-only"
                        />
                        {checked && <CheckCircle2 className="h-3 w-3 shrink-0" aria-hidden="true" />}
                        <span>{f.label}</span>
                      </label>
                    );
                  })}
                </div>
              </div>
            )}
          </div>
        )}
      </section>

      {/* ── 3. Precision Enterprise Data Table (Right-Aligned Numerics, Clear Row Hierarchy, Pagination) ── */}
      <section aria-label="正式报表精确数据表" className="arco-card overflow-hidden">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line px-5 py-3 text-[12px] tabular">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-[14px] font-medium text-fg">
              报表精确数据明细
              <span className="ml-2 text-[12px] font-normal text-muted">
                当前显示 {sortedRows.length} 行{isFilteredLocally ? `（全表 ${rawRows.length} 行，导出 CSV 含全量授权记录）` : ''}
              </span>
            </h2>
            {section === 'report-consumption' && (
              <span className="text-[11px] text-primary">点击任意分项行可展开上方计量来源下钻</span>
            )}
          </div>

          <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted">
            {canScrollH && (
              <div className="inline-flex items-center gap-1 rounded-[2px] border border-line bg-fill-1/60 px-2 py-0.5 text-[11px] text-fg-2">
                <span>宽表横向滚动（{columns.length} 列）</span>
                <button
                  type="button"
                  aria-label="表格向左滚动"
                  onClick={() => tableWrapRef.current?.scrollBy({ left: -360, behavior: 'smooth' })}
                  className="rounded-[2px] px-1 hover:bg-fill-2"
                >
                  ◀
                </button>
                <button
                  type="button"
                  aria-label="表格向右滚动"
                  onClick={() => tableWrapRef.current?.scrollBy({ left: 360, behavior: 'smooth' })}
                  className="rounded-[2px] px-1 hover:bg-fill-2"
                >
                  ▶
                </button>
              </div>
            )}
            <span>每页</span>
            <select
              aria-label="每页显示行数"
              value={pageSize}
              onChange={(e) => {
                setPageSize(Number(e.target.value));
                setPageIdx(0);
              }}
              className="arco-input !h-6 min-w-[64px] text-[11px]"
            >
              <option value={10}>10 行</option>
              <option value={20}>20 行</option>
              <option value={50}>50 行</option>
            </select>
          </div>
        </div>

        <div ref={tableWrapRef} className="overflow-x-auto">
          <table className="w-full border-collapse text-left text-[12px]">
            <thead className="sticky top-0 z-10 border-b border-line bg-fill-1 text-fg-2">
              <tr>
                {columns.map((c) => {
                  const isNum = NUMERIC_KEYS.has(c.key);
                  const dense = columns.length >= 14;
                  return (
                    <th
                      key={c.key}
                      className={`whitespace-nowrap ${dense ? 'px-2.5 py-2' : 'px-3.5 py-2.5'} font-medium ${isNum ? 'text-right' : 'text-left'}`}
                    >
                      <button
                        type="button"
                        onClick={() => handleSort(c.key)}
                        className={`inline-flex items-center gap-1 hover:text-fg ${isNum ? 'flex-row-reverse' : ''}`}
                      >
                        <span>{c.label}</span>
                        <ArrowUpDown
                          className={`h-3 w-3 shrink-0 ${sortKey === c.key ? 'text-primary' : 'opacity-35'}`}
                          aria-hidden="true"
                        />
                      </button>
                    </th>
                  );
                })}
              </tr>
            </thead>
            <tbody className="divide-y divide-line tabular">
              {pagedRows.length === 0 ? (
                <tr>
                  <td colSpan={Math.max(1, columns.length)} className="px-5 py-8 text-center text-[12px] text-muted">
                    当前筛选条件下暂无报表数据记录。
                  </td>
                </tr>
              ) : (
                pagedRows.map((row, rIdx) => {
                  const isTotalRow = row.category_id === 'TOTAL';
                  const isAlertRow =
                    row.metric_status === 'missing_denominator' ||
                    row.benchmark_status === 'exceeds_threshold' ||
                    row.review_status === 'appealed' ||
                    row.review_status === 'blocked_low_quality' ||
                    row.mv_status === 'pending_verification';
                  const isDrillable = section === 'report-consumption' && Boolean(row.energy && row.category_id);
                  const isDrilled =
                    f21DrillCat?.energy === row.energy && f21DrillCat?.categoryId === row.category_id;
                  const dense = columns.length >= 14;

                  return (
                    <tr
                      key={rIdx}
                      onClick={() => {
                        if (isDrillable) {
                          setF21DrillCat({
                            energy: row.energy,
                            categoryId: row.category_id,
                            categoryName: row.category_name || row.category_id,
                          });
                        }
                      }}
                      className={`transition-colors ${
                        isDrillable ? 'cursor-pointer' : ''
                      } ${
                        isDrilled
                          ? 'bg-primary-soft/45'
                          : isTotalRow
                            ? 'bg-fill-1/75 font-semibold'
                            : isAlertRow
                              ? 'bg-warning-soft/20 hover:bg-warning-soft/35'
                              : 'hover:bg-fill-1/50'
                      }`}
                    >
                      {columns.map((c) => {
                        const isNum = NUMERIC_KEYS.has(c.key);
                        const isLongNote = c.key === 'benchmark_notice' || c.key === 'review_note' || c.key === 'mv_note';
                        const isWrapName = c.key === 'name' || c.key === 'metric_name';
                        return (
                          <td
                            key={c.key}
                            className={`${dense ? 'px-2.5 py-2' : 'px-3.5 py-2.5'} ${
                              isNum
                                ? 'whitespace-nowrap text-right'
                                : isLongNote
                                  ? 'min-w-[180px] max-w-[280px] text-[11px] text-fg-2'
                                  : isWrapName
                                    ? 'min-w-[168px] max-w-[230px] whitespace-normal font-medium text-fg'
                                    : 'whitespace-nowrap text-fg'
                            }`}
                          >
                            {formatCell(c, row)}
                          </td>
                        );
                      })}
                    </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </div>

        {/* Pagination Footer */}
        {totalPages > 1 && (
          <div className="flex flex-wrap items-center justify-between gap-2 border-t border-line bg-fill-1/40 px-5 py-2.5 text-[11px] text-muted tabular">
            <span>
              第 {safePageIdx + 1} / {totalPages} 页 · 共 {sortedRows.length} 条记录
            </span>
            <div className="flex items-center gap-1.5">
              <button
                type="button"
                disabled={safePageIdx <= 0}
                onClick={() => setPageIdx((p) => Math.max(0, p - 1))}
                className="inline-flex h-6 items-center gap-1 rounded-[2px] border border-line bg-surface px-2 text-fg-2 hover:bg-fill-1 disabled:opacity-40"
              >
                <ChevronLeft className="h-3 w-3" aria-hidden="true" />
                上一页
              </button>
              <button
                type="button"
                disabled={safePageIdx >= totalPages - 1}
                onClick={() => setPageIdx((p) => Math.min(totalPages - 1, p + 1))}
                className="inline-flex h-6 items-center gap-1 rounded-[2px] border border-line bg-surface px-2 text-fg-2 hover:bg-fill-1 disabled:opacity-40"
              >
                下一页
                <ChevronRight className="h-3 w-3" aria-hidden="true" />
              </button>
            </div>
          </div>
        )}
      </section>

      {/* ── 4. Data Quality, Provenance & Export Governance Footer ── */}
      <footer aria-label="数据质量与来源说明" className="rounded-[4px] border border-line bg-fill-1/40 px-5 py-3 text-[11px] text-muted tabular">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <p className="text-fg-2">
            <strong className="font-medium text-fg">口径与质量声明：</strong>
            {data.disclaimer || '报表直接消费服务端正式计算结果，缺失分母、低覆盖或未核验记录如实留空不隐藏；CSV 导出启用权限校验与公式注入防护。'}
          </p>
          <span className="shrink-0 font-mono">
            生成时间 {data.generatedAt ? data.generatedAt.slice(0, 19).replace('T', ' ') : '—'}
          </span>
        </div>
        {data.dataOriginNotice && <p className="mt-1 text-[11px] text-muted">{data.dataOriginNotice}</p>}
      </footer>
    </div>
  );
}
