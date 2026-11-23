import { useEffect, useState } from 'react';
import type { ReactElement } from 'react';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { useSearchParams } from 'react-router-dom';
import type { Envelope } from '../api/types';
import { Drawer } from '../components/Drawer';
import { EmptyState, ErrorState, Skeleton } from '../components/States';
import { cn } from '../lib/cn';
import { EnergyFilterDrawer, energyFilterValues } from './BuildingEnergyWorkspace';

interface Coverage {
  expected: number;
  valid: number;
  missing: number;
  suspect: number;
  rate: number | null;
}

interface Source {
  channel: string;
  usage: number | null;
  valid: number;
  missing: number;
  suspect: number;
  observations: number;
  profileVersion: string;
  objectBindingVersion: string;
  categoryBindingVersion?: string;
  mappingGeneration: number;
  meterRuleVersion: string;
}

const statusLabels = {
  comparable: '可比',
  missing_data: '缺测',
  insufficient_quality: '数据质量不足',
  inconsistent_scope: '统计口径不一致',
} as const;

interface DimensionRow {
  id: string;
  name: string;
  usage: number | null;
  coverage: Coverage;
  status: keyof typeof statusLabels;
  rank: number | null;
  sources: Source[] | null;
}

export interface DimensionReport {
  sample: true;
  view: 'building' | 'space' | 'organization';
  unit: string;
  timezone: string;
  from: string;
  to: string;
  asOf: string;
  ruleVersion: string;
  classificationRuleVersion: string;
  catalogVersion: string;
  topologyVersion: string;
  rows: DimensionRow[];
}

const filters = [
  { key: 'view', label: '统计维度', options: [['building', '建筑'], ['space', '区域'], ['organization', '组织']] },
  { key: 'building', label: '建筑范围', options: [['all', '全部建筑'], ['office-a', '办公楼 A'], ['office-b', '办公楼 B']] },
  { key: 'energy', label: '能源', options: [['electricity', '用电'], ['water', '用水']] },
  { key: 'period', label: '时间范围', options: [['day', '样本自然日'], ['hour', '样本自然小时'], ['month', '样本自然月'], ['7', '样本末 7 天'], ['14', '样本末 14 天'], ['30', '样本末 30 天']] },
  { key: 'mode', label: '统计周期', options: [['complete', '完整周期'], ['to-date', '同期截至']] },
  { key: 'scenario', label: '样本场景', options: [['standard', '标准样本'], ['quality', '质量异常样本']] },
] as const;
type FilterKey = (typeof filters)[number]['key'];
type Selection = Record<FilterKey, string>;

const panel = 'min-w-0 rounded-xl border border-line bg-surface';
const plotHeight = 'h-48 sm:h-64 lg:h-78';
const quantity = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 });
const percentage = new Intl.NumberFormat('zh-CN', { style: 'percent', maximumFractionDigits: 1 });
const format = (value: number | null, formatter = quantity) => value === null ? '—' : formatter.format(value);
const allowsPartial = (period: string) => period === 'day' || period === 'month';

function formatReportTime(report: DimensionReport) {
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: report.timezone, year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
  });
}

function TimeRange({ report }: { report: DimensionReport }) {
  const date = formatReportTime(report);
  return (
    <span className="tabular">
      <time className="inline-block" dateTime={report.from}>{date.format(new Date(report.from))}</time>
      {' 至 '}
      <time className="inline-block" dateTime={report.to}>{date.format(new Date(report.to))}</time>
    </span>
  );
}

function ObjectSources({ row, report, trigger }: {
  row: DimensionRow; report: DimensionReport; trigger?: ReactElement;
}) {
  const [open, setOpen] = useState(false);
  const date = formatReportTime(report);
  return (
    <Drawer
      open={open} onOpenChange={setOpen} title={`计量来源 · ${row.name}`}
      description="合成数据。分配量与计量版本均对应当前对象和所选时段。"
      trigger={trigger ?? (
        <button type="button" aria-label={`查看${row.name}计量来源`}
          className="inline-flex min-h-9 max-w-full items-center gap-1 rounded text-left text-primary hover:underline">
          <span className="min-w-0 break-words">{row.name}</span>
          <ChevronRight className="size-3.5 shrink-0" aria-hidden />
        </button>
      )}
    >
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 rounded-lg border border-line bg-surface-2 p-3 text-xs">
        <dt className="text-muted">统计时间</dt><dd><TimeRange report={report} /></dd>
        <dt className="text-muted">项目时区</dt><dd>{report.timezone}</dd>
        <dt className="text-muted">数据可见截至</dt><dd><time dateTime={report.asOf}>{date.format(new Date(report.asOf))}</time></dd>
        <dt className="text-muted">对象用量</dt><dd className="tabular">{format(row.usage)}{row.usage !== null ? ` ${report.unit}` : ''}</dd>
        <dt className="text-muted">覆盖率</dt><dd className="tabular">{format(row.coverage.rate, percentage)}（有效 {format(row.coverage.valid)} / 预期 {format(row.coverage.expected)}）</dd>
        <dt className="text-muted">统计规则</dt><dd className="break-all">{report.ruleVersion}</dd>
        <dt className="text-muted">分类计算</dt><dd className="break-all">{report.classificationRuleVersion}</dd>
        <dt className="text-muted">分类目录</dt><dd className="break-all">{report.catalogVersion}</dd>
        <dt className="text-muted">物理拓扑</dt><dd className="break-all">{report.topologyVersion}</dd>
      </dl>
      {!row.sources?.length ? <p className="mt-5 text-sm text-muted">此对象在所选时段没有计量来源。</p> : (
        <div className="mt-5 space-y-3">
          <h3 className="text-sm font-semibold">对象计量通道</h3>
          {row.sources.map((source) => (
            <section key={`${source.channel}/${source.mappingGeneration}/${source.profileVersion}/${source.objectBindingVersion}/${source.categoryBindingVersion}`}
              className="space-y-2 rounded-lg border border-line p-3 text-xs">
              <h4 className="break-all text-sm font-medium">{source.channel}</h4>
              <p className="tabular">分配量 {format(source.usage)}{source.usage !== null ? ` ${report.unit}` : ''} · 有效 {format(source.valid)} / {format(source.observations)} 读数</p>
              <p className="text-muted tabular">缺测 {format(source.missing)} · 可疑 {format(source.suspect)}</p>
              <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 border-t border-line pt-2">
                <dt className="text-muted">映射代次</dt><dd>{source.mappingGeneration}</dd>
                <dt className="text-muted">计量规则</dt><dd className="break-all">{source.meterRuleVersion}</dd>
                <dt className="text-muted">通道配置</dt><dd className="break-all">{source.profileVersion}</dd>
                <dt className="text-muted">对象归属</dt><dd className="break-all">{source.objectBindingVersion}</dd>
                <dt className="text-muted">分类归属</dt><dd className="break-all">{source.categoryBindingVersion ?? '—'}</dd>
              </dl>
            </section>
          ))}
        </div>
      )}
    </Drawer>
  );
}

function FilterToolbar({ applied, report, onApply }: {
  applied: Selection; report?: DimensionReport; onApply: (value: Selection) => void;
}) {
  const label = (key: FilterKey) => filters.find((filter) => filter.key === key)?.options
    .find(([value]) => value === applied[key])?.[1];
  const period = report && applied.period === 'day' ? new Intl.DateTimeFormat('zh-CN', {
    timeZone: report.timezone, month: 'long', day: 'numeric',
  }).format(new Date(report.from)) : label('period');

  return (
    <div className={cn(panel, 'flex items-center justify-between gap-3 px-3 py-2.5 sm:border-transparent sm:bg-transparent sm:px-0 sm:py-1')}>
      <div className="min-w-0">
        <p className="text-sm font-medium leading-5">{label('building')} · {label('view')}</p>
        <p className="text-xs leading-5 text-muted">
          {label('energy')} · {period}{applied.mode === 'to-date' && ' · 同期截至'}
          {applied.scenario !== 'standard' && ` · ${label('scenario')}`}
        </p>
      </div>
      <EnergyFilterDrawer title="筛选多维统计" fields={filters} applied={applied} onApply={onApply}
        normalize={(value) => allowsPartial(value.period) ? value : { ...value, mode: 'complete' }}
        disabled={(key, value) => key === 'mode' && !allowsPartial(value.period)} />
    </div>
  );
}

function RankingChart({ report, ranked }: { report: DimensionReport; ranked: DimensionRow[] }) {
  const peak = Math.max(...ranked.map((row) => row.usage ?? 0));
  // Rounded tick spacing is a presentation scale. Bar heights use the exact
  // quantity and the rank order supplied by the report.
  const order = peak > 0 ? 10 ** Math.floor(Math.log10(peak / 4)) : 1;
  const step = peak > 0 ? Math.ceil(peak / 4 / order) * order : 1;
  return (
    <figure className={cn(panel, 'px-3 pb-3 pt-3.5 sm:px-5 sm:pb-5 sm:pt-4')}>
      <figcaption className="flex items-baseline justify-between gap-2">
        <span className="text-sm font-medium">用量（{report.unit}）</span>
        <span className="hidden text-xs text-muted sm:inline">{ranked.length} 个对象参与排名</span>
      </figcaption>
      <div className="mt-6 flex gap-2 sm:gap-3">
        <div className={cn(plotHeight, 'relative w-9 shrink-0 text-right text-[11px] text-muted tabular sm:w-11 sm:text-xs')} aria-hidden>
          {[0, 1, 2, 3, 4].map((mark) => (
            <span key={mark} className="absolute right-0 translate-y-1/2" style={{ bottom: `${mark * 25}%` }}>{format(mark * step)}</span>
          ))}
        </div>
        <div className="relative min-w-0 flex-1">
          <div className={cn(plotHeight, 'pointer-events-none absolute inset-x-0 top-0 border-l border-line-strong')} aria-hidden>
            {[0, 1, 2, 3, 4].map((mark) => (
              <span key={mark} className={cn('absolute inset-x-0 border-t', mark === 0 ? 'border-line-strong' : 'border-line')} style={{ bottom: `${mark * 25}%` }} />
            ))}
          </div>
          <ol role="list" aria-label="按用量排名的可比对象" className="relative grid list-none gap-1 px-1 sm:gap-3 sm:px-3"
            style={{ gridTemplateColumns: `repeat(${ranked.length}, minmax(0,1fr))` }}>
            {ranked.map((row) => {
              const ratio = (row.usage ?? 0) / (step * 4);
              const [building, area] = row.name.split(' · ');
              return (
                <li key={row.id} className="min-w-0">
                  <ObjectSources row={row} report={report} trigger={(
                    <button type="button" title="查看计量来源"
                      aria-label={`${row.name}，第 ${row.rank} 名，${format(row.usage)} ${report.unit}，查看计量来源`}
                      className="group flex w-full flex-col items-center rounded-md text-center hover:bg-primary-soft/60">
                      <span className={cn(plotHeight, 'relative block w-full')} aria-hidden>
                        <span className="absolute inset-x-0 pb-1.5 text-sm font-medium tabular lg:text-base"
                          style={{ bottom: `${ratio * 100}%` }}>
                          {format(row.usage)}
                        </span>
                        <span className={cn('absolute bottom-0 left-1/2 w-[60%] max-w-32 -translate-x-1/2 rounded-t bg-primary group-hover:bg-primary-hover',
                          row.usage === 0 && 'h-px')}
                          style={row.usage === 0 ? undefined : { height: `${ratio * 100}%` }} />
                      </span>
                      <span className="mt-2.5 w-full break-words pb-1 text-xs leading-4 text-muted group-hover:text-primary sm:text-sm sm:leading-5" aria-hidden>
                        {building}{area && <><span className="hidden sm:inline"> · </span><br className="sm:hidden" />{area}</>}
                      </span>
                    </button>
                  )} />
                </li>
              );
            })}
          </ol>
        </div>
      </div>
      <p className="mt-3 text-xs text-muted sm:hidden">{ranked.length} 个对象参与排名</p>
    </figure>
  );
}

export function DimensionResults({ report }: { report: DimensionReport }) {
  const ranked = report.rows.filter((row) => row.rank !== null && row.usage !== null);
  const excluded = report.rows.filter((row) => row.rank === null);
  if (report.rows.length === 0) {
    return <div className={panel}><EmptyState title="所选范围内暂无对象" hint="可调整统计维度、建筑范围或时间。" /></div>;
  }
  return (
    <section aria-labelledby="f07-comparison-title" className="space-y-3">
      <h2 id="f07-comparison-title" className="sr-only">同口径对象用量对比</h2>
      {ranked.length > 0 ? <RankingChart report={report} ranked={ranked} /> : (
        <div className={panel}><EmptyState title="暂无可排名对象" hint="展开明细查看用量、覆盖率和未排名原因。" /></div>
      )}
      {excluded.length > 0 && (
        <div className="space-y-1 px-1 text-xs leading-5 text-muted">
          {excluded.map((row) => (
            <p key={row.id}>{row.name} · {statusLabels[row.status]}{row.coverage.rate !== null && `（覆盖率 ${format(row.coverage.rate, percentage)}）`}</p>
          ))}
        </div>
      )}
      <details className={cn(panel, 'group overflow-hidden')}>
        <summary className="flex min-h-11 cursor-pointer list-none items-center justify-between gap-3 px-3 text-sm font-medium marker:content-none hover:bg-surface-2 sm:px-5 [&::-webkit-details-marker]:hidden">
          <span>用量明细与计量来源</span>
          <ChevronDown className="size-4 text-muted group-open:rotate-180" aria-hidden />
        </summary>
        <p className="border-t border-line px-3 py-3 text-xs leading-5 text-muted sm:px-5">
          <TimeRange report={report} /> <span className="inline-block">（{report.timezone}）</span>
        </p>
        <table className="w-full text-left tabular">
          <caption className="sr-only">对象排名、用量及覆盖率</caption>
          <thead className="border-y border-line bg-surface-2 text-xs text-muted">
            <tr>
              <th scope="col" className="w-16 whitespace-nowrap px-2.5 py-2.5 font-medium sm:w-20 sm:px-5">排名</th>
              <th scope="col" className="py-2.5 pr-1 font-medium">对象</th>
              <th scope="col" className="w-23 whitespace-nowrap px-1 py-2.5 text-right font-medium sm:w-32">用量（{report.unit}）</th>
              <th scope="col" className="w-25 px-3 py-2.5 text-right font-medium sm:w-44 sm:px-5">覆盖率</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {report.rows.map((row) => (
              <tr key={row.id} className="text-sm">
                <td className="whitespace-nowrap px-2.5 py-2 font-medium sm:px-5">
                  {row.rank !== null ? row.rank : <span className="text-xs text-muted">未排名</span>}
                </td>
                <th scope="row" className="min-w-0 py-2 pr-1 font-medium">
                  <ObjectSources row={row} report={report} />
                </th>
                <td className="whitespace-nowrap px-1 py-2 text-right font-medium">{format(row.usage)}</td>
                <td className="px-3 py-2 text-right sm:px-5">
                  {format(row.coverage.rate, percentage)}
                  <span className={cn('mt-0.5 block text-xs', row.status === 'comparable' ? 'text-positive' : 'text-warning')}>
                    {row.status === 'comparable' && row.usage === 0 ? '有效零' : statusLabels[row.status]}
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </details>
    </section>
  );
}

export default function BuildingEnergyDimensionsPage() {
  const [params, setParams] = useSearchParams();
  const selected = energyFilterValues(filters, params);
  if (!filters[0].options.some(([value]) => params.get('view') === value) && selected.building !== 'all') selected.view = 'space';
  if (!allowsPartial(selected.period)) selected.mode = 'complete';

  const query = new URLSearchParams(selected).toString();
  const [attempt, setAttempt] = useState(0);
  const [response, setResponse] = useState<{ key: string; data?: DimensionReport; error?: string }>();
  const requestKey = `${query}|${attempt}`;
  const current = response?.key === requestKey ? response : undefined;

  useEffect(() => {
    const controller = new AbortController();
    fetch(`/admin/v1/analytics/building-energy/preview/dimensions?${query}`, { signal: controller.signal })
      .then(async (res) => {
        const payload = (await res.json()) as Envelope<DimensionReport>;
        if (!res.ok || payload.code !== 0 || !payload.data?.sample) throw new Error(payload.message || '样本服务不可用');
        if (!controller.signal.aborted) setResponse({ key: requestKey, data: payload.data });
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) setResponse({ key: requestKey, error: error instanceof Error ? error.message : '样本加载失败' });
      });
    return () => controller.abort();
  }, [query, requestKey]);

  return (
    <div className="space-y-3 sm:space-y-4">
      <FilterToolbar applied={selected} report={current?.data} onApply={(value) => setParams(value)} />
      {current?.data && <DimensionResults report={current.data} />}
      {current?.error && <div className={panel}><ErrorState message={current.error} onRetry={() => setAttempt((value) => value + 1)} /></div>}
      {!current && <div role="status" aria-label="加载多维统计"><Skeleton className="h-48" /></div>}
    </div>
  );
}
