import { useCallback, useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  AlertTriangle,
  ArrowDownRight,
  ArrowUpRight,
  Download,
  Droplets,
  Flame,
  Gauge,
  Leaf,
  Maximize2,
  Minimize2,
  RefreshCw,
  Snowflake,
  Sparkles,
  Zap,
} from 'lucide-react';
import {
  ApiError,
  fetchAnalyticsOverview,
  type AnalyticsDomainOverview,
  type AnalyticsOverviewPayload,
} from '../api/client';
import { formatDateTime } from '../lib/format';

type QuickPeriod = 'day' | 'week' | 'month' | 'year';

const PERIOD_OPTIONS: Array<{
  key: QuickPeriod;
  label: string;
  hours: number;
  granularity: 'hour' | 'day' | 'month' | 'year';
}> = [
  { key: 'day', label: '今日 (逐时)', hours: 24, granularity: 'hour' },
  { key: 'week', label: '本周 (逐日)', hours: 7 * 24, granularity: 'day' },
  { key: 'month', label: '本月 (逐日)', hours: 30 * 24, granularity: 'day' },
  { key: 'year', label: '本年 (逐月)', hours: 365 * 24, granularity: 'month' },
];

const PALETTE = [
  '#2563eb',
  '#059669',
  '#d97706',
  '#7c3aed',
  '#db2777',
  '#0891b2',
  '#ea580c',
  '#4f46e5',
];

function fmtNum(v: number | undefined | null, digits = 2): string {
  if (v === undefined || v === null || Number.isNaN(v)) return '—';
  return Number(v).toLocaleString('zh-CN', {
    minimumFractionDigits: 0,
    maximumFractionDigits: digits,
  });
}

function domainIcon(code: string, name: string) {
  const c = (code + ' ' + name).toLowerCase();
  if (c.includes('water') || c.includes('水')) return Droplets;
  if (c.includes('gas') || c.includes('气') || c.includes('燃')) return Flame;
  if (c.includes('cold') || c.includes('cool') || c.includes('heat') || c.includes('冷') || c.includes('热') || c.includes('空调'))
    return Snowflake;
  return Zap;
}

export function AnalyticsOverviewPage() {
  const navigate = useNavigate();
  const [period, setPeriod] = useState<QuickPeriod>('month');
  const [granularity, setGranularity] = useState<'hour' | 'day' | 'month' | 'year'>('day');
  const [fromStr, setFromStr] = useState<string>(() =>
    new Date(Date.now() - 30 * 24 * 3600_000).toISOString(),
  );
  const [toStr, setToStr] = useState<string>(() => new Date().toISOString());

  const [data, setData] = useState<AnalyticsOverviewPayload | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [selectedDomainIdx, setSelectedDomainIdx] = useState<number>(0);
  const [activeCenterTab, setActiveCenterTab] = useState<'trend' | 'sankey' | 'subitems'>('trend');
  const [autoRefresh, setAutoRefresh] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);
  const [coalTargetKgce, setCoalTargetKgce] = useState<number>(5000);

  const applyPeriod = useCallback((p: QuickPeriod) => {
    const cfg = PERIOD_OPTIONS.find((o) => o.key === p) ?? PERIOD_OPTIONS[2];
    const now = new Date();
    const start = new Date(now.getTime() - cfg.hours * 3600_000);
    setPeriod(p);
    setGranularity(cfg.granularity);
    setFromStr(start.toISOString());
    setToStr(now.toISOString());
  }, []);

  const loadOverview = useCallback(
    async (signal?: AbortSignal) => {
      setLoading(true);
      setError(null);
      try {
        const res = await fetchAnalyticsOverview(
          {
            from: fromStr,
            to: toStr,
            granularity,
          },
          signal,
        );
        if (!signal?.aborted) {
          setData(res);
          setSelectedDomainIdx(0);
        }
      } catch (err) {
        if (!signal?.aborted) {
          setError(err instanceof ApiError ? err.message : String(err));
        }
      } finally {
        if (!signal?.aborted) setLoading(false);
      }
    },
    [fromStr, toStr, granularity],
  );

  useEffect(() => {
    const ctrl = new AbortController();
    void loadOverview(ctrl.signal);
    return () => ctrl.abort();
  }, [loadOverview]);

  useEffect(() => {
    if (!autoRefresh) return;
    const timer = window.setInterval(() => {
      void loadOverview();
    }, 30_000);
    return () => window.clearInterval(timer);
  }, [autoRefresh, loadOverview]);

  const activeDomain: AnalyticsDomainOverview | undefined = useMemo(() => {
    if (!data?.domains?.length) return undefined;
    return data.domains[selectedDomainIdx] ?? data.domains[0];
  }, [data, selectedDomainIdx]);

  const exportSubitemsCsv = useCallback(() => {
    if (!data) return;
    const headers = [
      '分类/物模型编码',
      '分类/物模型名称',
      '所属数据域',
      '主计量字段',
      '单位',
      '终端总数',
      '在线终端',
      '本期用能',
      '上期用能',
      '环比增幅(%)',
      '折合标准煤(kgce)',
      '综合能耗占比(%)',
    ];
    const rows = data.byDeviceType.map((r) => [
      r.typeCode,
      r.typeName,
      r.domainName,
      `${r.primaryItemName || r.primaryItemCode}`,
      r.unit,
      String(r.terminalCount),
      String(r.onlineCount),
      r.currentUsage.toFixed(3),
      r.prevUsage.toFixed(3),
      r.changeRate !== undefined ? r.changeRate.toFixed(2) : '',
      r.standardCoalKgce.toFixed(3),
      r.sharePercent.toFixed(2),
    ]);
    const csv =
      '\uFEFF' +
      [headers.join(','), ...rows.map((r) => r.map((c) => `"${c.replace(/"/g, '""')}"`).join(','))].join(
        '\n',
      );
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `能耗分类分项统计_${granularity}_${new Date().toISOString().slice(0, 10)}.csv`;
    a.click();
    URL.revokeObjectURL(url);
  }, [data, granularity]);

  return (
    <div
      className={
        fullscreen
          ? 'fixed inset-0 z-50 overflow-y-auto bg-surface p-6 space-y-5'
          : 'space-y-5'
      }
    >
      {/* 顶部标题与控制栏（4.5 能耗总览：今日/本周/本月/本年 + 自动刷新 + 全屏） */}
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border bg-elevated p-4">
        <div>
          <div className="flex items-center gap-2">
            <Sparkles className="h-5 w-5 text-accent" />
            <h1 className="text-base font-semibold text-ink">智慧能耗监测总览</h1>
            <span className="rounded bg-accent/10 px-2 py-0.5 text-xs font-medium text-accent">
              GB/T 2589 折标煤与碳排核算
            </span>
          </div>
          <p className="mt-0.5 text-xs text-muted">
            综合展示全站水、电、气、热/冷各数据域能耗总量、趋势对比、桑基能流图、分类分项占比、用能排名与异常告警
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <div className="inline-flex rounded-md border border-border bg-surface p-0.5">
            {PERIOD_OPTIONS.map((opt) => (
              <button
                key={opt.key}
                type="button"
                onClick={() => applyPeriod(opt.key)}
                className={`rounded px-2.5 py-1 text-xs font-medium transition-colors ${
                  period === opt.key
                    ? 'bg-accent text-white shadow-xs'
                    : 'text-muted hover:text-ink'
                }`}
              >
                {opt.label}
              </button>
            ))}
          </div>

          <button
            type="button"
            onClick={() => setAutoRefresh((v) => !v)}
            className={`inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-xs font-medium transition-colors ${
              autoRefresh
                ? 'border-accent bg-accent/10 text-accent'
                : 'border-border bg-surface text-muted hover:text-ink'
            }`}
            title="开启后每 30 秒自动刷新总览数据"
          >
            <RefreshCw className={`h-3.5 w-3.5 ${autoRefresh ? 'animate-spin' : ''}`} />
            {autoRefresh ? '自动刷新中' : '自动刷新'}
          </button>

          <button
            type="button"
            onClick={() => void loadOverview()}
            disabled={loading}
            className="inline-flex items-center gap-1.5 rounded-md border border-border bg-surface px-2.5 py-1.5 text-xs font-medium text-ink hover:bg-elevated disabled:opacity-50"
          >
            <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} />
            刷新
          </button>

          <button
            type="button"
            onClick={() => setFullscreen((v) => !v)}
            className="inline-flex items-center gap-1.5 rounded-md border border-border bg-surface px-2.5 py-1.5 text-xs font-medium text-ink hover:bg-elevated"
          >
            {fullscreen ? <Minimize2 className="h-3.5 w-3.5" /> : <Maximize2 className="h-3.5 w-3.5" />}
            {fullscreen ? '退出全屏' : '全屏大屏'}
          </button>
        </div>
      </div>

      {error && (
        <div className="rounded-lg border border-danger/30 bg-danger/10 px-4 py-3 text-xs text-danger">
          加载能耗总览失败：{error}
        </div>
      )}

      {/* 第一排：综合能耗(kgce)、碳排放(kgCO2) 与 各能源数据域汇总卡（对齐手册 4.5.1 顶栏卡片） */}
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <div className="rounded-lg border border-border bg-elevated p-4">
          <div className="flex items-center justify-between">
            <span className="text-xs font-medium text-muted">综合能耗总量 (折标煤)</span>
            <span className="rounded-full bg-accent/10 p-1.5 text-accent">
              <Gauge className="h-4 w-4" />
            </span>
          </div>
          <div className="mt-2 flex items-baseline gap-1.5">
            <span className="text-2xl font-bold tracking-tight text-ink">
              {fmtNum(data?.totalStandardCoalKgce)}
            </span>
            <span className="text-xs text-muted">kgce</span>
          </div>
          <div className="mt-2 flex items-center justify-between text-xs">
            <span className="text-muted">上期: {fmtNum(data?.prevStandardCoalKgce)} kgce</span>
            {data?.coalChangeRate !== undefined ? (
              <span
                className={`inline-flex items-center gap-0.5 font-medium ${
                  data.coalChangeRate > 0 ? 'text-danger' : 'text-success'
                }`}
              >
                {data.coalChangeRate > 0 ? (
                  <ArrowUpRight className="h-3.5 w-3.5" />
                ) : (
                  <ArrowDownRight className="h-3.5 w-3.5" />
                )}
                {data.coalChangeRate > 0 ? '+' : ''}
                {data.coalChangeRate.toFixed(1)}%
              </span>
            ) : (
              <span className="text-muted">环比 —</span>
            )}
          </div>
        </div>

        <div className="rounded-lg border border-border bg-elevated p-4">
          <div className="flex items-center justify-between">
            <span className="text-xs font-medium text-muted">碳排放折算总量 (CO₂)</span>
            <span className="rounded-full bg-emerald-500/10 p-1.5 text-emerald-600 dark:text-emerald-400">
              <Leaf className="h-4 w-4" />
            </span>
          </div>
          <div className="mt-2 flex items-baseline gap-1.5">
            <span className="text-2xl font-bold tracking-tight text-ink">
              {fmtNum(data?.totalCarbonKgCO2)}
            </span>
            <span className="text-xs text-muted">kgCO₂</span>
          </div>
          <div className="mt-2 flex items-center justify-between text-xs">
            <span className="text-muted">上期: {fmtNum(data?.prevCarbonKgCO2)} kgCO₂</span>
            {data?.carbonChangeRate !== undefined ? (
              <span
                className={`inline-flex items-center gap-0.5 font-medium ${
                  data.carbonChangeRate > 0 ? 'text-danger' : 'text-success'
                }`}
              >
                {data.carbonChangeRate > 0 ? (
                  <ArrowUpRight className="h-3.5 w-3.5" />
                ) : (
                  <ArrowDownRight className="h-3.5 w-3.5" />
                )}
                {data.carbonChangeRate > 0 ? '+' : ''}
                {data.carbonChangeRate.toFixed(1)}%
              </span>
            ) : (
              <span className="text-muted">环比 —</span>
            )}
          </div>
        </div>

        {(data?.domains ?? []).slice(0, 2).map((d, idx) => {
          const Icon = domainIcon(d.domainCode, d.domainName);
          return (
            <div
              key={d.domainId || idx}
              onClick={() => setSelectedDomainIdx(idx)}
              className={`cursor-pointer rounded-lg border p-4 transition-colors ${
                selectedDomainIdx === idx
                  ? 'border-accent bg-accent/5'
                  : 'border-border bg-elevated hover:border-accent/50'
              }`}
            >
              <div className="flex items-center justify-between">
                <span className="text-xs font-medium text-muted">
                  {d.domainName || '分类能耗'} ({d.terminalCount} 表)
                </span>
                <span className="rounded-full bg-accent/10 p-1.5 text-accent">
                  <Icon className="h-4 w-4" />
                </span>
              </div>
              <div className="mt-2 flex items-baseline gap-1.5">
                <span className="text-2xl font-bold tracking-tight text-ink">
                  {fmtNum(d.currentUsage)}
                </span>
                <span className="text-xs text-muted">{d.unit}</span>
              </div>
              <div className="mt-2 flex items-center justify-between text-xs">
                <span className="text-muted">
                  折标煤 {fmtNum(d.standardCoalKgce, 1)} kgce ({d.sharePercent.toFixed(1)}%)
                </span>
                {d.changeRate !== undefined ? (
                  <span
                    className={`inline-flex items-center gap-0.5 font-medium ${
                      d.changeRate > 0 ? 'text-danger' : 'text-success'
                    }`}
                  >
                    {d.changeRate > 0 ? '+' : ''}
                    {d.changeRate.toFixed(1)}%
                  </span>
                ) : (
                  <span className="text-muted">上期 {fmtNum(d.prevUsage)}</span>
                )}
              </div>
            </div>
          );
        })}
      </div>

      {/* 若存在更多数据域（如水、气、热、冷），渲染第二行数据域切换胶囊 */}
      {data && data.domains.length > 2 && (
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-xs font-medium text-muted">切换数据域曲线：</span>
          {data.domains.map((d, idx) => (
            <button
              key={d.domainId || idx}
              type="button"
              onClick={() => setSelectedDomainIdx(idx)}
              className={`inline-flex items-center gap-2 rounded-md border px-3 py-1.5 text-xs font-medium transition-colors ${
                selectedDomainIdx === idx
                  ? 'border-accent bg-accent text-white'
                  : 'border-border bg-elevated text-ink hover:border-accent/50'
              }`}
            >
              <span>{d.domainName}</span>
              <span className="opacity-80">
                {fmtNum(d.currentUsage)} {d.unit}
              </span>
            </button>
          ))}
        </div>
      )}

      {/* 中部核心区：4.5 能耗趋势与占比环形图 | 4.6.1 桑基能流图 | 4.6.6 分类分项明细 */}
      <div className="rounded-lg border border-border bg-elevated">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3">
          <div className="flex items-center gap-1">
            <button
              type="button"
              onClick={() => setActiveCenterTab('trend')}
              className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                activeCenterTab === 'trend'
                  ? 'bg-accent text-white'
                  : 'text-muted hover:bg-surface hover:text-ink'
              }`}
            >
              能耗趋势与结构占比 (4.5)
            </button>
            <button
              type="button"
              onClick={() => setActiveCenterTab('sankey')}
              className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                activeCenterTab === 'sankey'
                  ? 'bg-accent text-white'
                  : 'text-muted hover:bg-surface hover:text-ink'
              }`}
            >
              桑基能流图与平衡流向 (4.6.1 / 4.6.2)
            </button>
            <button
              type="button"
              onClick={() => setActiveCenterTab('subitems')}
              className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                activeCenterTab === 'subitems'
                  ? 'bg-accent text-white'
                  : 'text-muted hover:bg-surface hover:text-ink'
              }`}
            >
              分类分项能耗统计表 (4.6.6 / 4.9.1)
            </button>
          </div>

          {activeCenterTab === 'subitems' && (
            <button
              type="button"
              onClick={exportSubitemsCsv}
              className="inline-flex items-center gap-1.5 rounded-md border border-border bg-surface px-2.5 py-1 text-xs font-medium text-ink hover:bg-elevated"
            >
              <Download className="h-3.5 w-3.5" />
              导出分类分项 Excel (CSV)
            </button>
          )}
        </div>

        {activeCenterTab === 'trend' && (
          <div className="grid grid-cols-1 gap-6 p-4 lg:grid-cols-3">
            {/* 左侧 2/3：选中数据域的逐槽能耗柱状趋势图 */}
            <div className="lg:col-span-2">
              <div className="mb-3 flex items-center justify-between">
                <div>
                  <h3 className="text-sm font-semibold text-ink">
                    {activeDomain ? `${activeDomain.domainName} — 分时段用能趋势` : '分时段用能趋势'}
                  </h3>
                  <p className="text-xs text-muted">
                    粒度：{granularity === 'hour' ? '逐时' : granularity === 'day' ? '逐日' : '逐月'} · 单位：
                    {activeDomain?.unit || 'kWh'}
                  </p>
                </div>
                <button
                  type="button"
                  onClick={() => navigate('/a/trends')}
                  className="text-xs font-medium text-accent hover:underline"
                >
                  进入多表同环比与尖峰平谷分析 →
                </button>
              </div>
              <OverviewBarChart buckets={activeDomain?.buckets ?? []} unit={activeDomain?.unit ?? ''} />
            </div>

            {/* 右侧 1/3：分类分项综合能耗占比环形图 (Donut Chart) */}
            <div className="border-t border-border pt-4 lg:border-l lg:border-t-0 lg:pl-6 lg:pt-0">
              <h3 className="text-sm font-semibold text-ink">各分类/分项能耗占比 (折标煤)</h3>
              <p className="mb-3 text-xs text-muted">按 GB/T 2589 折合标准煤 (kgce) 计算构成比例</p>
              <OverviewDonutChart items={data?.byDeviceType ?? []} totalKgce={data?.totalStandardCoalKgce ?? 0} />
            </div>
          </div>
        )}

        {activeCenterTab === 'sankey' && (
          <div className="p-4">
            <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
              <div>
                <h3 className="text-sm font-semibold text-ink">
                  全站四级能源流向桑基图（数据域 → 物模型分类 → 网关支路 → 重点表计）
                </h3>
                <p className="text-xs text-muted">
                  流带宽度按折算标准煤 (kgce) 等比例映射，直观洞察各级支路能耗去向与损耗点
                </p>
              </div>
              <button
                type="button"
                onClick={() => navigate('/a/quality')}
                className="text-xs font-medium text-accent hover:underline"
              >
                配置总分表跑冒滴漏平衡监测 →
              </button>
            </div>
            <EnergySankeyDiagram
              nodes={data?.sankeyNodes ?? []}
              links={data?.sankeyLinks ?? []}
            />
          </div>
        )}

        {activeCenterTab === 'subitems' && (
          <div className="overflow-x-auto p-4">
            <table className="w-full border-collapse text-left text-xs">
              <thead>
                <tr className="border-b border-border bg-surface text-muted">
                  <th className="px-3 py-2 font-medium">分类/物模型</th>
                  <th className="px-3 py-2 font-medium">数据域</th>
                  <th className="px-3 py-2 font-medium">主计量字段</th>
                  <th className="px-3 py-2 text-right font-medium">在线/总表数</th>
                  <th className="px-3 py-2 text-right font-medium">本期用能</th>
                  <th className="px-3 py-2 text-right font-medium">上期用能</th>
                  <th className="px-3 py-2 text-right font-medium">环比</th>
                  <th className="px-3 py-2 text-right font-medium">折标准煤 (kgce)</th>
                  <th className="px-3 py-2 font-medium">综合能耗占比</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {(data?.byDeviceType ?? []).map((row) => (
                  <tr key={row.typeId} className="hover:bg-surface/60">
                    <td className="px-3 py-2.5 font-medium text-ink">
                      {row.typeName}{' '}
                      <span className="font-mono text-[11px] text-muted">({row.typeCode})</span>
                    </td>
                    <td className="px-3 py-2.5 text-muted">{row.domainName}</td>
                    <td className="px-3 py-2.5 text-muted">
                      {row.primaryItemName || row.primaryItemCode || '—'}
                    </td>
                    <td className="px-3 py-2.5 text-right font-mono">
                      {row.onlineCount} / {row.terminalCount}
                    </td>
                    <td className="px-3 py-2.5 text-right font-mono font-semibold text-ink">
                      {fmtNum(row.currentUsage)} {row.unit}
                    </td>
                    <td className="px-3 py-2.5 text-right font-mono text-muted">
                      {fmtNum(row.prevUsage)} {row.unit}
                    </td>
                    <td className="px-3 py-2.5 text-right font-mono">
                      {row.changeRate !== undefined ? (
                        <span className={row.changeRate > 0 ? 'text-danger' : 'text-success'}>
                          {row.changeRate > 0 ? '+' : ''}
                          {row.changeRate.toFixed(1)}%
                        </span>
                      ) : (
                        '—'
                      )}
                    </td>
                    <td className="px-3 py-2.5 text-right font-mono">
                      {fmtNum(row.standardCoalKgce)}
                    </td>
                    <td className="px-3 py-2.5">
                      <div className="flex items-center gap-2">
                        <div className="h-2 w-24 overflow-hidden rounded-full bg-surface">
                          <div
                            className="h-full rounded-full bg-accent"
                            style={{ width: `${Math.min(100, Math.max(2, row.sharePercent))}%` }}
                          />
                        </div>
                        <span className="font-mono text-xs text-muted">
                          {row.sharePercent.toFixed(1)}%
                        </span>
                      </div>
                    </td>
                  </tr>
                ))}
                {(!data?.byDeviceType || data.byDeviceType.length === 0) && (
                  <tr>
                    <td colSpan={9} className="px-3 py-8 text-center text-muted">
                      暂无物模型分类能耗数据
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* 底部三栏：终端用能排名 Top 榜 | 能耗定标对标与抄表在线率 | 实时用能异常与告警监控（对齐手册 4.5.1 底部三栏） */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        {/* 1) 用能排名 Top 榜 */}
        <div className="rounded-lg border border-border bg-elevated p-4">
          <div className="mb-3 flex items-center justify-between">
            <h3 className="text-sm font-semibold text-ink">重点表计用能排名 Top 8</h3>
            <span className="text-xs text-muted">按折标煤排序</span>
          </div>
          <div className="space-y-2.5">
            {(data?.topTerminals ?? []).slice(0, 8).map((t, idx) => (
              <div key={t.terminalId} className="space-y-1">
                <div className="flex items-center justify-between text-xs">
                  <div className="flex items-center gap-1.5 truncate">
                    <span
                      className={`inline-flex h-4 w-4 shrink-0 items-center justify-center rounded text-[10px] font-bold ${
                        idx < 3 ? 'bg-accent text-white' : 'bg-surface text-muted'
                      }`}
                    >
                      {idx + 1}
                    </span>
                    <span className="truncate font-medium text-ink">
                      {t.terminalName || t.terminalId}
                    </span>
                    <span className="text-[11px] text-muted">({t.typeName})</span>
                  </div>
                  <span className="shrink-0 font-mono font-semibold text-ink">
                    {fmtNum(t.usage)} {t.unit}
                  </span>
                </div>
                <div className="flex items-center gap-2">
                  <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-surface">
                    <div
                      className="h-full rounded-full bg-accent"
                      style={{ width: `${Math.min(100, Math.max(3, t.sharePercent))}%` }}
                    />
                  </div>
                  <span className="w-10 text-right font-mono text-[11px] text-muted">
                    {t.sharePercent.toFixed(1)}%
                  </span>
                </div>
              </div>
            ))}
            {(!data?.topTerminals || data.topTerminals.length === 0) && (
              <p className="py-6 text-center text-xs text-muted">当前时段暂无表计能耗数据</p>
            )}
          </div>
        </div>

        {/* 2) 能耗定标考核对标与网关支路分布（对齐手册 4.4 能耗定标 & 4.5 对标卡） */}
        <div className="rounded-lg border border-border bg-elevated p-4">
          <div className="mb-3 flex items-center justify-between">
            <h3 className="text-sm font-semibold text-ink">能耗定标考核 & 支路分布 (4.4)</h3>
            <div className="flex items-center gap-1 text-xs">
              <span className="text-muted">定标值:</span>
              <input
                type="number"
                min={1}
                value={coalTargetKgce}
                onChange={(e) => setCoalTargetKgce(Math.max(1, Number(e.target.value) || 1))}
                className="w-20 rounded border border-border bg-surface px-1.5 py-0.5 text-right font-mono text-xs text-ink"
              />
              <span className="text-muted">kgce</span>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3 rounded-lg bg-surface p-3">
            <div>
              <div className="text-xs text-muted">本期实测综合能耗</div>
              <div className="mt-1 font-mono text-lg font-bold text-ink">
                {fmtNum(data?.totalStandardCoalKgce)} <span className="text-xs font-normal">kgce</span>
              </div>
              <div className="mt-1 text-[11px] text-muted">
                定标占用率：{' '}
                <span
                  className={
                    (data?.totalStandardCoalKgce ?? 0) > coalTargetKgce
                      ? 'font-semibold text-danger'
                      : 'font-semibold text-success'
                  }
                >
                  {(((data?.totalStandardCoalKgce ?? 0) / coalTargetKgce) * 100).toFixed(1)}%
                </span>
              </div>
            </div>
            <div>
              <div className="text-xs text-muted">计量表计在线率</div>
              <div className="mt-1 font-mono text-lg font-bold text-ink">
                {fmtNum(data?.terminalOnlineRate, 1)}%
              </div>
              <div className="mt-1 text-[11px] text-muted">
                在线 {data?.onlineTerminals ?? 0} / 总计 {data?.totalTerminals ?? 0} 台
              </div>
            </div>
          </div>

          <div className="mt-3">
            <div className="mb-2 text-xs font-medium text-muted">各采集网关/支路能耗分布</div>
            <div className="space-y-2">
              {(data?.byGateway ?? []).slice(0, 4).map((gw) => (
                <div
                  key={gw.gatewayId}
                  className="flex items-center justify-between rounded border border-border/60 px-2.5 py-1.5 text-xs"
                >
                  <div className="flex items-center gap-2 truncate">
                    <span
                      className={`h-2 w-2 rounded-full ${gw.online ? 'bg-success' : 'bg-danger'}`}
                    />
                    <span className="truncate font-medium text-ink">
                      {gw.gatewayName || gw.gatewayId}
                    </span>
                    <span className="text-muted">({gw.onlineCount}/{gw.terminalCount}表)</span>
                  </div>
                  <span className="font-mono text-ink">
                    {fmtNum(gw.standardCoalKgce, 1)} kgce ({gw.sharePercent.toFixed(1)}%)
                  </span>
                </div>
              ))}
            </div>
          </div>
        </div>

        {/* 3) 用能异常与设备告警监测概览（对齐手册 4.11 & 4.12 告警监测） */}
        <div className="rounded-lg border border-border bg-elevated p-4">
          <div className="mb-3 flex items-center justify-between">
            <div className="flex items-center gap-1.5">
              <AlertTriangle className="h-4 w-4 text-warning" />
              <h3 className="text-sm font-semibold text-ink">用能与设备告警监测 (4.12)</h3>
            </div>
            <button
              type="button"
              onClick={() => navigate('/a/quality')}
              className="text-xs font-medium text-accent hover:underline"
            >
              查看告警明细 →
            </button>
          </div>

          <div className="grid grid-cols-2 gap-2.5">
            <div
              onClick={() => navigate('/a/quality')}
              className="cursor-pointer rounded-lg border border-border bg-surface p-3 hover:border-accent/50"
            >
              <div className="text-xs text-muted">超均值50%用能突增</div>
              <div className="mt-1 font-mono text-xl font-bold text-warning">
                {data?.alarmSummary.spike50PctCount ?? 0}
              </div>
              <div className="mt-0.5 text-[11px] text-muted">4.6.3 异常用电识别</div>
            </div>

            <div
              onClick={() => navigate('/a/quality')}
              className="cursor-pointer rounded-lg border border-border bg-surface p-3 hover:border-accent/50"
            >
              <div className="text-xs text-muted">表码倒走/换表异常</div>
              <div className="mt-1 font-mono text-xl font-bold text-danger">
                {data?.alarmSummary.rollbackCount ?? 0}
              </div>
              <div className="mt-0.5 text-[11px] text-muted">期末读数小于期初读数</div>
            </div>

            <div
              onClick={() => navigate('/a/quality')}
              className="cursor-pointer rounded-lg border border-border bg-surface p-3 hover:border-accent/50"
            >
              <div className="text-xs text-muted">数据质量/越限事件</div>
              <div className="mt-1 font-mono text-xl font-bold text-ink">
                {data?.alarmSummary.qualityIssueCount ?? 0}
              </div>
              <div className="mt-0.5 text-[11px] text-muted">物模型上下限与规则拦截</div>
            </div>

            <div
              onClick={() => navigate('/a/health')}
              className="cursor-pointer rounded-lg border border-border bg-surface p-3 hover:border-accent/50"
            >
              <div className="text-xs text-muted">离线/失联表计</div>
              <div className="mt-1 font-mono text-xl font-bold text-danger">
                {data?.alarmSummary.offlineTerminalCount ?? 0}
              </div>
              <div className="mt-0.5 text-[11px] text-muted">点击前往运行健康巡检</div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

function OverviewBarChart({
  buckets,
  unit,
}: {
  buckets: Array<{ time: string; currentVal: number; prevVal: number }>;
  unit: string;
}) {
  if (!buckets.length) {
    return (
      <div className="flex h-56 items-center justify-center rounded-lg border border-dashed border-border bg-surface/40 text-xs text-muted">
        当前时段内暂无分桶趋势数据
      </div>
    );
  }
  const width = 680;
  const height = 220;
  const padL = 52;
  const padR = 16;
  const padT = 16;
  const padB = 34;
  const plotW = width - padL - padR;
  const plotH = height - padT - padB;

  const maxVal = Math.max(1e-6, ...buckets.map((b) => b.currentVal));
  const barStep = plotW / buckets.length;
  const barW = Math.max(4, Math.min(26, barStep * 0.62));

  return (
    <div className="w-full overflow-x-auto">
      <svg viewBox={`0 0 ${width} ${height}`} className="h-56 w-full min-w-[480px]">
        {[0, 0.25, 0.5, 0.75, 1].map((ratio) => {
          const y = padT + plotH * (1 - ratio);
          const val = maxVal * ratio;
          return (
            <g key={ratio}>
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
                {val >= 100 ? Math.round(val) : val.toFixed(1)}
              </text>
            </g>
          );
        })}

        {buckets.map((b, idx) => {
          const xCenter = padL + idx * barStep + barStep / 2;
          const h = (b.currentVal / maxVal) * plotH;
          const y = padT + plotH - h;
          const showLabel =
            buckets.length <= 12 || idx === 0 || idx === buckets.length - 1 || idx % Math.ceil(buckets.length / 8) === 0;
          return (
            <g key={b.time}>
              <rect
                x={xCenter - barW / 2}
                y={y}
                width={barW}
                height={Math.max(2, h)}
                rx={2}
                fill="#2563eb"
                className="opacity-85 hover:opacity-100 transition-opacity"
              >
                <title>
                  {formatDateTime(b.time)}: {fmtNum(b.currentVal)} {unit}
                </title>
              </rect>
              {showLabel && (
                <text
                  x={xCenter}
                  y={height - 10}
                  textAnchor="middle"
                  className="fill-muted font-mono text-[10px]"
                >
                  {b.time.slice(5, 16).replace('T', ' ')}
                </text>
              )}
            </g>
          );
        })}
      </svg>
    </div>
  );
}

function OverviewDonutChart({
  items,
  totalKgce,
}: {
  items: Array<{ typeId: number; typeName: string; standardCoalKgce: number; sharePercent: number }>;
  totalKgce: number;
}) {
  const radius = 58;
  const circumference = 2 * Math.PI * radius;
  let accPct = 0;

  return (
    <div className="flex flex-col items-center gap-4 sm:flex-row lg:flex-col xl:flex-row">
      <div className="relative flex h-36 w-36 shrink-0 items-center justify-center">
        <svg viewBox="0 0 150 150" className="h-36 w-36 -rotate-90">
          <circle
            cx="75"
            cy="75"
            r={radius}
            fill="transparent"
            stroke="currentColor"
            className="text-surface"
            strokeWidth="18"
          />
          {items.map((item, idx) => {
            const pct = Math.max(0, item.sharePercent);
            const dash = (pct / 100) * circumference;
            const offset = -(accPct / 100) * circumference;
            accPct += pct;
            if (pct <= 0) return null;
            return (
              <circle
                key={item.typeId || idx}
                cx="75"
                cy="75"
                r={radius}
                fill="transparent"
                stroke={PALETTE[idx % PALETTE.length]}
                strokeWidth="18"
                strokeDasharray={`${dash} ${circumference}`}
                strokeDashoffset={offset}
              />
            );
          })}
        </svg>
        <div className="absolute inset-0 flex flex-col items-center justify-center text-center">
          <span className="text-[10px] text-muted">总标煤</span>
          <span className="font-mono text-xs font-bold text-ink">{fmtNum(totalKgce, 1)}</span>
          <span className="text-[10px] text-muted">kgce</span>
        </div>
      </div>

      <div className="w-full flex-1 space-y-1.5 text-xs">
        {items.slice(0, 6).map((item, idx) => (
          <div key={item.typeId || idx} className="flex items-center justify-between gap-2">
            <div className="flex items-center gap-1.5 truncate">
              <span
                className="h-2.5 w-2.5 shrink-0 rounded-xs"
                style={{ backgroundColor: PALETTE[idx % PALETTE.length] }}
              />
              <span className="truncate text-ink">{item.typeName}</span>
            </div>
            <span className="shrink-0 font-mono text-muted">{item.sharePercent.toFixed(1)}%</span>
          </div>
        ))}
        {items.length === 0 && (
          <div className="text-center text-xs text-muted">暂无占比数据</div>
        )}
      </div>
    </div>
  );
}

function EnergySankeyDiagram({
  nodes,
  links,
}: {
  nodes: AnalyticsOverviewPayload['sankeyNodes'];
  links: AnalyticsOverviewPayload['sankeyLinks'];
}) {
  if (!nodes.length) {
    return (
      <div className="flex h-60 items-center justify-center rounded-lg border border-dashed border-border bg-surface/40 text-xs text-muted">
        暂无能流图节点数据
      </div>
    );
  }

  const cols: Array<'domain' | 'device_type' | 'gateway' | 'terminal'> = [
    'domain',
    'device_type',
    'gateway',
    'terminal',
  ];
  const colTitles: Record<string, string> = {
    domain: '① 能源数据域',
    device_type: '② 分类物模型',
    gateway: '③ 采集网关/支路',
    terminal: '④ 重点计量表计',
  };

  const width = 860;
  const height = 300;
  const padX = 24;
  const padY = 32;
  const colStep = (width - padX * 2 - 120) / 3;

  const nodePos = new Map<string, { x: number; y: number; h: number; color: string }>();

  cols.forEach((cat, colIdx) => {
    const colNodes = nodes.filter((n) => n.category === cat);
    const x = padX + colIdx * colStep;
    const availH = height - padY * 2;
    const gap = 10;
    const itemH =
      colNodes.length > 0
        ? Math.max(18, (availH - gap * Math.max(0, colNodes.length - 1)) / colNodes.length)
        : 24;
    colNodes.forEach((n, idx) => {
      nodePos.set(n.id, {
        x,
        y: padY + idx * (itemH + gap),
        h: itemH,
        color: PALETTE[(colIdx * 2 + idx) % PALETTE.length],
      });
    });
  });

  return (
    <div className="w-full overflow-x-auto">
      <svg viewBox={`0 0 ${width} ${height}`} className="h-72 w-full min-w-[680px]">
        {cols.map((cat, idx) => (
          <text
            key={cat}
            x={padX + idx * colStep}
            y={18}
            className="fill-muted text-[11px] font-semibold"
          >
            {colTitles[cat]}
          </text>
        ))}

        {links.map((lk, idx) => {
          const s = nodePos.get(lk.source);
          const t = nodePos.get(lk.target);
          if (!s || !t) return null;
          const x1 = s.x + 115;
          const y1 = s.y + s.h / 2;
          const x2 = t.x;
          const y2 = t.y + t.h / 2;
          const cx1 = x1 + (x2 - x1) * 0.45;
          const cx2 = x1 + (x2 - x1) * 0.55;
          const strokeW = Math.max(2, Math.min(18, Math.sqrt(Math.max(0.5, lk.value)) * 1.6));
          return (
            <path
              key={`${lk.source}-${lk.target}-${idx}`}
              d={`M ${x1} ${y1} C ${cx1} ${y1}, ${cx2} ${y2}, ${x2} ${y2}`}
              fill="none"
              stroke={s.color}
              strokeOpacity={0.28}
              strokeWidth={strokeW}
            >
              <title>
                {lk.source} → {lk.target}: {fmtNum(lk.rawValue)} {lk.unit} ({fmtNum(lk.value)} kgce)
              </title>
            </path>
          );
        })}

        {nodes.map((n) => {
          const pos = nodePos.get(n.id);
          if (!pos) return null;
          return (
            <g key={n.id}>
              <rect
                x={pos.x}
                y={pos.y}
                width={115}
                height={pos.h}
                rx={4}
                fill={pos.color}
                fillOpacity={0.14}
                stroke={pos.color}
                strokeWidth={1.2}
              />
              <text
                x={pos.x + 6}
                y={pos.y + Math.min(pos.h / 2 + 4, pos.h - 4)}
                className="fill-ink text-[11px] font-medium"
              >
                {n.name.length > 10 ? n.name.slice(0, 10) + '…' : n.name}
              </text>
              <title>
                {n.name}: {fmtNum(n.rawValue)} {n.unit} (折标煤 {fmtNum(n.value)} kgce)
              </title>
            </g>
          );
        })}
      </svg>
    </div>
  );
}
