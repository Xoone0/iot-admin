import { useEffect, useState } from 'react';
import { Activity, ChevronRight, FileSearch } from 'lucide-react';
import { useSearchParams } from 'react-router-dom';
import type { Envelope } from '../api/types';
import { Drawer } from '../components/Drawer';
import { ErrorState, Skeleton } from '../components/States';
import { cn } from '../lib/cn';
import { EnergyFilterDrawer, energyFilterValues } from './BuildingEnergyWorkspace';

type Usage = number | null;
interface Coverage { expected: number; valid: number; missing: number; suspect: number; rate: number | null }
interface Value { usage: Usage; coverage: Coverage }
interface Category extends Value { id: string; name: string; children?: Category[] }
interface Share { id: string; name: string; usage: Usage; share: number | null }
interface Day { from: string; total: Value; classified: Usage; unclassified: Usage }
interface Source {
  name: string;
  source: string;
  mappingGeneration: number;
  profileVersion: string;
  bindingVersion: string;
  categoryBindingVersion?: string;
  observations: number;
  usage: Usage;
  valid: number;
  missing: number;
  suspect: number;
  breakdown: { name: string; usage: Usage }[];
}
interface Report {
  sample: true;
  unit: string;
  timezone: string;
  from: string;
  to: string;
  asOf: string;
  total: Value;
  classified: Usage;
  unclassified: Value;
  categories: Category[];
  composition: Share[];
  trend: Day[];
  sources: Source[];
  ruleVersion: string;
  classificationRuleVersion: string;
  catalogVersion: string;
  topologyVersion: string;
  topologySource: string;
}

const filters = [
  { key: 'building', label: '建筑', options: [['office-a', '办公楼 A'], ['office-b', '办公楼 B']] },
  { key: 'energy', label: '能源', options: [['electricity', '用电'], ['water', '用水']] },
  { key: 'period', label: '时间范围', options: [['7', '样本末 7 天'], ['14', '样本末 14 天'], ['30', '样本末 30 天'], ['day', '10 月 15 日（对账样本）']] },
] as const;
const panel = 'min-w-0 rounded-xl border border-line bg-surface shadow-[var(--shadow)]';
const integer = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 });
const percent = new Intl.NumberFormat('zh-CN', { style: 'percent', maximumFractionDigits: 1 });
const amount = (usage: Usage) => usage === null ? '—' : integer.format(usage);
const rate = (share: number | null) => share === null ? '—' : percent.format(share);
const colors = ['bg-primary', 'bg-positive', 'bg-neutral/40'];

function quality(value: Value) {
  const c = value.coverage;
  if (c.expected === 0) return '无计量来源';
  if (value.usage === null) return c.suspect > 0 ? '可疑值' : '缺测';
  if (c.valid < c.expected) return '部分覆盖';
  return value.usage === 0 ? '有效零' : '完整';
}

function Metric({ label, usage, unit, hint }: { label: string; usage: Usage; unit: string; hint: string }) {
  return (
    <div className={cn(panel, 'px-4 py-3.5')}>
      <dt className="text-xs font-medium text-muted">{label}</dt>
      <dd className="mt-1.5 text-xl font-semibold tabular sm:text-2xl">
        {amount(usage)}{usage === null ? null : <span className="ml-1.5 text-xs font-normal text-muted">{unit}</span>}
      </dd>
      <dd className="mt-1 text-xs text-muted">{hint}</dd>
    </div>
  );
}

function Composition({ data }: { data: Report }) {
  return (
    <section className={cn(panel, 'p-4')} aria-labelledby="f06-composition">
      <h2 id="f06-composition" className="text-sm font-semibold">用量构成</h2>
      <p className="mt-1 text-xs text-muted">顶层分类占总用量的比例</p>
      <div className="mt-5 flex h-3 overflow-hidden rounded-full bg-neutral-soft" aria-hidden>
        {data.composition.map((item, index) => (
          <span key={item.id} className={colors[index]} style={{ width: `${(item.share ?? 0) * 100}%` }} />
        ))}
      </div>
      <ul className="mt-4 divide-y divide-line">
        {data.composition.map((item, index) => (
          <li key={item.id} className="grid grid-cols-[1fr_auto_auto] items-center gap-3 py-2.5 text-xs">
            <span className="flex min-w-0 items-center gap-2"><span className={cn('size-2 shrink-0 rounded-sm', colors[index])} aria-hidden />{item.name}</span>
            <span className="text-muted tabular">{amount(item.usage)} {item.usage === null ? '' : data.unit}</span>
            <span className="min-w-10 text-right font-medium tabular">{rate(item.share)}</span>
          </li>
        ))}
      </ul>
    </section>
  );
}

function Trend({ data }: { data: Report }) {
  const date = new Intl.DateTimeFormat('zh-CN', { timeZone: data.timezone, month: 'numeric', day: 'numeric' });
  const dayLabel = (day: Day) => date.format(new Date(day.from));
  const peak = Math.max(1, ...data.trend.map((day) => day.total.usage ?? 0));
  return (
    <section className={cn(panel, 'p-4')} aria-labelledby="f06-trend">
      <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
        <h2 id="f06-trend" className="text-sm font-semibold">每日用量趋势</h2>
        <span className="text-xs text-muted">{data.unit} / 日</span>
      </div>
      <div className="mt-3 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-muted">
        <span className="flex items-center gap-1.5"><span className="size-2 rounded-sm bg-primary" aria-hidden />已分类</span>
        <span className="flex items-center gap-1.5"><span className="size-2 rounded-sm bg-neutral/40" aria-hidden />未分项</span>
        <span className="flex items-center gap-1.5"><span className="h-0.5 w-2 bg-positive" aria-hidden />有效零</span>
        <span className="flex items-center gap-1.5"><span className="size-2 rounded-sm border border-dashed border-muted" aria-hidden />缺测</span>
        <span className="flex items-center gap-1.5"><span className="size-2 rounded-sm bg-warning" aria-hidden />可疑值</span>
      </div>
      <div className="mt-3 grid grid-cols-[auto_1fr] gap-x-2" aria-hidden>
        <div className="flex h-28 flex-col justify-between pb-0.5 text-right text-[10px] text-muted tabular">
          <span>{integer.format(peak)}</span><span>{integer.format(peak / 2)}</span><span>0</span>
        </div>
        <div className="relative min-w-0">
          <div className="absolute inset-x-0 top-1 h-[calc(7rem-0.25rem)] border-y border-line before:absolute before:inset-x-0 before:top-1/2 before:border-t before:border-line" />
          <div className="relative flex h-28 items-end gap-1 px-1 sm:gap-2">
            {data.trend.map((day) => (
              <div key={day.from} className="flex h-full min-w-0 flex-1 flex-col items-center justify-end"
                title={`${dayLabel(day)} · ${amount(day.total.usage)} ${data.unit} · ${quality(day.total)}`}>
                {day.total.usage ? (
                  <>
                    <div className="w-full max-w-12 rounded-t-sm bg-neutral/40" style={{ height: `${(day.unclassified ?? 0) / peak * 95}%` }} />
                    <div className="w-full max-w-12 rounded-t-sm bg-primary" style={{ height: `${(day.classified ?? 0) / peak * 95}%` }} />
                  </>
                ) : (
                  <div className={cn('w-full max-w-12 rounded-sm', day.total.usage === 0 ? 'h-0.5 bg-positive'
                    : day.total.coverage.suspect > 0 ? 'h-2.5 bg-warning' : 'h-2.5 border border-dashed border-muted')} />
                )}
              </div>
            ))}
          </div>
          <div className="mt-1 flex justify-between text-[10px] text-muted tabular">
            {data.trend.filter((_, index) => index === 0 || index === Math.floor(data.trend.length / 2) || index === data.trend.length - 1)
              .map((day) => <span key={day.from}>{dayLabel(day)}</span>)}
          </div>
        </div>
      </div>
      <details className="mt-2 text-xs">
        <summary className="w-fit cursor-pointer rounded py-1 text-primary">查看每日数据</summary>
        <table className="mt-1 w-full text-left">
          <caption className="sr-only">每日用量（{data.unit}）与质量</caption>
          <thead className="text-muted"><tr><th scope="col" className="py-1">日期</th><th scope="col" className="text-right">合计</th><th scope="col" className="text-right">已分类</th><th scope="col" className="text-right">未分项</th><th scope="col" className="text-right">质量</th></tr></thead>
          <tbody className="divide-y divide-line">
            {data.trend.map((day) => (
              <tr key={day.from}>
                <th scope="row" className="py-1.5 font-normal">{dayLabel(day)}</th>
                <td className="text-right tabular">{amount(day.total.usage)}</td>
                <td className="text-right tabular">{amount(day.classified)}</td>
                <td className="text-right tabular">{amount(day.unclassified)}</td>
                <td className="text-right text-muted">{quality(day.total)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </details>
    </section>
  );
}

function CategoryTable({ data }: { data: Report }) {
  const [collapsed, setCollapsed] = useState<string[]>([]);
  function rows(nodes: Category[], level = 0): React.ReactNode {
    return nodes.map((node) => {
      const expanded = !collapsed.includes(node.id);
      return (
        <CategoryBranch key={node.id} node={node} level={level} expanded={expanded}
          onToggle={() => setCollapsed((state) => expanded ? [...state, node.id] : state.filter((id) => id !== node.id))}>
          {expanded && node.children ? rows(node.children, level + 1) : null}
        </CategoryBranch>
      );
    });
  }
  return (
    <section className={cn(panel, 'overflow-hidden')} aria-labelledby="f06-table">
      <h2 id="f06-table" className="px-4 py-3.5 text-sm font-semibold">分类分项明细</h2>
      <table className="w-full table-fixed text-left text-sm">
        <thead className="border-y border-line bg-surface-2 text-xs text-muted">
          <tr><th scope="col" className="w-[45%] px-4 py-2.5 font-medium">分类 / 分项</th><th scope="col" className="px-2 py-2.5 text-right font-medium">用量（{data.unit}）</th><th scope="col" className="px-4 py-2.5 text-right font-medium">覆盖率</th></tr>
        </thead>
        <tbody className="divide-y divide-line">
          {rows(data.categories)}
          <CategoryBranch node={{ id: 'unclassified', name: '未分项', ...data.unclassified }} level={0} />
        </tbody>
      </table>
    </section>
  );
}

function CategoryBranch({ node, level, expanded, onToggle, children }: {
  node: Category; level: number; expanded?: boolean; onToggle?: () => void; children?: React.ReactNode;
}) {
  return (
    <>
      <tr>
        <th scope="row" className={cn('px-4 py-2 text-sm', level ? 'font-normal text-muted' : 'font-medium')}>
          <div className="flex min-h-8 items-center" style={{ paddingInlineStart: level * 15 }}>
            {node.children?.length ? (
              <button type="button" aria-expanded={expanded} onClick={onToggle} className="-ml-1 flex min-h-8 items-center gap-1 rounded px-1 text-left hover:bg-surface-2">
                <ChevronRight className={cn('size-3.5 shrink-0 text-muted', expanded && 'rotate-90')} aria-hidden />{node.name}
              </button>
            ) : <span>{node.name}</span>}
          </div>
        </th>
        <td className="px-2 py-2 text-right tabular">{amount(node.usage)}</td>
        <td className="px-4 py-2 text-right">
          <span className="tabular">{rate(node.coverage.rate)}</span>
          <span className="block text-[11px] text-muted">{quality(node)}</span>
        </td>
      </tr>
      {children}
    </>
  );
}

function SourceDrawer({ data }: { data: Report }) {
  const [open, setOpen] = useState(false);
  const timestamp = new Intl.DateTimeFormat('zh-CN', { timeZone: data.timezone, dateStyle: 'short', timeStyle: 'short', hourCycle: 'h23' });
  return (
    <Drawer open={open} onOpenChange={setOpen} title="计量来源与计算版本"
      description="合成数据 · 当前建筑、能源与统计窗口 · 只读"
      trigger={<button type="button"
        className="inline-flex min-h-9 items-center gap-2 rounded-md border border-line bg-surface px-3 text-sm font-medium hover:bg-surface-2">
        <FileSearch className="size-4 text-primary" aria-hidden />计量来源
      </button>}>
        <dl className={cn(panel, 'grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 p-4 text-xs')}>
          <dt className="text-muted">计量规则</dt><dd>{data.ruleVersion}</dd>
          <dt className="text-muted">分类计算</dt><dd>{data.classificationRuleVersion}</dd>
          <dt className="text-muted">分类目录</dt><dd>{data.catalogVersion}</dd>
          <dt className="text-muted">物理拓扑</dt><dd>{data.topologyVersion}</dd>
          <dt className="text-muted">证据出处</dt><dd className="break-all">{data.topologySource}</dd>
          <dt className="text-muted">数据截止</dt><dd>{timestamp.format(new Date(data.asOf))}</dd>
        </dl>
        <h3 className="mb-2 mt-5 text-sm font-semibold">纳入统计的样本通道</h3>
        <ul className="space-y-2">
          {data.sources.map((source) => (
            <li key={source.source} className={cn(panel, 'p-3')}>
              <div className="flex flex-wrap justify-between gap-x-2 text-sm font-medium">
                <span>{source.name}</span><span className="tabular">{amount(source.usage)} {source.usage === null ? '' : data.unit}</span>
              </div>
              <p className="mt-1 break-all text-xs text-muted">{source.source}</p>
              <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
                <dt className="text-muted">映射代次</dt><dd>{source.mappingGeneration}</dd>
                <dt className="text-muted">计量口径</dt><dd>{source.profileVersion}</dd>
                <dt className="text-muted">建筑归属</dt><dd>{source.bindingVersion}</dd>
                <dt className="text-muted">分类归属</dt><dd>{source.categoryBindingVersion ?? '未分项'}</dd>
                <dt className="text-muted">有效区间</dt><dd>{source.valid} / {source.observations}</dd>
                <dt className="text-muted">数据质量</dt><dd>缺测 {source.missing} · 可疑 {source.suspect}</dd>
              </dl>
              <ul className="mt-2 space-y-1 border-t border-line pt-2 text-xs">
                {source.breakdown.map((item) => (
                  <li key={item.name} className="flex justify-between gap-2">
                    <span className="text-muted">{item.name}</span>
                    <span className="tabular">{amount(item.usage)} {item.usage === null ? '' : data.unit}</span>
                  </li>
                ))}
              </ul>
            </li>
          ))}
        </ul>
    </Drawer>
  );
}

export default function BuildingEnergyClassificationPage() {
  const [params, setParams] = useSearchParams();
  const selected = energyFilterValues(filters, params);
  const query = new URLSearchParams(selected).toString();
  const [attempt, setAttempt] = useState(0);
  const [response, setResponse] = useState<{ key: string; data?: Report; error?: string }>();
  const requestKey = `${query}|${attempt}`;
  const current = response?.key === requestKey ? response : undefined;
  const data = current?.data;

  useEffect(() => {
    const controller = new AbortController();
    fetch(`/admin/v1/analytics/building-energy/preview/classification?${query}`, { signal: controller.signal })
      .then(async (res) => {
        const payload = (await res.json()) as Envelope<Report>;
        if (!res.ok || payload.code !== 0 || !payload.data?.sample) throw new Error(payload.message || '样本服务不可用');
        if (!controller.signal.aborted) setResponse({ key: requestKey, data: payload.data });
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) setResponse({ key: requestKey, error: error instanceof Error ? error.message : '样本加载失败' });
      });
    return () => controller.abort();
  }, [query, requestKey]);

  const date = data && new Intl.DateTimeFormat('zh-CN', { timeZone: data.timezone, dateStyle: 'medium' });
  const labels = filters.map(({ key, options }) => options.find(([value]) => value === selected[key])?.[1]);
  return (
    <div className="space-y-4">
      <header className="flex flex-wrap items-center justify-between gap-2 border-b border-line pb-3">
        <div className="min-w-0">
          <p className="text-sm font-semibold">{labels[0]} · {labels[1]}</p>
          <p className="mt-0.5 text-xs leading-5 text-muted">
            {data && date ? `${date.format(new Date(data.from))} 至 ${date.format(new Date(data.to))} 00:00（项目时间）` : labels[2]}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {data ? <SourceDrawer key={requestKey} data={data} /> : null}
          <EnergyFilterDrawer title="筛选分类分项" fields={filters} applied={selected} onApply={(next) => setParams(next)} />
        </div>
      </header>

      {data ? (
        <div key={requestKey} className="space-y-4">
          <dl className="grid grid-cols-2 gap-3 lg:grid-cols-4">
            <Metric label="已统计总用量" usage={data.total.usage} unit={data.unit} hint={quality(data.total)} />
            <Metric label="已分类用量" usage={data.classified} unit={data.unit} hint="分项汇总" />
            <Metric label="未分项用量" usage={data.unclassified.usage} unit={data.unit} hint="尚未归入分类" />
            <div className={cn(panel, 'px-4 py-3.5')}>
              <dt className="flex items-center gap-1.5 text-xs font-medium text-muted"><Activity className="size-3.5 text-primary" aria-hidden />数据覆盖率</dt>
              <dd className="mt-1.5 text-xl font-semibold tabular sm:text-2xl">{rate(data.total.coverage.rate)}</dd>
              <dd className="mt-1 text-xs text-muted">
                缺测 {integer.format(data.total.coverage.missing)} · 可疑 {integer.format(data.total.coverage.suspect)} <span className="whitespace-nowrap">表小时</span>
              </dd>
            </div>
          </dl>
          <div className="grid gap-3 md:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
            <Composition data={data} />
            <Trend data={data} />
          </div>
          <CategoryTable data={data} />
        </div>
      ) : null}
      {current?.error ? <div className={panel}><ErrorState message={current.error} onRetry={() => setAttempt((value) => value + 1)} /></div> : null}
      {!current ? <div role="status" aria-label="加载分类分项统计"><Skeleton className="h-52" /></div> : null}
    </div>
  );
}
