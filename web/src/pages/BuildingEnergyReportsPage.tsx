import { Download } from 'lucide-react';
import type { ReactNode } from 'react';
import { useSearchParams } from 'react-router-dom';
import { EmptyState, ErrorState, Skeleton } from '../components/States';
import { EnergyScopeBar, EnergySectionHeading, energyNumber, useEnergyScope } from './BuildingEnergyWorkspace';
import { EnergyEvidence, EnergyInterval, energyPercent, energyQualityLabel, energyTime, useEnergyPreview } from './BuildingEnergyResults';
import type { EnergyEvidenceData, EnergyQuality, EnergyValue } from './BuildingEnergyResults';

type ReportType = 'classification' | 'summary';

interface ReportRow extends EnergyValue {
  id: string;
  name: string;
  depth: number;
  kind: 'category' | 'unclassified' | 'period';
  status: EnergyQuality;
  share: number | null;
}

export interface EnergyReportSnapshot extends EnergyEvidenceData {
  filename: string;
  csv: string;
  generatedAt: string;
  reportId: string;
  operator: string;
}

interface EnergyReport extends EnergyReportSnapshot {
  sample: true;
  type: ReportType;
  total: EnergyValue;
  rows: ReportRow[];
}

const reportTypes: Record<ReportType, string> = {
  classification: '分类分项',
  summary: '用量汇总',
};

function downloadReport(data: Pick<EnergyReportSnapshot, 'filename' | 'csv'>) {
  const url = URL.createObjectURL(new Blob([data.csv], { type: 'text/csv;charset=utf-8' }));
  const link = document.createElement('a');
  link.href = url;
  link.download = data.filename;
  document.body.append(link);
  link.click();
  link.remove();
  // The browser may resolve the URL after dispatching the click.
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export function EnergyReportDownload({ data }: { data: Pick<EnergyReportSnapshot, 'filename' | 'csv'> }) {
  return (
    <button type="button" onClick={() => downloadReport(data)}
      className="inline-flex min-h-10 items-center gap-1.5 whitespace-nowrap rounded-md bg-primary px-3 text-sm font-medium text-white hover:bg-primary-hover focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary dark:text-canvas">
      <Download className="size-4" aria-hidden />导出 CSV
    </button>
  );
}

export function EnergyReportEvidence({ data, children }: { data: EnergyReportSnapshot; children?: ReactNode }) {
  return (
    <EnergyEvidence data={data}>
      <div className="border-y border-line py-3">
        <h3 className="font-semibold">报表快照</h3>
        <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-xs leading-5 text-muted">
          <dt>报表编号</dt><dd className="break-all">{data.reportId}</dd>
          <dt>生成时间</dt><dd>{energyTime(data.generatedAt, data.timezone)}</dd>
          <dt>生成主体</dt><dd>{data.operator}</dd>
        </dl>
      </div>
      {children}
    </EnergyEvidence>
  );
}

function ReportPreview({ data }: { data: EnergyReport }) {
  return (
    <>
      <div className="flex flex-wrap items-end justify-between gap-4 border-y border-line px-4 py-4 sm:px-5">
        <dl className="flex flex-wrap gap-x-8 gap-y-3">
          <div>
            <dt className="text-xs font-medium text-muted">统计用量</dt>
            <dd className="mt-1 flex flex-wrap items-baseline gap-x-2">
              <span className="text-3xl font-semibold tracking-tight tabular">{energyNumber(data.total.usage)}</span>
              {data.total.usage !== null && <span className="text-sm text-muted">{data.unit}</span>}
            </dd>
          </div>
          <div>
            <dt className="text-xs font-medium text-muted">覆盖率</dt>
            <dd className="mt-1 text-lg font-semibold tabular">{energyPercent(data.total.coverage.rate)}</dd>
            <dd className="text-xs leading-5 text-muted">
              缺测 {energyNumber(data.total.coverage.missing)} · 可疑 {energyNumber(data.total.coverage.suspect)} 表小时
            </dd>
          </div>
        </dl>
        <EnergyReportEvidence data={data} />
      </div>
      {data.rows.length ? (
        <table className="w-full table-fixed text-sm">
          <caption className="sr-only">{reportTypes[data.type]}报表明细</caption>
          <thead className="border-b border-line bg-surface-2 text-xs text-muted">
            <tr>
              <th scope="col" className="w-[43%] py-2.5 pl-4 pr-2 text-left font-medium sm:pl-5">{data.type === 'classification' ? '分类 / 分项' : '统计日'}</th>
              <th scope="col" className="w-[27%] px-1 py-2.5 text-right font-medium">用量（{data.unit}）</th>
              <th scope="col" className="hidden w-[15%] px-2 py-2.5 text-right font-medium sm:table-cell">占比</th>
              <th scope="col" className="py-2.5 pr-4 pl-2 text-right font-medium sm:pr-5">数据质量</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {data.rows.map((row) => (
              <tr key={row.id} className="align-top">
                <th scope="row" className="break-words py-2.5 pl-4 pr-2 text-left font-medium sm:pl-5">
                  <span className="block" style={{ paddingInlineStart: `${row.depth * 0.75}rem` }}>{row.name}</span>
                </th>
                <td className="px-1 py-2.5 text-right tabular">
                  {energyNumber(row.usage)}
                  {row.share !== null && <span className="block text-xs text-muted sm:hidden">占比 {energyPercent(row.share)}</span>}
                </td>
                <td className="hidden px-2 py-2.5 text-right tabular text-muted sm:table-cell">{energyPercent(row.share)}</td>
                <td className="py-2.5 pr-4 pl-2 text-right text-xs leading-5 sm:pr-5">
                  <span className={row.status === 'valid' || row.status === 'valid_zero' ? 'text-muted' : 'text-warning'}>{energyQualityLabel[row.status]}</span>
                  <span className="block text-muted tabular">{energyPercent(row.coverage.rate)}</span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : <EmptyState title="此范围暂无报表明细" hint="可调整建筑、能源或统计期。" />}
    </>
  );
}

export default function BuildingEnergyReportsPage() {
  const [params, setParams] = useSearchParams();
  const { query } = useEnergyScope();
  const type: ReportType = params.get('type') === 'summary' ? 'summary' : 'classification';
  const { data, error, retry } = useEnergyPreview<EnergyReport>('reports', `${query}&type=${type}`);

  function changeReport(type: string) {
    const next = new URLSearchParams(params);
    next.set('type', type);
    setParams(next);
  }

  return (
    <div className="space-y-4 sm:space-y-6">
      <EnergyScopeBar />
      <section className="overflow-hidden rounded-xl border border-line bg-surface" aria-labelledby="energy-report-preview">
        <div className="flex flex-wrap items-start justify-between gap-4 px-4 py-4 sm:px-5">
          <div>
            <h2 id="energy-report-preview" className="text-base font-semibold">报表预览</h2>
            {data && <p className="mt-1 text-xs leading-5 text-muted"><EnergyInterval window={data} /></p>}
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <label className="flex items-center gap-2 text-sm text-muted">
              报表类型
              <select value={type} onChange={(event) => changeReport(event.target.value)}
                className="min-h-10 max-w-full rounded-md border border-line-strong bg-surface px-2.5 text-sm text-fg">
                <option value="classification">{reportTypes.classification}</option>
                <option value="summary">{reportTypes.summary}</option>
              </select>
            </label>
            {data?.rows.length && data.csv ? <EnergyReportDownload data={data} /> : null}
          </div>
        </div>
        {data ? (
          <ReportPreview key={`${query}|${type}`} data={data} />
        ) : error ? (
          <div role="alert"><ErrorState message={error} onRetry={retry} /></div>
        ) : (
          <div role="status" className="space-y-3 border-t border-line p-4 sm:p-5">
            <span className="sr-only">正在加载报表</span>
            <Skeleton className="h-14" /><Skeleton className="h-36" />
          </div>
        )}
        {data && (
          <p className="border-t border-line px-4 py-3 text-xs leading-5 text-muted sm:px-5">
            预览与 CSV 使用同一统计快照。能源按原单位单列。
            {data.type === 'classification' && '分类父项已包含子项，汇总时取顶层分类和未分项。'}
          </p>
        )}
      </section>
      <section aria-labelledby="energy-report-availability" className="border-t border-line pt-4">
        <div id="energy-report-availability"><EnergySectionHeading title="其他报表" /></div>
        <p className="mt-1 max-w-2xl text-sm leading-6 text-muted">
          指标与定额、行业专项报表可在左侧打开合成预览。组织考核和改造成效报表等待对应业务数据。
        </p>
      </section>
    </div>
  );
}
