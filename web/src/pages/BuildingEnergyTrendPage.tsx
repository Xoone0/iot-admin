import { useEffect, useState } from 'react';
import { FileSearch, ListOrdered } from 'lucide-react';
import { Link, useSearchParams } from 'react-router-dom';
import type { Envelope } from '../api/types';
import { ENERGY_STATISTICS_PATH } from '../components/AppShell';
import { Drawer } from '../components/Drawer';
import { ErrorState, Skeleton } from '../components/States';
import { cn } from '../lib/cn';
import { EnergyFilterDrawer, energyFilterValues } from './BuildingEnergyWorkspace';

interface Coverage {
  expected: number;
  valid: number;
  missing: number;
  suspect: number;
  rate: number | null;
}

interface Period {
  from: string;
  to: string;
  hours: number;
  usage: number | null;
  coverage: Coverage;
  meterRuleVersion: string;
  classificationRuleVersion: string;
  catalogVersion: string;
  topologyVersion: string;
}

const statusLabels = {
  comparable: '可对比',
  unequal_periods: '周期范围不等',
  missing_data: '缺少计量数据',
  insufficient_quality: '数据质量不足',
  inconsistent_scope: '统计口径不一致',
  zero_baseline: '基期用量为零',
} as const;

interface Comparison {
  period: Period;
  status: keyof typeof statusLabels;
  delta: number | null;
  changeRate: number | null;
  adjusted: boolean;
}

interface TrendReport {
  sample: true;
  timezone: string;
  unit: string;
  asOf: string;
  ruleVersion: string;
  partial: boolean;
  current: Period;
  previous: Comparison;
  priorYear: Comparison;
  trend: Period[];
}

const filters = [
  { key: 'building', label: '建筑', options: [['office-a', '办公楼 A'], ['office-b', '办公楼 B']] },
  { key: 'energy', label: '能源', options: [['electricity', '用电'], ['water', '用水']] },
  { key: 'granularity', label: '统计粒度', options: [['day', '逐日'], ['hour', '逐时'], ['month', '逐月']] },
  { key: 'mode', label: '对比口径', options: [['complete', '完整周期'], ['to-date', '同期截至']] },
  { key: 'scenario', label: '样本场景', options: [['standard', '标准样本'], ['missing', '缺测'], ['suspect', '可疑值'], ['zero', '基期零值'], ['scope', '口径变更']] },
] as const;

const panel = 'min-w-0 rounded-xl border border-line bg-surface shadow-panel';
const quantity = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 });
const signedQuantity = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2, signDisplay: 'exceptZero' });
const percentage = new Intl.NumberFormat('zh-CN', { style: 'percent', maximumFractionDigits: 1 });
const signedPercentage = new Intl.NumberFormat('zh-CN', { style: 'percent', maximumFractionDigits: 1, signDisplay: 'exceptZero' });
const format = (value: number | null, formatter = quantity) => value === null ? '—' : formatter.format(value);

function periodQuality(period: Period) {
  const { usage, coverage } = period;
  if (usage === null && coverage.suspect > 0) return '可疑值';
  if (usage === null) return '缺测';
  if (coverage.valid < coverage.expected) return '部分覆盖';
  return usage === 0 ? '有效零' : '覆盖完整';
}

const qualityStyles: Record<ReturnType<typeof periodQuality>, string> = {
  '覆盖完整': 'bg-primary',
  '部分覆盖': 'bg-warning',
  '可疑值': 'bg-warning',
  '有效零': 'bg-positive',
  '缺测': 'border border-dashed border-muted',
};

function RangeText({ period, timezone }: { period: Period; timezone: string }) {
  const date = new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZoneName: 'shortOffset',
  });
  return (
    <span className="tabular">
      <time className="inline-block" dateTime={period.from}>{date.format(new Date(period.from))}</time>
      {' 至 '}
      <time className="inline-block" dateTime={period.to}>{date.format(new Date(period.to))}</time>
    </span>
  );
}

function TraceDrawer({ data }: { data: TrendReport }) {
  const [open, setOpen] = useState(false);
  const ranges = [
    { label: '本期', period: data.current },
    { label: '环比基期', period: data.previous.period },
    { label: '同比基期', period: data.priorYear.period },
  ];
  const cutoff = new Intl.DateTimeFormat('zh-CN', {
    timeZone: data.timezone, dateStyle: 'medium', timeStyle: 'short', hourCycle: 'h23',
  });

  return (
    <Drawer open={open} onOpenChange={setOpen} title="对比依据" description="合成数据 · 区间按项目时区展示，采用前闭后开边界。"
      trigger={
        <button type="button" className="inline-flex min-h-9 items-center gap-2 rounded-md border border-line bg-surface px-3 text-sm font-medium hover:bg-surface-2">
          <FileSearch className="size-4 text-primary" aria-hidden />对比依据
        </button>
      }>
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 rounded-lg border border-line bg-surface-2 p-4 text-xs">
        <dt className="text-muted">项目时区</dt><dd>{data.timezone}</dd>
        <dt className="text-muted">单位</dt><dd>{data.unit}</dd>
        <dt className="text-muted">数据可见截至</dt><dd><time dateTime={data.asOf}>{cutoff.format(new Date(data.asOf))}</time></dd>
        <dt className="text-muted">对比计算</dt><dd>{data.ruleVersion}</dd>
      </dl>
      <div className="mt-4 space-y-3">
        {ranges.map(({ label, period }) => (
          <section key={label} className="space-y-1.5 rounded-lg border border-line p-3 text-xs">
            <h3 className="text-sm font-medium">{label}</h3>
            <p className="text-muted"><RangeText period={period} timezone={data.timezone} /></p>
            <p className="tabular">实际时长 {format(period.hours)} 小时 · 用量 {format(period.usage)} {period.usage === null ? '' : data.unit}</p>
            <p className="tabular">覆盖率 {format(period.coverage.rate, percentage)}（有效 {format(period.coverage.valid)} / 预期 {format(period.coverage.expected)}）</p>
            <p className="text-muted tabular">缺测 {format(period.coverage.missing)} · 可疑 {format(period.coverage.suspect)} 表小时</p>
            <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 border-t border-line pt-2">
              <dt className="text-muted">计量规则</dt><dd className="break-all">{period.meterRuleVersion}</dd>
              <dt className="text-muted">分类计算</dt><dd className="break-all">{period.classificationRuleVersion}</dd>
              <dt className="text-muted">分类目录</dt><dd className="break-all">{period.catalogVersion}</dd>
              <dt className="text-muted">物理拓扑</dt><dd className="break-all">{period.topologyVersion}</dd>
            </dl>
          </section>
        ))}
      </div>
    </Drawer>
  );
}

function CompareCard({ label, result, unit, timezone }: { label: string; result: Comparison; unit: string; timezone: string }) {
  return (
    <section className={cn(panel, 'space-y-2 p-4')}>
      <div className="flex flex-wrap items-center justify-between gap-1.5">
        <h3 className="text-sm font-semibold">{label}</h3>
        <span className={cn('rounded-md px-1.5 py-0.5 text-xs', result.status === 'comparable'
          ? 'bg-positive-soft text-positive' : 'bg-warning-soft text-warning')}>
          {statusLabels[result.status]}
        </span>
      </div>
      <p className="text-2xl font-semibold tabular">{format(result.changeRate, signedPercentage)}</p>
      <p className="text-xs text-muted tabular">
        差值 {format(result.delta, signedQuantity)}{result.delta !== null ? ` ${unit}` : ''}
        <span className="mx-1.5" aria-hidden>·</span>
        基期 {format(result.period.usage)}{result.period.usage !== null ? ` ${unit}` : ''}
      </p>
      <p className="text-xs text-muted tabular">基期覆盖率 {format(result.period.coverage.rate, percentage)}</p>
      <p className="text-xs text-muted"><RangeText period={result.period} timezone={timezone} /></p>
      {result.adjusted && <p className="text-xs font-medium text-warning">基期边界已按日历调整</p>}
    </section>
  );
}

function TrendChart({ data, granularity }: { data: TrendReport; granularity: string }) {
  const shortDate = new Intl.DateTimeFormat('zh-CN', {
    timeZone: data.timezone,
    ...(granularity === 'hour' ? { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' as const }
      : granularity === 'month' ? { year: '2-digit', month: 'numeric' } : { month: 'numeric', day: 'numeric' }),
  });
  const fullDate = new Intl.DateTimeFormat('zh-CN', {
    timeZone: data.timezone, year: 'numeric', month: '2-digit', day: '2-digit',
    ...(granularity === 'hour' && { hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZoneName: 'shortOffset' }),
  });
  const scale = Math.max(1, ...data.trend.map((period) => period.usage ?? 0));
  const label = filters[2].options.find(([value]) => value === granularity)?.[1];
  const isPartial = (period: Period) => data.partial && period.from === data.current.from && period.to === data.current.to;
  return (
    <section aria-labelledby="f08-chart-title" className={cn(panel, 'p-4 sm:p-5')}>
      <header className="flex flex-wrap items-baseline justify-between gap-2">
        <h3 id="f08-chart-title" className="text-sm font-semibold">{label}用量趋势</h3>
        <span className="text-xs text-muted">用量（{data.unit}）</span>
      </header>

      <div className="my-3 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted">
        <span className="flex items-center gap-1.5"><span className="size-2 rounded-sm bg-primary" aria-hidden />覆盖完整</span>
        <span className="flex items-center gap-1.5"><span className="size-2 rounded-sm bg-warning" aria-hidden />部分覆盖 / 可疑值</span>
        <span className="flex items-center gap-1.5"><span className="h-0.5 w-2 bg-positive" aria-hidden />有效零</span>
        <span className="flex items-center gap-1.5"><span className="size-2 rounded-sm border border-dashed border-muted" aria-hidden />缺测</span>
        {data.partial && <span className="flex items-center gap-1.5"><span className="size-2 rounded-sm border border-dashed border-primary bg-primary-soft" aria-hidden />同期截至</span>}
      </div>

      <div className="flex gap-2" aria-hidden>
        <div className="flex h-36 flex-col justify-between text-right text-[11px] text-muted tabular sm:h-44">
          <span>{format(scale)}</span><span>{format(scale / 2)}</span><span>0</span>
        </div>
        <div className="min-w-0 flex-1">
          <div className="relative flex h-36 items-end gap-0.5 border-y border-line before:absolute before:inset-x-0 before:top-1/2 before:border-t before:border-line sm:h-44 sm:gap-2">
            {data.trend.map((period) => {
              const quality = periodQuality(period);
              const barStyle = isPartial(period) && quality === '覆盖完整'
                ? 'border border-dashed border-primary bg-primary-soft' : qualityStyles[quality];
              return (
                <div key={period.from} className="relative flex h-full min-w-0 flex-1 items-end justify-center"
                  title={`${fullDate.format(new Date(period.from))} · ${format(period.usage)} ${data.unit} · ${quality}${isPartial(period) ? ' · 同期截至' : ''}`}>
                  <span
                    className={cn('w-full max-w-12 rounded-t-sm', barStyle,
                      period.usage === 0 && 'h-0.5', period.usage === null && 'h-2')}
                    style={period.usage ? { height: `${period.usage / scale * 100}%` } : undefined}
                  />
                </div>
              );
            })}
          </div>
          <div className="mt-1 flex justify-between gap-2 text-[11px] text-muted tabular">
            {data.trend.filter((_, index) => index === 0 || index === Math.floor(data.trend.length / 2) || index === data.trend.length - 1)
              .map((period) => <span key={period.from}>{shortDate.format(new Date(period.from))}</span>)}
          </div>
        </div>
      </div>

      <details className="mt-3 text-xs">
        <summary className="w-fit cursor-pointer rounded py-1.5 font-medium text-primary">查看各周期明细</summary>
        <table className="mt-2 w-full text-left tabular">
          <caption className="sr-only">用量（{data.unit}）、覆盖率与数据质量</caption>
          <thead className="border-b border-line text-muted">
            <tr>
              <th scope="col" className="py-2">周期起点</th>
              <th scope="col" className="px-1 text-right">用量</th>
              <th scope="col" className="px-1 text-right">覆盖率</th>
              <th scope="col" className="text-right">质量</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {data.trend.map((period) => (
              <tr key={period.from}>
                <th scope="row" className="max-w-32 py-1.5 font-normal"><time dateTime={period.from}>{fullDate.format(new Date(period.from))}</time></th>
                <td className="px-1 text-right">{format(period.usage)}</td>
                <td className="px-1 text-right">{format(period.coverage.rate, percentage)}</td>
                <td className="text-right text-muted">
                  {periodQuality(period)}
                  {isPartial(period) && <span className="block text-primary">同期截至</span>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </details>
    </section>
  );
}

export default function BuildingEnergyTrendPage() {
  const [params, setParams] = useSearchParams();
  const selected = energyFilterValues(filters, params);
  if (selected.granularity === 'hour') selected.mode = 'complete';

  const query = new URLSearchParams(selected).toString();
  const [attempt, setAttempt] = useState(0);
  const [response, setResponse] = useState<{ key: string; data?: TrendReport; error?: string }>();
  const requestKey = `${query}|${attempt}`;
  const current = response?.key === requestKey ? response : undefined;
  const data = current?.data;
  const rankingSearch = new URLSearchParams({
    view: 'space', building: selected.building, energy: selected.energy,
    period: selected.granularity, mode: selected.mode,
  }).toString();

  useEffect(() => {
    const controller = new AbortController();
    fetch(`/admin/v1/analytics/building-energy/preview/trend?${query}`, { signal: controller.signal })
      .then(async (res) => {
        const payload = (await res.json()) as Envelope<TrendReport>;
        if (!res.ok || payload.code !== 0 || !payload.data?.sample) throw new Error(payload.message || '样本服务不可用');
        if (!controller.signal.aborted) setResponse({ key: requestKey, data: payload.data });
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) setResponse({ key: requestKey, error: error instanceof Error ? error.message : '样本加载失败' });
      });
    return () => controller.abort();
  }, [query, requestKey]);

  const labels = filters.map(({ key, options }) => options.find(([value]) => value === selected[key])?.[1]);
  return (
    <div className="space-y-4">
      <header className="flex flex-wrap items-center justify-between gap-2 border-b border-line pb-3">
        <h2 className="sr-only">分时用量与同期对比</h2>
        <div className="min-w-0">
          <p className="text-sm font-semibold">{labels[0]} · {labels[1]}</p>
          <p className="mt-0.5 text-xs leading-5 text-muted">{labels[2]} · {labels[3]}{selected.scenario !== 'standard' && ` · ${labels[4]}`}</p>
        </div>
        <EnergyFilterDrawer title="筛选趋势与对比" fields={filters} applied={selected} onApply={(next) => setParams(next)}
          normalize={(value) => value.granularity === 'hour' ? { ...value, mode: 'complete' } : value}
          disabled={(key, value) => key === 'mode' && value.granularity === 'hour'} />
      </header>
      {data && (
        <>
          <div className="flex flex-wrap items-center gap-2">
            {selected.scenario === 'standard' && (
              <Link to={{ pathname: `${ENERGY_STATISTICS_PATH}/dimensions`, search: rankingSearch }}
                className="inline-flex min-h-9 items-center gap-2 rounded-md border border-line bg-surface px-3 text-sm font-medium hover:bg-surface-2">
                <ListOrdered className="size-4 text-primary" aria-hidden />查看对象排名
              </Link>
            )}
            <TraceDrawer key={requestKey} data={data} />
          </div>
          <div className="grid gap-3 md:grid-cols-3">
            <section className={cn(panel, 'space-y-2 p-4')}>
              <div className="flex flex-wrap items-center justify-between gap-1.5">
                <h3 className="text-sm font-semibold">本期用量</h3>
                <span className="rounded-md bg-primary-soft px-1.5 py-0.5 text-xs text-primary">{data.partial ? '同期截至' : '完整周期'}</span>
              </div>
              <p className="text-2xl font-semibold tabular">
                {format(data.current.usage)}
                {data.current.usage !== null && <span className="ml-1.5 text-xs font-normal text-muted">{data.unit}</span>}
              </p>
              <p className="text-xs text-muted tabular">覆盖率 {format(data.current.coverage.rate, percentage)}</p>
              <p className="text-xs text-muted"><RangeText period={data.current} timezone={data.timezone} /></p>
            </section>
            <CompareCard label="环比" result={data.previous} unit={data.unit} timezone={data.timezone} />
            <CompareCard label="同比" result={data.priorYear} unit={data.unit} timezone={data.timezone} />
          </div>
          <TrendChart data={data} granularity={selected.granularity} />
        </>
      )}
      {current?.error && <div className={panel}><ErrorState message={current.error} onRetry={() => setAttempt((value) => value + 1)} /></div>}
      {!current && <div role="status" aria-label="加载趋势与对比"><Skeleton className="h-52" /></div>}
    </div>
  );
}
