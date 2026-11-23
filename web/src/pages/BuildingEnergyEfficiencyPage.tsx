import { EmptyState, ErrorState, Skeleton } from '../components/States';
import { EnergyScopeBar, EnergySectionHeading, energyNumber, useEnergyScope } from './BuildingEnergyWorkspace';
import { EnergyEvidence, EnergyInterval, energyPercent, energyQualityLabel, energyTime, useEnergyPreview } from './BuildingEnergyResults';
import type { EnergyEvidenceData, EnergyQuality, EnergyValue } from './BuildingEnergyResults';

type MetricId = 'per-area' | 'per-person';
type MetricStatus = 'ready' | 'missing_denominator' | 'invalid_denominator' | 'denominator_out_of_period'
  | 'missing_usage' | 'insufficient_quality';

interface EfficiencyMetric {
  id: MetricId;
  value: number | null;
  unit: string;
  status: MetricStatus;
  denominator: {
    value: number;
    unit: string;
    source: string;
    version: string;
    basis: string;
    from: string;
    to: string;
  } | null;
}

interface EfficiencyReport extends EnergyEvidenceData {
  sample: true;
  numerator: EnergyValue;
  quality: EnergyQuality;
  metrics: EfficiencyMetric[];
}

const metricNames: Record<MetricId, string> = {
  'per-area': '单位建筑面积用量',
  'per-person': '人均用量',
};

const denominatorNames: Record<MetricId, string> = {
  'per-area': '建筑面积',
  'per-person': '统计期人数',
};

const metricStatus: Record<Exclude<MetricStatus, 'ready'>, string> = {
  missing_denominator: '缺少有效分母记录',
  invalid_denominator: '分母必须为正值',
  denominator_out_of_period: '分母记录未覆盖统计期',
  missing_usage: '统计期缺少有效用量',
  insufficient_quality: '用量数据质量不足',
};

function IntensityMetrics({ data }: { data: EfficiencyReport }) {
  return (
    <section aria-labelledby="energy-intensity" className="overflow-hidden rounded-xl border border-line bg-surface">
      <header className="px-4 py-4 sm:px-5">
        <h2 id="energy-intensity" className="text-base font-semibold">用量强度</h2>
        <p className="mt-1 text-xs leading-5 text-muted"><EnergyInterval window={data} /></p>
      </header>
      <dl className="grid divide-y divide-line border-y border-line sm:grid-cols-2 sm:divide-x sm:divide-y-0">
        {data.metrics.map((metric) => (
          <div key={metric.id} className="min-w-0 px-4 py-5 sm:px-5">
            <dt className="text-sm font-medium">{metricNames[metric.id]}</dt>
            <dd className="mt-2 flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
              {metric.status === 'ready' ? (
                <span className="text-3xl font-semibold tracking-tight tabular">{energyNumber(metric.value)}</span>
              ) : (
                <span className="text-xl font-medium text-muted">不可计算</span>
              )}
              <span className="text-sm text-muted">{metric.unit}</span>
            </dd>
            <dd className="mt-2 text-sm leading-5 text-muted">
              {denominatorNames[metric.id]} <span className="tabular">{energyNumber(metric.denominator?.value ?? null)}</span>
              {metric.denominator ? ` ${metric.denominator.unit}（合成）` : ''}
            </dd>
            {metric.status !== 'ready' && (
              <dd className="mt-1 text-sm leading-5 text-fg">{metricStatus[metric.status]}</dd>
            )}
          </div>
        ))}
      </dl>
      <div className="flex flex-wrap items-center justify-between gap-x-5 gap-y-2 px-4 py-3 sm:px-5">
        <dl className="flex flex-wrap gap-x-5 gap-y-1 text-sm">
          <div className="flex flex-wrap gap-x-2"><dt className="text-muted">统计用量（分子）</dt>
            <dd className="font-medium tabular">{energyNumber(data.numerator.usage)}{data.numerator.usage === null ? '' : ` ${data.unit}`}</dd>
          </div>
          <div className="flex flex-wrap gap-x-2"><dt className="text-muted">覆盖率</dt>
            <dd className="font-medium tabular">{energyPercent(data.numerator.coverage.rate)}</dd>
            <dd className={data.quality === 'valid' || data.quality === 'valid_zero' ? 'text-muted' : 'text-warning'}>
              {energyQualityLabel[data.quality]}
            </dd>
          </div>
        </dl>
        <EnergyEvidence data={data}>
          <div className="border-y border-line py-3">
            <h3 className="font-semibold">指标分母（合成）</h3>
            <dl className="mt-1 divide-y divide-line">
              {data.metrics.map(({ id, denominator }) => (
                <div key={id} className="py-2">
                  <dt className="font-medium">{denominatorNames[id]}</dt>
                  {denominator ? (
                    <>
                      <dd className="mt-1 break-words text-xs leading-5 text-muted">{denominator.source} · {denominator.version}</dd>
                      <dd className="text-xs leading-5 text-muted">{denominator.basis}</dd>
                      <dd className="text-xs leading-5 text-muted">
                        有效期 {energyTime(denominator.from, data.timezone)} 至 {energyTime(denominator.to, data.timezone)}（不含结束）
                      </dd>
                    </>
                  ) : <dd className="mt-1 text-xs text-muted">缺少记录</dd>}
                </div>
              ))}
            </dl>
          </div>
        </EnergyEvidence>
      </div>
    </section>
  );
}

export default function BuildingEnergyEfficiencyPage() {
  const { query } = useEnergyScope();
  const { data, error, retry } = useEnergyPreview<EfficiencyReport>('efficiency', query);

  return (
    <div className="space-y-4 sm:space-y-6">
      <EnergyScopeBar />
      {data ? (
        data.metrics.length ? <IntensityMetrics key={query} data={data} /> : (
          <EmptyState title="此范围暂无强度指标" hint="可调整建筑、能源或统计期。" />
        )
      ) : error ? (
        <div role="alert"><ErrorState message={error} onRetry={retry} /></div>
      ) : (
        <div role="status"><span className="sr-only">正在加载能效指标</span><Skeleton className="h-56" /></div>
      )}
      <section aria-labelledby="energy-assessment" className="border-t border-line pt-4">
        <div id="energy-assessment"><EnergySectionHeading title="对标与考核" /></div>
        <p className="mt-1 max-w-2xl text-sm leading-6 text-muted">
          行业指标缺少业务分母，定额对标缺少适用标准。分期目标和组织考核等待配置及签认。
        </p>
      </section>
    </div>
  );
}
