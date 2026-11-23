import { useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import {
  ArrowUpDown,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Columns3,
  Download,
  Eye,
  Printer,
  SlidersHorizontal,
  X,
} from 'lucide-react';
import {
  downloadLiveCsv,
  liveGet,
  verifyLiveCsvAuth,
  type InventoryData,
  type LiveReportData,
  type ReportColumn,
  type SourceTrace,
} from '../api/energyLive';
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
  missing_denominator: { label: '缺分母', cls: 'bg-warning-soft text-warning' },
  invalid_denominator: { label: '分母无效', cls: 'bg-warning-soft text-warning' },
  denominator_out_of_period: { label: '分母周期不匹配', cls: 'bg-warning-soft text-warning' },
  within_threshold: { label: '低于上限', cls: 'bg-positive-soft text-positive' },
  exceeds_threshold: { label: '超出上限', cls: 'bg-negative-soft text-negative' },
  no_standard: { label: '未配定额', cls: 'bg-fill-2 text-fg-2' },
  inapplicable: { label: '不适用', cls: 'bg-fill-2 text-fg-2' },
  not_comparable: { label: '不可对标', cls: 'bg-warning-soft text-warning' },
  demo: { label: '演示标准', cls: 'bg-warning-soft text-warning' },
  internal: { label: '内部目标', cls: 'bg-primary-soft text-primary' },
  external: { label: '权威标准', cls: 'bg-positive-soft text-positive' },
  confirmed: { label: '已确认', cls: 'bg-positive-soft text-positive' },
  pending_review: { label: '待复核', cls: 'bg-warning-soft text-warning' },
  appealed: { label: '申诉中', cls: 'bg-negative-soft text-negative' },
  blocked_low_quality: { label: '低质量阻断', cls: 'bg-negative-soft text-negative' },
  verified_simulated: { label: '模拟核验完成', cls: 'bg-positive-soft text-positive' },
  pending_verification: { label: '待核验', cls: 'bg-warning-soft text-warning' },
  estimated_only: { label: '仅估算', cls: 'bg-fill-2 text-fg-2' },
};

// Default 6-8 high-value business columns per report (technical IDs & repeated dates move to Column Settings & Row Detail Drawer)
const DEFAULT_VISIBLE_KEYS: Record<Exclude<ReportSection, 'report-custom'>, string[]> = {
  'report-consumption': ['category_name', 'energy', 'usage', 'unit', 'share', 'coverage_rate'],
  'report-metrics': ['metric_name', 'metric_value', 'metric_unit', 'benchmark_max', 'benchmark_kind', 'benchmark_status', 'metric_status'],
  'report-assessment': ['object_name', 'energy', 'target_value', 'actual_usage', 'unit', 'deviation', 'score', 'review_status'],
  'report-retrofit': ['name', 'building_name', 'energy', 'est_savings_base', 'verified_savings', 'unit', 'mv_status'],
};

const F25_DEFAULT_KEYS: Record<string, string[]> = {
  metrics: ['metric_name', 'building_name', 'metric_value', 'metric_unit', 'benchmark_max', 'benchmark_status', 'metric_status'],
  consumption: ['category_name', 'energy', 'usage', 'unit', 'share', 'coverage_rate', 'coverage_slots'],
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
  const [searchQuery, setSearchQuery] = useState<string>('');
  const [pageIdx, setPageIdx] = useState<number>(0);
  const [pageSize, setPageSize] = useState<number>(20);

  // Per-report lightweight filter states (rendered compactly inside the toolbar)
  const [f21EnergyTab, setF21EnergyTab] = useState<string>('all');
  const [f23Group, setF23Group] = useState<AssessmentGroup>('all');
  const [f24StatusFilter, setF24StatusFilter] = useState<string>('all');

  // Column Settings Popover (F21-F24) + Export Menu + F25 Field Config Panel + Row Detail Slide-Over Drawer
  const [colMenuOpen, setColMenuOpen] = useState<boolean>(false);
  const [exportMenuOpen, setExportMenuOpen] = useState<boolean>(false);
  const [customPanelOpen, setCustomPanelOpen] = useState<boolean>(false);
  const [visibleColKeys, setVisibleColKeys] = useState<string[] | null>(null);
  const [detailRow, setDetailRow] = useState<Record<string, string> | null>(null);

  // On-demand metadata for Row Detail Drawer (F21 bound meters, F24 M&V windows)
  const [f21CompSources, setF21CompSources] = useState<Record<string, SourceTrace[]>>({});
  const [f24Periods, setF24Periods] = useState<
    Record<string, { baselineFrom: string; baselineTo: string; reportFrom: string; reportTo: string }>
  >({});

  const tableWrapRef = useRef<HTMLDivElement | null>(null);
  const [canScrollH, setCanScrollH] = useState<boolean>(false);

  const rawRows = data.rows ?? [];
  const allColumns = data.columns ?? [];
  const allowedFields = data.allowedFields ?? [];
  const allowedFieldKeys = useMemo(() => new Set(allowedFields.map((f) => f.key)), [allowedFields]);

  // Reset UI state when report section / building / scope changes
  useEffect(() => {
    setPageIdx(0);
    setSortKey(null);
    setSearchQuery('');
    setF21EnergyTab('all');
    setF23Group('all');
    setF24StatusFilter('all');
    setVisibleColKeys(null);
    setColMenuOpen(false);
    setExportMenuOpen(false);
    setCustomPanelOpen(false);
    setDetailRow(null);
    setCsvErr(null);
  }, [section, data.buildingId, data.energy, scope]);

  // Fetch F21 real bound meters only when needed for Row Detail Drawer
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
        for (const e of res.data?.energies ?? []) map[e.energy] = e.sources ?? [];
        setF21CompSources(map);
      })
      .catch(() => {
        if (!ctrl.signal.aborted) setF21CompSources({});
      });
    return () => ctrl.abort();
  }, [section, data.buildingId, date, token]);

  // Fetch F24 exact baseline/report M&V periods for Row Detail Drawer
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

  // Sanitized F25 selected fields (strictly synced to server query & CSV export)
  const selectedFields = useMemo(() => {
    const base = data.selectedFields ?? allColumns.map((c) => c.key);
    if (section !== 'report-custom' || allowedFieldKeys.size === 0) return base;
    const valid = base.filter((k) => allowedFieldKeys.has(k));
    return valid.length > 0 ? valid : allowedFields.map((f) => f.key);
  }, [data.selectedFields, allColumns, section, allowedFieldKeys, allowedFields]);

  // Active displayed columns: 6-8 default business columns on F21-F24 (expandable via Column Settings);
  // on F25, displays the exact selectedFields (or default 7 core columns if user hasn't customized yet and all 17 came back).
  const defaultKeysForSection = useMemo(() => {
    const avail = new Set(allColumns.map((c) => c.key));
    if (section === 'report-custom') {
      const defaults = (F25_DEFAULT_KEYS[scope] ?? F25_DEFAULT_KEYS.metrics).filter((k) => avail.has(k));
      return defaults.length > 0 ? defaults : allColumns.slice(0, 7).map((c) => c.key);
    }
    const preferred = DEFAULT_VISIBLE_KEYS[section].filter((k) => avail.has(k));
    return preferred.length > 0 ? preferred : allColumns.slice(0, 7).map((c) => c.key);
  }, [section, scope, allColumns]);

  const activeColumns = useMemo<ReportColumn[]>(() => {
    if (section === 'report-custom') {
      // On F25, ALWAYS display exact server-returned columns matching selectedFields so preview === CSV 1:1!
      return allColumns;
    }
    const keys = visibleColKeys ?? defaultKeysForSection;
    const colMap = new Map(allColumns.map((c) => [c.key, c]));
    const ordered = keys.map((k) => colMap.get(k)).filter((c): c is ReportColumn => c != null);
    return ordered.length > 0 ? ordered : allColumns;
  }, [section, allColumns, visibleColKeys, defaultKeysForSection]);

  // Ensure F25 CSV export endpoint carries the exact sanitized fields & scope
  const effectiveExportEndpoint = useMemo(() => {
    if (section !== 'report-custom') return endpoint;
    const [basePath, qsRaw = ''] = endpoint.split('?');
    const qs = new URLSearchParams(qsRaw);
    qs.set('scope', scope);
    if (selectedFields.length > 0) qs.set('fields', selectedFields.join(','));
    else qs.delete('fields');
    return `${basePath}?${qs.toString()}`;
  }, [section, endpoint, scope, selectedFields]);

  // Official full-schema backend export URL (on F25 explicitly requests all allowed whitelist fields)
  const fullExportEndpoint = useMemo(() => {
    if (section !== 'report-custom') return endpoint;
    const [basePath, qsRaw = ''] = endpoint.split('?');
    const qs = new URLSearchParams(qsRaw);
    qs.set('scope', scope);
    if (allowedFields.length > 0) qs.set('fields', allowedFields.map((f) => f.key).join(','));
    else qs.delete('fields');
    return `${basePath}?${qs.toString()}`;
  }, [section, endpoint, scope, allowedFields]);

  const fullFieldCount = section === 'report-custom' ? allowedFields.length : allColumns.length;

  useEffect(() => {
    const el = tableWrapRef.current;
    if (!el) return;
    const measure = () => setCanScrollH(el.scrollWidth - el.clientWidth > 6);
    measure();
    window.addEventListener('resize', measure);
    return () => window.removeEventListener('resize', measure);
  }, [activeColumns, rawRows, section, scope]);

  // Category weight map for F21 Row Detail Drawer meter lookup
  const bindingMap = useMemo(() => {
    const map = new Map<string, Array<{ categoryId: string; weightPpm: number }>>();
    for (const b of inventory?.categoryBindings ?? []) {
      map.set(`${b.terminalId}::${b.itemCode}`, b.allocations ?? []);
    }
    return map;
  }, [inventory?.categoryBindings]);

  const detailF21Meters = useMemo(() => {
    if (section !== 'report-consumption' || !detailRow?.energy || !detailRow?.category_id) return [];
    const list = f21CompSources[detailRow.energy] ?? [];
    if (detailRow.category_id === 'TOTAL') return list.map((s) => ({ source: s, weightPct: 100 }));
    if (detailRow.category_id === 'UNCLASSIFIED') {
      return list
        .map((s) => {
          const sumPpm = (bindingMap.get(`${s.terminalId}::${s.itemCode}`) ?? []).reduce((a, x) => a + x.weightPpm, 0);
          return { source: s, weightPct: Math.max(0, (1_000_000 - sumPpm) / 10_000) };
        })
        .filter((x) => x.weightPct > 0);
    }
    return list
      .map((s) => {
        const hit = (bindingMap.get(`${s.terminalId}::${s.itemCode}`) ?? []).find((a) => a.categoryId === detailRow.category_id);
        return hit ? { source: s, weightPct: hit.weightPpm / 10_000 } : null;
      })
      .filter((x): x is { source: SourceTrace; weightPct: number } => x != null);
  }, [section, detailRow, f21CompSources, bindingMap]);

  // Unique energy list for F21 compact toolbar filter
  const f21Energies = useMemo(() => {
    if (section !== 'report-consumption') return [];
    const order = ['electricity', 'water', 'gas', 'heat', 'cooling', 'steam'];
    const set = new Set(rawRows.map((r) => r.energy).filter(Boolean));
    return Array.from(set).sort(
      (a, b) => (order.indexOf(a) === -1 ? 99 : order.indexOf(a)) - (order.indexOf(b) === -1 ? 99 : order.indexOf(b)),
    );
  }, [section, rawRows]);

  const filteredRows = useMemo(() => {
    let list = rawRows;
    if (section === 'report-consumption' && f21EnergyTab !== 'all') {
      list = list.filter((r) => r.energy === f21EnergyTab);
    }
    if (section === 'report-assessment' && f23Group !== 'all') {
      list = list.filter((r) => classifyAssessmentRow(r) === f23Group);
    }
    if (section === 'report-retrofit' && f24StatusFilter !== 'all') {
      list = list.filter((r) => r.mv_status === f24StatusFilter);
    }
    const q = searchQuery.trim().toLowerCase();
    if (q) {
      list = list.filter((r) =>
        allColumns.some((c) => {
          const val = (r[c.key] ?? '').toLowerCase();
          if (val.includes(q)) return true;
          if (c.key === 'energy' && (energyLabels[r[c.key] ?? ''] ?? '').toLowerCase().includes(q)) return true;
          const badge = statusBadges[r[c.key] ?? ''];
          if (badge && badge.label.toLowerCase().includes(q)) return true;
          return false;
        }),
      );
    }
    return list;
  }, [section, rawRows, f21EnergyTab, f23Group, f24StatusFilter, searchQuery, allColumns]);

  // Concise single-line footer counts (no boilerplate disclaimers)
  const footerSummary = useMemo(() => {
    const shown = filteredRows.length;
    const total = rawRows.length;
    const countLabel = shown === total ? `共 ${total} 条` : `当前筛选 ${shown} / 共 ${total} 条`;
    if (section === 'report-consumption') {
      return `${countLabel} · 各能源独立计单位`;
    }
    if (section === 'report-metrics') {
      const ready = filteredRows.filter((r) => r.metric_status === 'ready').length;
      const missing = shown - ready;
      return `${countLabel} · 已计算 ${ready} 项${missing > 0 ? ` · 缺分母 ${missing} 项` : ''}`;
    }
    if (section === 'report-assessment') {
      const confirmed = filteredRows.filter((r) => r.review_status === 'confirmed').length;
      const wSum = filteredRows.reduce((s, r) => s + (parseNum(r.weighted_score) ?? 0), 0);
      return `${countLabel} · 已确认 ${confirmed} 项 · 加权分合计 ${fmtNum(wSum)}`;
    }
    if (section === 'report-retrofit') {
      const verified = filteredRows.filter((r) => r.mv_status === 'verified_simulated');
      const carb = verified.reduce((s, r) => s + (parseNum(r.verified_carbon_kgco2e) ?? 0), 0);
      return `${countLabel} · 已核验 ${verified.length} 项${carb > 0 ? ` · 参考减碳 ${fmtNum(carb)} kgCO₂e` : ''}`;
    }
    return `${countLabel} · 已选 ${activeColumns.length}/${allowedFields.length} 列`;
  }, [section, rawRows.length, filteredRows, activeColumns.length, allowedFields.length]);

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

  const toggleDisplayCol = (key: string) => {
    const current = visibleColKeys ?? defaultKeysForSection;
    if (current.includes(key)) {
      if (current.length <= 1) return;
      setVisibleColKeys(current.filter((k) => k !== key));
    } else {
      // Preserve canonical column order from server schema
      const nextSet = new Set([...current, key]);
      setVisibleColKeys(allColumns.map((c) => c.key).filter((k) => nextSet.has(k)));
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
    const filtered = presetKeys.filter((k) => allowedFieldKeys.has(k));
    if (filtered.length > 0) onFieldsChange(filtered);
  };

  const escapeCsvCell = (raw: string): string => {
    const s = raw ?? '';
    return /[",\r\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
  };

  const triggerCsvDownload = (cols: ReportColumn[], rows: Array<Record<string, string>>, filename: string) => {
    const header = cols.map((c) => escapeCsvCell(c.label)).join(',');
    const lines = rows.map((row) => cols.map((c) => escapeCsvCell(row[c.key] ?? '')).join(','));
    const csvText = `\uFEFF${[header, ...lines].join('\r\n')}\r\n`;
    const blob = new Blob([csvText], { type: 'text/csv;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  };

  const applyFilterAndSort = (
    rowsToProcess: Array<Record<string, string>>,
    colsForSearch: ReportColumn[],
  ): Array<Record<string, string>> => {
    let list = rowsToProcess;
    if (section === 'report-consumption' && f21EnergyTab !== 'all') {
      list = list.filter((r) => r.energy === f21EnergyTab);
    }
    if (section === 'report-assessment' && f23Group !== 'all') {
      list = list.filter((r) => classifyAssessmentRow(r) === f23Group);
    }
    if (section === 'report-retrofit' && f24StatusFilter !== 'all') {
      list = list.filter((r) => r.mv_status === f24StatusFilter);
    }
    const q = searchQuery.trim().toLowerCase();
    if (q) {
      list = list.filter((r) =>
        colsForSearch.some((c) => {
          const val = (r[c.key] ?? '').toLowerCase();
          if (val.includes(q)) return true;
          if (c.key === 'energy' && (energyLabels[r[c.key] ?? ''] ?? '').toLowerCase().includes(q)) return true;
          const badge = statusBadges[r[c.key] ?? ''];
          if (badge && badge.label.toLowerCase().includes(q)) return true;
          return false;
        }),
      );
    }
    if (!sortKey) return list;
    return [...list].sort((a, b) => {
      const av = a[sortKey] ?? '';
      const bv = b[sortKey] ?? '';
      const an = parseNum(av);
      const bn = parseNum(bv);
      const cmp = an != null && bn != null ? an - bn : av.localeCompare(bv, 'zh-CN');
      return sortAsc ? cmp : -cmp;
    });
  };

  // Export strictly current filtered & sorted scope (across all pages), with either visible columns or all columns.
  // Always verifies backend export permission first (403 RBAC enforced).
  const handleDownloadFiltered = async (fieldsMode: 'visible' | 'all') => {
    setExportMenuOpen(false);
    setCsvErr(null);
    setDownloading(true);
    try {
      const authTarget = fieldsMode === 'all' ? fullExportEndpoint : effectiveExportEndpoint;
      const authErr = await verifyLiveCsvAuth(authTarget, token);
      if (authErr) {
        setCsvErr(authErr);
        return;
      }
      if (fieldsMode === 'visible') {
        triggerCsvDownload(
          activeColumns,
          sortedRows,
          `energy-report-${data.reportCode.toLowerCase()}-filtered-visible-${activeColumns.length}cols.csv`,
        );
        return;
      }
      if (section !== 'report-custom') {
        triggerCsvDownload(
          allColumns,
          sortedRows,
          `energy-report-${data.reportCode.toLowerCase()}-filtered-all-${allColumns.length}cols.csv`,
        );
        return;
      }
      // F25 all-fields within current search & sort: fetch full whitelist rows and apply identical search & sort
      const fullRes = await liveGet<LiveReportData>(fullExportEndpoint, token);
      if (fullRes.error || !fullRes.data) {
        setCsvErr(fullRes.error ?? '获取完整字段数据失败');
        return;
      }
      const fullCols = fullRes.data.columns ?? allColumns;
      const fullFilteredSorted = applyFilterAndSort(fullRes.data.rows ?? [], fullCols);
      triggerCsvDownload(
        fullCols,
        fullFilteredSorted,
        `energy-report-${data.reportCode.toLowerCase()}-filtered-all-${fullCols.length}cols.csv`,
      );
    } finally {
      setDownloading(false);
    }
  };

  // Export unfiltered raw backend snapshot (explicitly separated so users never mistake it for filtered rows)
  const handleDownloadFullRaw = async () => {
    setExportMenuOpen(false);
    setCsvErr(null);
    setDownloading(true);
    try {
      const err = await downloadLiveCsv(fullExportEndpoint, token, `energy-report-${data.reportCode.toLowerCase()}-raw-all.csv`);
      if (err) setCsvErr(err);
    } finally {
      setDownloading(false);
    }
  };

  const formatCell = (col: ReportColumn, row: Record<string, string>) => {
    const raw = row[col.key] ?? '';
    if (!raw) {
      if (col.key === 'metric_value' && row.metric_status === 'missing_denominator') {
        return <span className="text-[11px] text-warning">缺分母</span>;
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
        <span className="inline-flex rounded-[2px] bg-warning-soft/75 px-1.5 py-0 text-[10px] font-medium text-warning">
          模拟数据
        </span>
      );
    }

    return raw;
  };

  const windowRangeText =
    section === 'report-retrofit'
      ? `快照 ${fmtShanghaiDate(data.asOf || data.generatedAt)}`
      : data.from && data.to
        ? `${fmtShanghaiDate(data.from)} ~ ${fmtShanghaiDate(data.to)}`
        : fmtShanghaiDate(data.asOf || data.generatedAt);

  return (
    <div className="relative space-y-3">
      {/* ── 1. Unified Compact Report Toolbar (Directly Above Data Table — Zero KPI Walls / Zero Dashboard Bloat) ── */}
      <section aria-label="报表查询与操作工具栏" className="arco-card overflow-visible">
        <div className="flex flex-wrap items-center justify-between gap-2.5 px-4 py-3 text-[12px] tabular">
          {/* Left: Scope + Lightweight Status Filters */}
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-semibold text-fg">{data.buildingName || '全部授权范围'}</span>
            <span className="text-[11px] text-muted">{windowRangeText}</span>

            {/* F21 5-Energy Compact Filter */}
            {section === 'report-consumption' && f21Energies.length > 0 && (
              <div className="inline-flex rounded-[2px] border border-line bg-fill-1 p-0.5 text-[11px]" role="tablist" aria-label="按能源筛选">
                <button
                  type="button"
                  data-f21-energy="all"
                  onClick={() => { setF21EnergyTab('all'); setPageIdx(0); }}
                  className={`rounded-[2px] px-2 py-0.5 ${f21EnergyTab === 'all' ? 'bg-surface font-medium text-primary shadow-xs' : 'text-fg-2 hover:text-fg'}`}
                >
                  全部
                </button>
                {f21Energies.map((en) => (
                  <button
                    key={en}
                    type="button"
                    data-f21-energy={en}
                    onClick={() => { setF21EnergyTab(en); setPageIdx(0); }}
                    className={`rounded-[2px] px-2 py-0.5 ${f21EnergyTab === en ? 'bg-surface font-medium text-primary shadow-xs' : 'text-fg-2 hover:text-fg'}`}
                  >
                    {energyLabels[en] ?? en}
                  </button>
                ))}
              </div>
            )}

            {/* F23 Assessment Group Compact Filter */}
            {section === 'report-assessment' && (
              <select
                aria-label="按考核分档筛选"
                value={f23Group}
                onChange={(e) => { setF23Group(e.target.value as AssessmentGroup); setPageIdx(0); }}
                className="arco-input !h-7 text-[11px]"
              >
                <option value="all">全部分档 ({rawRows.length})</option>
                <option value="excellent">优秀 (≥90)</option>
                <option value="qualified">达标 (70–89)</option>
                <option value="rectify">待整改 (&lt;70)</option>
                <option value="exempt">豁免对象</option>
                <option value="blocked">低质量阻断</option>
              </select>
            )}

            {/* F24 Verification Status Compact Filter */}
            {section === 'report-retrofit' && (
              <select
                aria-label="按核验状态筛选"
                value={f24StatusFilter}
                onChange={(e) => { setF24StatusFilter(e.target.value); setPageIdx(0); }}
                className="arco-input !h-7 text-[11px]"
              >
                <option value="all">全部状态 ({rawRows.length})</option>
                <option value="verified_simulated">模拟核验完成</option>
                <option value="pending_verification">待核验</option>
                <option value="estimated_only">仅模型估算</option>
              </select>
            )}

            {/* F25 Domain Switcher + Independent Field Config Drawer Trigger */}
            {section === 'report-custom' && (
              <>
                <div className="inline-flex rounded-[2px] border border-line bg-fill-1 p-0.5 text-[11px]">
                  {[
                    { id: 'metrics', label: '指标与定额域' },
                    { id: 'consumption', label: '能耗分项域' },
                  ].map((sc) => (
                    <button
                      key={sc.id}
                      type="button"
                      data-f25-scope={sc.id}
                      onClick={() => onScopeChange?.(sc.id)}
                      className={`rounded-[2px] px-2.5 py-0.5 ${
                        scope === sc.id ? 'bg-surface font-medium text-primary shadow-xs' : 'text-fg-2 hover:text-fg'
                      }`}
                    >
                      {sc.label}
                    </button>
                  ))}
                </div>
                <button
                  type="button"
                  data-f25-open-config
                  onClick={() => setCustomPanelOpen(true)}
                  className="inline-flex h-7 items-center gap-1.5 rounded-[2px] border border-primary/40 bg-primary-soft/50 px-2.5 text-[11px] font-medium text-primary hover:bg-primary-soft"
                >
                  <SlidersHorizontal className="h-3.5 w-3.5" aria-hidden="true" />
                  <span>字段配置 ({selectedFields.length}/{allowedFields.length})</span>
                </button>
              </>
            )}
          </div>

          {/* Right: Live Search, Column Settings, Print & CSV Export */}
          <div className="flex flex-wrap items-center gap-2">
            <input
              type="search"
              data-report-search
              value={searchQuery}
              onChange={(e) => { setSearchQuery(e.target.value); setPageIdx(0); }}
              placeholder="搜索名称 / 状态…"
              aria-label="筛选报表行"
              className="arco-input !h-7 w-40 text-[11px]"
            />

            {(sortKey || searchQuery) && (
              <button
                type="button"
                onClick={() => { setSortKey(null); setSearchQuery(''); setPageIdx(0); }}
                className="h-7 rounded-[2px] border border-line bg-surface px-2 text-[11px] text-fg-2 hover:bg-fill-1"
              >
                重置
              </button>
            )}

            {/* Column Settings Popover (F21-F24: Default 6-8 cols, toggle more cols on demand) */}
            {section !== 'report-custom' && (
              <div className="relative">
                <button
                  type="button"
                  data-col-settings-btn
                  onClick={() => setColMenuOpen((v) => !v)}
                  className="inline-flex h-7 items-center gap-1.5 rounded-[2px] border border-line bg-surface px-2.5 text-[11px] font-medium text-fg-2 hover:bg-fill-1 hover:text-fg"
                >
                  <Columns3 className="h-3.5 w-3.5 text-primary" aria-hidden="true" />
                  <span>列设置 ({activeColumns.length}/{allColumns.length})</span>
                </button>

                {colMenuOpen && (
                  <div
                    role="dialog"
                    aria-label="表格列显示设置"
                    data-col-settings-menu
                    className="absolute right-0 top-9 z-30 w-64 rounded-[4px] border border-line bg-surface p-3 shadow-xl"
                  >
                    <div className="mb-2 flex items-center justify-between border-b border-line pb-2 text-[11px]">
                      <span className="font-semibold text-fg">显示列设置</span>
                      <div className="flex items-center gap-2">
                        <button
                          type="button"
                          onClick={() => setVisibleColKeys(null)}
                          className="text-primary hover:underline"
                        >
                          默认精简
                        </button>
                        <button
                          type="button"
                          data-col-show-all
                          onClick={() => setVisibleColKeys(allColumns.map((c) => c.key))}
                          className="text-fg-2 hover:text-fg hover:underline"
                        >
                          全列
                        </button>
                      </div>
                    </div>
                    <div className="max-h-64 space-y-1 overflow-y-auto pr-1 text-[11px]">
                      {allColumns.map((col) => {
                        const checked = activeColumns.some((ac) => ac.key === col.key);
                        return (
                          <label
                            key={col.key}
                            className="flex cursor-pointer items-center justify-between gap-2 rounded-[2px] px-2 py-1 hover:bg-fill-1"
                          >
                            <span className="flex items-center gap-2 text-fg">
                              <input
                                type="checkbox"
                                checked={checked}
                                onChange={() => toggleDisplayCol(col.key)}
                                className="accent-primary"
                              />
                              <span>{col.label}</span>
                            </span>
                            {checked && <Check className="h-3 w-3 text-primary" aria-hidden="true" />}
                          </label>
                        );
                      })}
                    </div>
                  </div>
                )}
              </div>
            )}

            <button
              type="button"
              onClick={() => window.print()}
              className="inline-flex h-7 items-center gap-1 rounded-[2px] border border-line bg-surface px-2.5 text-[11px] text-fg-2 hover:bg-fill-1 hover:text-fg"
            >
              <Printer className="h-3.5 w-3.5" aria-hidden="true" />
              <span>打印</span>
            </button>

            <div className="relative">
              <button
                type="button"
                data-report-export-csv
                disabled={downloading}
                onClick={() => {
                  setColMenuOpen(false);
                  setExportMenuOpen((v) => !v);
                }}
                className="inline-flex h-7 items-center gap-1.5 rounded-[2px] bg-primary px-3 text-[11px] font-medium text-white hover:bg-primary/90 disabled:opacity-50"
              >
                <Download className="h-3.5 w-3.5" aria-hidden="true" />
                <span>{downloading ? '导出中…' : '导出 CSV'}</span>
                <ChevronDown className="h-3 w-3 opacity-85" aria-hidden="true" />
              </button>

              {exportMenuOpen && (
                <div
                  role="menu"
                  aria-label="选择 CSV 导出范围与字段"
                  data-export-csv-menu
                  className="absolute right-0 top-9 z-30 w-72 rounded-[4px] border border-line bg-surface p-1.5 shadow-xl"
                >
                  <div className="px-2.5 pb-1 pt-1 text-[10px] font-medium text-muted">
                    当前筛选与排序范围（{sortedRows.length} 条{totalPages > 1 ? ' · 跨全部页' : ''}）
                  </div>
                  <button
                    type="button"
                    role="menuitem"
                    data-export-visible-csv
                    onClick={() => handleDownloadFiltered('visible')}
                    className="flex w-full flex-col items-start rounded-[2px] px-2.5 py-2 text-left hover:bg-fill-1"
                  >
                    <span className="text-[12px] font-medium text-fg">
                      导出可见字段（{activeColumns.length} 列）
                    </span>
                    <span className="mt-0.5 text-[10px] text-muted">
                      遵循当前建筑、日期、筛选与排序
                    </span>
                  </button>
                  <button
                    type="button"
                    role="menuitem"
                    data-export-filtered-all-cols-csv
                    onClick={() => handleDownloadFiltered('all')}
                    className="flex w-full flex-col items-start rounded-[2px] px-2.5 py-2 text-left hover:bg-fill-1"
                  >
                    <span className="text-[12px] font-medium text-fg">
                      导出全部字段（{fullFieldCount} 列）
                    </span>
                    <span className="mt-0.5 text-[10px] text-muted">
                      保持当前筛选与排序，补全隐藏审计字段
                    </span>
                  </button>

                  <div className="mt-1 border-t border-line px-2.5 pb-1 pt-2 text-[10px] font-medium text-muted">
                    全部原始记录（不含前端筛选/搜索/排序）
                  </div>
                  <button
                    type="button"
                    role="menuitem"
                    data-export-full-raw-csv
                    onClick={handleDownloadFullRaw}
                    className="flex w-full flex-col items-start rounded-[2px] px-2.5 py-2 text-left hover:bg-fill-1"
                  >
                    <span className="text-[12px] font-medium text-fg-2">
                      导出全部原始记录（{rawRows.length} 条 · {fullFieldCount} 列）
                    </span>
                    <span className="mt-0.5 text-[10px] text-muted">
                      后端原始完整快照，忽略表内筛选与排序
                    </span>
                  </button>
                </div>
              )}
            </div>
          </div>
        </div>

        {csvErr && (
          <div role="alert" className="border-t border-negative/30 bg-negative-soft px-4 py-2 text-[12px] text-negative">
            {csvErr}
          </div>
        )}
      </section>

      {/* ── 2. Clean Primary HTML Data Table (Frozen First Column, Selectable Text, Right-Aligned Numerics, Pagination) ── */}
      <section aria-label="正式报表精确数据表" className="arco-card overflow-hidden">
        <div ref={tableWrapRef} className="w-full overflow-x-auto">
          <table data-report-table={section} className="w-full min-w-max border-collapse text-left text-[12px]">
            <thead className="sticky top-0 z-10 border-b border-line-strong bg-fill-1 text-fg-2">
              <tr>
                {activeColumns.map((c, idx) => {
                  const isNum = NUMERIC_KEYS.has(c.key);
                  const isFrozen = idx === 0;
                  const activeSort = sortKey === c.key;
                  return (
                    <th
                      key={c.key}
                      scope="col"
                      className={`whitespace-nowrap px-3.5 py-2.5 font-medium ${isNum ? 'text-right' : 'text-left'} ${
                        isFrozen ? 'sticky left-0 z-20 border-r border-line bg-fill-1' : ''
                      }`}
                    >
                      <button
                        type="button"
                        data-sort-col={c.key}
                        onClick={() => handleSort(c.key)}
                        className={`inline-flex items-center gap-1 hover:text-fg ${isNum ? 'flex-row-reverse' : ''} ${activeSort ? 'font-semibold text-primary' : ''}`}
                      >
                        <span>{c.label}</span>
                        {activeSort ? (
                          <span className="text-[10px] leading-none">{sortAsc ? '↑' : '↓'}</span>
                        ) : (
                          <ArrowUpDown className="h-3 w-3 shrink-0 opacity-35" aria-hidden="true" />
                        )}
                      </button>
                    </th>
                  );
                })}
                <th scope="col" className="w-14 whitespace-nowrap px-3 py-2.5 text-right font-medium text-muted">
                  详情
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line tabular">
              {pagedRows.length === 0 ? (
                <tr>
                  <td colSpan={activeColumns.length + 1} className="px-5 py-10 text-center text-[12px] text-muted">
                    当前筛选条件下暂无报表数据记录。
                  </td>
                </tr>
              ) : (
                pagedRows.map((row, rIdx) => {
                  const isTotalRow = row.category_id === 'TOTAL';
                  const rowBg = isTotalRow
                    ? 'bg-fill-1/80 font-semibold'
                    : rIdx % 2 === 1
                      ? 'bg-fill-1/20 hover:bg-fill-1/55'
                      : 'bg-surface hover:bg-fill-1/50';
                  const stickyBg = isTotalRow ? 'bg-fill-1' : rIdx % 2 === 1 ? 'bg-[#FAFBFC]' : 'bg-surface';

                  return (
                    <tr
                      key={rIdx}
                      onClick={() => {
                        if (window.getSelection()?.toString()) return;
                        setDetailRow(row);
                      }}
                      className={`cursor-pointer transition-colors ${rowBg}`}
                    >
                      {activeColumns.map((c, idx) => {
                        const isNum = NUMERIC_KEYS.has(c.key);
                        const isFrozen = idx === 0;
                        const isLongNote = c.key === 'benchmark_notice' || c.key === 'review_note' || c.key === 'mv_note';
                        return (
                          <td
                            key={c.key}
                            className={`px-3.5 py-2.5 ${
                              isNum
                                ? 'whitespace-nowrap text-right'
                                : isLongNote
                                  ? 'min-w-[180px] max-w-[280px] text-[11px] text-fg-2'
                                  : 'whitespace-nowrap text-fg'
                            } ${isFrozen ? `sticky left-0 z-[5] border-r border-line/70 font-medium ${stickyBg}` : ''}`}
                          >
                            {formatCell(c, row)}
                          </td>
                        );
                      })}
                      <td className="whitespace-nowrap px-3 py-2.5 text-right">
                        <button
                          type="button"
                          onClick={(e) => {
                            e.stopPropagation();
                            setDetailRow(row);
                          }}
                          aria-label="查看行详情与技术口径"
                          className="inline-flex items-center gap-1 rounded-[2px] px-1.5 py-0.5 text-[11px] text-primary hover:bg-primary-soft"
                        >
                          <Eye className="h-3 w-3" aria-hidden="true" />
                        </button>
                      </td>
                    </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </div>

        {/* Compact Footer: Summary + Horizontal Scroll + Page Size + Pagination */}
        <div className="flex flex-wrap items-center justify-between gap-2 border-t border-line bg-fill-1/35 px-4 py-2 text-[11px] text-muted tabular">
          <div className="flex flex-wrap items-center gap-2">
            <span>{footerSummary}</span>
            {canScrollH && (
              <span className="inline-flex items-center gap-1 rounded-[2px] border border-line bg-surface px-1.5 py-0 text-fg-2">
                <button type="button" aria-label="向左滚动" onClick={() => tableWrapRef.current?.scrollBy({ left: -320, behavior: 'smooth' })} className="px-0.5 hover:text-primary">◀</button>
                <span>横向</span>
                <button type="button" aria-label="向右滚动" onClick={() => tableWrapRef.current?.scrollBy({ left: 320, behavior: 'smooth' })} className="px-0.5 hover:text-primary">▶</button>
              </span>
            )}
          </div>

          <div className="flex items-center gap-2">
            <span>每页</span>
            <select
              aria-label="每页显示行数"
              value={pageSize}
              onChange={(e) => { setPageSize(Number(e.target.value)); setPageIdx(0); }}
              className="arco-input !h-6 min-w-[60px] text-[11px]"
            >
              <option value={10}>10</option>
              <option value={20}>20</option>
              <option value={50}>50</option>
            </select>
            {totalPages > 1 && (
              <>
                <span>第 {safePageIdx + 1}/{totalPages} 页</span>
                <button
                  type="button"
                  disabled={safePageIdx <= 0}
                  onClick={() => setPageIdx((p) => Math.max(0, p - 1))}
                  className="inline-flex h-6 items-center rounded-[2px] border border-line bg-surface px-1.5 text-fg-2 hover:bg-fill-1 disabled:opacity-40"
                >
                  <ChevronLeft className="h-3 w-3" aria-hidden="true" />
                </button>
                <button
                  type="button"
                  disabled={safePageIdx >= totalPages - 1}
                  onClick={() => setPageIdx((p) => Math.min(totalPages - 1, p + 1))}
                  className="inline-flex h-6 items-center rounded-[2px] border border-line bg-surface px-1.5 text-fg-2 hover:bg-fill-1 disabled:opacity-40"
                >
                  <ChevronRight className="h-3 w-3" aria-hidden="true" />
                </button>
              </>
            )}
          </div>
        </div>
      </section>

      {/* ── 3. F25 Independent Field Configuration Slide-Over Panel (Portaled to Body for Full Viewport Height) ── */}
      {section === 'report-custom' && customPanelOpen && createPortal(
        <div className="fixed inset-0 z-50 flex justify-end bg-slate-900/20" onClick={() => setCustomPanelOpen(false)}>
        <aside
          role="dialog"
          aria-label="F25 自定义报表字段配置"
          data-f25-config-drawer
          onClick={(e) => e.stopPropagation()}
          className="flex h-full w-full max-w-[400px] flex-col border-l border-line bg-surface shadow-2xl tabular"
        >
          <div className="flex items-center justify-between border-b border-line px-5 py-4">
            <div>
              <h3 className="text-[15px] font-semibold text-fg">自定义报表字段配置</h3>
              <p className="mt-0.5 text-[11px] text-muted">
                白名单已选 {selectedFields.length}/{allowedFields.length} 列 · 实时同步至表格与 CSV
              </p>
            </div>
            <button
              type="button"
              onClick={() => setCustomPanelOpen(false)}
              aria-label="关闭字段配置面板"
              className="rounded-[4px] p-1 text-muted hover:bg-fill-1 hover:text-fg"
            >
              <X className="h-4 w-4" aria-hidden="true" />
            </button>
          </div>

          <div className="flex-1 space-y-4 overflow-y-auto p-5 text-[12px]">
            <div className="flex flex-wrap gap-2">
              <button
                type="button"
                data-f25-preset="exec"
                onClick={() => applyCustomPreset(F25_DEFAULT_KEYS[scope] ?? F25_DEFAULT_KEYS.metrics)}
                className="rounded-[2px] border border-primary/40 bg-primary-soft/50 px-2.5 py-1 text-[11px] font-medium text-primary hover:bg-primary-soft"
              >
                精简业务视图 (7列)
              </button>
              <button
                type="button"
                data-f25-preset="all"
                onClick={() => applyCustomPreset(allowedFields.map((f) => f.key))}
                className="rounded-[2px] border border-line bg-surface px-2.5 py-1 text-[11px] text-fg-2 hover:bg-fill-1"
              >
                全部白名单字段 ({allowedFields.length}列)
              </button>
            </div>

            <div className="divide-y divide-line rounded-[4px] border border-line">
              {allowedFields.map((f) => {
                const checked = selectedFields.includes(f.key);
                return (
                  <label
                    key={f.key}
                    data-f25-field={f.key}
                    className="flex cursor-pointer items-center justify-between gap-2 px-3.5 py-2 hover:bg-fill-1/60"
                  >
                    <span className="flex items-center gap-2.5">
                      <input
                        type="checkbox"
                        checked={checked}
                        onChange={() => toggleCustomField(f.key)}
                        className="accent-primary"
                      />
                      <span className="font-medium text-fg">{f.label}</span>
                    </span>
                    <span className="font-mono text-[10px] text-muted">{f.key}</span>
                  </label>
                );
              })}
            </div>

            <p className="text-[11px] leading-relaxed text-muted">
              口径隔离说明：指标定额域与能耗分项域不可混排；导出 CSV 将严格按当前所选字段顺序与行值生成。
            </p>
          </div>
        </aside>
        </div>,
        document.body,
      )}

      {/* ── 4. Row Detail Slide-Over Drawer (Portaled to Body for Full Viewport Height) ── */}
      {detailRow && createPortal(
        <div className="fixed inset-0 z-50 flex justify-end bg-slate-900/20" onClick={() => setDetailRow(null)}>
        <aside
          role="dialog"
          aria-label="报表行详情与技术口径"
          data-report-detail-drawer
          onClick={(e) => e.stopPropagation()}
          className="flex h-full w-full max-w-[440px] flex-col border-l border-line bg-surface shadow-2xl tabular"
        >
          <div className="flex items-start justify-between gap-3 border-b border-line px-5 py-4">
            <div className="min-w-0">
              <span className="text-[11px] text-muted">报表完整审计字段 · {data.reportCode}</span>
              <h3 className="mt-0.5 truncate text-[15px] font-semibold text-fg">
                {detailRow.category_name || detailRow.metric_name || detailRow.object_name || detailRow.name || '记录详情'}
              </h3>
            </div>
            <button
              type="button"
              onClick={() => setDetailRow(null)}
              aria-label="关闭行详情抽屉"
              className="rounded-[4px] p-1 text-muted hover:bg-fill-1 hover:text-fg"
            >
              <X className="h-4 w-4" aria-hidden="true" />
            </button>
          </div>

          <div className="flex-1 space-y-4 overflow-y-auto p-5 text-[12px]">
            {section === 'report-retrofit' && detailRow.retrofit_id && f24Periods[detailRow.retrofit_id] && (
              <div className="rounded-[4px] bg-fill-1/60 p-3 text-[11px] text-fg-2">
                <div>基线统计期：{f24Periods[detailRow.retrofit_id].baselineFrom} ~ {f24Periods[detailRow.retrofit_id].baselineTo}</div>
                <div className="mt-1">M&amp;V 核验报告期：{f24Periods[detailRow.retrofit_id].reportFrom} ~ {f24Periods[detailRow.retrofit_id].reportTo}</div>
              </div>
            )}

            <dl className="divide-y divide-line rounded-[4px] border border-line">
              {allColumns.map((col) => (
                <div key={col.key} className="flex items-start justify-between gap-3 px-3.5 py-2">
                  <dt className="shrink-0 text-[11px] text-muted">{col.label}</dt>
                  <dd className="text-right text-[12px] text-fg">{formatCell(col, detailRow)}</dd>
                </div>
              ))}
            </dl>

            {section === 'report-consumption' && detailF21Meters.length > 0 && (
              <div>
                <h4 className="mb-2 text-[12px] font-medium text-fg">对应计量来源（{detailF21Meters.length} 块表）</h4>
                <ul className="divide-y divide-line rounded-[4px] border border-line text-[11px]">
                  {detailF21Meters.map(({ source, weightPct }) => (
                    <li key={`${source.terminalId}-${source.itemCode}`} className="px-3 py-2">
                      <div className="flex items-center justify-between gap-2">
                        <span className="font-medium text-fg">{source.terminalName}</span>
                        <span className="font-semibold text-fg">{fmtNum(source.usage)} {source.unit}</span>
                      </div>
                      <div className="mt-0.5 flex items-center justify-between text-[10px] text-muted">
                        <span className="font-mono">{source.terminalId} · {source.sourceTable}</span>
                        <span>权重 {weightPct.toFixed(0)}% · 有效 {source.valid}/{source.expected}</span>
                      </div>
                    </li>
                  ))}
                </ul>
              </div>
            )}

            {data.disclaimer && (
              <p className="text-[11px] leading-relaxed text-muted">{data.disclaimer}</p>
            )}
          </div>
        </aside>
        </div>,
        document.body,
      )}
    </div>
  );
}
