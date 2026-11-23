import { Link } from 'react-router-dom';
import { ChevronDown } from 'lucide-react';
import { ENERGY_STATISTICS_PATH } from '../components/AppShell';
import { EmptyState, ErrorState, Skeleton } from '../components/States';
import { DimensionResults } from './BuildingEnergyDimensionsPage';
import type { DimensionReport } from './BuildingEnergyDimensionsPage';
import { useEnergyPreview } from './BuildingEnergyResults';
import { EnergyScopeBar, EnergySectionHeading, energyNumber, useEnergyScope } from './BuildingEnergyWorkspace';

const evaluation = [
  ['改造前基线', '待提供可用的历史工况、天气和业务活动量。'],
  ['潜力与收益评估', '待建立基线并提供改造方案、成本和适用价格。'],
  ['改造后节能核验', '待取得改造后计量及可复核的对照记录。'],
] as const;

export default function BuildingEnergyDiagnosisPage() {
  const { query } = useEnergyScope();
  const comparison = `${query}&view=space&mode=complete&scenario=standard`;
  const { data: report, error, retry } = useEnergyPreview<DimensionReport>('dimensions', comparison);
  const comparable = report?.rows.filter(({ rank }) => rank !== null) ?? [];
  const highest = comparable.filter(({ rank, usage }) => rank === 1 && usage !== null && usage > 0);
  const hasObservation = comparable.length >= 2 && highest.length > 0;

  return (
    <div className="space-y-5">
      <EnergyScopeBar />
      {error ? <div role="alert"><ErrorState message={error} onRetry={retry} /></div> : !report ? (
        <div role="status" aria-label="加载用能观察"><Skeleton className="h-48" /></div>
      ) : (
        <section className="space-y-3" aria-label="用能观察">
          <EnergySectionHeading title="区域用能差异" action={
            <Link to={`${ENERGY_STATISTICS_PATH}/dimensions?${comparison}`}
              className="inline-flex min-h-10 items-center rounded-md px-2 text-sm font-medium text-primary hover:bg-primary-soft focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary">
              查看多维统计
            </Link>
          } />
          {hasObservation ? (
            <p className="text-sm leading-relaxed text-fg">
              <strong>{highest.map(({ name }) => name).join('、')}</strong>在当前可比区域中用量最高，
              为 <span className="font-semibold tabular">{energyNumber(highest[0].usage)} {report.unit}</span>。
            </p>
          ) : (
            <div role="status">
              <EmptyState title="暂无高用量观察"
                hint="需要至少两个同口径且质量合格的区域，以及高于零的用量。可在下方查看覆盖率与未排名原因。" />
            </div>
          )}
          <DimensionResults report={report} />
          {hasObservation && (
            <div className="grid gap-3 border-t border-line pt-4 text-sm leading-relaxed sm:grid-cols-2">
              <div><h3 className="font-semibold">可进一步核查</h3><p className="mt-1 text-muted">核对区域运行时长、使用面积和设备负荷，再结合用能时段定位差异。</p></div>
              <div><h3 className="font-semibold">结论边界</h3><p className="mt-1 text-muted">当前证据支持用量对比。是否存在浪费及节能空间仍需现场和工况证据。</p></div>
            </div>
          )}
        </section>
      )}
      <details className="group border-t border-line pt-1">
        <summary className="flex min-h-11 cursor-pointer list-none items-center gap-2 text-sm font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary [&::-webkit-details-marker]:hidden">
          改造评估条件 <span className="font-normal text-muted">待补齐</span>
          <ChevronDown className="size-4 text-muted group-open:rotate-180" aria-hidden />
        </summary>
        <dl className="grid gap-4 pb-3 text-sm leading-relaxed sm:grid-cols-3">
          {evaluation.map(([label, detail]) => (
            <div key={label}><dt className="font-semibold">{label}</dt><dd className="mt-1 text-muted">{detail}</dd></div>
          ))}
        </dl>
      </details>
    </div>
  );
}
