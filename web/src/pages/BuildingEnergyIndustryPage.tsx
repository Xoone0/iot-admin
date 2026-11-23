import type { ReactNode } from 'react';
import { ArrowUpRight } from 'lucide-react';
import { Link, useLocation, useSearchParams } from 'react-router-dom';
import { ENERGY_TARGET_PERIODS_PATH, energyItemLocation } from '../components/BuildingEnergyNavigation';
import { StatusBadge } from '../components/FieldValue';
import { EmptyState, ErrorState, Skeleton } from '../components/States';
import { EnergyEvidence, EnergyInterval, energyPercent, energyQualityLabel, useEnergyPreview } from './BuildingEnergyResults';
import type { EnergyEvidenceData, EnergyQuality, EnergyValue } from './BuildingEnergyResults';
import { EnergyFilterDrawer, EnergySectionHeading, energyFilterValues, energyNumber } from './BuildingEnergyWorkspace';

const buildingLabel = {
  office: '办公建筑',
  hospital: '医院',
  school: '学校',
  hotel: '酒店',
} as const;

export const energyIndustryFilters = [
  { key: 'building', label: '建筑', options: [
    ['office-a', '办公楼 A'], ['office-b', '办公楼 B'], ['hospital-a', '合成医院 A'],
    ['school-a', '合成学校 A'], ['hotel-a', '合成酒店 A'],
  ] },
  { key: 'energy', label: '能源', options: [['electricity', '用电'], ['water', '用水']] },
  { key: 'scenario', label: '样本情景', options: [
    ['standard', '常规数据'], ['missing-denominator', '缺少分母'], ['invalid-denominator', '无效分母'],
    ['out-of-period', '分母跨期'], ['partial', '部分覆盖'], ['zero', '有效零'],
    ['no-standard', '无演示规则'], ['not-applicable', '规则不适用'], ['incomparable', '口径不可比'],
  ] },
] as const;

const filters = energyIndustryFilters;

const metricReasons = {
  missing_denominator: '缺少有效分母记录。',
  invalid_denominator: '分母数值、单位或记录来源无效。',
  denominator_out_of_period: '分母记录与当前统计期不匹配。',
  missing_usage: '统计期缺少有效用量。',
  insufficient_quality: '计量覆盖率或数据质量不足。',
  incompatible_usage: '计量结果与模板范围或单位不一致。',
  invalid_template: '模板定义或版本无效。',
  inapplicable_template: '模板不适用于当前建筑、能源或统计期。',
} as const;

const comparisonStatus = {
  no_standard: '无演示规则',
  inapplicable: '不适用',
  not_comparable: '不可比较',
  insufficient_data: '数据不足',
  within_threshold: '未超过演示阈值',
  exceeds_threshold: '超过演示阈值',
} as const;

const comparisonReasons: Record<keyof typeof comparisonStatus, string> = {
  no_standard: '该指标未配置演示规则。',
  inapplicable: '当前对象、地区、统计期或适用条件未满足规则要求。',
  not_comparable: '实测指标与演示规则的计量口径或单位不一致。',
  insufficient_data: '需取得可用的实测指标和完整计量后再比较。',
  within_threshold: '',
  exceeds_threshold: '',
};

type BuildingType = keyof typeof buildingLabel;
type View = 'templates' | 'benchmarks';
type EvidencePeriod = { from: string; to: string };

interface BenchmarkRule extends EvidencePeriod {
  id: string;
  name: string;
  version: string;
  source: string;
  publisher: string;
  kind: 'demo';
  region: string;
  buildingType: BuildingType;
  unit: string;
  condition: string;
}

interface IndustryMetric {
  id: string;
  name: string;
  unit: string;
  value: number | null;
  status: 'ready' | keyof typeof metricReasons;
  definition: string;
  template: EvidencePeriod & { version: string; source: string; denominatorKind: string; denominatorUnit: string };
  denominator: (EvidencePeriod & { value: number | null; unit: string; source: string; version: string; basis: string }) | null;
  benchmark: {
    status: keyof typeof comparisonStatus;
    limit: number | null;
    delta: number | null;
    standard: BenchmarkRule | null;
  };
}

interface IndustryReport extends EnergyEvidenceData {
  sample: true;
  project: string;
  building: string;
  buildingName: string;
  buildingType: BuildingType;
  region: string;
  energy: string;
  scenario: string;
  numerator: EnergyValue;
  quality: EnergyQuality;
  metrics: IndustryMetric[];
}

const panel = 'min-w-0 rounded-xl border border-line bg-surface';

function Quantity({ value, unit }: { value: number | null; unit: string }) {
  return (
    <span className="inline-flex min-w-0 flex-wrap items-baseline gap-x-1.5 tabular">
      <span className="break-all">{energyNumber(value)}</span>
      {value !== null && <span className="break-words text-xs font-normal text-muted sm:text-sm">{unit}</span>}
    </span>
  );
}

function SourceRows({ data, view }: { data: IndustryReport; view: View }) {
  return (
    <section className="space-y-3 border-y border-line py-4" aria-label="指标和规则来源">
      <h3 className="font-semibold">{view === 'templates' ? '模板与合成分母' : '指标口径与演示规则'}</h3>
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs leading-5 text-muted">
        <dt>项目</dt><dd className="break-words">{data.project}</dd>
        <dt>适用对象</dt><dd>{data.buildingName} · {buildingLabel[data.buildingType]}</dd>
        <dt>地区</dt><dd>{data.region}</dd>
      </dl>
      {data.metrics.map((metric) => {
        const { template, denominator, benchmark } = metric;
        const rule = benchmark.standard;
        return (
          <details key={metric.id} className="border-t border-line pt-3">
            <summary className="w-fit cursor-pointer rounded py-1 font-medium text-primary">{metric.name}</summary>
            <dl className="mt-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1.5 text-xs leading-5 text-muted">
              <dt>公式定义</dt><dd>{metric.definition}</dd>
              <dt>指标单位</dt><dd>{metric.unit}</dd>
              <dt>模板来源</dt><dd className="break-words">{template.source} · {template.version}</dd>
              <dt>模板有效期</dt><dd><EnergyInterval window={{ ...data, ...template }} /></dd>
              <dt>分母单位</dt><dd>{template.denominatorUnit}</dd>
              <dt>分母记录</dt>
              <dd className="break-words">{denominator ? `${denominator.source} · ${denominator.version}` : '暂无记录'}</dd>
              {denominator && (
                <>
                  <dt>分母有效期</dt><dd><EnergyInterval window={{ ...data, ...denominator }} /></dd>
                  <dt>业务口径</dt><dd>{denominator.basis}</dd>
                </>
              )}
              {view === 'benchmarks' && rule && (
                <>
                  <dt>演示规则</dt><dd className="break-words">{rule.name} · {rule.version}</dd>
                  <dt>规则编号</dt><dd className="break-words">{rule.id}</dd>
                  <dt>规则来源</dt><dd className="break-words">{rule.source}</dd>
                  <dt>样本编制方</dt><dd>{rule.publisher}</dd>
                  <dt>适用范围</dt><dd>{rule.region} · {buildingLabel[rule.buildingType]} · {rule.unit}</dd>
                  <dt>适用条件</dt><dd>{rule.condition}</dd>
                  <dt>规则有效期</dt><dd><EnergyInterval window={{ ...data, ...rule }} /></dd>
                </>
              )}
            </dl>
          </details>
        );
      })}
    </section>
  );
}

function ReportSummary({ data, view }: { data: IndustryReport; view: View }) {
  const coverage = data.numerator.coverage;
  return (
    <section className={`${panel} px-4 py-4 sm:px-5`} aria-label="统计用量与质量">
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-2">
        <h2 className="text-base font-semibold">统计用量与质量</h2>
        <EnergyEvidence data={data}><SourceRows data={data} view={view} /></EnergyEvidence>
      </div>
      <dl className="mt-2 flex flex-wrap items-baseline gap-x-8 gap-y-3 sm:gap-x-12">
        <div>
          <dt className="text-sm text-muted">统计用量（分子）</dt>
          <dd className="mt-0.5 text-2xl font-semibold tracking-tight"><Quantity value={data.numerator.usage} unit={data.unit} /></dd>
        </div>
        <div>
          <dt className="text-sm text-muted">计量覆盖率</dt>
          <dd className="mt-1 flex flex-wrap items-center gap-2">
            <span className="font-semibold tabular">{energyPercent(coverage.rate)}</span>
            <StatusBadge label={energyQualityLabel[data.quality]} tone={data.quality === 'valid' || data.quality === 'valid_zero' ? 'neutral' : 'warning'} />
          </dd>
        </div>
        <div>
          <dt className="text-sm text-muted">计量记录</dt>
          <dd className="mt-1 text-sm text-muted tabular">有效 {energyNumber(coverage.valid)} / {energyNumber(coverage.expected)} 表小时</dd>
        </div>
      </dl>
    </section>
  );
}

function MetricCard({ metric, children }: { metric: IndustryMetric; children: ReactNode }) {
  return (
    <article aria-label={metric.name} className={`${panel} space-y-3 px-4 py-4 sm:px-5`}>
      <h3 className="text-base font-semibold">{metric.name}</h3>
      {children}
    </article>
  );
}

function MetricTemplates({ data }: { data: IndustryReport }) {
  return (
    <section className="space-y-3" aria-label="模板指标">
      <EnergySectionHeading title="模板指标" />
      <div className="grid gap-4 md:grid-cols-2">
        {data.metrics.map((metric) => (
          <MetricCard key={metric.id} metric={metric}>
            {metric.status === 'ready' && metric.value !== null ? (
              <p className="text-3xl font-semibold tracking-tight"><Quantity value={metric.value} unit={metric.unit} /></p>
            ) : (
              <div>
                <p className="text-xl font-medium text-muted">不可计算</p>
                <p className="mt-1 text-sm text-muted">{metric.status === 'ready' ? '缺少可用的实测指标。' : metricReasons[metric.status]}</p>
              </div>
            )}
            <dl className="space-y-2 border-t border-line pt-3 text-sm leading-5">
              <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                <dt className="text-muted">业务分母</dt>
                <dd className="min-w-0 font-medium"><Quantity value={metric.denominator?.value ?? null} unit={metric.denominator?.unit ?? ''} /></dd>
              </div>
              <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                <dt className="text-muted">计算口径</dt>
                <dd className="min-w-0 break-words">{metric.definition}</dd>
              </div>
            </dl>
          </MetricCard>
        ))}
      </div>
    </section>
  );
}

function MetricBenchmarks({ data }: { data: IndustryReport }) {
  const canOpenTargets = data.building === 'office-a' || data.building === 'office-b';
  const { pathname, search } = useLocation();

  return (
    <section className="space-y-4" aria-label="演示定额比较">
      <EnergySectionHeading title="演示定额比较" description="演示阈值仅在指定地区、业态和统计期内参与样本比较。" />
      <div className="grid gap-4 md:grid-cols-2">
        {data.metrics.map((metric) => {
          const { benchmark } = metric;
          return (
            <MetricCard key={metric.id} metric={metric}>
              <dl className="grid grid-cols-2 gap-x-4 gap-y-3">
                <div className="min-w-0">
                  <dt className="text-sm text-muted">实测指标</dt>
                  <dd className="mt-1 text-2xl font-semibold tracking-tight"><Quantity value={metric.status === 'ready' ? metric.value : null} unit={metric.unit} /></dd>
                  {metric.status !== 'ready' && <dd className="mt-1 text-xs leading-5 text-muted">{metricReasons[metric.status]}</dd>}
                </div>
                <div className="min-w-0">
                  <dt className="text-sm text-muted">演示阈值</dt>
                  <dd className="mt-1 text-2xl font-semibold tracking-tight"><Quantity value={benchmark.limit} unit={benchmark.standard?.unit ?? ''} /></dd>
                </div>
                <div className="col-span-2 flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-line pt-3 text-sm">
                  <dt className="text-muted">演示比较</dt>
                  <dd><StatusBadge label={comparisonStatus[benchmark.status]} tone={benchmark.status === 'exceeds_threshold' || benchmark.status === 'insufficient_data' ? 'warning' : 'neutral'} /></dd>
                  {benchmark.delta !== null && (
                    <dd className="basis-full text-xs text-muted">实测与演示阈值差 <Quantity value={benchmark.delta} unit={metric.unit} /></dd>
                  )}
                  {comparisonReasons[benchmark.status] && (
                    <dd className="basis-full text-xs leading-5 text-muted">{comparisonReasons[benchmark.status]}</dd>
                  )}
                </div>
              </dl>
              {benchmark.standard && (
                <p className="break-words text-xs leading-5 text-muted">
                  {benchmark.standard.name} · {benchmark.standard.region} · {buildingLabel[benchmark.standard.buildingType]}
                </p>
              )}
            </MetricCard>
          );
        })}
      </div>
      <section className="grid gap-y-3 border-t border-line pt-4 sm:grid-cols-3 sm:gap-x-6" aria-label="目标及考核边界">
        <div>
          <h3 className="text-sm font-medium">内部目标</h3>
          <p className="mt-1 text-sm leading-5 text-muted">内部用量目标在分期目标页单独查看。</p>
          {canOpenTargets && (
            <Link {...energyItemLocation(ENERGY_TARGET_PERIODS_PATH, pathname, search)}
              className="inline-flex min-h-10 items-center gap-1.5 rounded-md text-sm font-medium text-primary hover:bg-primary-soft">
              查看内部目标 <ArrowUpRight className="size-4" aria-hidden />
            </Link>
          )}
        </div>
        <div>
          <h3 className="text-sm font-medium">外部标准</h3>
          <p className="mt-1 text-sm leading-5 text-muted">权威外部标准待接入，须先核定来源、适用范围和有效期。</p>
        </div>
        <div>
          <h3 className="text-sm font-medium">正式考核</h3>
          <p className="mt-1 text-sm leading-5 text-muted">正式考核须使用已签认标准和审核后的业务及计量数据。</p>
        </div>
      </section>
    </section>
  );
}

export default function BuildingEnergyIndustryPage({ view = 'templates' }: { view?: View }) {
  const [params, setParams] = useSearchParams();
  const scope = energyFilterValues(filters, params);
  const query = new URLSearchParams(scope).toString();
  const { data, error, retry } = useEnergyPreview<IndustryReport>('industry', query);
  const building = filters[0].options.find(([value]) => value === scope.building)?.[1];
  const energy = filters[1].options.find(([value]) => value === scope.energy)?.[1];
  const scenario = filters[2].options.find(([value]) => value === scope.scenario)?.[1];

  return (
    <div className="space-y-4 sm:space-y-6">
      <div className="flex min-w-0 items-start justify-between gap-3 border-b border-line pb-3 sm:items-center">
        <div className="min-w-0">
          <p className="text-sm font-semibold">{data?.buildingName ?? building} · {energy}</p>
          <p className="mt-0.5 text-xs leading-5 text-muted">
            {data && <>{buildingLabel[data.buildingType]} · {data.region} · </>}{scenario}
          </p>
          {data && <p className="mt-0.5 text-xs leading-5 text-muted"><EnergyInterval window={data} /></p>}
        </div>
        <EnergyFilterDrawer title="筛选业态与情景" fields={filters} applied={scope} onApply={(next) => {
          const nextParams = new URLSearchParams(next);
          if (nextParams.toString() !== params.toString()) setParams(nextParams);
        }} />
      </div>
      {data ? (
        <>
          <ReportSummary key={query} data={data} view={view} />
          {data.metrics.length ? (view === 'templates' ? <MetricTemplates data={data} /> : <MetricBenchmarks data={data} />) : (
            <EmptyState title="当前范围暂无可用行业指标" hint="可在筛选中选择其他样本建筑或能源。" />
          )}
        </>
      ) : error ? (
        <div role="alert"><ErrorState message={error} onRetry={retry} /></div>
      ) : (
        <div role="status"><span className="sr-only">正在加载行业能效样本</span><Skeleton className="h-56" /></div>
      )}
    </div>
  );
}
