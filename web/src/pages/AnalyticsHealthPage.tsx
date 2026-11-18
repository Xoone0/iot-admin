import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  HeartPulse,
  RotateCw,
} from 'lucide-react';
import { fetchAnalyticsHealth } from '../api/client';
import type {
  AnalyticsHealthPayload,
  AnalyticsTerminalHealth,
} from '../api/client';
import { StatusBadge } from '../components/FieldValue';
import { EmptyState, ErrorState, Skeleton } from '../components/States';
import { cn } from '../lib/cn';
import { formatDateTime, formatNumber } from '../lib/format';

function toLocalInput(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function presetRange(hours: number): { from: string; to: string } {
  const now = new Date();
  const from = new Date(now.getTime() - hours * 3600_000);
  return { from: toLocalInput(from), to: toLocalInput(now) };
}

function formatBytes(bytes: number): string {
  if (!bytes || bytes <= 0) return '0 B';
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
}

function formatDurationSec(seconds?: number): string {
  if (seconds === undefined || seconds === null) return '从未上报';
  if (seconds < 60) return `${seconds} 秒`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟`;
  if (seconds < 86400) return `${(seconds / 3600).toFixed(1)} 小时`;
  return `${(seconds / 86400).toFixed(1)} 天`;
}

const PRESETS = [
  { label: '近 6 小时', hours: 6 },
  { label: '近 24 小时', hours: 24 },
  { label: '近 7 天', hours: 24 * 7 },
  { label: '近 30 天', hours: 24 * 30 },
];

const HEALTH_META: Record<
  AnalyticsTerminalHealth['healthStatus'],
  { label: string; tone: 'positive' | 'warning' | 'negative' | 'neutral' }
> = {
  healthy: { label: '健康活跃', tone: 'positive' },
  silent: { label: '在线静默', tone: 'warning' },
  warning: { label: '异常/迟到', tone: 'warning' },
  offline: { label: '离线', tone: 'negative' },
  disabled: { label: '已停用', tone: 'neutral' },
};

/** 设备与网关运行健康分析页：在线率、报文吞吐、静默失联检测、上报间隔与迟到诊断。 */
export function AnalyticsHealthPage() {
  const [range, setRange] = useState(() => presetRange(24));
  const [activePreset, setActivePreset] = useState<number | null>(24);
  const [gatewayFilter, setGatewayFilter] = useState<string>('');
  const [statusFilter, setStatusFilter] = useState<string>('all');

  const [payload, setPayload] = useState<AnalyticsHealthPayload | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const runQuery = useCallback(
    async (overrideRange = range, overrideGw = gatewayFilter) => {
      setLoading(true);
      setError(null);
      try {
        const res = await fetchAnalyticsHealth({
          from: new Date(overrideRange.from).toISOString(),
          to: new Date(overrideRange.to).toISOString(),
          gatewayId: overrideGw || undefined,
        });
        setPayload(res);
      } catch (err) {
        setPayload(null);
        setError(err instanceof Error ? err.message : '运行健康分析加载失败');
      } finally {
        setLoading(false);
      }
    },
    [range, gatewayFilter],
  );

  useEffect(() => {
    void runQuery();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [gatewayFilter]);

  const applyPreset = (hours: number) => {
    const next = presetRange(hours);
    setActivePreset(hours);
    setRange(next);
    void runQuery(next);
  };

  const overview = payload?.overview;
  const gateways = payload?.gateways ?? [];
  const byDeviceType = payload?.byDeviceType ?? [];
  const terminals = payload?.terminals ?? [];

  const filteredTerminals = useMemo(() => {
    if (statusFilter === 'all') return terminals;
    return terminals.filter((t) => t.healthStatus === statusFilter);
  }, [terminals, statusFilter]);

  const statusCounts = useMemo(() => {
    const counts: Record<string, number> = {
      all: terminals.length,
      silent: 0,
      warning: 0,
      offline: 0,
      healthy: 0,
      disabled: 0,
    };
    for (const t of terminals) {
      counts[t.healthStatus] = (counts[t.healthStatus] ?? 0) + 1;
    }
    return counts;
  }, [terminals]);

  return (
    <div className="mx-auto max-w-[1500px] px-6 py-5">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="min-w-0">
          <p className="text-xs text-muted">分析 / 运行健康</p>
          <h1 className="mt-1 flex items-center gap-2 text-xl font-semibold text-fg">
            <HeartPulse className="size-5 text-primary" aria-hidden />
            设备与网关运行健康分析
          </h1>
          <p className="mt-1 text-xs text-muted">
            监控边缘网关与终端在线率、上报吞吐量、平均采集间隔，识别「在线但无数据上报」或「频繁迟到丢弃」的亚健康设备
          </p>
        </div>
        <button
          type="button"
          onClick={() => void runQuery()}
          disabled={loading}
          className="inline-flex h-9 items-center gap-1.5 rounded-md border border-line bg-surface px-3 text-sm text-fg hover:bg-surface-2 disabled:opacity-50"
        >
          <RotateCw className={cn('size-4', loading && 'animate-spin')} aria-hidden />
          刷新分析
        </button>
      </div>

      {/* 过滤栏 */}
      <section className="mt-4 flex flex-wrap items-center gap-3 rounded-xl border border-line bg-surface px-4 py-3 shadow-panel">
        <div className="inline-flex h-8 items-center rounded-md border border-line p-0.5" role="group" aria-label="快捷时间范围">
          {PRESETS.map((p) => (
            <button
              key={p.hours}
              type="button"
              onClick={() => applyPreset(p.hours)}
              className={cn(
                'h-7 rounded px-2.5 text-xs transition-colors',
                activePreset === p.hours ? 'bg-primary-soft font-medium text-primary' : 'text-muted hover:text-fg',
              )}
            >
              {p.label}
            </button>
          ))}
        </div>

        <label className="flex items-center gap-1.5 text-xs text-muted">
          起
          <input
            type="datetime-local"
            value={range.from}
            onChange={(e) => {
              setActivePreset(null);
              setRange((cur) => ({ ...cur, from: e.target.value }));
            }}
            className="h-8 rounded-md border border-line bg-surface px-2 text-xs text-fg"
          />
        </label>
        <label className="flex items-center gap-1.5 text-xs text-muted">
          至
          <input
            type="datetime-local"
            value={range.to}
            onChange={(e) => {
              setActivePreset(null);
              setRange((cur) => ({ ...cur, to: e.target.value }));
            }}
            className="h-8 rounded-md border border-line bg-surface px-2 text-xs text-fg"
          />
        </label>

        {gatewayFilter ? (
          <button
            type="button"
            onClick={() => setGatewayFilter('')}
            className="inline-flex h-8 items-center gap-1 rounded-md bg-primary-soft px-2.5 text-xs font-medium text-primary"
          >
            网关: {gatewayFilter} ×
          </button>
        ) : null}

        <button
          type="button"
          onClick={() => void runQuery()}
          disabled={loading}
          className="inline-flex h-8 items-center rounded-md bg-primary px-3 text-xs font-medium text-white hover:bg-primary-hover disabled:opacity-50"
        >
          {loading ? '诊断中…' : '执行诊断'}
        </button>
      </section>

      {error ? (
        <div className="mt-4 rounded-xl border border-line bg-surface p-6 shadow-panel">
          <ErrorState message={error} onRetry={() => void runQuery()} />
        </div>
      ) : loading && !payload ? (
        <div className="mt-4 space-y-3 rounded-xl border border-line bg-surface p-6 shadow-panel">
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-64 w-full" />
        </div>
      ) : payload && overview ? (
        <>
          {/* 全局健康 KPI */}
          <div className="mt-4 grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
            <HealthKpi
              label="边缘网关在线率"
              value={`${overview.gatewayOnlineRate.toFixed(1)}%`}
              tone={
                overview.enabledGateways === 0
                  ? 'neutral'
                  : overview.gatewayOnlineRate >= 95
                    ? 'positive'
                    : overview.gatewayOnlineRate >= 70
                      ? 'warning'
                      : 'negative'
              }
              sub={`在线 ${overview.onlineGateways} / 启用 ${overview.enabledGateways}（共 ${overview.totalGateways} 台）`}
            />
            <HealthKpi
              label="终端设备在线率"
              value={`${overview.terminalOnlineRate.toFixed(1)}%`}
              tone={
                overview.enabledTerminals === 0
                  ? 'neutral'
                  : overview.terminalOnlineRate >= 90
                    ? 'positive'
                    : overview.terminalOnlineRate >= 60
                      ? 'warning'
                      : 'negative'
              }
              sub={`在线 ${overview.onlineTerminals} / 启用 ${overview.enabledTerminals}（共 ${overview.totalTerminals} 台）`}
            />
            <HealthKpi
              label="窗口活跃 / 在线静默"
              value={`${overview.activeTerminalsInWindow} / ${overview.silentOnlineTerminals}`}
              tone={overview.silentOnlineTerminals > 0 ? 'warning' : 'positive'}
              sub="在线静默指在线但超 1h 或窗口内无上报"
            />
            <HealthKpi
              label="窗口报文吞吐量"
              value={`${formatNumber(overview.totalMessagesInWindow)} 包`}
              tone="neutral"
              sub={`流量 ${formatBytes(overview.totalBytesInWindow)} · 数据项 ${formatNumber(overview.totalItemsInWindow)}`}
            />
            <HealthKpi
              label="快照迟到丢弃次数"
              value={formatNumber(overview.lateDroppedInWindow)}
              tone={overview.lateDroppedInWindow > 0 ? 'warning' : 'positive'}
              sub="超过段结束 + 15 分钟宽限被丢弃"
            />
          </div>

          {/* 网关健康吞吐 + 物模型在线率分布 */}
          <div className="mt-4 grid gap-4 lg:grid-cols-12">
            <section className="overflow-hidden rounded-xl border border-line bg-surface shadow-panel lg:col-span-7">
              <header className="flex items-center justify-between border-b border-line px-4 py-3">
                <div>
                  <h2 className="text-sm font-medium text-fg">边缘网关运行状态与吞吐</h2>
                  <p className="mt-0.5 text-xs text-muted">
                    点击网关行可按该网关过滤下方终端健康列表
                  </p>
                </div>
                <Link to="/e/gateways" className="text-xs text-primary hover:underline">
                  管理网关
                </Link>
              </header>
              {gateways.length === 0 ? (
                <div className="p-8">
                  <EmptyState title="尚未注册边缘网关" hint="可在「边缘网关」页面新增并启用网关。" />
                </div>
              ) : (
                <div className="overflow-x-auto">
                  <table className="min-w-full text-sm">
                    <thead>
                      <tr className="bg-surface-2 text-xs font-medium text-muted">
                        <th className="border-b border-line px-3 py-2 text-left">网关</th>
                        <th className="border-b border-line px-3 py-2 text-left">健康度</th>
                        <th className="border-b border-line px-3 py-2 text-right">下属终端 (在线/启用)</th>
                        <th className="border-b border-line px-3 py-2 text-right">窗口报文 / 流量</th>
                        <th className="border-b border-line px-3 py-2 text-right">异常数</th>
                        <th className="border-b border-line px-3 py-2 text-left">最近上报</th>
                      </tr>
                    </thead>
                    <tbody>
                      {gateways.map((gw) => {
                        const isSelected = gatewayFilter === gw.gatewayId;
                        const badge =
                          gw.healthStatus === 'healthy'
                            ? { label: '正常', tone: 'positive' as const }
                            : gw.healthStatus === 'warning'
                              ? { label: '告警', tone: 'warning' as const }
                              : gw.healthStatus === 'offline'
                                ? { label: '离线', tone: 'negative' as const }
                                : { label: '停用', tone: 'neutral' as const };
                        return (
                          <tr
                            key={gw.gatewayId}
                            onClick={() =>
                              setGatewayFilter((cur) => (cur === gw.gatewayId ? '' : gw.gatewayId))
                            }
                            className={cn(
                              'cursor-pointer border-b border-line last:border-b-0 transition-colors',
                              isSelected ? 'bg-primary-soft/60' : 'hover:bg-surface-2/60',
                            )}
                          >
                            <td className="px-3 py-2 align-middle">
                              <div className="font-medium text-fg">{gw.name || gw.gatewayId}</div>
                              <div className="font-mono text-xs text-muted">{gw.gatewayId}</div>
                            </td>
                            <td className="px-3 py-2 align-middle whitespace-nowrap">
                              <StatusBadge label={badge.label} tone={badge.tone} />
                            </td>
                            <td className="px-3 py-2 text-right align-middle tabular">
                              <span className="font-medium text-fg">{gw.terminalOnline}</span>
                              <span className="text-muted"> / {gw.terminalEnabled}</span>
                              <span className="ml-1 text-xs text-muted">(共{gw.terminalTotal})</span>
                            </td>
                            <td className="px-3 py-2 text-right align-middle whitespace-nowrap tabular">
                              <span>{formatNumber(gw.messageCount)} 包</span>
                              <span className="ml-1.5 text-xs text-muted">
                                ({formatBytes(gw.byteSize)})
                              </span>
                            </td>
                            <td className="px-3 py-2 text-right align-middle tabular">
                              <span className={gw.exceptionCount > 0 ? 'font-medium text-warning' : 'text-muted'}>
                                {formatNumber(gw.exceptionCount)}
                              </span>
                            </td>
                            <td className="px-3 py-2 align-middle text-xs text-muted whitespace-nowrap tabular">
                              {gw.lastSeenAt ? formatDateTime(gw.lastSeenAt) : '—'}
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                </div>
              )}
            </section>

            <section className="rounded-xl border border-line bg-surface p-4 shadow-panel lg:col-span-5">
              <div className="flex items-center justify-between border-b border-line pb-3">
                <div>
                  <h2 className="text-sm font-medium text-fg">物模型终端在线率分布</h2>
                  <p className="mt-0.5 text-xs text-muted">按数据域与物模型统计启用终端的实时在线占比</p>
                </div>
                <Link to="/e/thing-models" className="text-xs text-primary hover:underline">
                  查看物模型
                </Link>
              </div>
              {byDeviceType.length === 0 ? (
                <p className="py-8 text-center text-sm text-muted">暂无物模型数据</p>
              ) : (
                <div className="mt-3 space-y-3">
                  {byDeviceType.map((dt) => {
                    const rate = dt.onlineRate;
                    const barColor =
                      dt.terminalEnabled === 0
                        ? 'bg-neutral/40'
                        : rate >= 90
                          ? 'bg-positive'
                          : rate >= 50
                            ? 'bg-warning'
                            : 'bg-negative';
                    return (
                      <div key={dt.typeId} className="space-y-1">
                        <div className="flex items-center justify-between text-xs">
                          <Link
                            to={`/e/thing-models/${dt.typeId}`}
                            className="truncate font-medium text-fg hover:text-primary"
                          >
                            {dt.domainName ? `${dt.domainName} · ` : ''}
                            {dt.typeName} ({dt.typeCode})
                          </Link>
                          <span className="shrink-0 text-muted tabular">
                            在线 {dt.terminalOnline}/{dt.terminalEnabled} ({rate.toFixed(0)}%)
                          </span>
                        </div>
                        <div className="h-2 w-full overflow-hidden rounded-full bg-surface-2">
                          <div
                            className={cn('h-full rounded-full transition-all', barColor)}
                            style={{ width: `${Math.max(rate, dt.terminalEnabled > 0 ? 2 : 0)}%` }}
                          />
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}
            </section>
          </div>

          {/* 终端健康度与上报活跃度诊断明细 */}
          <section className="mt-4 overflow-hidden rounded-xl border border-line bg-surface shadow-panel">
            <header className="flex flex-wrap items-center justify-between gap-3 border-b border-line px-4 py-3">
              <div>
                <h2 className="text-sm font-medium text-fg">终端运行健康与活跃度诊断表</h2>
                <p className="mt-0.5 text-xs text-muted">
                  自动将「在线静默」「异常/迟到」「离线」的亚健康终端置顶，辅助快速排查断连与时钟漂移问题
                </p>
              </div>
              <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="健康状态过滤">
                {(
                  [
                    { key: 'all', label: '全部' },
                    { key: 'silent', label: '在线静默' },
                    { key: 'warning', label: '异常/迟到' },
                    { key: 'offline', label: '离线' },
                    { key: 'healthy', label: '健康活跃' },
                    { key: 'disabled', label: '已停用' },
                  ] as const
                ).map((tab) => (
                  <button
                    key={tab.key}
                    type="button"
                    onClick={() => setStatusFilter(tab.key)}
                    className={cn(
                      'inline-flex h-7 items-center gap-1 rounded-md border px-2.5 text-xs transition-colors',
                      statusFilter === tab.key
                        ? 'border-primary bg-primary-soft font-medium text-primary'
                        : 'border-line bg-surface text-muted hover:bg-surface-2 hover:text-fg',
                    )}
                  >
                    <span>{tab.label}</span>
                    <span className="tabular text-[11px] opacity-80">
                      ({statusCounts[tab.key] ?? 0})
                    </span>
                  </button>
                ))}
              </div>
            </header>

            {filteredTerminals.length === 0 ? (
              <div className="p-8">
                <EmptyState title="当前筛选条件下没有终端" hint="可切换健康状态标签或清除网关筛选。" />
              </div>
            ) : (
              <div className="overflow-x-auto">
                <table className="min-w-full text-sm">
                  <thead>
                    <tr className="bg-surface-2 text-xs font-medium text-muted">
                      <th className="sticky left-0 z-10 border-b border-line bg-surface-2 px-4 py-2.5 text-left">
                        终端
                      </th>
                      <th className="border-b border-line px-3 py-2.5 text-left">健康诊断</th>
                      <th className="border-b border-line px-3 py-2.5 text-left">物模型 / 网关</th>
                      <th className="border-b border-line px-3 py-2.5 text-right">窗口有效上报</th>
                      <th className="border-b border-line px-3 py-2.5 text-right">平均上报间隔</th>
                      <th className="border-b border-line px-3 py-2.5 text-right">距今静默时长</th>
                      <th className="border-b border-line px-3 py-2.5 text-right">迟到丢弃 / 总异常</th>
                      <th className="border-b border-line px-3 py-2.5 text-left">最近上报时间</th>
                    </tr>
                  </thead>
                  <tbody>
                    {filteredTerminals.map((t) => {
                      const meta = HEALTH_META[t.healthStatus] ?? HEALTH_META.healthy;
                      return (
                        <tr
                          key={t.terminalId}
                          className="border-b border-line last:border-b-0 hover:bg-surface-2/60"
                        >
                          <td className="sticky left-0 z-10 border-r border-line bg-surface px-4 py-2.5 align-middle">
                            <Link
                              to={`/e/terminals/${encodeURIComponent(t.terminalId)}`}
                              className="font-medium text-primary hover:underline"
                            >
                              {t.name || t.terminalId}
                            </Link>
                            <div className="font-mono text-xs text-muted">{t.terminalId}</div>
                          </td>
                          <td className="px-3 py-2.5 align-middle whitespace-nowrap">
                            <StatusBadge label={meta.label} tone={meta.tone} />
                          </td>
                          <td className="px-3 py-2.5 align-middle">
                            <div className="truncate text-xs text-fg">{t.typeName || `#${t.typeId}`}</div>
                            <div className="truncate font-mono text-[11px] text-muted">{t.gatewayId}</div>
                          </td>
                          <td className="px-3 py-2.5 text-right align-middle tabular">
                            <span className="font-medium text-fg">{formatNumber(t.writtenCount)}</span>
                            {t.droppedCount > 0 ? (
                              <span className="ml-1 text-xs text-negative">
                                (剔除 {t.droppedCount})
                              </span>
                            ) : null}
                          </td>
                          <td className="px-3 py-2.5 text-right align-middle text-xs text-muted tabular">
                            {t.avgIntervalSeconds !== undefined
                              ? `${t.avgIntervalSeconds.toFixed(1)} s`
                              : '—'}
                          </td>
                          <td className="px-3 py-2.5 text-right align-middle text-xs whitespace-nowrap tabular">
                            <span
                              className={cn(
                                t.silenceSeconds !== undefined && t.silenceSeconds > 3600 && t.online
                                  ? 'font-medium text-warning'
                                  : 'text-muted',
                              )}
                            >
                              {formatDurationSec(t.silenceSeconds)}
                            </span>
                          </td>
                          <td className="px-3 py-2.5 text-right align-middle whitespace-nowrap tabular">
                            <span className={t.lateDroppedCount > 0 ? 'font-medium text-warning' : 'text-muted'}>
                              {t.lateDroppedCount}
                            </span>
                            {' / '}
                            <span className={t.exceptionCount > 0 ? 'font-medium text-negative' : 'text-muted'}>
                              {t.exceptionCount}
                            </span>
                          </td>
                          <td className="px-3 py-2.5 align-middle text-xs text-muted whitespace-nowrap tabular">
                            {t.lastSeenAt ? formatDateTime(t.lastSeenAt) : '—'}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </>
      ) : null}
    </div>
  );
}

function HealthKpi({
  label,
  value,
  sub,
  tone,
}: {
  label: string;
  value: string;
  sub: string;
  tone: 'neutral' | 'positive' | 'warning' | 'negative';
}) {
  const color =
    tone === 'positive'
      ? 'text-positive'
      : tone === 'warning'
        ? 'text-warning'
        : tone === 'negative'
          ? 'text-negative'
          : 'text-fg';
  return (
    <div className="rounded-xl border border-line bg-surface p-3.5 shadow-panel">
      <p className="text-xs text-muted">{label}</p>
      <p className={cn('mt-1.5 truncate text-lg font-semibold tabular', color)}>{value}</p>
      <p className="mt-1 truncate text-[11px] text-muted">{sub}</p>
    </div>
  );
}
