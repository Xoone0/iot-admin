import { useEffect, useState } from 'react';
import { Activity, ArrowUpRight, Building2, Clock3, Droplets, FileSearch, Zap } from 'lucide-react';
import { Link, useSearchParams } from 'react-router-dom';
import type { Envelope } from '../api/types';
import { ENERGY_STATISTICS_PATH } from '../components/AppShell';
import { ENERGY_TARGET_SUMMARY_PATH } from '../components/BuildingEnergyNavigation';
import { Drawer } from '../components/Drawer';
import { EmptyState, ErrorState, Skeleton } from '../components/States';
import { cn } from '../lib/cn';
import { ENERGY_PREVIEW_API, EnergyScopeBar, useEnergyScope } from './BuildingEnergyWorkspace';

type Granularity = 'hour' | 'day' | 'month';
type Energy = 'electricity' | 'water';

interface Coverage {
  expected: number;
  valid: number;
  missing: number;
  suspect: number;
}

interface Bucket extends Coverage {
  from: string;
  to: string;
  usage: number | null;
}

interface Overview {
  sample: true;
  project: string;
  point: string;
  unit: string;
  timezone: string;
  from: string;
  to: string;
  result: Coverage & {
    usage: number | null;
    latestEnd: string | null;
    buckets: Bucket[];
  };
}

const periods = [
  { value: 'hour', label: '按小时', length: '24 小时' },
  { value: 'day', label: '按日', length: '14 天' },
  { value: 'month', label: '按月', length: '3 个月' },
] as const;

const energies = [
  { value: 'electricity', label: '用电' },
  { value: 'water', label: '用水' },
] as const;

const number = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 });
const percentage = new Intl.NumberFormat('zh-CN', { style: 'percent', maximumFractionDigits: 1 });
const panel = 'rounded-xl border border-line bg-surface shadow-[var(--shadow)]';

function coverageLabel(row: Coverage) {
  if (row.valid === row.expected) return '完整';
  if (row.valid > 0) return '部分覆盖';
  return row.suspect > 0 ? '待核查' : '缺测';
}

function Report({ data, granularity }: { data: Overview; granularity: Granularity }) {
  const { result } = data;
  const timestamp = new Intl.DateTimeFormat('zh-CN', {
    timeZone: data.timezone, year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
  });
  const short = new Intl.DateTimeFormat('zh-CN', {
    timeZone: data.timezone,
    ...(granularity === 'hour'
      ? { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }
      : granularity === 'day'
        ? { month: '2-digit', day: '2-digit' }
        : { year: 'numeric', month: '2-digit' }),
  });
  const fullTime = (value: string) => timestamp.format(new Date(value));
  const tick = (bucket: Bucket) => short.format(new Date(bucket.from));
  const latest = result.latestEnd ? fullTime(result.latestEnd) : '—';
  const peak = Math.max(0, ...result.buckets.map((bucket) => bucket.usage ?? 0));
  return (
    <>
      <p className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted">
        <span className="inline-flex items-center gap-1.5"><Building2 className="size-4" aria-hidden />{data.project}</span>
        <span>{data.point}</span>
        <span>{fullTime(data.from)} 至 {fullTime(data.to)}（{data.timezone}）</span>
      </p>

      <dl className="grid gap-3 sm:grid-cols-3">
        <div className={cn(panel, 'p-4')}>
          <dt className="flex items-center gap-2 text-sm text-muted"><Zap className="size-4 text-primary" aria-hidden />已统计用量</dt>
          <dd className="mt-2 text-2xl font-semibold tabular">
            {result.usage === null ? '—' : number.format(result.usage)}
            {result.usage === null ? null : <span className="ml-1.5 text-xs font-normal text-muted">{data.unit}</span>}
          </dd>
          <dd className="mt-1 text-xs text-muted">{coverageLabel(result)} · {result.valid}/{result.expected} 小时</dd>
        </div>
        <div className={cn(panel, 'p-4')}>
          <dt className="flex items-center gap-2 text-sm text-muted"><Activity className="size-4 text-primary" aria-hidden />数据覆盖率</dt>
          <dd className="mt-2 text-2xl font-semibold tabular">{percentage.format(result.valid / result.expected)}</dd>
          <dd className="mt-1 text-xs text-muted">缺测 {result.missing} 小时 · 待核查 {result.suspect} 小时</dd>
        </div>
        <div className={cn(panel, 'p-4')}>
          <dt className="flex items-center gap-2 text-sm text-muted"><Clock3 className="size-4 text-primary" aria-hidden />最近有效区间截止</dt>
          <dd className="mt-2 break-words text-xl font-semibold tabular">{latest}</dd>
          <dd className="mt-1 text-xs text-muted">项目当地时间</dd>
        </div>
      </dl>

      <section className={cn(panel, 'overflow-hidden p-4')} aria-labelledby="energy-trend-heading">
        <div className="mb-5 flex flex-wrap items-start justify-between gap-2">
          <h2 id="energy-trend-heading" className="text-sm font-semibold">用量趋势 <span className="font-normal text-muted">（{data.unit}）</span></h2>
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted">
            <span className="flex items-center gap-1"><span className="size-2 rounded-sm bg-primary" />完整</span>
            <span className="flex items-center gap-1"><span className="size-2 rounded-sm bg-warning" />部分覆盖 / 待核查</span>
            <span className="flex items-center gap-1"><span className="h-2 w-3 rounded-sm border border-dashed border-muted" />缺测</span>
          </div>
        </div>
        <div className="overflow-x-auto pb-1" tabIndex={0} aria-label="用量趋势，详细数值见下方时段明细">
          <div className="flex h-44 min-w-fit items-stretch gap-1.5" aria-hidden>
            {result.buckets.map((bucket, index) => {
              const partial = bucket.valid !== bucket.expected;
              const missing = bucket.usage === null && bucket.suspect === 0;
              const height = bucket.usage === null || peak === 0 ? 0 : (bucket.usage / peak) * 100;
              const showTick = result.buckets.length < 15 || index % 3 === 0 || index === result.buckets.length - 1;
              return (
                <div key={bucket.from} className="flex min-w-8 flex-1 flex-col">
                  <div className="flex min-h-0 flex-1 items-end justify-center border-b border-line"
                    title={`${tick(bucket)} · ${bucket.usage === null ? coverageLabel(bucket) : `${number.format(bucket.usage)} ${data.unit} · ${coverageLabel(bucket)}`}`}>
                    <div className={cn('w-full max-w-28 rounded-t-sm',
                      missing ? 'border border-dashed border-muted/60' : partial ? 'bg-warning' : 'bg-primary')}
                      style={{ height: height > 0 ? `${height}%` : bucket.usage === 0 ? 2 : 7 }} />
                  </div>
                  <div className="mt-2 h-4 text-center text-[10px] text-muted tabular">{showTick ? tick(bucket) : ''}</div>
                </div>
              );
            })}
          </div>
        </div>
      </section>

      <section className={cn(panel, 'overflow-hidden')} aria-labelledby="energy-details-heading">
        <h2 id="energy-details-heading" className="border-b border-line px-4 py-3 text-sm font-semibold">时段明细</h2>
        <div className="overflow-x-auto">
          <table className="w-full min-w-120 text-left text-sm">
            <thead className="bg-surface-2 text-xs text-muted">
              <tr>
                <th scope="col" className="px-4 py-2.5 font-medium">时段起点</th>
                <th scope="col" className="px-4 py-2.5 text-right font-medium">用量（{data.unit}）</th>
                <th scope="col" className="px-4 py-2.5 text-right font-medium">有效 / 预期小时</th>
                <th scope="col" className="px-4 py-2.5 font-medium">数据质量</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {result.buckets.map((bucket) => (
                <tr key={bucket.from}>
                  <td className="whitespace-nowrap px-4 py-2.5 tabular">{fullTime(bucket.from)}</td>
                  <td className="px-4 py-2.5 text-right tabular">{bucket.usage === null ? '—' : number.format(bucket.usage)}</td>
                  <td className="px-4 py-2.5 text-right text-muted tabular">{bucket.valid} / {bucket.expected}</td>
                  <td className="whitespace-nowrap px-4 py-2.5 text-xs">
                    <span className={cn('inline-flex rounded-md px-2 py-1',
                      bucket.valid === bucket.expected ? 'bg-positive-soft text-positive'
                        : bucket.missing === bucket.expected ? 'bg-neutral-soft text-neutral' : 'bg-warning-soft text-warning')}>
                      {coverageLabel(bucket)}
                    </span>
                    {bucket.valid > 0 && bucket.valid < bucket.expected ? (
                      <span className="ml-2 text-muted">缺测 {bucket.missing} · 待核查 {bucket.suspect}</span>
                    ) : null}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </>
  );
}

function LegacyMeterOverview() {
  const [params, setParams] = useSearchParams();
  const granularity: Granularity = periods.find((period) => period.value === params.get('granularity'))?.value ?? 'hour';
  const energy: Energy = energies.find((option) => option.value === params.get('energy'))?.value ?? 'electricity';
  const [attempt, setAttempt] = useState(0);
  const [response, setResponse] = useState<{ key: string; data?: Overview; error?: string }>();
  const query = new URLSearchParams({ energy, granularity }).toString();
  const requestKey = `${query}|${attempt}`;
  const current = response?.key === requestKey ? response : undefined;

  useEffect(() => {
    const controller = new AbortController();
    fetch(`${ENERGY_PREVIEW_API}?${query}`, { signal: controller.signal })
      .then(async (res) => {
        const payload = (await res.json()) as Envelope<Overview>;
        if (!res.ok || payload.code !== 0 || !payload.data?.sample) {
          throw new Error(payload.message || '样本服务不可用');
        }
        if (!controller.signal.aborted) setResponse({ key: requestKey, data: payload.data });
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) {
          setResponse({ key: requestKey, error: error instanceof Error ? error.message : '样本加载失败' });
        }
      });
    return () => controller.abort();
  }, [query, requestKey]);

  return (
    <div className="space-y-4">
      <h2 className="text-base font-semibold">单表时段明细</h2>
      <div className={cn(panel, 'flex flex-wrap items-center gap-x-6 gap-y-3 px-4 py-3')}>
        <label className="flex items-center gap-2 text-sm text-muted">
          能源
          <select value={energy} onChange={(event) => setParams({ energy: event.target.value, granularity })}
            className="rounded-md border border-line bg-surface px-3 py-1.5 text-sm text-fg">
            {energies.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
          </select>
        </label>
        <div className="flex items-center gap-2 text-sm text-muted">
          <span id="energy-period-label">统计粒度</span>
          <div role="group" aria-labelledby="energy-period-label" className="inline-flex rounded-lg border border-line bg-surface-2 p-0.5">
            {periods.map((period) => (
              <button key={period.value} type="button" aria-pressed={period.value === granularity}
                onClick={() => setParams({ energy, granularity: period.value })}
                className={cn('rounded-md px-2.5 py-1 text-xs font-medium',
                  period.value === granularity ? 'bg-primary-soft text-primary' : 'hover:bg-surface hover:text-fg')}>
                {period.label}
              </button>
            ))}
          </div>
        </div>
        <span className="text-xs text-muted">样本范围 {periods.find((period) => period.value === granularity)?.length}</span>
      </div>

      {current?.data ? <Report data={current.data} granularity={granularity} /> : null}
      {current?.error ? (
        <div className={panel}><ErrorState message={current.error} onRetry={() => setAttempt((value) => value + 1)} /></div>
      ) : null}
      {!current ? <div role="status" aria-label="加载时段统计"><Skeleton className="h-48" /></div> : null}
    </div>
  );
}

interface ScopeCoverage extends Coverage {
  rate: number | null;
}

interface Measure {
  usage: number | null;
  coverage: ScopeCoverage;
}

interface WindowVersions {
  from: string;
  to: string;
  classificationRuleVersion: string;
  catalogVersion: string;
  topologyVersion: string;
}

interface WindowReport extends WindowVersions {
  sample: true;
  unit: string;
  timezone: string;
  asOf: string;
  ruleVersion: string;
}

interface Composition {
  id: string;
  name: string;
  usage: number | null;
  share: number | null;
}

interface Source {
  name: string;
  source: string;
  usage: number | null;
  valid: number;
  missing: number;
  suspect: number;
  profileVersion: string;
}

interface DailyMeasure extends Measure {
  from: string;
  to: string;
}

interface CategoryOverview extends WindowReport {
  total: Measure;
  composition: Composition[];
  trend: { from: string; total: Measure }[];
  sources: Source[];
}

const dimensionViews = [
  { value: 'building', label: '建筑' },
  { value: 'space', label: '区域' },
  { value: 'organization', label: '组织' },
] as const;
type Dimension = (typeof dimensionViews)[number]['value'];

interface DimensionOverview extends WindowReport {
  rows: (Measure & { id: string; name: string; sources: { meterRuleVersion: string }[] })[];
}

const comparisonStatus = {
  comparable: '可对比',
  unequal_periods: '周期范围不等',
  missing_data: '缺少计量数据',
  insufficient_quality: '数据质量不足',
  inconsistent_scope: '统计口径不一致',
  zero_baseline: '基期用量为零',
};

interface Comparison {
  status: keyof typeof comparisonStatus;
  changeRate: number | null;
}

interface DailyOverview {
  sample: true;
  unit: string;
  timezone: string;
  asOf: string;
  current: WindowVersions & Measure & { meterRuleVersion: string };
  previous: Comparison;
  priorYear: Comparison;
  trend: DailyMeasure[];
}

const section = 'min-w-0 rounded-xl border border-line bg-surface p-4 sm:p-5';
const inlineAction = 'inline-flex min-h-10 shrink-0 items-center gap-1.5 rounded-md px-2 text-sm font-medium text-primary hover:bg-primary-soft focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary';
const signedPercentage = new Intl.NumberFormat('zh-CN', { style: 'percent', maximumFractionDigits: 1, signDisplay: 'exceptZero' });

function present(value: number | null) {
  return value === null ? '—' : number.format(value);
}

function quality({ coverage, usage }: Measure) {
  if (usage === null) return coverage.suspect > 0 ? '可疑值' : '缺测';
  if (coverage.valid < coverage.expected) return '部分覆盖';
  return usage === 0 ? '有效零' : '覆盖完整';
}

function localTimestamp(value: string, timezone: string) {
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
  }).format(new Date(value));
}

function sameInstant(a: string, b: string) {
  return Date.parse(a) === Date.parse(b);
}

function sameWindow(a: WindowVersions, b: WindowVersions) {
  return sameInstant(a.from, b.from) && sameInstant(a.to, b.to) && a.catalogVersion === b.catalogVersion
    && a.classificationRuleVersion === b.classificationRuleVersion && a.topologyVersion === b.topologyVersion;
}

function sameMeasure(a: Measure, b: Measure) {
  return a.usage === b.usage && a.coverage.valid === b.coverage.valid && a.coverage.expected === b.coverage.expected
    && a.coverage.missing === b.coverage.missing && a.coverage.suspect === b.coverage.suspect;
}

function useOverviewSample<T extends { sample: true }>(path: string | null) {
  const [attempt, setAttempt] = useState(0);
  const [result, setResult] = useState<{ key: string; data?: T; error?: string }>();
  const key = `${path}|${attempt}`;

  useEffect(() => {
    if (path === null) return;
    const controller = new AbortController();
    fetch(`${ENERGY_PREVIEW_API}/${path}`, { signal: controller.signal })
      .then(async (response) => {
        const payload = (await response.json()) as Envelope<T>;
        if (!response.ok || payload.code !== 0 || !payload.data?.sample) {
          throw new Error(payload.message || '合成数据服务不可用');
        }
        return payload.data;
      })
      .then((data) => {
        if (!controller.signal.aborted) setResult({ key, data });
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        const message = error instanceof TypeError || error instanceof SyntaxError ? '暂时无法读取合成样本，请重试。'
          : error instanceof Error ? error.message : '合成样本加载失败';
        setResult({ key, error: message });
      });
    return () => controller.abort();
  }, [key, path]);

  return { ...(result?.key === key ? result : undefined), loading: path !== null && result?.key !== key, retry: () => setAttempt((value) => value + 1) };
}

function EnergyMeasure({
  active, energy, query, data, loading, error, retry,
}: ReturnType<typeof useOverviewSample<CategoryOverview>> & { active: boolean; energy: Energy; query: string }) {
  const Icon = energy === 'electricity' ? Zap : Droplets;
  const label = energy === 'electricity' ? '用电量' : '用水量';
  return (
    <div className={cn('min-w-0 px-4 py-4 sm:px-5', active && 'bg-primary-soft/35')}>
      <div className="flex flex-wrap items-center justify-between gap-x-2">
        <h3 className="flex min-w-0 items-center gap-2 text-sm font-medium text-muted">
          <Icon className={cn('size-4 shrink-0', active && 'text-primary')} aria-hidden />{label}
        </h3>
        {data ? (
          <Link className={inlineAction} to={{ pathname: ENERGY_STATISTICS_PATH, search: query }}
            aria-label={`查看${label}统计`}>
            统计 <ArrowUpRight className="size-4" aria-hidden />
          </Link>
        ) : null}
      </div>
      {loading ? <div role="status" aria-label={`正在加载${label}`} className="mt-2 max-w-36"><Skeleton className="h-9" /></div> : null}
      {error ? (
        <div role="alert" className="mt-2 flex flex-wrap items-center gap-x-2 text-sm text-muted">
          <span>样本加载失败</span><button type="button" onClick={retry} className={inlineAction}>重试{label}</button>
        </div>
      ) : null}
      {data ? (
        <>
          <p className="mt-1 flex flex-wrap items-baseline gap-x-2 tabular">
            <span className="text-3xl font-semibold tracking-tight">{present(data.total.usage)}</span>
            <span className="text-sm text-muted">{data.unit}</span>
          </p>
          <p className="mt-2 text-xs leading-5 text-muted">
            {quality(data.total)} · {data.total.coverage.rate === null ? '覆盖率未知' : `覆盖率 ${percentage.format(data.total.coverage.rate)}`}
          </p>
        </>
      ) : null}
    </div>
  );
}

function UsageHistory({ data, rows, comparison, detailsLink, loading, error, retry }: {
  data: CategoryOverview; rows: DailyMeasure[]; comparison?: DailyOverview;
  detailsLink: { pathname: string; search: string }; loading: boolean; error?: string; retry: () => void;
}) {
  const latestComplete = [...rows].reverse().find((day) => day.usage !== null && day.coverage.expected > 0 && day.coverage.valid === day.coverage.expected);
  const tick = new Intl.DateTimeFormat('en-US', { timeZone: data.timezone, month: '2-digit', day: '2-digit' });
  const tickIndexes = new Set([0, Math.floor((rows.length - 1) / 3), Math.floor((rows.length - 1) * 2 / 3), rows.length - 1]);
  const top = Math.max(0, ...rows.map((row) => row.usage ?? 0));
  const chartMax = top === 0 ? 1 : top;
  return (
    <section className={section} aria-labelledby="overview-trend">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="overview-trend" className="text-base font-semibold">日用量历史 <span className="ml-1 text-sm font-normal text-muted">({data.unit})</span></h2>
        <Link className={inlineAction} to={detailsLink} aria-label="查看趋势明细">
          明细 <ArrowUpRight className="size-4" aria-hidden />
        </Link>
      </div>
      <p className="mt-1 break-words text-xs leading-5 text-muted">
        {latestComplete ? `最近完整区间截至 ${localTimestamp(latestComplete.to, data.timezone)}` : '暂无完整日用量区间'} · 项目当地时间
      </p>
      {loading ? <div role="status" aria-label="补充自然日历史样本" className="mt-2 text-xs text-muted">正在加载历史对比…</div> : null}
      {error ? <p role="alert" className="mt-1 flex flex-wrap items-center gap-x-2 text-xs text-muted">{error}<button type="button" className={inlineAction} onClick={retry}>重试历史对比</button></p> : null}
      {comparison ? (
        <dl className="mt-3 flex flex-wrap gap-x-6 gap-y-2 text-sm">
          {([{ label: '环比', value: comparison.previous }, { label: '同比', value: comparison.priorYear }]).map(({ label, value }) => (
            <div key={label} className="flex items-baseline gap-2">
              <dt className="text-xs text-muted">{label}</dt>
              <dd className="tabular">{value.changeRate === null ? comparisonStatus[value.status] : signedPercentage.format(value.changeRate)}</dd>
            </div>
          ))}
        </dl>
      ) : null}
      {rows.some((row) => row.usage !== null) ? (
        <>
          <div aria-hidden className="mt-7 grid min-w-0 grid-cols-[auto_1fr] gap-x-2">
            <div className="flex h-40 flex-col justify-between text-right text-[10px] text-muted tabular">
              <span className="-translate-y-1/2">{top === 0 ? '' : present(chartMax)}</span>
              <span>{top === 0 ? '' : present(chartMax / 2)}</span>
              <span className="translate-y-1/2">0</span>
            </div>
            <div className="relative grid min-w-0 auto-cols-fr grid-flow-col gap-[clamp(2px,0.5vw,8px)]">
              <div className="pointer-events-none absolute inset-x-0 top-0 h-40 border-y border-line"><div className="absolute inset-x-0 top-1/2 border-t border-dashed border-line" /></div>
              {rows.map((day, i) => {
                const label = tick.format(new Date(day.from));
                const current = sameInstant(day.from, data.from) && sameInstant(day.to, data.to);
                const unavailable = day.usage === null;
                const suspect = day.coverage.suspect > 0;
                return (
                  <div className="relative flex min-w-0 flex-col items-center" key={day.from}>
                    <div title={`${label} · ${present(day.usage)} ${data.unit} · ${quality(day)}`} className="flex h-40 w-full items-end justify-center">
                      <div className={cn('w-full max-w-16 rounded-t-sm',
                        unavailable ? cn('border border-dashed', suspect ? 'border-warning' : 'border-muted') :
                          day.coverage.valid < day.coverage.expected ? 'bg-warning' : current ? 'bg-primary' : 'bg-primary/55')}
                        style={{ height: day.usage === null ? 6 : day.usage === 0 ? 2 : `${day.usage / chartMax * 100}%` }} />
                    </div>
                    <div className="relative mt-2 h-5 w-full whitespace-nowrap text-[10px] text-muted tabular">
                      {tickIndexes.has(i) ? (
                        <span className={cn('absolute', i === 0 ? 'left-0' : i === rows.length - 1 ? 'right-0' : 'left-1/2 -translate-x-1/2')}>{label}</span>
                      ) : null}
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
          {rows.some((row) => row.usage === null || row.coverage.valid < row.coverage.expected) ? (
            <p className="mt-2 text-xs text-muted">虚线短标为缺测或可疑值，橙色实柱为部分覆盖。各日质量见明细。</p>
          ) : null}
        </>
      ) : <EmptyState title="该窗口没有可用的日用量" hint="统计明细保留缺测和可疑值状态。" />}
      {rows.length > 0 ? (
        <details className="mt-3 border-t border-line">
          <summary className="min-h-10 cursor-pointer py-2.5 text-sm font-medium text-primary">展开日用量明细</summary>
          <table className="w-full text-left text-sm">
            <caption className="sr-only">各日用量及数据质量</caption>
            <thead className="text-xs text-muted">
              <tr><th scope="col" className="py-2">日期</th><th scope="col" className="py-2 text-right">用量 ({data.unit})</th><th scope="col" className="py-2 text-right">数据质量</th></tr>
            </thead>
            <tbody className="divide-y divide-line">
              {rows.map((day) => (
                <tr key={day.from}>
                  <th scope="row" className="py-2 text-left font-normal tabular">{tick.format(new Date(day.from))}</th>
                  <td className="py-2 text-right tabular">{present(day.usage)}</td>
                  <td className="py-2 text-right text-xs text-muted">{quality(day)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </details>
      ) : null}
    </section>
  );
}

function UsageComposition({ data, query }: { data: CategoryOverview; query: string }) {
  return (
    <section className={section} aria-labelledby="overview-composition">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="overview-composition" className="text-base font-semibold">分类用量构成</h2>
        <Link className={inlineAction} to={{ pathname: ENERGY_STATISTICS_PATH, search: query }}>分类分项 <ArrowUpRight className="size-4" aria-hidden /></Link>
      </div>
      {data.composition.some((row) => row.usage !== null) ? (
        <dl className="mt-3 space-y-4">
          {data.composition.map((row) => (
            <div key={row.id}>
              <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1 text-sm">
                <dt>{row.name}</dt>
                <dd className="text-muted tabular">
                  <span className="font-medium text-fg">{present(row.usage)}</span> {data.unit}
                  {row.share !== null && <span className="ml-2 text-xs">{percentage.format(row.share)}</span>}
                </dd>
              </div>
              {row.share !== null ? (
                <div aria-hidden className="mt-2 h-1.5 overflow-hidden rounded-full bg-neutral-soft">
                  <div className={cn('h-full rounded-full', row.id === 'unclassified' ? 'bg-muted' : 'bg-primary')} style={{ width: `${row.share * 100}%` }} />
                </div>
              ) : null}
            </div>
          ))}
        </dl>
      ) : <EmptyState title="该窗口没有可用的分类用量" />}
    </section>
  );
}

function ObjectUsage({ unit, view, setView, link, result }: {
  unit: string; view: Dimension; setView: (view: Dimension) => void; link: { pathname: string; search: string };
  result: ReturnType<typeof useOverviewSample<DimensionOverview>>;
}) {
  return (
    <section className={section} aria-labelledby="overview-objects">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="overview-objects" className="text-base font-semibold">对象用量</h2>
        <Link className={inlineAction} to={link}>多维统计 <ArrowUpRight className="size-4" aria-hidden /></Link>
      </div>
      <div className="mt-1 flex flex-wrap items-center gap-x-4 gap-y-2">
        <div role="group" aria-label="对象视角" className="inline-flex flex-wrap gap-1 rounded-md bg-surface-2 p-1">
          {dimensionViews.map((option) => (
            <button key={option.value} type="button" aria-pressed={view === option.value} onClick={() => setView(option.value)}
              className={cn('min-h-10 rounded-md px-3 text-sm focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary',
                view === option.value ? 'bg-surface font-semibold text-primary shadow-sm' : 'text-muted hover:text-fg')}>
              {option.label}
            </button>
          ))}
        </div>
        <p className="text-xs text-muted">建筑、区域与组织分别汇总，同一笔用量可能出现在多个视角。</p>
      </div>
      {result.loading ? <div role="status" aria-label="加载对象用量" className="mt-4"><Skeleton className="h-20" /></div> : null}
      {result.error ? <div role="alert"><ErrorState message={result.error} onRetry={result.retry} /></div> : null}
      {result.data && result.data.rows.length === 0 ? <EmptyState title="该建筑没有本视角的统计对象" /> : null}
      {result.data && result.data.rows.length > 0 ? (
        <table className="mt-3 w-full text-left text-sm">
          <caption className="sr-only">当前视角的对象用量与覆盖情况</caption>
          <thead className="border-b border-line text-xs text-muted">
            <tr><th scope="col" className="py-2.5">对象</th><th scope="col" className="py-2.5 text-right">用量 ({unit})</th><th scope="col" className="py-2.5 text-right">数据质量</th></tr>
          </thead>
          <tbody className="divide-y divide-line">
            {result.data.rows.map((row) => (
              <tr key={row.id}>
                <th scope="row" className="break-words py-3 pr-3 font-medium">{row.name}</th>
                <td className="py-3 text-right tabular">{present(row.usage)}</td>
                <td className="py-3 pl-2 text-right text-xs text-muted tabular">
                  {quality(row)}{row.coverage.rate !== null && ` · ${percentage.format(row.coverage.rate)}`}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : null}
    </section>
  );
}

function OverviewSources({ data, query }: { data: CategoryOverview; query: string }) {
  const [open, setOpen] = useState(false);
  return (
    <Drawer open={open} onOpenChange={setOpen} title="计量来源" description="当前建筑、能源及时间范围内的合成计量点。"
      trigger={<button className={inlineAction} type="button" aria-label="查看计量来源"><FileSearch className="size-4" aria-hidden />计量来源</button>}>
      <p className="text-sm text-muted">{localTimestamp(data.from, data.timezone)} 至 {localTimestamp(data.to, data.timezone)}</p>
      <dl className="mt-3 grid gap-2 text-sm">
        <div className="flex flex-wrap gap-x-2"><dt className="text-muted">数据截止</dt><dd>{localTimestamp(data.asOf, data.timezone)}</dd></div>
        <div className="flex flex-wrap gap-x-2"><dt className="text-muted">计量口径</dt><dd>{data.ruleVersion}</dd></div>
        <div className="flex flex-wrap gap-x-2"><dt className="text-muted">分类口径</dt><dd>{data.classificationRuleVersion}</dd></div>
      </dl>
      {data.sources.length > 0 ? (
        <ul className="mt-4 divide-y divide-line border-y border-line">
          {data.sources.map((source) => (
            <li key={source.source} className="py-3">
              <div className="flex flex-wrap justify-between gap-2 text-sm">
                <p className="font-medium">{source.name}</p>
                <p className="tabular">{present(source.usage)} {data.unit}</p>
              </div>
              <p className="mt-1 text-xs text-muted tabular">有效 {source.valid} · 缺测 {source.missing} · 可疑 {source.suspect}</p>
              <p className="mt-1 break-all text-xs text-muted">{source.source} · {source.profileVersion}</p>
            </li>
          ))}
        </ul>
      ) : <EmptyState title="该窗口没有可显示的计量来源" />}
      <Link className={cn(inlineAction, 'mt-4')} to={{ pathname: ENERGY_STATISTICS_PATH, search: query }} onClick={() => setOpen(false)}>
        查看分类分项依据 <ArrowUpRight className="size-4" aria-hidden />
      </Link>
    </Drawer>
  );
}

type OverviewSection = 'summary' | 'composition' | 'objects' | 'trend';

function BuildingOverview({ section: currentSection }: { section: OverviewSection }) {
  const { scope, query } = useEnergyScope();
  const [view, setView] = useState<Dimension>('space');
  const summary = currentSection === 'summary';
  const hasTrend = summary || currentSection === 'trend';
  const hasObjects = summary || currentSection === 'objects';
  const hasComposition = summary || currentSection === 'composition';
  const electricQuery = new URLSearchParams({ ...scope, energy: 'electricity' }).toString();
  const waterQuery = new URLSearchParams({ ...scope, energy: 'water' }).toString();
  const dimensionQuery = new URLSearchParams({ ...scope, view, mode: 'complete', scenario: 'standard' }).toString();
  const trendQuery = new URLSearchParams({ building: scope.building, energy: scope.energy, granularity: 'day', mode: 'complete', scenario: 'standard' }).toString();

  const electricity = useOverviewSample<CategoryOverview>(summary || scope.energy === 'electricity' ? `classification?${electricQuery}` : null);
  const water = useOverviewSample<CategoryOverview>(summary || scope.energy === 'water' ? `classification?${waterQuery}` : null);
  const dimensions = useOverviewSample<DimensionOverview>(hasObjects ? `dimensions?${dimensionQuery}` : null);
  const trend = useOverviewSample<DailyOverview>(hasTrend && scope.period === 'day' ? `trend?${trendQuery}` : null);

  const selectedResult = scope.energy === 'electricity' ? electricity : water;
  const selected = selectedResult.data;
  const dimensionsMatch = selected && dimensions.data &&
    sameWindow(selected, dimensions.data) && sameInstant(selected.asOf, dimensions.data.asOf) && selected.unit === dimensions.data.unit &&
    selected.timezone === dimensions.data.timezone && dimensions.data.rows.every((row) =>
      row.sources.every((source) => source.meterRuleVersion === selected.ruleVersion));
  const trendMatch = selected && trend.data &&
    sameWindow(selected, trend.data.current) && sameInstant(selected.asOf, trend.data.asOf) && selected.unit === trend.data.unit &&
    selected.timezone === trend.data.timezone && selected.ruleVersion === trend.data.current.meterRuleVersion &&
    sameMeasure(selected.total, trend.data.current);
  const trendRows = trendMatch ? trend.data?.trend : selected?.trend.map((day, i, all) => ({
    ...day.total, from: day.from, to: all[i + 1]?.from ?? selected.to,
  }));
  const trendLink = scope.period === 'day'
    ? { pathname: `${ENERGY_STATISTICS_PATH}/trend`, search: trendQuery }
    : { pathname: ENERGY_STATISTICS_PATH, search: query };
  const objectLink = { pathname: `${ENERGY_STATISTICS_PATH}/dimensions`, search: dimensionQuery };
  return (
    <div className="space-y-4">
      <EnergyScopeBar action={selected ? <OverviewSources key={query} data={selected} query={query} /> : undefined} />
      {summary && <section aria-labelledby="overview-consumption" className="overflow-hidden rounded-xl border border-line bg-surface">
        <h2 id="overview-consumption" className="sr-only">能源消费</h2>
        <div className="grid grid-cols-2 divide-x divide-line">
          <EnergyMeasure {...electricity} energy="electricity" active={scope.energy === 'electricity'} query={electricQuery} />
          <EnergyMeasure {...water} energy="water" active={scope.energy === 'water'} query={waterQuery} />
        </div>
        <p className="border-t border-line px-4 py-2 text-xs leading-5 text-muted sm:px-5">
          综合能耗待配置有效折标系数与统计边界。用水单列。
        </p>
      </section>}
      {currentSection === 'trend' && <p className="text-sm text-muted">当前可查看历史样本。实时计量待接入。</p>}

      {selected ? (
        <>
          <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted tabular">
            <span>统计区间 {localTimestamp(selected.from, selected.timezone)} 至 {localTimestamp(selected.to, selected.timezone)}</span>
            <span>数据截止 {localTimestamp(selected.asOf, selected.timezone)}（{selected.timezone}）</span>
            {selected.total.coverage.valid < selected.total.coverage.expected && (
              <span className="text-warning">有效 {present(selected.total.coverage.valid)} / {present(selected.total.coverage.expected)} 点位小时 · 缺测 {present(selected.total.coverage.missing)} · 可疑 {present(selected.total.coverage.suspect)}</span>
            )}
          </div>
          {(hasTrend || hasComposition) && <div className={cn('grid min-w-0 gap-4', summary && 'xl:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]')}>
            {hasTrend && <UsageHistory data={selected} rows={trendRows ?? []} comparison={trendMatch ? trend.data : undefined}
              detailsLink={trendLink} loading={trend.loading} error={trend.data && !trendMatch ? '历史对比口径与本次统计不一致' : trend.error}
              retry={trend.retry} />}
            {hasComposition && <UsageComposition data={selected} query={query} />}
          </div>}
          {hasObjects && <ObjectUsage unit={selected.unit} view={view} setView={setView} link={objectLink}
            result={{ ...dimensions, data: dimensionsMatch ? dimensions.data : undefined, error: dimensions.data && !dimensionsMatch ? '对象统计口径与本次总览不一致' : dimensions.error }} />}
        </>
      ) : !summary && (selectedResult.error ? (
        <div role="alert"><ErrorState message={selectedResult.error} onRetry={selectedResult.retry} /></div>
      ) : <div role="status" aria-label="加载统计结果"><Skeleton className="h-48" /></div>)}

      {summary && <section className="border-t border-line pt-3" aria-labelledby="overview-target">
        <h2 id="overview-target" className="inline text-sm font-medium">年度目标</h2>
        <Link to={{ pathname: ENERGY_TARGET_SUMMARY_PATH, search: new URLSearchParams({ building: scope.building, energy: scope.energy }).toString() }}
          className={cn(inlineAction, 'ml-2')}>查看年度目标摘要 <ArrowUpRight className="size-4" aria-hidden /></Link>
      </section>}
    </div>
  );
}

export default function BuildingEnergyPreviewPage({ section = 'summary' }: { section?: OverviewSection }) {
  const [params] = useSearchParams();
  return section === 'summary' && periods.some(({ value }) => value === params.get('granularity'))
    ? <LegacyMeterOverview />
    : <BuildingOverview key={section} section={section} />;
}
