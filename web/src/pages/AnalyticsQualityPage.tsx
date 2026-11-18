import { useCallback, useEffect, useState } from 'react';
import {
  AlertTriangle,
  Check,
  Download,
  Flame,
  RefreshCw,
  Scale,
  ShieldAlert,
  ShieldCheck,
} from 'lucide-react';
import {
  ApiError,
  fetchAnalyticsAnomalies,
  fetchAnalyticsQuality,
  fetchList,
  type AnalyticsAnomaliesPayload,
  type AnalyticsQualityPayload,
} from '../api/client';
import { formatDateTime } from '../lib/format';

interface GatewayRow {
  gateway_id: string;
  name: string;
}

interface DeviceTypeRow {
  id: number;
  code: string;
  name: string;
}

interface TerminalRow {
  id: string;
  name: string;
  type_id: number;
  gateway_id: string;
}

const QUICK_WINDOWS: Array<{ label: string; hours: number; gran: 'hour' | 'day' }> = [
  { label: '近 24 小时', hours: 24, gran: 'hour' },
  { label: '近 7 天', hours: 7 * 24, gran: 'hour' },
  { label: '近 30 天', hours: 30 * 24, gran: 'day' },
];

function fmtVal(v: number | undefined | null, digits = 2): string {
  if (v === undefined || v === null || Number.isNaN(v)) return '—';
  return Number(v).toLocaleString('zh-CN', {
    minimumFractionDigits: 0,
    maximumFractionDigits: digits,
  });
}

export function AnalyticsQualityPage() {
  const [activeTab, setActiveTab] = useState<'energy_alarms' | 'data_quality'>('energy_alarms');
  const [windowIdx, setWindowIdx] = useState<number>(1);
  const [fromIso, setFromIso] = useState<string>(() =>
    new Date(Date.now() - 7 * 24 * 3600_000).toISOString(),
  );
  const [toIso, setToIso] = useState<string>(() => new Date().toISOString());
  const [granularity, setGranularity] = useState<'hour' | 'day' | 'month'>('hour');

  const [gateways, setGateways] = useState<GatewayRow[]>([]);
  const [deviceTypes, setDeviceTypes] = useState<DeviceTypeRow[]>([]);
  const [terminals, setTerminals] = useState<TerminalRow[]>([]);

  const [selectedGateway, setSelectedGateway] = useState<string>('');
  const [selectedTypeId, setSelectedTypeId] = useState<number>(0);

  // 4.11 用能监测规则参数：能耗超标、非工作时段用能、跑冒滴漏总分表误差
  const [dailyLimit, setDailyLimit] = useState<string>('');
  const [offHoursSpec, setOffHoursSpec] = useState<string>('23:00-07:00,12:00-13:30');
  const [offHoursLimit, setOffHoursLimit] = useState<string>('');
  const [masterTermId, setMasterTermId] = useState<string>('');
  const [subTermIds, setSubTermIds] = useState<string[]>([]);
  const [leakageThresholdPct, setLeakageThresholdPct] = useState<number>(5.0);
  const [alarmTypeFilter, setAlarmTypeFilter] = useState<string>('all');

  const [anomaliesData, setAnomaliesData] = useState<AnalyticsAnomaliesPayload | null>(null);
  const [qualityData, setQualityData] = useState<AnalyticsQualityPayload | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const ctrl = new AbortController();
    Promise.all([
      fetchList<GatewayRow>('entity', 'gateways', { pageSize: 100 }, ctrl.signal),
      fetchList<DeviceTypeRow>('entity', 'thing-models', { pageSize: 100 }, ctrl.signal),
      fetchList<TerminalRow>('entity', 'terminals', { pageSize: 100 }, ctrl.signal),
    ])
      .then(([gwRes, dtRes, termRes]) => {
        setGateways(gwRes.rows);
        setDeviceTypes(dtRes.rows);
        setTerminals(termRes.rows);
        if (termRes.rows.length >= 2) {
          setMasterTermId(termRes.rows[0].id);
          setSubTermIds(termRes.rows.slice(1, 5).map((t) => t.id));
        }
      })
      .catch(() => {});
    return () => ctrl.abort();
  }, []);

  const applyQuickWindow = (idx: number) => {
    const cfg = QUICK_WINDOWS[idx] ?? QUICK_WINDOWS[1];
    setWindowIdx(idx);
    setGranularity(cfg.gran);
    const now = new Date();
    setFromIso(new Date(now.getTime() - cfg.hours * 3600_000).toISOString());
    setToIso(now.toISOString());
  };

  const loadAll = useCallback(
    async (signal?: AbortSignal) => {
      setLoading(true);
      setError(null);
      try {
        const [anomRes, qualRes] = await Promise.all([
          fetchAnalyticsAnomalies(
            {
              from: fromIso,
              to: toIso,
              granularity,
              typeId: selectedTypeId || undefined,
              gatewayId: selectedGateway || undefined,
              dailyLimit: dailyLimit ? Number(dailyLimit) : undefined,
              offHours: offHoursSpec,
              offHoursLimit: offHoursLimit ? Number(offHoursLimit) : undefined,
              masterTerminalId: masterTermId || undefined,
              subTerminalIds: subTermIds.length > 0 ? subTermIds : undefined,
              leakageThresholdPct,
            },
            signal,
          ),
          fetchAnalyticsQuality(
            {
              from: fromIso,
              to: toIso,
              gatewayId: selectedGateway || undefined,
              typeId: selectedTypeId || undefined,
            },
            signal,
          ),
        ]);
        if (!signal?.aborted) {
          setAnomaliesData(anomRes);
          setQualityData(qualRes);
        }
      } catch (err) {
        if (!signal?.aborted) {
          setError(err instanceof ApiError ? err.message : String(err));
        }
      } finally {
        if (!signal?.aborted) setLoading(false);
      }
    },
    [
      fromIso,
      toIso,
      granularity,
      selectedTypeId,
      selectedGateway,
      dailyLimit,
      offHoursSpec,
      offHoursLimit,
      masterTermId,
      subTermIds,
      leakageThresholdPct,
    ],
  );

  useEffect(() => {
    const ctrl = new AbortController();
    void loadAll(ctrl.signal);
    return () => ctrl.abort();
  }, [loadAll]);

  const toggleSubTerm = (id: string) => {
    if (id === masterTermId) return;
    setSubTermIds((prev) =>
      prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id],
    );
  };

  const filteredEvents = (anomaliesData?.events ?? []).filter(
    (ev) => alarmTypeFilter === 'all' || ev.alarmType === alarmTypeFilter,
  );

  const exportAlarmsCsv = () => {
    if (!filteredEvents.length) return;
    const headers = [
      '告警类别',
      '终端ID',
      '终端名称',
      '所属网关',
      '物模型',
      '计量字段',
      '发生时间',
      '实测值',
      '基准/阈值',
      '偏差率(%)',
      '告警说明',
    ];
    const rows = filteredEvents.map((e) => [
      e.label,
      e.terminalId,
      e.terminalName,
      e.gatewayId,
      e.typeName,
      e.itemName || e.itemCode,
      e.time,
      `${e.actualValue.toFixed(2)} ${e.unit}`,
      `${e.baselineValue.toFixed(2)} ${e.unit}`,
      e.deviationPct.toFixed(1),
      e.detail,
    ]);
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
    a.download = `用能监测告警日志_${new Date().toISOString().slice(0, 10)}.csv`;
    a.click();
    URL.revokeObjectURL(url);
  };

  return (
    <div className="space-y-4">
      {/* 顶部模式切换与时间窗控制 */}
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border bg-elevated p-4">
        <div>
          <h1 className="text-base font-semibold text-ink">
            用能异常监测、跑冒滴漏平衡与数据质量诊断
          </h1>
          <p className="text-xs text-muted">
            覆盖手册 4.11 / 4.12「能耗超标、非工作时段用能异常、总分表跑冒滴漏、同点超均值50%突增、表码倒走」及接入清洗质量分析
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <div className="inline-flex rounded-md border border-border bg-surface p-0.5">
            {QUICK_WINDOWS.map((w, i) => (
              <button
                key={w.label}
                type="button"
                onClick={() => applyQuickWindow(i)}
                className={`rounded px-2.5 py-1 text-xs font-medium transition-colors ${
                  windowIdx === i
                    ? 'bg-accent text-white'
                    : 'text-muted hover:text-ink'
                }`}
              >
                {w.label}
              </button>
            ))}
          </div>

          <select
            value={selectedGateway}
            onChange={(e) => setSelectedGateway(e.target.value)}
            className="rounded-md border border-border bg-surface px-2.5 py-1.5 text-xs text-ink"
          >
            <option value="">全部网关</option>
            {gateways.map((g) => (
              <option key={g.gateway_id} value={g.gateway_id}>
                {g.name || g.gateway_id}
              </option>
            ))}
          </select>

          <select
            value={selectedTypeId}
            onChange={(e) => setSelectedTypeId(Number(e.target.value))}
            className="rounded-md border border-border bg-surface px-2.5 py-1.5 text-xs text-ink"
          >
            <option value={0}>全部物模型</option>
            {deviceTypes.map((dt) => (
              <option key={dt.id} value={dt.id}>
                {dt.name}
              </option>
            ))}
          </select>

          <button
            type="button"
            onClick={() => void loadAll()}
            disabled={loading}
            className="inline-flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-xs font-medium text-white hover:bg-accent/90 disabled:opacity-50"
          >
            <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} />
            执行诊断
          </button>
        </div>
      </div>

      {error && (
        <div className="rounded-lg border border-danger/30 bg-danger/10 px-4 py-3 text-xs text-danger">
          诊断加载失败：{error}
        </div>
      )}

      {/* 子视图切换栏 */}
      <div className="flex items-center gap-2 border-b border-border pb-2">
        <button
          type="button"
          onClick={() => setActiveTab('energy_alarms')}
          className={`inline-flex items-center gap-1.5 rounded-md px-3.5 py-1.5 text-xs font-semibold transition-colors ${
            activeTab === 'energy_alarms'
              ? 'bg-accent text-white'
              : 'bg-elevated text-muted hover:text-ink'
          }`}
        >
          <Flame className="h-3.5 w-3.5" />
          用能异常监测与跑冒滴漏平衡 (4.11 / 4.12 / 4.6.2)
        </button>

        <button
          type="button"
          onClick={() => setActiveTab('data_quality')}
          className={`inline-flex items-center gap-1.5 rounded-md px-3.5 py-1.5 text-xs font-semibold transition-colors ${
            activeTab === 'data_quality'
              ? 'bg-accent text-white'
              : 'bg-elevated text-muted hover:text-ink'
          }`}
        >
          <ShieldCheck className="h-3.5 w-3.5" />
          物联接入数据清洗与质量规则分析
        </button>
      </div>

      {activeTab === 'energy_alarms' ? (
        <div className="space-y-4">
          {/* 4.11 用能监测规则配置栏 */}
          <div className="rounded-lg border border-border bg-elevated p-4 space-y-3">
            <div className="text-xs font-semibold text-ink">
              用能监测规则参数配置（对齐手册 4.11.1：能耗超标 / 非工作时段能耗异常 / 跑冒滴漏总分表关系）
            </div>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-5">
              <div>
                <label className="mb-1 block text-[11px] text-muted">
                  能耗超标上限值 (留空按1.8倍均值)
                </label>
                <input
                  type="number"
                  placeholder="自动按车队均值×1.8"
                  value={dailyLimit}
                  onChange={(e) => setDailyLimit(e.target.value)}
                  className="w-full rounded border border-border bg-surface px-2.5 py-1.5 font-mono text-xs text-ink"
                />
              </div>

              <div>
                <label className="mb-1 block text-[11px] text-muted">
                  非工作时间段配置 (4.11.1)
                </label>
                <input
                  type="text"
                  value={offHoursSpec}
                  onChange={(e) => setOffHoursSpec(e.target.value)}
                  placeholder="23:00-07:00,12:00-13:30"
                  className="w-full rounded border border-border bg-surface px-2.5 py-1.5 font-mono text-xs text-ink"
                />
              </div>

              <div>
                <label className="mb-1 block text-[11px] text-muted">
                  非工作时段累计上限 (留空按35%)
                </label>
                <input
                  type="number"
                  placeholder="默认超全天35%告警"
                  value={offHoursLimit}
                  onChange={(e) => setOffHoursLimit(e.target.value)}
                  className="w-full rounded border border-border bg-surface px-2.5 py-1.5 font-mono text-xs text-ink"
                />
              </div>

              <div>
                <label className="mb-1 block text-[11px] text-muted">
                  跑冒滴漏 — 选择总表 (4.11.1)
                </label>
                <select
                  value={masterTermId}
                  onChange={(e) => {
                    const nextMaster = e.target.value;
                    setMasterTermId(nextMaster);
                    setSubTermIds((prev) => prev.filter((id) => id !== nextMaster));
                  }}
                  className="w-full rounded border border-border bg-surface px-2.5 py-1.5 text-xs text-ink"
                >
                  {terminals.map((t) => (
                    <option key={t.id} value={t.id}>
                      总表: {t.name || t.id}
                    </option>
                  ))}
                </select>
              </div>

              <div>
                <label className="mb-1 block text-[11px] text-muted">
                  跑冒滴漏允许误差率 (%)
                </label>
                <input
                  type="number"
                  step="0.5"
                  min={0}
                  value={leakageThresholdPct}
                  onChange={(e) => setLeakageThresholdPct(Math.max(0, Number(e.target.value) || 0))}
                  className="w-full rounded border border-border bg-surface px-2.5 py-1.5 font-mono text-xs text-ink"
                />
              </div>
            </div>

            {terminals.length > 1 && (
              <div className="flex flex-wrap items-center gap-1.5 border-t border-border pt-2.5">
                <span className="text-xs text-muted">跑冒滴漏 — 勾选下属分表：</span>
                {terminals
                  .filter((t) => t.id !== masterTermId)
                  .map((t) => {
                    const checked = subTermIds.includes(t.id);
                    return (
                      <button
                        key={t.id}
                        type="button"
                        onClick={() => toggleSubTerm(t.id)}
                        className={`inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-xs ${
                          checked
                            ? 'border-accent bg-accent/10 font-medium text-accent'
                            : 'border-border bg-surface text-muted'
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

          {/* 6 张告警分类统计卡 */}
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
            {[
              {
                key: 'over_limit',
                label: '能耗超标告警 (4.11)',
                count: anomaliesData?.summary.overLimitCount ?? 0,
                color: 'text-danger',
              },
              {
                key: 'spike_50pct',
                label: '超均值50%突增 (4.6.3)',
                count: anomaliesData?.summary.spike50PctCount ?? 0,
                color: 'text-warning',
              },
              {
                key: 'off_hours',
                label: '非工作时段违规用能',
                count: anomaliesData?.summary.offHoursAnomalyCount ?? 0,
                color: 'text-warning',
              },
              {
                key: 'leakage',
                label: '跑冒滴漏支路失衡',
                count: anomaliesData?.summary.leakageAnomalyCount ?? 0,
                color: 'text-danger',
              },
              {
                key: 'meter_rollback',
                label: '表码倒走/换表重置',
                count: anomaliesData?.summary.rollbackCount ?? 0,
                color: 'text-danger',
              },
              {
                key: 'range_out',
                label: '物模型上下限越界',
                count: anomaliesData?.summary.rangeOutCount ?? 0,
                color: 'text-ink',
              },
            ].map((card) => (
              <button
                key={card.key}
                type="button"
                onClick={() =>
                  setAlarmTypeFilter((prev) => (prev === card.key ? 'all' : card.key))
                }
                className={`rounded-lg border p-3 text-left transition-colors ${
                  alarmTypeFilter === card.key
                    ? 'border-accent bg-accent/5'
                    : 'border-border bg-elevated hover:border-accent/50'
                }`}
              >
                <div className="text-xs text-muted">{card.label}</div>
                <div className={`mt-1 font-mono text-2xl font-bold ${card.color}`}>
                  {card.count}
                </div>
                <div className="mt-0.5 text-[10px] text-muted">
                  {alarmTypeFilter === card.key ? '已筛选此类告警' : '点击筛选明细'}
                </div>
              </button>
            ))}
          </div>

          {/* 4.6.2 能源平衡图 & 4.11.1 跑冒滴漏总分表差额分析 */}
          {anomaliesData?.balanceAnalysis && (
            <div className="rounded-lg border border-border bg-elevated p-4">
              <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
                <div className="flex items-center gap-2">
                  <Scale className="h-4 w-4 text-accent" />
                  <h3 className="text-sm font-semibold text-ink">
                    总分表能源平衡与跑冒滴漏监测图 (4.6.2 能源平衡图 / 4.11.1 跑冒滴漏)
                  </h3>
                </div>
                <div className="flex flex-wrap items-center gap-4 text-xs font-mono">
                  <span>
                    总表 ({anomaliesData.balanceAnalysis.masterTerminalName || anomaliesData.balanceAnalysis.masterTerminalId}):{' '}
                    <strong className="text-ink">
                      {fmtVal(anomaliesData.balanceAnalysis.masterTotalUsage)}{' '}
                      {anomaliesData.balanceAnalysis.unit}
                    </strong>
                  </span>
                  <span>
                    分表合计 ({anomaliesData.balanceAnalysis.subTerminalIds.length} 块):{' '}
                    <strong className="text-ink">
                      {fmtVal(anomaliesData.balanceAnalysis.subTotalUsage)}{' '}
                      {anomaliesData.balanceAnalysis.unit}
                    </strong>
                  </span>
                  <span
                    className={
                      Math.abs(anomaliesData.balanceAnalysis.totalDiffPercent) >
                      anomaliesData.balanceAnalysis.thresholdPct
                        ? 'font-bold text-danger'
                        : 'text-success'
                    }
                  >
                    总差额损耗: {fmtVal(anomaliesData.balanceAnalysis.totalDiffUsage)}{' '}
                    {anomaliesData.balanceAnalysis.unit} (
                    {anomaliesData.balanceAnalysis.totalDiffPercent.toFixed(1)}%)
                  </span>
                </div>
              </div>

              <BalanceLeakageChart balance={anomaliesData.balanceAnalysis} />
            </div>
          )}

          {/* 4.12 告警监测日志明细表 */}
          <div className="rounded-lg border border-border bg-elevated">
            <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3">
              <div className="flex items-center gap-2">
                <AlertTriangle className="h-4 w-4 text-warning" />
                <h3 className="text-sm font-semibold text-ink">
                  用能监测告警日志 ({filteredEvents.length} 条)
                </h3>
                {alarmTypeFilter !== 'all' && (
                  <button
                    type="button"
                    onClick={() => setAlarmTypeFilter('all')}
                    className="rounded bg-surface px-2 py-0.5 text-xs text-accent hover:underline"
                  >
                    清除类型筛选
                  </button>
                )}
              </div>

              <button
                type="button"
                onClick={exportAlarmsCsv}
                disabled={filteredEvents.length === 0}
                className="inline-flex items-center gap-1.5 rounded-md border border-border bg-surface px-2.5 py-1 text-xs font-medium text-ink hover:bg-elevated disabled:opacity-50"
              >
                <Download className="h-3.5 w-3.5" />
                导出监测日志 Excel (CSV)
              </button>
            </div>

            <div className="overflow-x-auto">
              <table className="w-full border-collapse text-left text-xs">
                <thead>
                  <tr className="border-b border-border bg-surface text-muted">
                    <th className="px-3 py-2 font-medium">监测类别</th>
                    <th className="px-3 py-2 font-medium">监测点/表计</th>
                    <th className="px-3 py-2 font-medium">所属网关</th>
                    <th className="px-3 py-2 font-medium">仪表类型</th>
                    <th className="px-3 py-2 font-medium">告警时间</th>
                    <th className="px-3 py-2 text-right font-medium">实测数值</th>
                    <th className="px-3 py-2 text-right font-medium">基准/上限</th>
                    <th className="px-3 py-2 text-right font-medium">偏差率</th>
                    <th className="px-3 py-2 font-medium">诊断详情</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {filteredEvents.map((ev, idx) => (
                    <tr key={`${ev.terminalId}-${ev.time}-${idx}`} className="hover:bg-surface/60">
                      <td className="px-3 py-2.5">
                        <span
                          className={`inline-flex rounded px-2 py-0.5 text-[11px] font-medium ${
                            ev.tone === 'danger'
                              ? 'bg-danger/10 text-danger'
                              : 'bg-warning/10 text-warning'
                          }`}
                        >
                          {ev.label}
                        </span>
                      </td>
                      <td className="px-3 py-2.5 font-medium text-ink">
                        {ev.terminalName || ev.terminalId}
                        <span className="ml-1 font-mono text-[11px] text-muted">
                          ({ev.terminalId})
                        </span>
                      </td>
                      <td className="px-3 py-2.5 font-mono text-muted">{ev.gatewayId}</td>
                      <td className="px-3 py-2.5 text-muted">{ev.typeName}</td>
                      <td className="px-3 py-2.5 font-mono text-muted">
                        {ev.time ? formatDateTime(ev.time) : '—'}
                      </td>
                      <td className="px-3 py-2.5 text-right font-mono font-semibold text-ink">
                        {fmtVal(ev.actualValue)} {ev.unit}
                      </td>
                      <td className="px-3 py-2.5 text-right font-mono text-muted">
                        {fmtVal(ev.baselineValue)} {ev.unit}
                      </td>
                      <td className="px-3 py-2.5 text-right font-mono font-semibold text-danger">
                        {ev.deviationPct > 0 ? '+' : ''}
                        {ev.deviationPct.toFixed(1)}%
                      </td>
                      <td className="px-3 py-2.5 text-muted">{ev.detail}</td>
                    </tr>
                  ))}
                  {filteredEvents.length === 0 && (
                    <tr>
                      <td colSpan={9} className="px-3 py-8 text-center text-muted">
                        当前时段内未检测到符合条件的用能异常事件
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </div>
        </div>
      ) : (
        /* ── Tab 2: 物联接入数据清洗与质量规则分析 ─────────────────────────── */
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
            <div className="rounded-lg border border-border bg-elevated p-3">
              <div className="text-xs text-muted">接入数据项总数</div>
              <div className="mt-1 font-mono text-xl font-bold text-ink">
                {fmtVal(qualityData?.rawSummary.totalItems, 0)}
              </div>
              <div className="text-[11px] text-muted">
                有效入库 {fmtVal(qualityData?.rawSummary.writtenCount, 0)}
              </div>
            </div>

            <div className="rounded-lg border border-border bg-elevated p-3">
              <div className="text-xs text-muted">清洗完全合格率</div>
              <div className="mt-1 font-mono text-xl font-bold text-success">
                {qualityData?.rawSummary.passRate !== undefined
                  ? `${qualityData.rawSummary.passRate.toFixed(1)}%`
                  : '—'}
              </div>
              <div className="text-[11px] text-muted">
                合格 {fmtVal(qualityData?.rawSummary.cleanPassCount, 0)} 条
              </div>
            </div>

            <div className="rounded-lg border border-border bg-elevated p-3">
              <div className="text-xs text-muted">带病入库 (Quality &gt; 0)</div>
              <div className="mt-1 font-mono text-xl font-bold text-warning">
                {fmtVal(qualityData?.rawSummary.qualityViolationCount, 0)}
              </div>
              <div className="text-[11px] text-muted">
                越限 {qualityData?.rawSummary.rangeOutCount ?? 0} · 规则命中{' '}
                {qualityData?.rawSummary.ruleHitCount ?? 0}
              </div>
            </div>

            <div className="rounded-lg border border-border bg-elevated p-3">
              <div className="text-xs text-muted">清洗剔除 (Dropped)</div>
              <div className="mt-1 font-mono text-xl font-bold text-danger">
                {fmtVal(qualityData?.rawSummary.droppedCount, 0)}
              </div>
              <div className="text-[11px] text-muted">
                忽略未绑定 {qualityData?.rawSummary.ignoredCount ?? 0}
              </div>
            </div>

            <div className="rounded-lg border border-border bg-elevated p-3">
              <div className="text-xs text-muted">异常事件累计次数</div>
              <div className="mt-1 font-mono text-xl font-bold text-danger">
                {fmtVal(qualityData?.totalExceptionCount, 0)}
              </div>
              <div className="text-[11px] text-muted">来自 exception_count 统计</div>
            </div>

            <div className="rounded-lg border border-border bg-elevated p-3">
              <div className="text-xs text-muted">受影响终端数</div>
              <div className="mt-1 font-mono text-xl font-bold text-ink">
                {fmtVal(qualityData?.affectedTerminals, 0)}
              </div>
              <div className="text-[11px] text-muted">产生过异常的终端</div>
            </div>
          </div>

          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            {/* 高频异常终端 Top 15 */}
            <div className="rounded-lg border border-border bg-elevated p-4">
              <div className="mb-3 flex items-center gap-2">
                <ShieldAlert className="h-4 w-4 text-danger" />
                <h3 className="text-sm font-semibold text-ink">高频异常终端 Top 15</h3>
              </div>
              <div className="overflow-x-auto">
                <table className="w-full border-collapse text-left text-xs">
                  <thead>
                    <tr className="border-b border-border bg-surface text-muted">
                      <th className="px-2.5 py-2 font-medium">终端</th>
                      <th className="px-2.5 py-2 font-medium">网关</th>
                      <th className="px-2.5 py-2 font-medium">首要错误</th>
                      <th className="px-2.5 py-2 text-right font-medium">异常总数</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-border">
                    {(qualityData?.topTerminals ?? []).map((t) => (
                      <tr key={t.terminalId}>
                        <td className="px-2.5 py-2 font-medium text-ink">
                          {t.terminalName || t.terminalId}
                        </td>
                        <td className="px-2.5 py-2 font-mono text-muted">{t.gatewayId}</td>
                        <td className="px-2.5 py-2 text-warning">{t.primaryErrorLabel}</td>
                        <td className="px-2.5 py-2 text-right font-mono font-bold text-danger">
                          {t.totalCount}
                        </td>
                      </tr>
                    ))}
                    {(!qualityData?.topTerminals || qualityData.topTerminals.length === 0) && (
                      <tr>
                        <td colSpan={4} className="py-6 text-center text-muted">
                          暂无异常终端记录
                        </td>
                      </tr>
                    )}
                  </tbody>
                </table>
              </div>
            </div>

            {/* 高频问题字段 Top 15 */}
            <div className="rounded-lg border border-border bg-elevated p-4">
              <h3 className="mb-3 text-sm font-semibold text-ink">高频问题计量字段 Top 15</h3>
              <div className="overflow-x-auto">
                <table className="w-full border-collapse text-left text-xs">
                  <thead>
                    <tr className="border-b border-border bg-surface text-muted">
                      <th className="px-2.5 py-2 font-medium">字段编码</th>
                      <th className="px-2.5 py-2 text-right font-medium">质量越界</th>
                      <th className="px-2.5 py-2 text-right font-medium">清洗剔除</th>
                      <th className="px-2.5 py-2 font-medium">原因示例</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-border">
                    {(qualityData?.topItems ?? []).map((it) => (
                      <tr key={it.itemCode}>
                        <td className="px-2.5 py-2 font-mono font-medium text-ink">
                          {it.itemCode}
                        </td>
                        <td className="px-2.5 py-2 text-right font-mono text-warning">
                          {it.qualityViolationCount}
                        </td>
                        <td className="px-2.5 py-2 text-right font-mono text-danger">
                          {it.droppedCount}
                        </td>
                        <td className="px-2.5 py-2 text-muted">{it.sampleReason || '—'}</td>
                      </tr>
                    ))}
                    {(!qualityData?.topItems || qualityData.topItems.length === 0) && (
                      <tr>
                        <td colSpan={4} className="py-6 text-center text-muted">
                          暂无问题字段记录
                        </td>
                      </tr>
                    )}
                  </tbody>
                </table>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function BalanceLeakageChart({
  balance,
}: {
  balance: NonNullable<AnalyticsAnomaliesPayload['balanceAnalysis']>;
}) {
  if (!balance.buckets.length) {
    return (
      <div className="py-6 text-center text-xs text-muted">当前选定总分表暂无对齐快照数据</div>
    );
  }
  const width = 860;
  const height = 190;
  const padL = 52;
  const padR = 16;
  const padT = 14;
  const padB = 28;
  const plotW = width - padL - padR;
  const plotH = height - padT - padB;

  const maxV = Math.max(
    1e-6,
    ...balance.buckets.map((b) => Math.max(b.masterUsage, b.subUsageSum)),
  );
  const step = plotW / balance.buckets.length;
  const barW = Math.max(3, Math.min(16, step * 0.35));

  return (
    <div className="w-full overflow-x-auto">
      <svg viewBox={`0 0 ${width} ${height}`} className="h-44 w-full min-w-[540px]">
        {balance.buckets.map((b, idx) => {
          const cx = padL + idx * step + step / 2;
          const mH = (b.masterUsage / maxV) * plotH;
          const sH = (b.subUsageSum / maxV) * plotH;
          return (
            <g key={b.time}>
              <rect
                x={cx - barW - 1}
                y={padT + plotH - mH}
                width={barW}
                height={Math.max(2, mH)}
                rx={1.5}
                fill={b.leakageAlarm ? '#dc2626' : '#2563eb'}
              >
                <title>
                  {formatDateTime(b.time)} 总表: {fmtVal(b.masterUsage)} {balance.unit} | 分表合计:{' '}
                  {fmtVal(b.subUsageSum)} {balance.unit} | 差额: {fmtVal(b.diffUsage)} (
                  {b.diffPercent.toFixed(1)}%)
                </title>
              </rect>
              <rect
                x={cx + 1}
                y={padT + plotH - sH}
                width={barW}
                height={Math.max(2, sH)}
                rx={1.5}
                fill="#059669"
              />
            </g>
          );
        })}
      </svg>
    </div>
  );
}
