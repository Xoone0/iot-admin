import { useState } from 'react';
import { ArrowUpRight, FileSearch } from 'lucide-react';
import { Link, useSearchParams } from 'react-router-dom';
import { ENERGY_TARGET_PERIODS_PATH } from '../components/BuildingEnergyNavigation';
import { Drawer } from '../components/Drawer';
import { StatusBadge } from '../components/FieldValue';
import { EmptyState, ErrorState, Skeleton } from '../components/States';
import { EnergyInterval, energyPercent, energyTime, useEnergyPreview } from './BuildingEnergyResults';
import type { EnergyCoverage, EnergyWindow } from './BuildingEnergyResults';
import { EnergyFilterDrawer, EnergySectionHeading, energyFilterValues, energyNumber } from './BuildingEnergyWorkspace';

const filters = [
  { key: 'building', label: '建筑', options: [['office-a', '办公楼 A'], ['office-b', '办公楼 B']] },
  { key: 'energy', label: '能源', options: [['electricity', '用电'], ['water', '用水']] },
  { key: 'year', label: '自然年度', options: [['2026', '2026 年'], ['2025', '2025 年']] },
] as const;

const statusText = {
  comparable: '可比',
  unconfigured: '未配置有效目标',
  outside_effective_period: '目标有效期未覆盖',
  incomplete_period: '统计期未闭合',
  missing_usage: '缺少实测数据',
  partial_coverage: '实测覆盖不足',
  incompatible_usage: '实测口径不一致',
  zero_target: '零目标',
} as const;

const explanations: Record<keyof typeof statusText, string> = {
  comparable: '用量占目标比例以同一自然周期的实测计算，仅供样本比较。',
  unconfigured: '当前建筑、能源和年度暂无有效目标。',
  outside_effective_period: '目标版本的生效区间未覆盖完整统计期。',
  incomplete_period: '统计期尚未闭合或观察窗口未覆盖完整周期。',
  missing_usage: '该统计期暂无可用实测数据。',
  partial_coverage: '实测含缺测或可疑记录，覆盖不足。',
  incompatible_usage: '实测的范围、单位或时间与目标口径不一致。',
  zero_target: '目标为零，可查看差额，比例暂不可计算。',
};

const methodLabel = { linear: '按月均分', seasonal: '季节权重' } as const;

interface TargetPlan {
  version: string;
  method: keyof typeof methodLabel;
  from: string;
  to: string;
  source: string;
  weightsPPM?: number[] | null;
}

interface TargetPeriod {
  index: number;
  from: string;
  to: string;
  target: number | null;
  usage: number | null;
  coverage: EnergyCoverage | null;
  status: keyof typeof statusText;
  ratio: number | null;
  delta: number | null;
}

interface TargetMeasurement {
  meterRuleVersion: string;
  classificationRuleVersion: string;
  catalogVersion: string;
  topologyVersion: string;
  sources: { name: string; source: string; profileVersion: string; bindingVersion: string; mappingGeneration: number }[] | null;
}

interface TargetReport extends Pick<EnergyWindow, 'timezone' | 'asOf'> {
  sample: true;
  project: string;
  year: number;
  unit: string;
  ruleVersion: string;
  targetSelectedAt: string;
  target: TargetPlan | null;
  annual: TargetPeriod;
  months: TargetPeriod[];
  measurement: TargetMeasurement | null;
}

const panel = 'min-w-0 rounded-xl border border-line bg-surface';

function Quantity({ value, unit, signed = false }: { value: number | null; unit: string; signed?: boolean }) {
  return (
    <span className="tabular">
      {signed && value !== null && value > 0 ? '+' : ''}{energyNumber(value)}
      {value !== null && <span className="ml-1 whitespace-nowrap text-sm font-normal text-muted">{unit}</span>}
    </span>
  );
}

function PlanEvidence({ data }: { data: TargetReport }) {
  const [open, setOpen] = useState(false);
  const plan = data.target;
  const measurement = data.measurement;
  return (
    <Drawer open={open} onOpenChange={setOpen} title="目标版本与计量来源"
      description="当前筛选范围内的合成目标及实测口径。"
      trigger={(
        <button type="button" className="inline-flex min-h-10 items-center gap-1.5 rounded-md px-2 text-sm font-medium text-primary hover:bg-primary-soft">
          <FileSearch className="size-4" aria-hidden /> 查看来源
        </button>
      )}>
      <div className="space-y-5 text-sm">
        <section aria-label="统计窗口及截止时间">
          <h3 className="font-semibold">统计范围</h3>
          <p className="mt-1.5 leading-6 text-muted"><EnergyInterval window={{ ...data.annual, timezone: data.timezone, asOf: data.asOf }} /></p>
          <p className="leading-6 text-muted">实测数据截至 {energyTime(data.asOf, data.timezone)} · {data.timezone}</p>
        </section>
        <section aria-label="目标配置版本" className="border-t border-line pt-3">
          <h3 className="font-semibold">目标版本</h3>
          {plan ? (
            <dl className="mt-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-muted">
              <dt>版本</dt><dd className="break-all">{plan.version}</dd>
              <dt>来源</dt><dd className="break-words">{plan.source}</dd>
              <dt>分期方式</dt><dd>{methodLabel[plan.method]}</dd>
              <dt>生效区间</dt><dd>{energyTime(plan.from, data.timezone)} 至 {energyTime(plan.to, data.timezone)}（不含结束）</dd>
              <dt>版本查询时点</dt><dd>{energyTime(data.targetSelectedAt, data.timezone)}</dd>
            </dl>
          ) : <p className="mt-1.5 text-muted">当前范围暂无有效目标。</p>}
        </section>
        <section aria-label="计量点" className="border-t border-line pt-3">
          <h3 className="font-semibold">计量点</h3>
          {measurement?.sources?.length ? (
            <ul className="mt-1 divide-y divide-line">
              {measurement.sources.map((source) => (
                <li key={source.source} className="py-2.5">
                  <p className="font-medium">{source.name}</p>
                  <details className="mt-1 text-xs text-muted">
                    <summary className="w-fit cursor-pointer rounded py-1 text-primary">通道及归属版本</summary>
                    <dl className="mt-1 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1.5">
                      <dt>计量通道</dt><dd className="break-all">{source.source}</dd>
                      <dt>计量点版本</dt><dd className="break-all">{source.profileVersion}</dd>
                      <dt>归属版本</dt><dd className="break-all">{source.bindingVersion}</dd>
                      <dt>映射代次</dt><dd>{source.mappingGeneration}</dd>
                    </dl>
                  </details>
                </li>
              ))}
            </ul>
          ) : <p className="mt-1.5 text-muted">当前范围暂无可展示的计量点。</p>}
        </section>
        <details className="border-t border-line pt-3 text-xs text-muted">
          <summary className="w-fit cursor-pointer rounded py-1 text-primary">计算与目录版本</summary>
          <dl className="mt-1 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1.5 break-all">
            <dt>项目</dt><dd>{data.project}</dd>
            <dt>目标计算</dt><dd>{data.ruleVersion}</dd>
            {measurement && <>
              <dt>计量规则</dt><dd>{measurement.meterRuleVersion}</dd>
              <dt>分类规则</dt><dd>{measurement.classificationRuleVersion}</dd>
              <dt>分类目录</dt><dd>{measurement.catalogVersion}</dd>
              <dt>计量拓扑</dt><dd>{measurement.topologyVersion}</dd>
            </>}
          </dl>
        </details>
      </div>
    </Drawer>
  );
}

function PeriodStatus({ period }: { period: TargetPeriod }) {
  return <StatusBadge label={statusText[period.status]} tone={
    period.status === 'partial_coverage' || period.status === 'incompatible_usage' ? 'warning' : 'neutral'
  } />;
}

function ComparisonValues({ period, unit, annual = false }: { period: TargetPeriod; unit: string; annual?: boolean }) {
  return (
    <dl className="grid grid-cols-2 gap-x-6 gap-y-4 px-4 py-4 sm:grid-cols-4 sm:px-5">
      <div>
        <dt className="text-sm text-muted">{annual ? '年度目标' : '月度目标'}</dt>
        <dd className="mt-1 text-2xl font-semibold tracking-tight break-words sm:text-[28px]">
          <Quantity value={period.target} unit={unit} />
        </dd>
      </div>
      <div>
        <dt className="text-sm text-muted">{period.status === 'comparable' ? '期内实测' : '已记录用量'}</dt>
        <dd className="mt-1 text-2xl font-semibold tracking-tight break-words sm:text-[28px]">
          <Quantity value={period.usage} unit={unit} />
        </dd>
        {period.coverage?.rate != null && <dd className="mt-1 text-xs text-muted">覆盖率 {energyPercent(period.coverage.rate)}</dd>}
      </div>
      {period.delta !== null && (
        <div>
          <dt className="text-sm text-muted">实测与目标差额</dt>
          <dd className="mt-1 text-2xl font-semibold tracking-tight break-words sm:text-[28px]">
            <Quantity value={period.delta} unit={unit} signed />
          </dd>
        </div>
      )}
      {period.ratio !== null && (
        <div>
          <dt className="text-sm text-muted">用量占目标比例</dt>
          <dd className="mt-1 text-2xl font-semibold tracking-tight tabular sm:text-[28px]">{energyPercent(period.ratio)}</dd>
        </div>
      )}
    </dl>
  );
}

function AnnualSummary({ data, query, linkToPeriods }: { data: TargetReport; query: string; linkToPeriods: boolean }) {
  const hasAnnualData = data.target !== null || data.annual.usage !== null;
  return (
    <section className={panel} aria-label="年度目标及实测">
      <div className="border-b border-line px-4 py-4 sm:px-5">
        <EnergySectionHeading title={`${data.year} 年目标`} action={<PlanEvidence key={query} data={data} />}
          description={data.target ? `${methodLabel[data.target.method]} · 版本 ${data.target.version}` : undefined} />
      </div>
      {hasAnnualData ? (
        <>
          <ComparisonValues period={data.annual} unit={data.unit} annual />
          <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-2 border-t border-line px-4 py-3 sm:px-5">
            <div className="flex flex-wrap items-baseline gap-2 text-sm">
              <PeriodStatus period={data.annual} />
              <p className="text-muted">{explanations[data.annual.status]}</p>
            </div>
            {linkToPeriods && data.target && (
              <Link to={{ pathname: ENERGY_TARGET_PERIODS_PATH, search: query }}
                className="inline-flex min-h-9 items-center gap-1.5 rounded-md px-2 text-sm font-medium text-primary hover:bg-primary-soft">
                查看月度分解 <ArrowUpRight className="size-4" aria-hidden />
              </Link>
            )}
          </div>
        </>
      ) : (
        <EmptyState title="未配置有效目标" hint="可切换建筑、能源或年度查看其他样本。正式目标须配置并生效后才参与比较。" />
      )}
    </section>
  );
}

function LatestMeasurement({ data }: { data: TargetReport }) {
  const latest = data.months.filter((period) => period.usage !== null).at(-1);
  if (!latest) return null;
  return (
    <section className={panel} aria-label="最近有实测月份">
      <div className="border-b border-line px-4 py-4 sm:px-5">
        <EnergySectionHeading title={`最近有实测月份 · ${latest.index} 月`}
          description="此处按该自然月的目标比较。" action={<PeriodStatus period={latest} />} />
      </div>
      <ComparisonValues period={latest} unit={data.unit} />
      {latest.status !== 'comparable' && <p className="border-t border-line px-4 py-3 text-sm text-muted sm:px-5">{explanations[latest.status]}</p>}
    </section>
  );
}

function MonthlyChart({ months, unit }: { months: TargetPeriod[]; unit: string }) {
  const max = Math.max(0, ...months.flatMap((period) => [period.target ?? 0, period.status === 'comparable' ? period.usage ?? 0 : 0]));
  function barHeight(value: number) {
    return max ? `${value / max * 100}%` : undefined;
  }
  return (
    <figure className="space-y-3 px-4 py-4 sm:px-5" aria-label={`12 个月的目标及可比实测（${unit}），详见下表。`}>
      <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted">
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
          <span className="flex items-center gap-1.5"><span className="size-2.5 rounded-sm border border-primary bg-primary-soft" aria-hidden />月度目标</span>
          <span className="flex items-center gap-1.5"><span className="size-2.5 rounded-sm bg-primary" aria-hidden />同窗可比实测</span>
        </div>
        <span>单位 {unit}</span>
      </div>
      <div className="flex gap-2 sm:gap-3" aria-hidden>
        <div className="flex h-32 flex-col justify-between text-right text-[11px] text-muted tabular sm:h-44">
          {max > 0 ? <><span>{energyNumber(max)}</span><span>{energyNumber(max / 2)}</span><span>0</span></>
            : <span className="mt-auto">0</span>}
        </div>
        <div className="min-w-0 flex-1">
          <div className="relative grid h-32 grid-cols-12 gap-1 border-y border-line before:absolute before:inset-x-0 before:top-1/2 before:border-t before:border-line sm:h-44 sm:gap-2">
            {months.map((period) => (
              <div key={period.index} className="relative flex min-w-0 items-end justify-center gap-px"
                title={`${period.index} 月 · 目标 ${energyNumber(period.target)} ${unit} · 实测 ${energyNumber(period.usage)} ${unit} · ${statusText[period.status]}`}>
                {period.target !== null && <span className="min-h-px w-1/2 max-w-8 rounded-t-sm border border-primary bg-primary-soft" style={{ height: barHeight(period.target) }} />}
                {period.status === 'comparable' && period.usage !== null && (
                  <span className="min-h-px w-1/2 max-w-8 rounded-t-sm bg-primary" style={{ height: barHeight(period.usage) }} />
                )}
              </div>
            ))}
          </div>
          <div className="mt-1 grid grid-cols-12 text-center text-[11px] text-muted tabular">
            {months.map((period) => <span key={period.index}>{period.index}</span>)}
          </div>
        </div>
      </div>
      <p className="text-right text-xs text-muted">月份</p>
    </figure>
  );
}

function MonthlyPeriods({ data }: { data: TargetReport }) {
  return (
    <section className={`${panel} overflow-hidden`} aria-label="月度目标分解">
      <div className="border-b border-line px-4 py-4 sm:px-5">
        <EnergySectionHeading title="月度目标分解" description="图中实测柱仅展示同窗可比月份。其余月份的用量和覆盖状态可在表中查看。" />
      </div>
      <MonthlyChart months={data.months} unit={data.unit} />
      <table className="w-full table-fixed border-t border-line text-sm">
        <caption className="sr-only">月度目标、实测与覆盖状态（{data.unit}）</caption>
        <thead className="border-b border-line bg-surface-2 text-xs text-muted">
          <tr>
            <th scope="col" className="w-[18%] py-2.5 pl-4 text-left font-medium sm:pl-5">月份</th>
            <th scope="col" className="w-[23%] px-1 py-2.5 text-right font-medium sm:w-1/4">目标（{data.unit}）</th>
            <th scope="col" className="w-[23%] px-1 py-2.5 text-right font-medium sm:w-1/4">实测（{data.unit}）</th>
            <th scope="col" className="py-2.5 pl-1 pr-4 text-right font-medium sm:pr-5">比较及覆盖</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-line">
          {data.months.map((period) => {
            const weight = data.target?.weightsPPM?.[period.index - 1];
            return (
              <tr key={period.index} className="align-top">
                <th scope="row" className="py-2.5 pl-4 text-left font-medium sm:pl-5">
                  <time dateTime={`${data.year}-${String(period.index).padStart(2, '0')}`}>{period.index} 月</time>
                  {weight != null && <span className="block text-xs font-normal text-muted">权重 {energyPercent(weight / 1_000_000)}</span>}
                </th>
                <td className="break-words px-1 py-2.5 text-right tabular">{energyNumber(period.target)}</td>
                <td className="break-words px-1 py-2.5 text-right tabular">{energyNumber(period.usage)}</td>
                <td className="py-2.5 pl-1 pr-4 text-right text-xs leading-5 sm:pr-5">
                  {period.ratio === null
                    ? <span className={period.status === 'partial_coverage' ? 'text-warning' : 'text-muted'}>{statusText[period.status]}</span>
                    : <span className="tabular">占目标 {energyPercent(period.ratio)}</span>}
                  {period.coverage?.rate != null && <span className="block text-muted tabular">覆盖率 {energyPercent(period.coverage.rate)}</span>}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </section>
  );
}

export default function BuildingEnergyTargetsPage({ view = 'summary' }: { view?: 'summary' | 'periods' }) {
  const [params, setParams] = useSearchParams();
  const applied = energyFilterValues(filters, params);
  const query = new URLSearchParams(applied).toString();
  const { data, error, retry } = useEnergyPreview<TargetReport>('targets', query);
  const label = (filter: typeof filters[number]) => filter.options.find(([value]) => value === applied[filter.key])?.[1];

  return (
    <div className="space-y-4 sm:space-y-6">
      <header className="flex min-w-0 items-center justify-between gap-3 border-b border-line pb-3">
        <div className="min-w-0">
          <p className="text-sm font-semibold">{label(filters[0])} · {label(filters[1])}</p>
          <p className="mt-0.5 text-xs leading-5 text-muted">
            {label(filters[2])}{data && ` · 数据截至 ${energyTime(data.asOf, data.timezone)}（项目时间）`}
          </p>
        </div>
        <EnergyFilterDrawer title="筛选目标范围" fields={filters} applied={applied}
          onApply={(next) => { if (new URLSearchParams(next).toString() !== params.toString()) setParams(next); }} />
      </header>
      {data ? (
        <>
          <AnnualSummary data={data} query={query} linkToPeriods={view === 'summary'} />
          {data.target && (view === 'summary' ? <LatestMeasurement data={data} /> : <MonthlyPeriods data={data} />)}
        </>
      ) : error ? (
        <div role="alert"><ErrorState message={error} onRetry={retry} /></div>
      ) : (
        <div role="status" aria-label="正在加载目标"><Skeleton className="h-52" /></div>
      )}
    </div>
  );
}
