import { ArrowDown, ArrowUp, ChevronDown, SlidersHorizontal, TriangleAlert } from 'lucide-react';
import { useId, useState } from 'react';
import type { FormEvent } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Drawer } from '../components/Drawer';
import { EmptyState, ErrorState, Skeleton } from '../components/States';
import { cn } from '../lib/cn';
import { energyIndustryFilters } from './BuildingEnergyIndustryPage';
import { EnergyReportDownload, EnergyReportEvidence } from './BuildingEnergyReportsPage';
import type { EnergyReportSnapshot } from './BuildingEnergyReportsPage';
import { EnergyInterval, energyTime, useEnergyPreview } from './BuildingEnergyResults';
import { EnergyFilterDrawer, energyFilterValues } from './BuildingEnergyWorkspace';

const demoNotice = '演示规则，非权威标准';
const numericFields = new Set(['value', 'numerator', 'denominator', 'coverage', 'benchmarkThreshold', 'benchmarkDelta']);
const wideFields = new Set(['versions', 'benchmarkRule', 'denominatorEvidence', 'scope', 'period', 'template', 'definition']);
const focus = 'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary';
const selectClass = `min-h-11 w-full rounded-md border border-line bg-surface px-2.5 text-sm font-normal text-fg ${focus}`;

const [buildingFilter, energyFilter, scenarioFilter] = energyIndustryFilters;
const reportFilters = [buildingFilter, energyFilter, {
  ...scenarioFilter,
  options: [...scenarioFilter.options, ['display-stress', '医院长名称与大数值']],
}] as const;
const customFilters = [buildingFilter, energyFilter, {
  ...scenarioFilter,
  options: [...reportFilters[2].options, ['no-template', '无适用模板']],
}, {
  key: 'template', label: '行业模板', options: [
    ['auto', '按建筑选择'], ['office', '办公楼'], ['hospital', '医院'], ['school', '学校'], ['hotel', '酒店'],
  ],
}] as const;

type FilterKey = typeof customFilters[number]['key'];
type Scope = Record<FilterKey, string>;
type Direction = 'asc' | 'desc';
interface ReportField { id: string; label: string }
interface ReportLayout { fields: string[]; sort: string; direction: Direction }

interface IndustryReportSnapshot extends EnergyReportSnapshot {
  sample: true;
  buildingName?: string;
  status: 'ready' | 'no_applicable_template';
  statusMessage: string;
  notice: string;
  metricRuleVersion: string;
  columns: ReportField[];
  rows: string[][];
  availableFields?: ReportField[];
  availableSorts?: ReportField[];
  sort: string;
  direction: Direction;
}

function normalizeScope(scope: Scope): Scope {
  return scope.scenario === 'display-stress' ? { ...scope, building: 'hospital-a' } : scope;
}

function fieldValue(data: IndustryReportSnapshot, row: string[], id: string) {
  return row[data.columns.findIndex((column) => column.id === id)] ?? '';
}

function PreviewCell({ value }: { value: string }) {
  return value || <span className="text-muted"><span aria-hidden="true">—</span><span className="sr-only">暂无值</span></span>;
}

function ReportRows({ data }: { data: IndustryReportSnapshot }) {
  const sortNote = data.availableSorts?.find(({ id }) => id === data.sort)?.label;
  return (
    <>
      <div className="hidden sm:block">
        <div role="region" aria-label="可横向滚动的报表预览" tabIndex={0}
          className="relative max-w-full overflow-x-auto focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-primary">
          <table className="min-w-full w-max border-separate border-spacing-0 text-sm leading-5">
            <caption className="sr-only">报表预览数据</caption>
            <thead className="text-xs text-muted">
              <tr>
                {data.columns.map(({ id, label }, index) => (
                  <th key={id} scope="col" data-column={id}
                    aria-sort={sortNote && id === data.sort ? (id === 'value' ? 'other' : data.direction === 'asc' ? 'ascending' : 'descending') : undefined}
                    className={cn('border-y border-line bg-surface-2 px-4 py-2.5 font-medium',
                      wideFields.has(id) ? 'min-w-60 max-w-96' : 'min-w-24 max-w-56',
                      numericFields.has(id) ? 'text-right whitespace-nowrap' : id === 'unit' ? 'text-left whitespace-nowrap' : 'text-left',
                      index === 0 && 'sticky left-0 z-10 min-w-36 border-r sm:pl-5')}>
                    {label}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {data.rows.map((row, rowIndex) => (
                <tr key={rowIndex}>
                  {data.columns.map(({ id }, index) => {
                    const cell = row[index];
                    const className = cn('border-b border-line px-4 py-3 align-top',
                      wideFields.has(id) ? 'min-w-60 max-w-96' : 'min-w-24 max-w-56',
                      '[overflow-wrap:anywhere]',
                      numericFields.has(id) && 'text-right tabular whitespace-nowrap',
                      id === 'unit' && 'whitespace-nowrap',
                      (id === 'quality' || id === 'status' || id === 'notice') && 'text-muted');
                    return index === 0 ? (
                      <th key={id} scope="row" data-value={cell}
                        className={cn(className, 'sticky left-0 z-10 min-w-36 border-r bg-surface text-left font-medium sm:pl-5')}>
                        <PreviewCell value={cell} />
                      </th>
                    ) : (
                      <td key={id} data-value={cell} className={className}><PreviewCell value={cell} /></td>
                    );
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
      <ul className="divide-y divide-line border-y border-line sm:hidden" aria-label="报表预览数据">
        {data.rows.map((row, rowIndex) => {
          const metric = fieldValue(data, row, 'metric');
          const value = fieldValue(data, row, 'value');
          const unit = fieldValue(data, row, 'unit');
          const quality = fieldValue(data, row, 'quality');
          const status = fieldValue(data, row, 'status');
          return (
            <li key={rowIndex}>
              <details className="group px-4">
                <summary className={cn('flex cursor-pointer list-none items-start justify-between gap-3 rounded-sm py-3 [&::-webkit-details-marker]:hidden', focus)}>
                  <span className="min-w-0 break-words">
                    <span className="block text-sm font-medium">{metric}</span>
                    <span className="mt-1 block text-xs leading-relaxed text-muted">{quality}</span>
                    {!value && status && <span className="block text-xs leading-relaxed text-muted">{status}</span>}
                  </span>
                  <span className="flex min-w-0 shrink-0 items-start gap-2 text-right">
                    {data.columns.some(({ id }) => id === 'value') && (
                      <span className="max-w-24 text-sm tabular [overflow-wrap:anywhere]">
                        <PreviewCell value={value} />
                        {value && <span className="block text-xs text-muted">{unit}</span>}
                      </span>
                    )}
                    <ChevronDown className="mt-0.5 size-4 shrink-0 text-muted group-open:rotate-180" aria-hidden />
                  </span>
                </summary>
                <dl className="grid grid-cols-[minmax(0,0.8fr)_minmax(0,1.4fr)] gap-x-3 gap-y-2 border-t border-line pb-4 pt-3 text-sm leading-5">
                  {data.columns.map(({ id, label }, index) => (
                    <div key={id} className="contents">
                      <dt className="text-muted [overflow-wrap:anywhere]">{label}</dt>
                      <dd data-value={row[index]} className="tabular [overflow-wrap:anywhere]"><PreviewCell value={row[index]} /></dd>
                    </div>
                  ))}
                </dl>
              </details>
            </li>
          );
        })}
      </ul>
    </>
  );
}

function ReportLayoutDrawer({ data, onApply }: { data?: IndustryReportSnapshot; onApply: (layout: ReportLayout) => void }) {
  const formId = useId();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<ReportLayout>({ fields: [], sort: 'metric', direction: 'asc' });
  const availableFields = data?.availableFields ?? [];
  const availableSorts = data?.availableSorts ?? [];
  const labels = new Map(availableFields.map(({ id, label }) => [id, label]));
  const orderedFields = [...draft.fields, ...availableFields.map(({ id }) => id).filter((id) => !draft.fields.includes(id))];

  function show(next: boolean) {
    if (next) {
      if (!data || !availableFields.length) return;
      setDraft({ fields: data.columns.map(({ id }) => id).filter((id) => labels.has(id)), sort: data.sort, direction: data.direction });
    }
    setOpen(next);
  }

  function setField(id: string, checked: boolean) {
    setDraft((current) => ({ ...current, fields: checked ? [...current.fields, id] : current.fields.filter((field) => field !== id) }));
  }

  function move(index: number, offset: number) {
    setDraft((current) => {
      const destination = index + offset;
      if (destination < 0 || destination >= current.fields.length) return current;
      const fields = [...current.fields];
      [fields[index], fields[destination]] = [fields[destination], fields[index]];
      return { ...current, fields };
    });
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setOpen(false);
    onApply(draft);
  }

  return (
    <Drawer open={open} onOpenChange={show} title="字段与排序" description="勾选报表列并调整顺序。对象、统计期、指标和质量等基本信息始终保留。"
      trigger={(
        <button type="button" aria-disabled={!data || !availableFields.length}
          className={cn('inline-flex min-h-10 items-center gap-1.5 rounded-md border border-line-strong bg-surface px-3 text-sm font-medium hover:bg-surface-2 aria-disabled:text-muted', focus)}>
          <SlidersHorizontal className="size-4 text-primary" aria-hidden />字段与排序
        </button>
      )}
      footer={(
        <>
          <button type="button" onClick={() => setOpen(false)} className={cn('min-h-10 rounded-md px-3 text-sm text-muted hover:bg-surface-2', focus)}>取消</button>
          <button type="submit" form={formId}
            className={cn('min-h-10 rounded-md bg-primary px-4 text-sm font-semibold text-white hover:bg-primary-hover dark:text-canvas', focus)}>应用报表配置</button>
        </>
      )}>
      <form id={formId} onSubmit={submit} className="space-y-5">
        <div className="grid grid-cols-2 gap-3">
          <label className="flex min-w-0 flex-col gap-1.5 text-sm font-medium">
            排序字段
            <select value={draft.sort} onChange={(event) => setDraft({ ...draft, sort: event.target.value })} className={selectClass}>
              {availableSorts.map(({ id, label }) => <option key={id} value={id}>{label}</option>)}
            </select>
          </label>
          <label className="flex min-w-0 flex-col gap-1.5 text-sm font-medium">
            排序方向
            <select value={draft.direction} onChange={(event) => setDraft({ ...draft, direction: event.target.value as Direction })} className={selectClass}>
              <option value="asc">升序</option><option value="desc">降序</option>
            </select>
          </label>
        </div>
        <fieldset className="min-w-0">
          <legend className="text-sm font-medium">报表列</legend>
          <ul className="mt-2 divide-y divide-line border-y border-line">
            {orderedFields.map((id) => {
              const index = draft.fields.indexOf(id);
              const label = labels.get(id);
              return (
                <li key={id} className="flex min-h-11 items-center justify-between gap-2">
                  <label className="flex min-w-0 flex-1 cursor-pointer items-center gap-2 py-2 text-sm">
                    <input type="checkbox" checked={index >= 0} onChange={(event) => setField(id, event.target.checked)}
                      className={cn('size-4 shrink-0 accent-primary', focus)} />
                    <span className="break-words">{label}</span>
                  </label>
                  {index >= 0 && (
                    <div className="flex shrink-0 gap-1">
                      <button type="button" aria-label={`上移${label}`} aria-disabled={index === 0} onClick={() => move(index, -1)}
                        className={cn('grid size-9 place-items-center rounded text-muted hover:bg-surface-2 aria-disabled:opacity-35', focus)}><ArrowUp className="size-4" aria-hidden /></button>
                      <button type="button" aria-label={`下移${label}`} aria-disabled={index === draft.fields.length - 1} onClick={() => move(index, 1)}
                        className={cn('grid size-9 place-items-center rounded text-muted hover:bg-surface-2 aria-disabled:opacity-35', focus)}><ArrowDown className="size-4" aria-hidden /></button>
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        </fieldset>
      </form>
    </Drawer>
  );
}

export default function BuildingEnergyIndustryReportsPage({ view = 'metrics' }: { view?: 'metrics' | 'custom' }) {
  const [params, setParams] = useSearchParams();
  const custom = view === 'custom';
  const filters = custom ? customFilters : reportFilters;
  const scope = normalizeScope(energyFilterValues<FilterKey>(filters, params));
  const building = buildingFilter.options.find(([id]) => scope.building === id)?.[1];
  const energy = energyFilter.options.find(([id]) => scope.energy === id)?.[1];
  const scenario = filters[2].options.find(([id]) => scope.scenario === id)?.[1];
  const template = customFilters[3].options.find(([id]) => scope.template === id)?.[1];
  const query = new URLSearchParams({ kind: custom ? 'f25' : 'f22', building: scope.building, energy: scope.energy, scenario: scope.scenario });
  const project = params.get('project');
  if (project !== null) query.set('project', project);
  if (custom) {
    if (scope.template !== 'auto') query.set('template', scope.template);
    for (const key of ['fields', 'sort', 'direction']) {
      const requested = params.get(key);
      if (requested !== null) query.set(key, requested);
    }
  }
  const { data, error, retry } = useEnergyPreview<IndustryReportSnapshot>('industry-reports', query.toString());
  const hasRows = data?.status === 'ready' && data.rows.length > 0;
  const dataSort = data?.availableSorts?.find(({ id }) => id === data.sort)?.label;

  function updateScope(nextScope: Scope) {
    const next = new URLSearchParams(params);
    for (const { key } of filters) next.set(key, nextScope[key]);
    if (next.toString() !== params.toString()) setParams(next);
  }

  function updateLayout(layout: ReportLayout) {
    const next = new URLSearchParams(params);
    next.set('fields', layout.fields.join(','));
    next.set('sort', layout.sort);
    next.set('direction', layout.direction);
    if (next.toString() !== params.toString()) setParams(next);
  }

  return (
    <div className="min-w-0 space-y-4 sm:space-y-6">
      <div className="flex min-w-0 items-center justify-between gap-3 border-b border-line pb-3">
        <div className="min-w-0">
          <p className="break-words text-sm font-semibold text-fg">{data?.buildingName || building}</p>
          <p className="mt-0.5 text-xs leading-relaxed text-muted">{energy} · {scenario}{custom && scope.template !== 'auto' && ` · ${template}模板`}</p>
        </div>
        <EnergyFilterDrawer<FilterKey> title="筛选报表范围" fields={filters} applied={scope} onApply={updateScope}
          normalize={normalizeScope} disabled={(key, draft) => key === 'building' && draft.scenario === 'display-stress'} />
      </div>

      <section className="min-w-0 overflow-hidden rounded-xl border border-line bg-surface" aria-labelledby="energy-industry-report-preview">
        <div className="flex flex-wrap items-start justify-between gap-3 px-4 py-4 sm:px-5">
          <div className="min-w-0">
            <h2 id="energy-industry-report-preview" className="text-base font-semibold">报表预览</h2>
            {data && <p className="mt-1 text-xs leading-5 text-muted"><EnergyInterval window={data} /></p>}
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {custom && <ReportLayoutDrawer data={data} onApply={updateLayout} />}
            {hasRows && data.csv && <EnergyReportDownload data={data} />}
          </div>
        </div>
        <div className="flex items-start gap-2 border-y border-line bg-warning-soft px-4 py-2.5 text-xs leading-5 text-warning sm:px-5">
          <TriangleAlert className="mt-0.5 size-4 shrink-0" aria-hidden />
          <p><strong className="font-semibold">{data?.notice || demoNotice}</strong>。{custom ? ' 此处仅提供合成报表预览和下载。' : ' 内部目标与外部标准分别列示。'}</p>
        </div>
        {data ? (
          <>
            <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 px-4 py-1.5 sm:px-5">
              <p className="text-xs leading-5 text-muted">
                {hasRows && <>共 {data.rows.length} 条{dataSort && ` · ${dataSort}${data.direction === 'asc' ? '升序' : '降序'}`}<span className="hidden sm:inline"> · 可横向查看全部字段</span></>}
              </p>
              <EnergyReportEvidence data={data}>
                {data.metricRuleVersion && <p className="break-all text-xs text-muted">指标计算版本 {data.metricRuleVersion}</p>}
              </EnergyReportEvidence>
            </div>
            {hasRows ? <ReportRows data={data} /> : (
              <EmptyState title={data.statusMessage || '此范围暂无报表明细'}
                hint={data.status === 'no_applicable_template' ? '请选择适用于当前建筑的行业模板，或切换建筑重新预览。' : '请调整建筑、能源或样本情景。'} />
            )}
            <footer className="border-t border-line bg-surface-2/60 px-4 py-3 text-xs leading-5 text-muted sm:px-5" aria-label="报表快照页脚">
              {hasRows && <p>预览与 CSV 使用同一快照。— 表示暂无值。</p>}
              <dl className={cn('grid grid-cols-1 gap-x-6 gap-y-1.5 sm:grid-cols-2 lg:grid-cols-4', hasRows && 'mt-2')}>
                <div className="min-w-0">
                  <dt className="text-muted">生成时间</dt>
                  <dd className="min-w-0 break-words text-fg tabular">{energyTime(data.generatedAt, data.timezone)}</dd>
                </div>
                <div className="min-w-0">
                  <dt className="text-muted">数据截止</dt>
                  <dd className="min-w-0 break-words text-fg tabular">{energyTime(data.asOf, data.timezone)}</dd>
                </div>
                <div className="min-w-0">
                  <dt className="text-muted">统计时区</dt>
                  <dd className="min-w-0 break-words text-fg">{data.timezone}</dd>
                </div>
                <div className="min-w-0">
                  <dt className="text-muted">口径版本</dt>
                  <dd className="min-w-0 break-words text-fg">{data.ruleVersion} · {data.metricRuleVersion}</dd>
                </div>
                <div className="min-w-0">
                  <dt className="text-muted">来源点位</dt>
                  <dd className="min-w-0 break-words text-fg tabular">{data.sources?.length ?? 0} 个</dd>
                </div>
                <div className="min-w-0">
                  <dt className="text-muted">数据质量与缺失率</dt>
                  <dd className="min-w-0 break-words text-fg">{hasRows ? fieldValue(data, data.rows[0], 'quality') : '无适用指标明细'}</dd>
                </div>
                <div className="min-w-0">
                  <dt className="text-muted">报表编号</dt>
                  <dd className="min-w-0 break-all text-fg">{data.reportId}</dd>
                </div>
                <div className="min-w-0">
                  <dt className="text-muted">操作人</dt>
                  <dd className="min-w-0 break-words text-fg">{data.operator}</dd>
                </div>
              </dl>
            </footer>
          </>
        ) : error ? (
          <div role="alert"><ErrorState message={error} onRetry={retry} /></div>
        ) : (
          <div role="status" className="space-y-3 p-4 sm:p-5">
            <span className="sr-only">正在加载报表</span>
            <Skeleton className="h-10" /><Skeleton className="h-28" />
          </div>
        )}
      </section>
    </div>
  );
}
