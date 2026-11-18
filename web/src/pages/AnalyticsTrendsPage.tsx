import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  BarChart3,
  Check,
  Coins,
  Download,
  FileSpreadsheet,
  Gauge,
  RefreshCw,
  TrendingUp,
} from 'lucide-react';
import {
  ApiError,
  fetchAnalyticsSeries,
  fetchList,
  type AnalyticsAggBucket,
  type AnalyticsCompare,
  type AnalyticsMode,
  type AnalyticsSeriesPayload,
  type AnalyticsSource,
  type AnalyticsTerminalSeries,
} from '../api/client';
import { formatDateTime } from '../lib/format';

interface DeviceTypeRow {
  id: number;
  code: string;
  name: string;
  domain_id?: number;
}

interface PropertyRow {
  id: number;
  type_id: number;
  item_code: string;
  name: string;
  value_type: string;
  unit?: string;
  precision?: number;
  min_value?: number;
  max_value?: number;
}

interface TerminalRow {
  id: string;
  name: string;
  type_id: number;
  gateway_id: string;
  online: boolean;
  enabled: boolean;
}

const SOURCE_OPTIONS: Array<{
  value: AnalyticsSource;
  label: string;
  defaultHours: number;
}> = [
  { value: 'hour', label: '逐时快照 (Hour)', defaultHours: 24 },
  { value: 'day', label: '逐日快照 (Day)', defaultHours: 30 * 24 },
  { value: 'month', label: '逐月快照 (Month)', defaultHours: 365 * 24 },
  { value: 'year', label: '逐年快照 (Year)', defaultHours: 3 * 365 * 24 },
  { value: 'raw', label: '原始接入流水 (Raw)', defaultHours: 6 },
];

const SERIES_COLORS = [
  '#2563eb',
  '#059669',
  '#d97706',
  '#7c3aed',
  '#db2777',
  '#0891b2',
  '#ea580c',
  '#4f46e5',
];

const TOU_META: Record<
  'sharp' | 'peak' | 'flat' | 'valley',
  { label: string; color: string; badge: string }
> = {
  sharp: { label: '尖峰', color: '#dc2626', badge: 'bg-danger/15 text-danger' },
  peak: { label: '高峰', color: '#d97706', badge: 'bg-warning/15 text-warning' },
  flat: { label: '平段', color: '#2563eb', badge: 'bg-accent/15 text-accent' },
  valley: { label: '低谷', color: '#059669', badge: 'bg-success/15 text-success' },
};

function fmtVal(v: number | undefined | null, precision = 2): string {
  if (v === undefined || v === null || Number.isNaN(v)) return '—';
  return Number(v).toLocaleString('zh-CN', {
    minimumFractionDigits: 0,
    maximumFractionDigits: precision,
  });
}

function toLocalInput(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export function AnalyticsTrendsPage() {
  const [deviceTypes, setDeviceTypes] = useState<DeviceTypeRow[]>([]);
  const [properties, setProperties] = useState<PropertyRow[]>([]);
  const [terminals, setTerminals] = useState<TerminalRow[]>([]);

  const [selectedTypeId, setSelectedTypeId] = useState<number>(0);
  const [selectedItemCode, setSelectedItemCode] = useState<string>('');
  const [source, setSource] = useState<AnalyticsSource>('hour');
  const [mode, setMode] = useState<AnalyticsMode>('delta');
  const [compare, setCompare] = useState<AnalyticsCompare>('mom');
  const [selectedTermIds, setSelectedTermIds] = useState<string[]>([]);

  const [fromIso, setFromIso] = useState<string>(() =>
    new Date(Date.now() - 24 * 3600_000).toISOString(),
  );
  const [toIso, setToIso] = useState<string>(() => new Date().toISOString());

  const [payload, setPayload] = useState<AnalyticsSeriesPayload | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // 5 大功能模块 Tab（对齐手册 4.6 趋势 / 4.7 同环比与对比 / 4.8 费用与尖峰平谷 / 4.9 能耗报表 / 4.10 空调能效）
  const [activeTab, setActiveTab] = useState<
    'trend' | 'compare' | 'cost' | 'report' | 'cop'
  >('trend');

  // 4.8 费用与尖峰平谷参数（支持固定单价与分时电价、建筑面积、用能人数）
  const [priceSharp, setPriceSharp] = useState<number>(1.25);
  const [pricePeak, setPricePeak] = useState<number>(0.98);
  const [priceFlat, setPriceFlat] = useState<number>(0.68);
  const [priceValley, setPriceValley] = useState<number>(0.36);
  const [buildingArea, setBuildingArea] = useState<number>(12000);
  const [occupantCount, setOccupantCount] = useState<number>(300);

  // 4.10 空调/系统能效参数
  const [copInputTermId, setCopInputTermId] = useState<string>('');
  const [copOutputTermId, setCopOutputTermId] = useState<string>('');
  const [copDesignTarget, setCopDesignTarget] = useState<number>(3.6);
  const [copCoolingFactor, setCopCoolingFactor] = useState<number>(3.5);

  // 1) 初始化加载物模型列表
  useEffect(() => {
    const ctrl = new AbortController();
    fetchList<DeviceTypeRow>('entity', 'thing-models', { pageSize: 100 }, ctrl.signal)
      .then((res) => {
        setDeviceTypes(res.rows);
        if (res.rows.length > 0) {
          setSelectedTypeId((prev) => (prev > 0 ? prev : res.rows[0].id));
        }
      })
      .catch(() => {});
    return () => ctrl.abort();
  }, []);

  // 2) 当物模型切换时，加载该物模型的属性与下属终端
  useEffect(() => {
    if (!selectedTypeId) return;
    const ctrl = new AbortController();
    Promise.all([
      fetchList<PropertyRow>(
        'entity',
        'thing-model-properties',
        { pageSize: 100, type_id: String(selectedTypeId) },
        ctrl.signal,
      ),
      fetchList<TerminalRow>(
        'entity',
        'terminals',
        { pageSize: 100, type_id: String(selectedTypeId) },
        ctrl.signal,
      ),
    ])
      .then(([propRes, termRes]) => {
        setProperties(propRes.rows);
        setTerminals(termRes.rows);
        if (propRes.rows.length > 0) {
          setSelectedItemCode((prev) =>
            propRes.rows.some((p) => p.item_code === prev)
              ? prev
              : propRes.rows[0].item_code,
          );
        } else {
          setSelectedItemCode('');
        }
        const defaultIds = termRes.rows.slice(0, 6).map((t) => t.id);
        setSelectedTermIds(defaultIds);
        if (termRes.rows.length > 0) {
          setCopInputTermId(termRes.rows[0].id);
          setCopOutputTermId(termRes.rows[Math.min(1, termRes.rows.length - 1)].id);
        }
      })
      .catch(() => {});
    return () => ctrl.abort();
  }, [selectedTypeId]);

  const handleSourceChange = useCallback((nextSource: AnalyticsSource) => {
    setSource(nextSource);
    const opt = SOURCE_OPTIONS.find((o) => o.value === nextSource);
    if (opt) {
      const now = new Date();
      setFromIso(new Date(now.getTime() - opt.defaultHours * 3600_000).toISOString());
      setToIso(now.toISOString());
    }
  }, []);

  const loadSeries = useCallback(
    async (signal?: AbortSignal) => {
      if (!selectedTypeId || !selectedItemCode) return;
      setLoading(true);
      setError(null);
      try {
        const res = await fetchAnalyticsSeries(
          {
            typeId: selectedTypeId,
            itemCode: selectedItemCode,
            source,
            mode,
            compare,
            from: fromIso,
            to: toIso,
            terminalIds: selectedTermIds.length > 0 ? selectedTermIds : undefined,
          },
          signal,
        );
        if (!signal?.aborted) {
          setPayload(res);
        }
      } catch (err) {
        if (!signal?.aborted) {
          setError(err instanceof ApiError ? err.message : String(err));
        }
      } finally {
        if (!signal?.aborted) setLoading(false);
      }
    },
    [selectedTypeId, selectedItemCode, source, mode, compare, fromIso, toIso, selectedTermIds],
  );

  useEffect(() => {
    const ctrl = new AbortController();
    void loadSeries(ctrl.signal);
    return () => ctrl.abort();
  }, [loadSeries]);

  const unit = payload?.property.unit || '';
  const precision = payload?.property.precision ?? 2;

  const toggleTerminal = (id: string) => {
    setSelectedTermIds((prev) =>
      prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id].slice(0, 20),
    );
  };

  // 构建 4.9.2 能耗交叉明细报表的时间槽列集合
  const reportSlotTimes = useMemo(() => {
    if (!payload) return [];
    const set = new Set<string>();
    payload.series.forEach((s) => s.points.forEach((p) => set.add(p.time)));
    return Array.from(set).sort();
  }, [payload]);

  // 导出 4.9.2 二维能耗交叉报表 CSV (Excel)
  const exportCrossReportCsv = useCallback(() => {
    if (!payload) return;
    const slotHeaders = reportSlotTimes.map((t) => t.slice(5, 16).replace('T', ' '));
    const headers = [
      '终端ID',
      '终端名称',
      '所属网关',
      '期初读数',
      '期末读数',
      mode === 'delta' ? `区间总用能(${unit})` : `读数均值(${unit})`,
      `上期用能(${unit})`,
      '同环比(%)',
      '超均值50%异常数',
      '表码倒走数',
      ...slotHeaders,
    ];
    const rows = payload.series.map((s) => {
      const slotMap = new Map<string, string>();
      s.points.forEach((p) => {
        const v = mode === 'delta' ? p.delta : p.num;
        slotMap.set(p.time, v !== undefined ? v.toFixed(precision) : '');
      });
      const mainStat = mode === 'delta' ? s.stats.usageSum : s.stats.avg;
      return [
        s.terminalId,
        s.terminalName || s.terminalId,
        s.gatewayId,
        s.stats.first !== undefined ? s.stats.first.toFixed(precision) : '',
        s.stats.last !== undefined ? s.stats.last.toFixed(precision) : '',
        mainStat !== undefined ? mainStat.toFixed(precision) : '',
        s.stats.prevUsageSum !== undefined ? s.stats.prevUsageSum.toFixed(precision) : '',
        s.stats.compareRate !== undefined ? s.stats.compareRate.toFixed(2) : '',
        String(s.stats.spikeCount ?? 0),
        String(s.stats.rollbackCount ?? 0),
        ...reportSlotTimes.map((t) => slotMap.get(t) ?? ''),
      ];
    });
    const csv =
      '\uFEFF' +
      [
        headers.join(','),
        ...rows.map((r) => r.map((c) => `"${String(c).replace(/"/g, '""')}"`).join(',')),
      ].join('\n');
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `能耗分项明细报表_${payload.property.itemCode}_${source}_${new Date().toISOString().slice(0, 10)}.csv`;
    a.click();
    URL.revokeObjectURL(url);
  }, [payload, reportSlotTimes, mode, unit, precision, source]);

  // 4.8 费用与尖峰平谷计算汇总
  const costSummary = useMemo(() => {
    const tou = payload?.fleetStats.touBreakdown ?? {
      sharpUsage: 0,
      peakUsage: 0,
      flatUsage: 0,
      valleyUsage: 0,
    };
    const sharpCost = tou.sharpUsage * priceSharp;
    const peakCost = tou.peakUsage * pricePeak;
    const flatCost = tou.flatUsage * priceFlat;
    const valleyCost = tou.valleyUsage * priceValley;
    const totalUsage =
      tou.sharpUsage + tou.peakUsage + tou.flatUsage + tou.valleyUsage;
    const totalCost = sharpCost + peakCost + flatCost + valleyCost;
    const areaCost = buildingArea > 0 ? totalCost / buildingArea : 0;
    const perCapitaCost = occupantCount > 0 ? totalCost / occupantCount : 0;
    const areaUsage = buildingArea > 0 ? totalUsage / buildingArea : 0;
    const perCapitaUsage = occupantCount > 0 ? totalUsage / occupantCount : 0;
    return {
      tou,
      sharpCost,
      peakCost,
      flatCost,
      valleyCost,
      totalUsage,
      totalCost,
      areaCost,
      perCapitaCost,
      areaUsage,
      perCapitaUsage,
    };
  }, [payload, priceSharp, pricePeak, priceFlat, priceValley, buildingArea, occupantCount]);

  return (
    <div className="space-y-4">
      {/* 顶部筛选与计算口径控制面板 */}
      <div className="rounded-lg border border-border bg-elevated p-4 space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <h1 className="text-base font-semibold text-ink">
              能耗统计、同环比、尖峰平谷费用与明细报表
            </h1>
            <p className="text-xs text-muted">
              支持「累计表码差值用量 (Δ = 期末 - 期初)」与「原始/瞬时读数」双模式切换，覆盖趋势、同环比、多表对比、尖峰平谷费用、交叉报表与空调能效
            </p>
          </div>

          <div className="flex items-center gap-2">
            {/* 双模式切换开关 */}
            <div className="inline-flex rounded-md border border-border bg-surface p-0.5">
              <button
                type="button"
                onClick={() => setMode('delta')}
                className={`rounded px-2.5 py-1 text-xs font-medium transition-colors ${
                  mode === 'delta'
                    ? 'bg-accent text-white'
                    : 'text-muted hover:text-ink'
                }`}
              >
                差值用量模式 (Δ=期末-期初)
              </button>
              <button
                type="button"
                onClick={() => setMode('reading')}
                className={`rounded px-2.5 py-1 text-xs font-medium transition-colors ${
                  mode === 'reading'
                    ? 'bg-accent text-white'
                    : 'text-muted hover:text-ink'
                }`}
              >
                原始/瞬时读数模式
              </button>
            </div>

            <button
              type="button"
              onClick={() => void loadSeries()}
              disabled={loading}
              className="inline-flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-xs font-medium text-white hover:bg-accent/90 disabled:opacity-50"
            >
              <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} />
              查询分析
            </button>
          </div>
        </div>

        {/* 过滤条件栅格 */}
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-6">
          <div>
            <label className="mb-1 block text-xs font-medium text-muted">分类/物模型</label>
            <select
              value={selectedTypeId}
              onChange={(e) => setSelectedTypeId(Number(e.target.value))}
              className="w-full rounded-md border border-border bg-surface px-2.5 py-1.5 text-xs text-ink"
            >
              {deviceTypes.map((dt) => (
                <option key={dt.id} value={dt.id}>
                  {dt.name} ({dt.code})
                </option>
              ))}
            </select>
          </div>

          <div>
            <label className="mb-1 block text-xs font-medium text-muted">计量/监测字段</label>
            <select
              value={selectedItemCode}
              onChange={(e) => setSelectedItemCode(e.target.value)}
              className="w-full rounded-md border border-border bg-surface px-2.5 py-1.5 text-xs text-ink"
            >
              {properties.map((p) => (
                <option key={p.item_code} value={p.item_code}>
                  {p.name} ({p.item_code}{p.unit ? ` · ${p.unit}` : ''})
                </option>
              ))}
            </select>
          </div>

          <div>
            <label className="mb-1 block text-xs font-medium text-muted">统计周期粒度</label>
            <select
              value={source}
              onChange={(e) => handleSourceChange(e.target.value as AnalyticsSource)}
              className="w-full rounded-md border border-border bg-surface px-2.5 py-1.5 text-xs text-ink"
            >
              {SOURCE_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
          </div>

          <div>
            <label className="mb-1 block text-xs font-medium text-muted">同环比基准 (4.7.1)</label>
            <select
              value={compare}
              onChange={(e) => setCompare(e.target.value as AnalyticsCompare)}
              className="w-full rounded-md border border-border bg-surface px-2.5 py-1.5 text-xs text-ink"
            >
              <option value="mom">环比上一等长周期 (MoM)</option>
              <option value="yoy">同比去年同期 (YoY)</option>
              <option value="none">不启用同环比</option>
            </select>
          </div>

          <div>
            <label className="mb-1 block text-xs font-medium text-muted">起始时间</label>
            <input
              type="datetime-local"
              value={toLocalInput(fromIso)}
              onChange={(e) => {
                const d = new Date(e.target.value);
                if (!Number.isNaN(d.getTime())) setFromIso(d.toISOString());
              }}
              className="w-full rounded-md border border-border bg-surface px-2 py-1.5 font-mono text-xs text-ink"
            />
          </div>

          <div>
            <label className="mb-1 block text-xs font-medium text-muted">结束时间</label>
            <input
              type="datetime-local"
              value={toLocalInput(toIso)}
              onChange={(e) => {
                const d = new Date(e.target.value);
                if (!Number.isNaN(d.getTime())) setToIso(d.toISOString());
              }}
              className="w-full rounded-md border border-border bg-surface px-2 py-1.5 font-mono text-xs text-ink"
            />
          </div>
        </div>

        {/* 终端表计多选芯片栏 */}
        {terminals.length > 0 && (
          <div className="flex flex-wrap items-center gap-1.5 border-t border-border pt-2.5">
            <span className="text-xs font-medium text-muted">对比表计 ({selectedTermIds.length}/{terminals.length})：</span>
            {terminals.map((t) => {
              const checked = selectedTermIds.includes(t.id);
              return (
                <button
                  key={t.id}
                  type="button"
                  onClick={() => toggleTerminal(t.id)}
                  className={`inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-xs transition-colors ${
                    checked
                      ? 'border-accent bg-accent/10 font-medium text-accent'
                      : 'border-border bg-surface text-muted hover:text-ink'
                  }`}
                >
                  {checked && <Check className="h-3 w-3" />}
                  <span>{t.name || t.id}</span>
                </button>
              );
            })}
          </div>
        )}
      </div>

      {error && (
        <div className="rounded-lg border border-danger/30 bg-danger/10 px-4 py-3 text-xs text-danger">
          查询失败：{error}
        </div>
      )}

      {/* 顶部全局 KPI 指标卡 */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
        <div className="rounded-lg border border-border bg-elevated p-3">
          <div className="text-xs text-muted">
            {mode === 'delta' ? '区间累计总用能 (ΣΔ)' : '全局读数均值'}
          </div>
          <div className="mt-1 font-mono text-lg font-bold text-ink">
            {fmtVal(
              mode === 'delta' ? payload?.fleetStats.totalUsage : payload?.fleetStats.avg,
              precision,
            )}{' '}
            <span className="text-xs font-normal text-muted">{unit}</span>
          </div>
          <div className="mt-0.5 text-[11px] text-muted">
            覆盖 {payload?.fleetStats.terminalCount ?? 0} 台终端 ({payload?.fleetStats.totalPoints ?? 0} 点)
          </div>
        </div>

        <div className="rounded-lg border border-border bg-elevated p-3">
          <div className="text-xs text-muted">
            {compare === 'yoy' ? '同比上期总用能' : '环比上期总用能'}
          </div>
          <div className="mt-1 font-mono text-lg font-bold text-ink">
            {fmtVal(payload?.fleetStats.prevTotalUsage, precision)}{' '}
            <span className="text-xs font-normal text-muted">{unit}</span>
          </div>
          <div className="mt-0.5 text-[11px]">
            {payload?.fleetStats.compareRate !== undefined ? (
              <span
                className={
                  payload.fleetStats.compareRate > 0 ? 'font-medium text-danger' : 'font-medium text-success'
                }
              >
                {compare === 'yoy' ? '同比' : '环比'} {payload.fleetStats.compareRate > 0 ? '+' : ''}
                {payload.fleetStats.compareRate.toFixed(1)}%
              </span>
            ) : (
              <span className="text-muted">对比基准 —</span>
            )}
          </div>
        </div>

        <div className="rounded-lg border border-border bg-elevated p-3">
          <div className="text-xs text-muted">读数区间极值 (Min / Max)</div>
          <div className="mt-1 font-mono text-sm font-bold text-ink">
            {fmtVal(payload?.fleetStats.min, precision)} ~ {fmtVal(payload?.fleetStats.max, precision)}
          </div>
          <div className="mt-0.5 text-[11px] text-muted">单位：{unit || '无量纲'}</div>
        </div>

        <div className="rounded-lg border border-border bg-elevated p-3">
          <div className="text-xs text-muted">估算总成本费用 (4.8)</div>
          <div className="mt-1 font-mono text-lg font-bold text-ink">
            ¥{fmtVal(costSummary.totalCost, 2)}
          </div>
          <div className="mt-0.5 text-[11px] text-muted">
            单位面积 ¥{fmtVal(costSummary.areaCost, 2)}/m² · 人均 ¥{fmtVal(costSummary.perCapitaCost, 1)}
          </div>
        </div>

        <div className="rounded-lg border border-border bg-elevated p-3">
          <div className="text-xs text-muted">超均值50%突增 (4.6.3)</div>
          <div className="mt-1 font-mono text-lg font-bold text-warning">
            {payload?.fleetStats.spikeCount ?? 0} <span className="text-xs font-normal">次</span>
          </div>
          <div className="mt-0.5 text-[11px] text-muted">单槽用能超出均值 50%</div>
        </div>

        <div className="rounded-lg border border-border bg-elevated p-3">
          <div className="text-xs text-muted">表码倒走 & 质量违规</div>
          <div className="mt-1 font-mono text-lg font-bold text-danger">
            {(payload?.fleetStats.rollbackCount ?? 0) + (payload?.fleetStats.badQualityCount ?? 0)}{' '}
            <span className="text-xs font-normal">次</span>
          </div>
          <div className="mt-0.5 text-[11px] text-muted">
            倒走 {payload?.fleetStats.rollbackCount ?? 0} · 质量越界 {payload?.fleetStats.badQualityCount ?? 0}
          </div>
        </div>
      </div>

      {/* 5 大核心能耗模块子页签切换 */}
      <div className="rounded-lg border border-border bg-elevated">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-2.5">
          <div className="flex flex-wrap items-center gap-1">
            <button
              type="button"
              onClick={() => setActiveTab('trend')}
              className={`inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                activeTab === 'trend'
                  ? 'bg-accent text-white'
                  : 'text-muted hover:bg-surface hover:text-ink'
              }`}
            >
              <TrendingUp className="h-3.5 w-3.5" />
              趋势与同环比 (4.6.5 / 4.7.1)
            </button>

            <button
              type="button"
              onClick={() => setActiveTab('compare')}
              className={`inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                activeTab === 'compare'
                  ? 'bg-accent text-white'
                  : 'text-muted hover:bg-surface hover:text-ink'
              }`}
            >
              <BarChart3 className="h-3.5 w-3.5" />
              多表对比与占比 (4.6.4 / 4.7.2)
            </button>

            <button
              type="button"
              onClick={() => setActiveTab('cost')}
              className={`inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                activeTab === 'cost'
                  ? 'bg-accent text-white'
                  : 'text-muted hover:bg-surface hover:text-ink'
              }`}
            >
              <Coins className="h-3.5 w-3.5" />
              费用与尖峰平谷分析 (4.8)
            </button>

            <button
              type="button"
              onClick={() => setActiveTab('report')}
              className={`inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                activeTab === 'report'
                  ? 'bg-accent text-white'
                  : 'text-muted hover:bg-surface hover:text-ink'
              }`}
            >
              <FileSpreadsheet className="h-3.5 w-3.5" />
              分项能耗明细报表 (4.9)
            </button>

            <button
              type="button"
              onClick={() => setActiveTab('cop')}
              className={`inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                activeTab === 'cop'
                  ? 'bg-accent text-white'
                  : 'text-muted hover:bg-surface hover:text-ink'
              }`}
            >
              <Gauge className="h-3.5 w-3.5" />
              空调/系统能效 COP (4.10)
            </button>
          </div>

          <button
            type="button"
            onClick={exportCrossReportCsv}
            disabled={!payload}
            className="inline-flex items-center gap-1.5 rounded-md border border-border bg-surface px-2.5 py-1 text-xs font-medium text-ink hover:bg-elevated disabled:opacity-50"
          >
            <Download className="h-3.5 w-3.5" />
            导出 Excel 报表 (CSV)
          </button>
        </div>

        {/* ── Tab 1: 能耗趋势与同环比分析 (4.6.5 & 4.7.1) ────────────────────── */}
        {activeTab === 'trend' && (
          <div className="space-y-6 p-4">
            <div>
              <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
                <div>
                  <h3 className="text-sm font-semibold text-ink">
                    多表{mode === 'delta' ? '区间用能 (Δ)' : '原始读数'}变化曲线
                  </h3>
                  <p className="text-xs text-muted">
                    红色菱形标记「超均值 50% 用能突增点 (4.6.3)」，橙色圆点标记「表码倒走/质量异常」
                  </p>
                </div>
                <div className="flex flex-wrap items-center gap-3 text-xs">
                  {(payload?.series ?? []).map((s, idx) => (
                    <span key={s.terminalId} className="inline-flex items-center gap-1.5">
                      <span
                        className="h-2.5 w-2.5 rounded-full"
                        style={{ backgroundColor: SERIES_COLORS[idx % SERIES_COLORS.length] }}
                      />
                      <span className="text-ink">{s.terminalName || s.terminalId}</span>
                    </span>
                  ))}
                </div>
              </div>
              <MultiSeriesLineChart
                series={payload?.series ?? []}
                mode={mode}
                unit={unit}
                precision={precision}
                minBound={mode === 'reading' ? payload?.property.minValue : undefined}
                maxBound={mode === 'reading' ? payload?.property.maxValue : undefined}
              />
            </div>

            {/* 4.7.1 区域/支路同环比对比柱状图与明细表 */}
            <div className="border-t border-border pt-4">
              <div className="mb-3 flex items-center justify-between">
                <div>
                  <h3 className="text-sm font-semibold text-ink">
                    同环比柱状对比与时段增幅明细 (4.7.1)
                  </h3>
                  <p className="text-xs text-muted">
                    当期时段 ({formatDateTime(fromIso)} ~ {formatDateTime(toIso)}) vs{' '}
                    {compare === 'yoy' ? '同比去年同期' : '环比上一周期'}
                  </p>
                </div>
              </div>
              <YoYMoMBarChart
                buckets={payload?.aggregatedBuckets ?? []}
                unit={unit}
                precision={precision}
              />
            </div>
          </div>
        )}

        {/* ── Tab 2: 多表横向对比与用量占比 (4.6.4 & 4.7.2) ─────────────────── */}
        {activeTab === 'compare' && (
          <div className="space-y-6 p-4">
            <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
              {/* 左侧 2/3：多表横向对比柱状图 */}
              <div className="lg:col-span-2">
                <h3 className="text-sm font-semibold text-ink">
                  多表/多区域同一时段用能横向对比柱状图 (4.7.2)
                </h3>
                <p className="mb-3 text-xs text-muted">
                  对比各选中表计在本期与上期的累计用能量 ({unit || '标准单位'})
                </p>
                <div className="space-y-3">
                  {(payload?.series ?? []).map((s, idx) => {
                    const curVal =
                      mode === 'delta' ? (s.stats.usageSum ?? 0) : (s.stats.avg ?? 0);
                    const prevVal = s.stats.prevUsageSum ?? 0;
                    const maxInSeries = Math.max(
                      1e-6,
                      ...(payload?.series ?? []).map((item) =>
                        Math.max(
                          mode === 'delta' ? (item.stats.usageSum ?? 0) : (item.stats.avg ?? 0),
                          item.stats.prevUsageSum ?? 0,
                        ),
                      ),
                    );
                    return (
                      <div
                        key={s.terminalId}
                        className="rounded-lg border border-border/70 bg-surface/50 p-3"
                      >
                        <div className="mb-1.5 flex items-center justify-between text-xs">
                          <div className="flex items-center gap-2">
                            <span
                              className="h-2.5 w-2.5 rounded-xs"
                              style={{
                                backgroundColor: SERIES_COLORS[idx % SERIES_COLORS.length],
                              }}
                            />
                            <span className="font-semibold text-ink">
                              {s.terminalName || s.terminalId}
                            </span>
                            <span className="font-mono text-muted">({s.gatewayId})</span>
                          </div>
                          <div className="flex items-center gap-3 font-mono">
                            <span className="font-bold text-ink">
                              本期: {fmtVal(curVal, precision)} {unit}
                            </span>
                            {compare !== 'none' && (
                              <span className="text-muted">
                                上期: {fmtVal(prevVal, precision)} {unit}
                              </span>
                            )}
                            {s.stats.compareRate !== undefined && (
                              <span
                                className={
                                  s.stats.compareRate > 0 ? 'text-danger' : 'text-success'
                                }
                              >
                                {s.stats.compareRate > 0 ? '+' : ''}
                                {s.stats.compareRate.toFixed(1)}%
                              </span>
                            )}
                          </div>
                        </div>
                        <div className="space-y-1">
                          <div className="h-2.5 w-full overflow-hidden rounded-full bg-elevated">
                            <div
                              className="h-full rounded-full"
                              style={{
                                width: `${Math.min(100, Math.max(2, (curVal / maxInSeries) * 100))}%`,
                                backgroundColor: SERIES_COLORS[idx % SERIES_COLORS.length],
                              }}
                            />
                          </div>
                          {compare !== 'none' && (
                            <div className="h-1.5 w-full overflow-hidden rounded-full bg-elevated">
                              <div
                                className="h-full rounded-full bg-muted/50"
                                style={{
                                  width: `${Math.min(100, Math.max(1, (prevVal / maxInSeries) * 100))}%`,
                                }}
                              />
                            </div>
                          )}
                        </div>
                      </div>
                    );
                  })}
                </div>
              </div>

              {/* 右侧 1/3：多表用能占比饼图 (4.6.5 饼图) */}
              <div className="rounded-lg border border-border bg-surface/40 p-4">
                <h3 className="text-sm font-semibold text-ink">各表计/支路用能占比饼图 (4.6.5)</h3>
                <p className="mb-4 text-xs text-muted">展示选中表计在当前总用能中的构成比例</p>
                <TerminalSharePie
                  series={payload?.series ?? []}
                  mode={mode}
                  unit={unit}
                  precision={precision}
                />
              </div>
            </div>
          </div>
        )}

        {/* ── Tab 3: 费用与尖峰平谷分析 (4.8 费用分析) ───────────────────────── */}
        {activeTab === 'cost' && (
          <div className="space-y-6 p-4">
            {/* 单价、尖峰平谷分时电价、建筑面积与用能人数配置栏 */}
            <div className="grid grid-cols-2 gap-3 rounded-lg border border-border bg-surface p-3 sm:grid-cols-3 lg:grid-cols-6">
              <div>
                <label className="mb-1 block text-[11px] font-medium text-danger">
                  尖峰单价 (10-12,19-21时)
                </label>
                <div className="flex items-center gap-1">
                  <input
                    type="number"
                    step="0.01"
                    value={priceSharp}
                    onChange={(e) => setPriceSharp(Math.max(0, Number(e.target.value) || 0))}
                    className="w-full rounded border border-border bg-elevated px-2 py-1 font-mono text-xs text-ink"
                  />
                  <span className="text-xs text-muted">元</span>
                </div>
              </div>

              <div>
                <label className="mb-1 block text-[11px] font-medium text-warning">
                  高峰单价 (08-10,14-19时)
                </label>
                <div className="flex items-center gap-1">
                  <input
                    type="number"
                    step="0.01"
                    value={pricePeak}
                    onChange={(e) => setPricePeak(Math.max(0, Number(e.target.value) || 0))}
                    className="w-full rounded border border-border bg-elevated px-2 py-1 font-mono text-xs text-ink"
                  />
                  <span className="text-xs text-muted">元</span>
                </div>
              </div>

              <div>
                <label className="mb-1 block text-[11px] font-medium text-accent">
                  平段单价 (12-14,21-23时)
                </label>
                <div className="flex items-center gap-1">
                  <input
                    type="number"
                    step="0.01"
                    value={priceFlat}
                    onChange={(e) => setPriceFlat(Math.max(0, Number(e.target.value) || 0))}
                    className="w-full rounded border border-border bg-elevated px-2 py-1 font-mono text-xs text-ink"
                  />
                  <span className="text-xs text-muted">元</span>
                </div>
              </div>

              <div>
                <label className="mb-1 block text-[11px] font-medium text-success">
                  低谷单价 (23-08时)
                </label>
                <div className="flex items-center gap-1">
                  <input
                    type="number"
                    step="0.01"
                    value={priceValley}
                    onChange={(e) => setPriceValley(Math.max(0, Number(e.target.value) || 0))}
                    className="w-full rounded border border-border bg-elevated px-2 py-1 font-mono text-xs text-ink"
                  />
                  <span className="text-xs text-muted">元</span>
                </div>
              </div>

              <div>
                <label className="mb-1 block text-[11px] font-medium text-muted">
                  对标建筑面积 (4.4.2)
                </label>
                <div className="flex items-center gap-1">
                  <input
                    type="number"
                    min={1}
                    value={buildingArea}
                    onChange={(e) => setBuildingArea(Math.max(1, Number(e.target.value) || 1))}
                    className="w-full rounded border border-border bg-elevated px-2 py-1 font-mono text-xs text-ink"
                  />
                  <span className="text-xs text-muted">m²</span>
                </div>
              </div>

              <div>
                <label className="mb-1 block text-[11px] font-medium text-muted">
                  组织机构人数 (4.4.3)
                </label>
                <div className="flex items-center gap-1">
                  <input
                    type="number"
                    min={1}
                    value={occupantCount}
                    onChange={(e) => setOccupantCount(Math.max(1, Number(e.target.value) || 1))}
                    className="w-full rounded border border-border bg-elevated px-2 py-1 font-mono text-xs text-ink"
                  />
                  <span className="text-xs text-muted">人</span>
                </div>
              </div>
            </div>

            {/* 尖峰平谷四段汇总卡 + 单位面积/人均指标（对齐手册 2.1.4 & 4.6.3 & 4.8） */}
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
              <div className="rounded-lg border border-danger/30 bg-danger/5 p-3">
                <div className="text-xs font-medium text-danger">尖峰时段电量 / 费用</div>
                <div className="mt-1 font-mono text-base font-bold text-ink">
                  ¥{fmtVal(costSummary.sharpCost, 2)}
                </div>
                <div className="text-[11px] text-muted">
                  用量 {fmtVal(costSummary.tou.sharpUsage, precision)} {unit}
                </div>
              </div>

              <div className="rounded-lg border border-warning/30 bg-warning/5 p-3">
                <div className="text-xs font-medium text-warning">高峰时段电量 / 费用</div>
                <div className="mt-1 font-mono text-base font-bold text-ink">
                  ¥{fmtVal(costSummary.peakCost, 2)}
                </div>
                <div className="text-[11px] text-muted">
                  用量 {fmtVal(costSummary.tou.peakUsage, precision)} {unit}
                </div>
              </div>

              <div className="rounded-lg border border-accent/30 bg-accent/5 p-3">
                <div className="text-xs font-medium text-accent">平段时段电量 / 费用</div>
                <div className="mt-1 font-mono text-base font-bold text-ink">
                  ¥{fmtVal(costSummary.flatCost, 2)}
                </div>
                <div className="text-[11px] text-muted">
                  用量 {fmtVal(costSummary.tou.flatUsage, precision)} {unit}
                </div>
              </div>

              <div className="rounded-lg border border-success/30 bg-success/5 p-3">
                <div className="text-xs font-medium text-success">低谷时段电量 / 费用</div>
                <div className="mt-1 font-mono text-base font-bold text-ink">
                  ¥{fmtVal(costSummary.valleyCost, 2)}
                </div>
                <div className="text-[11px] text-muted">
                  用量 {fmtVal(costSummary.tou.valleyUsage, precision)} {unit}
                </div>
              </div>

              <div className="rounded-lg border border-border bg-surface p-3">
                <div className="text-xs text-muted">单位面积能耗 / 成本</div>
                <div className="mt-1 font-mono text-base font-bold text-ink">
                  {fmtVal(costSummary.areaUsage, 3)} {unit}/m²
                </div>
                <div className="text-[11px] text-muted">
                  单位面积成本 ¥{fmtVal(costSummary.areaCost, 3)}/m²
                </div>
              </div>

              <div className="rounded-lg border border-border bg-surface p-3">
                <div className="text-xs text-muted">人均能耗 / 人均成本</div>
                <div className="mt-1 font-mono text-base font-bold text-ink">
                  {fmtVal(costSummary.perCapitaUsage, 2)} {unit}/人
                </div>
                <div className="text-[11px] text-muted">
                  人均成本 ¥{fmtVal(costSummary.perCapitaCost, 2)}/人
                </div>
              </div>
            </div>

            {/* 分时段尖峰平谷彩色趋势柱状图 */}
            <div>
              <h3 className="mb-2 text-sm font-semibold text-ink">
                分时段尖峰平谷用能与电费分布图 (4.8 尖峰平谷分析)
              </h3>
              <TouCostBarChart
                buckets={payload?.aggregatedBuckets ?? []}
                prices={{
                  sharp: priceSharp,
                  peak: pricePeak,
                  flat: priceFlat,
                  valley: priceValley,
                }}
                unit={unit}
              />
            </div>

            {/* 各表计尖峰平谷电量与费用明细表 */}
            <div className="overflow-x-auto">
              <table className="w-full border-collapse text-left text-xs">
                <thead>
                  <tr className="border-b border-border bg-surface text-muted">
                    <th className="px-3 py-2 font-medium">终端表计</th>
                    <th className="px-3 py-2 text-right font-medium">尖峰用量</th>
                    <th className="px-3 py-2 text-right font-medium">高峰用量</th>
                    <th className="px-3 py-2 text-right font-medium">平段用量</th>
                    <th className="px-3 py-2 text-right font-medium">低谷用量</th>
                    <th className="px-3 py-2 text-right font-medium">非工作时段用量</th>
                    <th className="px-3 py-2 text-right font-medium">总用能 ({unit})</th>
                    <th className="px-3 py-2 text-right font-medium">总费用 (元)</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {(payload?.series ?? []).map((s) => {
                    const tb = s.stats.touBreakdown ?? {
                      sharpUsage: 0,
                      peakUsage: 0,
                      flatUsage: 0,
                      valleyUsage: 0,
                    };
                    const rowCost =
                      tb.sharpUsage * priceSharp +
                      tb.peakUsage * pricePeak +
                      tb.flatUsage * priceFlat +
                      tb.valleyUsage * priceValley;
                    return (
                      <tr key={s.terminalId} className="hover:bg-surface/60">
                        <td className="px-3 py-2 font-medium text-ink">
                          {s.terminalName || s.terminalId}
                        </td>
                        <td className="px-3 py-2 text-right font-mono text-danger">
                          {fmtVal(tb.sharpUsage, precision)}
                        </td>
                        <td className="px-3 py-2 text-right font-mono text-warning">
                          {fmtVal(tb.peakUsage, precision)}
                        </td>
                        <td className="px-3 py-2 text-right font-mono text-accent">
                          {fmtVal(tb.flatUsage, precision)}
                        </td>
                        <td className="px-3 py-2 text-right font-mono text-success">
                          {fmtVal(tb.valleyUsage, precision)}
                        </td>
                        <td className="px-3 py-2 text-right font-mono text-muted">
                          {fmtVal(s.stats.offHoursUsage, precision)}
                        </td>
                        <td className="px-3 py-2 text-right font-mono font-semibold text-ink">
                          {fmtVal(s.stats.usageSum, precision)}
                        </td>
                        <td className="px-3 py-2 text-right font-mono font-bold text-ink">
                          ¥{fmtVal(rowCost, 2)}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </div>
        )}

        {/* ── Tab 4: 分项能耗明细交叉报表 (4.9 能耗报表) ─────────────────────── */}
        {activeTab === 'report' && (
          <div className="space-y-3 p-4">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <h3 className="text-sm font-semibold text-ink">
                  分项能耗二维交叉统计报表 (4.9.2 分项能耗明细报表)
                </h3>
                <p className="text-xs text-muted">
                  完整展示各计量表计的期初表码、期末表码、区间累计用能与每一时间槽明细（点击右上角「导出 Excel 报表」可下载 CSV）
                </p>
              </div>
            </div>

            <div className="overflow-x-auto rounded-lg border border-border">
              <table className="w-full border-collapse text-left text-xs">
                <thead>
                  <tr className="border-b border-border bg-surface text-muted">
                    <th className="sticky left-0 z-10 bg-surface px-3 py-2 font-medium">
                      表计名称 / ID
                    </th>
                    <th className="px-3 py-2 font-medium">网关</th>
                    <th className="px-3 py-2 text-right font-medium">期初读数</th>
                    <th className="px-3 py-2 text-right font-medium">期末读数</th>
                    <th className="px-3 py-2 text-right font-medium">
                      {mode === 'delta' ? `累计用能(${unit})` : `均值(${unit})`}
                    </th>
                    <th className="px-3 py-2 text-right font-medium">峰值</th>
                    {reportSlotTimes.slice(0, 24).map((t) => (
                      <th key={t} className="px-2.5 py-2 text-right font-mono font-medium">
                        {t.slice(5, 16).replace('T', ' ')}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {(payload?.series ?? []).map((s) => {
                    const ptMap = new Map<string, { val?: number; spike?: boolean; rollback?: boolean }>();
                    s.points.forEach((p) => {
                      ptMap.set(p.time, {
                        val: mode === 'delta' ? p.delta : p.num,
                        spike: p.spikeAnomaly,
                        rollback: p.rollback,
                      });
                    });
                    return (
                      <tr key={s.terminalId} className="hover:bg-surface/60">
                        <td className="sticky left-0 z-10 bg-elevated px-3 py-2 font-medium text-ink">
                          {s.terminalName || s.terminalId}
                          <div className="font-mono text-[10px] text-muted">{s.terminalId}</div>
                        </td>
                        <td className="px-3 py-2 font-mono text-muted">{s.gatewayId}</td>
                        <td className="px-3 py-2 text-right font-mono text-muted">
                          {fmtVal(s.stats.first, precision)}
                        </td>
                        <td className="px-3 py-2 text-right font-mono text-muted">
                          {fmtVal(s.stats.last, precision)}
                        </td>
                        <td className="px-3 py-2 text-right font-mono font-bold text-ink">
                          {fmtVal(
                            mode === 'delta' ? s.stats.usageSum : s.stats.avg,
                            precision,
                          )}
                        </td>
                        <td className="px-3 py-2 text-right font-mono text-muted">
                          {fmtVal(
                            mode === 'delta' ? s.stats.usageMax : s.stats.max,
                            precision,
                          )}
                        </td>
                        {reportSlotTimes.slice(0, 24).map((t) => {
                          const cell = ptMap.get(t);
                          return (
                            <td
                              key={t}
                              className={`px-2.5 py-2 text-right font-mono ${
                                cell?.rollback
                                  ? 'bg-danger/10 font-bold text-danger'
                                  : cell?.spike
                                    ? 'bg-warning/10 font-semibold text-warning'
                                    : 'text-ink'
                              }`}
                            >
                              {cell?.val !== undefined ? fmtVal(cell.val, precision) : '—'}
                            </td>
                          );
                        })}
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </div>
        )}

        {/* ── Tab 5: 空调/系统制冷能效分析 COP / EER (4.10 空调能效) ─────────── */}
        {activeTab === 'cop' && (
          <HvacCopPanel
            series={payload?.series ?? []}
            inputTermId={copInputTermId}
            setInputTermId={setCopInputTermId}
            outputTermId={copOutputTermId}
            setOutputTermId={setCopOutputTermId}
            designTarget={copDesignTarget}
            setDesignTarget={setCopDesignTarget}
            coolingFactor={copCoolingFactor}
            setCoolingFactor={setCopCoolingFactor}
          />
        )}
      </div>
    </div>
  );
}

function MultiSeriesLineChart({
  series,
  mode,
  unit,
  precision,
  minBound,
  maxBound,
}: {
  series: AnalyticsTerminalSeries[];
  mode: AnalyticsMode;
  unit: string;
  precision: number;
  minBound?: number;
  maxBound?: number;
}) {
  const allTimes = useMemo(() => {
    const s = new Set<string>();
    series.forEach((ser) => ser.points.forEach((p) => s.add(p.time)));
    return Array.from(s).sort();
  }, [series]);

  if (allTimes.length === 0) {
    return (
      <div className="flex h-60 items-center justify-center rounded-lg border border-dashed border-border bg-surface/40 text-xs text-muted">
        当前选定条件与时间窗内暂无时序数据
      </div>
    );
  }

  const width = 860;
  const height = 250;
  const padL = 56;
  const padR = 20;
  const padT = 18;
  const padB = 34;
  const plotW = width - padL - padR;
  const plotH = height - padT - padB;

  const vals: number[] = [];
  series.forEach((ser) =>
    ser.points.forEach((p) => {
      const v = mode === 'delta' ? p.delta : p.num;
      if (v !== undefined && !Number.isNaN(v)) vals.push(v);
    }),
  );
  if (minBound !== undefined) vals.push(minBound);
  if (maxBound !== undefined) vals.push(maxBound);

  const minV = vals.length > 0 ? Math.min(0, ...vals) : 0;
  const maxV = vals.length > 0 ? Math.max(1e-6, ...vals) : 10;
  const spanV = Math.max(1e-6, maxV - minV);

  const xOf = (timeStr: string) => {
    const idx = allTimes.indexOf(timeStr);
    if (allTimes.length <= 1) return padL + plotW / 2;
    return padL + (idx / (allTimes.length - 1)) * plotW;
  };
  const yOf = (v: number) => padT + plotH - ((v - minV) / spanV) * plotH;

  return (
    <div className="w-full overflow-x-auto">
      <svg viewBox={`0 0 ${width} ${height}`} className="h-64 w-full min-w-[560px]">
        {[0, 0.25, 0.5, 0.75, 1].map((r) => {
          const y = padT + plotH * (1 - r);
          const v = minV + spanV * r;
          return (
            <g key={r}>
              <line
                x1={padL}
                y1={y}
                x2={width - padR}
                y2={y}
                stroke="currentColor"
                className="text-border"
                strokeDasharray="3 3"
              />
              <text
                x={padL - 6}
                y={y + 3}
                textAnchor="end"
                className="fill-muted font-mono text-[10px]"
              >
                {v.toFixed(Math.min(2, precision))}
              </text>
            </g>
          );
        })}

        {series.map((ser, sIdx) => {
          const color = SERIES_COLORS[sIdx % SERIES_COLORS.length];
          const validPts = ser.points
            .map((p) => ({
              time: p.time,
              val: mode === 'delta' ? p.delta : p.num,
              spike: p.spikeAnomaly,
              rollback: p.rollback,
              quality: p.quality,
            }))
            .filter((p): p is typeof p & { val: number } => p.val !== undefined);
          if (!validPts.length) return null;

          const pathD = validPts
            .map((p, i) => `${i === 0 ? 'M' : 'L'} ${xOf(p.time)} ${yOf(p.val)}`)
            .join(' ');

          return (
            <g key={ser.terminalId}>
              <path d={pathD} fill="none" stroke={color} strokeWidth={2} />
              {validPts.map((p) => {
                const cx = xOf(p.time);
                const cy = yOf(p.val);
                const isAlert = p.spike || p.rollback || p.quality > 0;
                return (
                  <circle
                    key={p.time}
                    cx={cx}
                    cy={cy}
                    r={isAlert ? 4.5 : 2.8}
                    fill={p.rollback || p.quality > 0 ? '#dc2626' : p.spike ? '#d97706' : color}
                    stroke="#fff"
                    strokeWidth={1}
                  >
                    <title>
                      {ser.terminalName || ser.terminalId} @ {formatDateTime(p.time)}:{' '}
                      {fmtVal(p.val, precision)} {unit}
                      {p.spike ? ' [超均值50%突增]' : ''}
                      {p.rollback ? ' [表码倒走]' : ''}
                    </title>
                  </circle>
                );
              })}
            </g>
          );
        })}

        {allTimes.map((t, idx) => {
          const show =
            allTimes.length <= 10 ||
            idx === 0 ||
            idx === allTimes.length - 1 ||
            idx % Math.ceil(allTimes.length / 8) === 0;
          if (!show) return null;
          return (
            <text
              key={t}
              x={xOf(t)}
              y={height - 10}
              textAnchor="middle"
              className="fill-muted font-mono text-[10px]"
            >
              {t.slice(5, 16).replace('T', ' ')}
            </text>
          );
        })}
      </svg>
    </div>
  );
}

function YoYMoMBarChart({
  buckets,
  unit,
  precision,
}: {
  buckets: AnalyticsAggBucket[];
  unit: string;
  precision: number;
}) {
  if (!buckets.length) {
    return (
      <div className="py-6 text-center text-xs text-muted">暂无同环比聚合时间槽数据</div>
    );
  }
  const width = 860;
  const height = 210;
  const padL = 56;
  const padR = 20;
  const padT = 16;
  const padB = 32;
  const plotW = width - padL - padR;
  const plotH = height - padT - padB;

  const maxV = Math.max(
    1e-6,
    ...buckets.map((b) => Math.max(b.usage, b.prevUsage ?? 0)),
  );
  const step = plotW / buckets.length;
  const barW = Math.max(3, Math.min(16, step * 0.34));

  return (
    <div className="space-y-3">
      <div className="w-full overflow-x-auto">
        <svg viewBox={`0 0 ${width} ${height}`} className="h-52 w-full min-w-[560px]">
          {[0, 0.5, 1].map((r) => {
            const y = padT + plotH * (1 - r);
            return (
              <g key={r}>
                <line
                  x1={padL}
                  y1={y}
                  x2={width - padR}
                  y2={y}
                  stroke="currentColor"
                  className="text-border"
                  strokeDasharray="3 3"
                />
                <text
                  x={padL - 6}
                  y={y + 3}
                  textAnchor="end"
                  className="fill-muted font-mono text-[10px]"
                >
                  {(maxV * r).toFixed(1)}
                </text>
              </g>
            );
          })}

          {buckets.map((b, idx) => {
            const cx = padL + idx * step + step / 2;
            const curH = (b.usage / maxV) * plotH;
            const prevH = ((b.prevUsage ?? 0) / maxV) * plotH;
            return (
              <g key={b.time}>
                <rect
                  x={cx - barW - 1}
                  y={padT + plotH - curH}
                  width={barW}
                  height={Math.max(2, curH)}
                  rx={1.5}
                  fill={b.spikeAnomaly ? '#d97706' : '#2563eb'}
                >
                  <title>
                    本期 ({formatDateTime(b.time)}): {fmtVal(b.usage, precision)} {unit}
                  </title>
                </rect>
                <rect
                  x={cx + 1}
                  y={padT + plotH - prevH}
                  width={barW}
                  height={Math.max(1, prevH)}
                  rx={1.5}
                  fill="#94a3b8"
                >
                  <title>
                    对比期: {fmtVal(b.prevUsage ?? 0, precision)} {unit}
                  </title>
                </rect>
              </g>
            );
          })}
        </svg>
      </div>
    </div>
  );
}

function TerminalSharePie({
  series,
  mode,
  unit,
  precision,
}: {
  series: AnalyticsTerminalSeries[];
  mode: AnalyticsMode;
  unit: string;
  precision: number;
}) {
  const items = series.map((s, idx) => ({
    id: s.terminalId,
    name: s.terminalName || s.terminalId,
    val: Math.max(0, mode === 'delta' ? (s.stats.usageSum ?? 0) : (s.stats.avg ?? 0)),
    color: SERIES_COLORS[idx % SERIES_COLORS.length],
  }));
  const total = items.reduce((acc, x) => acc + x.val, 0);
  const radius = 56;
  const circ = 2 * Math.PI * radius;
  let accPct = 0;

  return (
    <div className="flex flex-col items-center gap-4">
      <svg viewBox="0 0 150 150" className="h-36 w-36 -rotate-90">
        <circle
          cx="75"
          cy="75"
          r={radius}
          fill="transparent"
          stroke="currentColor"
          className="text-surface"
          strokeWidth="20"
        />
        {items.map((item) => {
          const pct = total > 0 ? (item.val / total) * 100 : 0;
          const dash = (pct / 100) * circ;
          const offset = -(accPct / 100) * circ;
          accPct += pct;
          if (pct <= 0) return null;
          return (
            <circle
              key={item.id}
              cx="75"
              cy="75"
              r={radius}
              fill="transparent"
              stroke={item.color}
              strokeWidth="20"
              strokeDasharray={`${dash} ${circ}`}
              strokeDashoffset={offset}
            />
          );
        })}
      </svg>

      <div className="w-full space-y-1.5 text-xs">
        {items.map((item) => {
          const pct = total > 0 ? (item.val / total) * 100 : 0;
          return (
            <div key={item.id} className="flex items-center justify-between">
              <div className="flex items-center gap-1.5 truncate">
                <span
                  className="h-2.5 w-2.5 shrink-0 rounded-xs"
                  style={{ backgroundColor: item.color }}
                />
                <span className="truncate text-ink">{item.name}</span>
              </div>
              <span className="font-mono text-muted">
                {fmtVal(item.val, precision)} {unit} ({pct.toFixed(1)}%)
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}

function TouCostBarChart({
  buckets,
  prices,
  unit,
}: {
  buckets: AnalyticsAggBucket[];
  prices: Record<'sharp' | 'peak' | 'flat' | 'valley', number>;
  unit: string;
}) {
  if (!buckets.length) {
    return <div className="py-6 text-center text-xs text-muted">暂无尖峰平谷分时数据</div>;
  }
  const width = 860;
  const height = 200;
  const padL = 56;
  const padR = 20;
  const padT = 16;
  const padB = 30;
  const plotW = width - padL - padR;
  const plotH = height - padT - padB;

  const costs = buckets.map((b) => b.usage * (prices[b.touPeriod] ?? prices.flat));
  const maxCost = Math.max(1e-6, ...costs);
  const step = plotW / buckets.length;
  const barW = Math.max(4, Math.min(24, step * 0.65));

  return (
    <div className="w-full overflow-x-auto">
      <svg viewBox={`0 0 ${width} ${height}`} className="h-48 w-full min-w-[560px]">
        {buckets.map((b, idx) => {
          const c = costs[idx];
          const h = (c / maxCost) * plotH;
          const x = padL + idx * step + (step - barW) / 2;
          const y = padT + plotH - h;
          const meta = TOU_META[b.touPeriod] ?? TOU_META.flat;
          return (
            <rect
              key={b.time}
              x={x}
              y={y}
              width={barW}
              height={Math.max(2, h)}
              rx={2}
              fill={meta.color}
            >
              <title>
                [{meta.label}] {formatDateTime(b.time)}: 用量 {fmtVal(b.usage)} {unit} · 电费 ¥
                {fmtVal(c, 2)}
              </title>
            </rect>
          );
        })}
      </svg>
    </div>
  );
}

function HvacCopPanel({
  series,
  inputTermId,
  setInputTermId,
  outputTermId,
  setOutputTermId,
  designTarget,
  setDesignTarget,
  coolingFactor,
  setCoolingFactor,
}: {
  series: AnalyticsTerminalSeries[];
  inputTermId: string;
  setInputTermId: (id: string) => void;
  outputTermId: string;
  setOutputTermId: (id: string) => void;
  designTarget: number;
  setDesignTarget: (v: number) => void;
  coolingFactor: number;
  setCoolingFactor: (v: number) => void;
}) {
  const inputSer = series.find((s) => s.terminalId === inputTermId) ?? series[0];
  const outputSer = series.find((s) => s.terminalId === outputTermId) ?? series[1] ?? series[0];

  const elecInput = inputSer?.stats.usageSum ?? 0;
  const rawCoolingOutput =
    outputSer && outputSer.terminalId !== inputSer?.terminalId
      ? (outputSer.stats.usageSum ?? 0)
      : elecInput * coolingFactor;
  const actualCop = elecInput > 1e-6 ? rawCoolingOutput / elecInput : coolingFactor;
  const diffTarget = actualCop - designTarget;

  return (
    <div className="space-y-4 p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h3 className="text-sm font-semibold text-ink">
            空调/冷站系统制冷能效比分析 (4.10 空调能效 COP / EER)
          </h3>
          <p className="text-xs text-muted">
            对齐手册 4.10：电表默认计算因子为耗电量 (kWh)，能量表默认计算因子为制冷/热量，实时核算系统实测能效比并与设计标准值对标
          </p>
        </div>
      </div>

      <div className="grid grid-cols-1 gap-3 rounded-lg border border-border bg-surface p-3 sm:grid-cols-4">
        <div>
          <label className="mb-1 block text-xs font-medium text-muted">
            输入耗电仪表 (电表)
          </label>
          <select
            value={inputSer?.terminalId ?? ''}
            onChange={(e) => setInputTermId(e.target.value)}
            className="w-full rounded border border-border bg-elevated px-2 py-1.5 text-xs text-ink"
          >
            {series.map((s) => (
              <option key={s.terminalId} value={s.terminalId}>
                {s.terminalName || s.terminalId} (耗电 {fmtVal(s.stats.usageSum)} )
              </option>
            ))}
          </select>
        </div>

        <div>
          <label className="mb-1 block text-xs font-medium text-muted">
            输出冷/热量仪表 (能量表)
          </label>
          <select
            value={outputSer?.terminalId ?? ''}
            onChange={(e) => setOutputTermId(e.target.value)}
            className="w-full rounded border border-border bg-elevated px-2 py-1.5 text-xs text-ink"
          >
            {series.map((s) => (
              <option key={s.terminalId} value={s.terminalId}>
                {s.terminalName || s.terminalId}
              </option>
            ))}
          </select>
        </div>

        <div>
          <label className="mb-1 block text-xs font-medium text-muted">
            设计标准能效值 (COP 基准)
          </label>
          <input
            type="number"
            step="0.1"
            value={designTarget}
            onChange={(e) => setDesignTarget(Math.max(0.1, Number(e.target.value) || 3.6))}
            className="w-full rounded border border-border bg-elevated px-2 py-1.5 font-mono text-xs text-ink"
          />
        </div>

        <div>
          <label className="mb-1 block text-xs font-medium text-muted">
            单表等效制冷转换系数
          </label>
          <input
            type="number"
            step="0.1"
            value={coolingFactor}
            onChange={(e) => setCoolingFactor(Math.max(0.1, Number(e.target.value) || 3.5))}
            className="w-full rounded border border-border bg-elevated px-2 py-1.5 font-mono text-xs text-ink"
          />
        </div>
      </div>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-4">
        <div className="rounded-lg border border-border bg-surface p-3.5">
          <div className="text-xs text-muted">区间累计耗电量 (输入)</div>
          <div className="mt-1 font-mono text-xl font-bold text-ink">
            {fmtVal(elecInput)} <span className="text-xs font-normal text-muted">kWh</span>
          </div>
        </div>

        <div className="rounded-lg border border-border bg-surface p-3.5">
          <div className="text-xs text-muted">区间累计制冷/热量 (输出)</div>
          <div className="mt-1 font-mono text-xl font-bold text-ink">
            {fmtVal(rawCoolingOutput)} <span className="text-xs font-normal text-muted">kWh(冷)</span>
          </div>
        </div>

        <div className="rounded-lg border border-border bg-surface p-3.5">
          <div className="text-xs text-muted">实测制冷能效系数 (COP)</div>
          <div className="mt-1 font-mono text-xl font-bold text-accent">
            {fmtVal(actualCop, 2)}
          </div>
        </div>

        <div className="rounded-lg border border-border bg-surface p-3.5">
          <div className="text-xs text-muted">与设计标准值 ({designTarget.toFixed(2)}) 偏差</div>
          <div
            className={`mt-1 font-mono text-xl font-bold ${
              diffTarget >= 0 ? 'text-success' : 'text-danger'
            }`}
          >
            {diffTarget >= 0 ? '+' : ''}
            {diffTarget.toFixed(2)} ({diffTarget >= 0 ? '达标高效' : '能效偏低'})
          </div>
        </div>
      </div>
    </div>
  );
}
