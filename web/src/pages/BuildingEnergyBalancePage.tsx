import { useState } from 'react';
import { CornerDownRight, FileSearch, Info } from 'lucide-react';
import { useSearchParams } from 'react-router-dom';
import { Drawer } from '../components/Drawer';
import { ErrorState, Skeleton } from '../components/States';
import { cn } from '../lib/cn';
import { EnergyInterval, energyPercent, energyTime, useEnergyPreview } from './BuildingEnergyResults';
import type { EnergyValue, EnergyWindow } from './BuildingEnergyResults';
import { EnergyFilterDrawer, EnergySectionHeading, energyFilterValues, energyNumber } from './BuildingEnergyWorkspace';

type BalanceState = 'balanced' | 'unallocated' | 'negative_difference' | 'missing_data' | 'partial'
  | 'suspect_data' | 'meter_change' | 'time_mismatch';
type MeterState = 'complete' | 'missing_data' | 'partial' | 'suspect_data' | 'meter_change' | 'time_mismatch';

interface BalanceMeter extends EnergyValue, Omit<EnergyWindow, 'timezone'> {
  id: string;
  name: string;
  channel: string;
  regions: string[];
  profileVersion: string;
  mappingGeneration: number;
  bindingVersion: string;
  meterRuleVersion: string;
  state: MeterState;
  issues: { reason: string; from: string; to: string }[] | null;
}

interface BalanceReport extends EnergyWindow {
  sample: true;
  unit: string;
  ruleVersion: string;
  topology: { version: string; source: string; from: string; to: string; unmeteredZones: string[] };
  state: BalanceState;
  parent: BalanceMeter;
  children: BalanceMeter[];
  childTotal: number | null;
  difference: number | null;
  differenceRate: number | null;
}

const filters = [
  { key: 'building', label: '建筑', options: [['office-a', '办公楼 A'], ['office-b', '办公楼 B']] },
  { key: 'energy', label: '能源', options: [['electricity', '用电'], ['water', '用水']] },
  { key: 'period', label: '时间', options: [['day', '自然日'], ['7', '样本末 7 天'], ['14', '样本末 14 天'], ['30', '样本末 30 天']] },
  {
    key: 'scenario', label: '样本场景', options: [
      ['standard', '标准样本'], ['negative', '负差额'], ['missing', '缺测'], ['suspect', '可疑读数'],
      ['replacement', '换表'], ['time-mismatch', '统计时段不一致'],
    ],
  },
] as const;

const states: Record<BalanceState, { label: string; detail: string }> = {
  balanced: { label: '差额为零', detail: '同窗总表用量与分表合计一致。' },
  unallocated: { label: '存在未分配差额', detail: '差额按总表用量减去已纳入的分表合计计算，成因待核查。' },
  negative_difference: { label: '总分表不平衡', detail: '分表合计超过总表。可核对计量范围、倍率和读数。' },
  missing_data: { label: '计量缺测', detail: '至少一个计量点缺少有效记录，平衡差额暂不可用。' },
  partial: { label: '部分覆盖', detail: '至少一个计量点的有效记录未覆盖整个统计窗口，平衡差额暂不可用。' },
  suspect_data: { label: '存在可疑读数', detail: '至少一个区间需要核查，平衡差额暂不可用。' },
  meter_change: { label: '统计期内换表', detail: '仪表更换处存在计量断段，平衡差额暂不可用。' },
  time_mismatch: { label: '统计时段不一致', detail: '总表与分表的统计窗口或数据截点未对齐，平衡差额暂不可用。' },
};

const meterStates: Record<MeterState, string> = {
  complete: '覆盖完整',
  missing_data: '缺测',
  partial: '部分覆盖',
  suspect_data: '读数可疑',
  meter_change: '换表断段',
  time_mismatch: '时间未对齐',
};

const issueLabels: Record<string, string> = {
  missing_sample: '缺少读数',
  conflicting_sample: '同一时刻读数冲突',
  source_quality: '读数质量异常',
  invalid_value: '读数无效',
  epoch_change: '换表断段',
  negative_jump: '累计读数反向跳变',
  unconfirmed_rollover: '表码翻转未经确认',
  time_mismatch: '统计时段不一致',
};

const panel = 'min-w-0 rounded-xl border border-line bg-surface';

function Quantity({ value, unit }: { value: number | null; unit: string }) {
  return (
    <span className="tabular">
      {energyNumber(value)}{value !== null && <span className="ml-1.5 text-xs font-normal text-muted">{unit}</span>}
    </span>
  );
}

function ResultSummary({ data }: { data: BalanceReport }) {
  const measures = [
    { title: '总表用量', usage: data.parent.usage, detail: '独立计量边界' },
    { title: '分表合计', usage: data.childTotal, detail: '仅汇总已纳入的分表' },
    {
      title: '未分配/待核查差额',
      usage: data.difference,
      detail: data.differenceRate == null ? '差额率 —' : `差额率 ${energyPercent(data.differenceRate)}`,
    },
  ];
  const state = states[data.state];
  const needsReview = data.state !== 'balanced' && data.state !== 'unallocated';

  return (
    <section className={cn(panel, 'overflow-hidden')} aria-label="总分表平衡结果">
      <dl className="grid grid-cols-2 sm:grid-cols-3">
        {measures.map(({ title, usage, detail }, index) => (
          <div key={title} className={cn('min-w-0 px-3 py-4 sm:px-5',
            index === 1 && 'border-l border-line', index === 2 && 'col-span-2 border-t border-line sm:col-span-1 sm:border-l sm:border-t-0')}>
            <dt className="text-sm font-medium text-muted">{title}</dt>
            <dd className="mt-1.5 break-words text-2xl font-semibold sm:text-3xl">
              <Quantity value={usage} unit={data.unit} />
            </dd>
            <dd className="mt-1.5 text-xs leading-5 text-muted">{detail}</dd>
          </div>
        ))}
      </dl>
      <p className="flex items-start gap-2 border-t border-line px-3 py-3 text-sm leading-relaxed sm:px-5">
        <Info aria-hidden className={cn('mt-0.5 size-4 shrink-0', needsReview ? 'text-warning' : 'text-muted')} />
        <span><strong className={cn('font-medium', needsReview && 'text-warning')}>{state.label}</strong>
          <span className="ml-2 text-muted">{state.detail}</span>
        </span>
      </p>
    </section>
  );
}

function BalanceChart({ parent, childTotal, unit }: { parent: number; childTotal: number; unit: string }) {
  const maximum = Math.max(parent, childTotal);
  const rows = [
    { label: '总表', value: parent, bar: 'bg-primary' },
    { label: '分表合计', value: childTotal, bar: 'bg-primary/50' },
  ];

  return (
    <figure className="mt-4" aria-label={`用量对比，总表 ${energyNumber(parent)} ${unit}，分表合计 ${energyNumber(childTotal)} ${unit}`}>
      <div className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-2 gap-y-3 text-sm sm:gap-x-4" aria-hidden>
        {rows.map(({ label, value, bar }) => (
          <div key={label} className="contents">
            <span className="font-medium">{label}</span>
            <div className="relative h-5 border-l border-line-strong bg-surface-2">
              {value === 0
                ? <span className={cn('absolute inset-y-0 left-0 w-px', bar)} />
                : <span className={cn('block h-full rounded-r', bar)} style={{ width: `${value / maximum * 100}%` }} />}
            </div>
            <span className="whitespace-nowrap font-medium"><Quantity value={value} unit={unit} /></span>
          </div>
        ))}
        <div className="col-start-2 -mt-1 flex justify-between gap-2 border-t border-line pt-1 text-xs text-muted tabular">
          <span>0</span>{maximum > 0 && <span>{energyNumber(maximum)} {unit}</span>}
        </div>
      </div>
    </figure>
  );
}

function SourceDrawer({ data }: { data: BalanceReport }) {
  const [open, setOpen] = useState(false);
  return (
    <Drawer
      open={open}
      onOpenChange={setOpen}
      title="计量来源与拓扑"
      description="合成数据 · 范围、计量版本和质量记录。"
      trigger={
        <button type="button" className="inline-flex min-h-10 items-center gap-1.5 rounded-md px-2 text-sm font-medium text-primary hover:bg-primary-soft">
          <FileSearch className="size-4" aria-hidden />查看计量来源
        </button>
      }
    >
      <section aria-label="拓扑与区间" className="space-y-2 text-sm">
        <h3 className="font-semibold">物理计量边界</h3>
        <p className="text-xs leading-5 text-muted"><EnergyInterval window={data} /></p>
        <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-2 break-words text-xs">
          <dt className="text-muted">拓扑依据</dt><dd>{data.topology.source}</dd>
          <dt className="text-muted">拓扑版本</dt><dd>{data.topology.version}</dd>
          <dt className="text-muted">拓扑有效期</dt><dd>{energyTime(data.topology.from, data.timezone)} 至 {energyTime(data.topology.to, data.timezone)}（不含结束）</dd>
          <dt className="text-muted">统计截点</dt><dd>{energyTime(data.asOf, data.timezone)}（{data.timezone}）</dd>
          <dt className="text-muted">未布分表范围</dt><dd>{data.topology.unmeteredZones.join('、') || '无'}</dd>
          <dt className="text-muted">计算版本</dt><dd>{data.ruleVersion}</dd>
        </dl>
      </section>
      <div className="mt-5 space-y-3 border-t border-line pt-4">
        {[data.parent, ...data.children].map((meter) => (
          <section key={meter.id} className="rounded-lg border border-line px-3 py-3 text-sm">
            <h3 className="font-semibold">{meter.name}</h3>
            <p className="mt-1.5 text-xs leading-5 text-muted"><EnergyInterval window={{ ...meter, timezone: data.timezone }} /></p>
            <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 break-words text-xs">
              <dt className="text-muted">计量通道</dt><dd>{meter.channel}</dd>
              <dt className="text-muted">物理范围</dt><dd>{meter.regions.join('、')}</dd>
              <dt className="text-muted">用量</dt><dd><Quantity value={meter.usage} unit={data.unit} /></dd>
              <dt className="text-muted">区间覆盖</dt>
              <dd>{energyPercent(meter.coverage.rate)}
                {meter.coverage.expected > 0 && `，有效 ${meter.coverage.valid}/${meter.coverage.expected}`} · {meterStates[meter.state]}
              </dd>
              <dt className="text-muted">数据质量</dt>
              <dd>{meter.state === 'time_mismatch' ? '未统计（时段未对齐）' : `缺测 ${meter.coverage.missing} · 可疑 ${meter.coverage.suspect}`}</dd>
              <dt className="text-muted">可见截点</dt><dd>{energyTime(meter.asOf, data.timezone)}</dd>
              <dt className="text-muted">计量与归属</dt><dd>{meter.profileVersion} · {meter.bindingVersion}（代次 {meter.mappingGeneration}）</dd>
              <dt className="text-muted">仪表计算</dt><dd>{meter.meterRuleVersion}</dd>
            </dl>
            {meter.issues?.length ? (
              <ul className="mt-3 space-y-1.5 border-t border-line pt-2 text-xs leading-5 text-warning">
                {meter.issues.map((issue, index) => (
                  <li key={`${issue.reason}-${issue.from}-${index}`}>
                    {issueLabels[issue.reason] ?? '需核查的计量质量记录'} · {energyTime(issue.from, data.timezone)} 至 {energyTime(issue.to, data.timezone)}
                  </li>
                ))}
              </ul>
            ) : null}
          </section>
        ))}
      </div>
    </Drawer>
  );
}

function BalanceDetails({ data }: { data: BalanceReport }) {
  return (
    <section className={cn(panel, 'p-3 sm:p-5')} aria-label="总分表与计量范围">
      <EnergySectionHeading title="计量范围" action={<SourceDrawer data={data} />} />
      <p className="text-xs leading-5 text-muted"><EnergyInterval window={data} /></p>
      <p className="text-xs leading-5 text-muted">本页使用独立的合成计量样本。用量和差额仅对应下列范围，与分类分项、多维统计和趋势页的样本口径不同。</p>
      {data.difference !== null && data.parent.usage !== null && data.childTotal !== null && (
        <BalanceChart parent={data.parent.usage} childTotal={data.childTotal} unit={data.unit} />
      )}
      <table className="mt-4 w-full table-fixed border-t border-line text-left text-sm tabular">
        <caption className="sr-only">总表与分表用量和覆盖率</caption>
        <thead className="border-b border-line text-xs text-muted">
          <tr>
            <th scope="col" className="w-[44%] py-2.5 pr-2 font-medium sm:w-1/2">计量表与物理边界</th>
            <th scope="col" className="w-[23%] px-1 py-2.5 text-right font-medium sm:w-1/4">用量</th>
            <th scope="col" className="py-2.5 pl-1 text-right font-medium">覆盖率</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-line">
          {[data.parent, ...data.children].map((meter, index) => (
            <tr key={meter.id} className="align-top">
              <th scope="row" className="py-2.5 pr-2 font-medium">
                <div className="flex items-start gap-1.5">
                  {index > 0 && <CornerDownRight aria-hidden className="mt-0.5 size-3.5 shrink-0 text-muted" />}
                  <div>
                    {meter.name}
                    <span className="mt-0.5 block break-words text-xs font-normal leading-5 text-muted">{meter.regions.join('、')}</span>
                  </div>
                </div>
              </th>
              <td className="break-words px-1 py-2.5 text-right font-medium"><Quantity value={meter.usage} unit={data.unit} /></td>
              <td className="py-2.5 pl-1 text-right">
                {energyPercent(meter.coverage.rate)}
                {meter.coverage.expected > 0 && (
                  <span className="mt-0.5 block text-xs leading-5 text-muted">有效 {meter.coverage.valid}/{meter.coverage.expected}</span>
                )}
                {meter.state !== 'complete' && <span className="block text-xs leading-5 text-warning">{meterStates[meter.state]}</span>}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}

export default function BuildingEnergyBalancePage() {
  const [params, setParams] = useSearchParams();
  const selection = energyFilterValues(filters, params);
  const query = new URLSearchParams(selection).toString();
  const { data, error, retry } = useEnergyPreview<BalanceReport>('balance', query);
  const [building, energy, period, scenario] = filters.map(({ key, options }) =>
    options.find(([value]) => value === selection[key])?.[1]);

  return (
    <div className="min-w-0 space-y-4">
      <div className="flex min-w-0 items-center justify-between gap-3 border-b border-line pb-3">
        <div className="min-w-0">
          <p className="text-sm font-semibold">{building} · {energy}</p>
          <p className="mt-0.5 text-xs leading-5 text-muted">{period} · {scenario}</p>
        </div>
        <EnergyFilterDrawer title="筛选总分表平衡" fields={filters} applied={selection} onApply={(next) => setParams(next)} />
      </div>
      {data ? (
        <>
          <ResultSummary data={data} />
          <BalanceDetails key={query} data={data} />
        </>
      ) : error ? (
        <div role="alert" className={panel}><ErrorState message={error} onRetry={retry} /></div>
      ) : (
        <div role="status" aria-label="加载总分表平衡"><Skeleton className="h-48" /></div>
      )}
    </div>
  );
}
