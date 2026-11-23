import { ArrowUpRight } from 'lucide-react';
import type { ReactNode } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { ENERGY_ANOMALIES_PATH, ENERGY_DIAGNOSIS_PATH, ENERGY_OPPORTUNITIES_PATH } from '../components/BuildingEnergyNavigation';
import { StatusBadge } from '../components/FieldValue';
import { EmptyState, ErrorState, Skeleton } from '../components/States';
import { EnergyEvidence, EnergyInterval, energyPercent, energyTime, useEnergyPreview } from './BuildingEnergyResults';
import type { EnergyCoverage, EnergyEvidenceData, EnergySource, EnergyWindow } from './BuildingEnergyResults';
import { EnergyFilterDrawer, EnergySectionHeading, energyFilterValues, energyNumber } from './BuildingEnergyWorkspace';

const filters = [
  { key: 'building', label: '建筑', options: [['office-a', '办公楼 A'], ['office-b', '办公楼 B']] },
  { key: 'energy', label: '能源', options: [['electricity', '用电'], ['water', '用水']] },
  {
    key: 'scenario', label: '合成样本场景', options: [
      ['standard', '标准样本'], ['normal', '无异常'], ['missing', '缺测'], ['suspect', '可疑值'],
      ['partial', '部分覆盖'], ['zero', '实测零值'], ['incomparable', '周期不可比'],
      ['baseline-zero', '基线为零'], ['no-rule', '无适用规则'],
    ],
  },
] as const;

const findings = {
  usage_candidate: '异常候选',
  insufficient_evidence: '证据不足',
  no_candidate: '未发现用量异常候选',
} as const;

const reasons: Record<string, string> = {
  missing_rule: '当前范围缺少已配置的判定规则。',
  rule_not_applicable: '当前周期未落在规则有效期或适用范围内。',
  missing_usage: '当前周期缺少可用用量。',
  partial_coverage: '当前计量覆盖不完整。',
  suspect_readings: '当前周期含可疑表小时。',
  insufficient_history: '符合条件的历史样本数量不足。',
  incompatible_period: '观测与历史周期不可比。',
  incompatible_scope: '观测和历史样本的统计范围或口径不同。',
  invalid_baseline_quality: '历史样本的计量覆盖或质量不足。',
  zero_baseline: '历史基线用量为零，无法计算相对偏差。',
};

const clueLabels = {
  baseline_increase: '核查用量上升',
  high_usage_observation: '核查区域高用量',
} as const;

const panel = 'min-w-0 rounded-xl border border-line bg-surface';
const linkStyle = 'inline-flex min-h-11 items-center gap-1.5 rounded-md px-2 text-sm font-medium text-primary hover:bg-primary-soft focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary';

type MeasurementSource = Omit<EnergySource, 'usage'> & { usage?: number | null };

interface Observation {
  from: string;
  to: string;
  usage: number | null;
  coverage: EnergyCoverage;
  meterRuleVersion: string;
  classificationRuleVersion: string;
  catalogVersion: string;
  topologyVersion: string;
  sources: MeasurementSource[] | null;
}

interface DetectionRule {
  id: string;
  version: string;
  source: string;
  from: string;
  to: string;
  minHistory: number;
  minChangeRate: number;
  minIncrease: number;
}

interface Opportunity {
  kind: keyof typeof clueLabels;
  status: 'pending_manual_review';
  object: string;
  name: string;
  usage: number | null;
  baseline: number | null;
  evidence: string;
  comparablePeers: number;
  limitations: string[];
  confirm: string[];
  sources: MeasurementSource[] | null;
  measurement?: Pick<EnergyEvidenceData, 'ruleVersion' | 'meterRuleVersion' | 'classificationRuleVersion' | 'catalogVersion' | 'topologyVersion'>;
}

interface AnomalyReport extends Pick<EnergyWindow, 'timezone' | 'asOf'> {
  sample: true;
  project: string;
  unit: string;
  current: Observation;
  history: Observation[];
  rule: DetectionRule | null;
  finding: {
    status: keyof typeof findings | 'metering_anomaly';
    usageStatus: keyof typeof findings;
    reason: string;
    baselineCount: number;
    baseline: number | null;
    delta: number | null;
    changeRate: number | null;
  };
  opportunities: Opportunity[];
}

function Quantity({ value, unit, signed = false }: { value: number | null; unit: string; signed?: boolean }) {
  return (
    <span className="tabular">
      {signed && value !== null && value > 0 ? '+' : ''}{energyNumber(value)}
      {value !== null && <span className="ml-1 whitespace-nowrap text-sm font-normal text-muted">{unit}</span>}
    </span>
  );
}

function Measure({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="text-sm text-muted">{label}</dt>
      <dd className="mt-1 break-words text-2xl font-semibold tracking-tight tabular sm:text-[28px]">{children}</dd>
    </div>
  );
}

function RuleDetails({ rule, data }: { rule: DetectionRule | null; data: AnomalyReport }) {
  return (
    <section aria-label="判定规则">
      <h3 className="font-semibold">判定规则</h3>
      {rule ? (
        <>
          <p className="mt-1.5 leading-6 text-muted">基线采用项目当地同一星期几的可比完整日用量中位数。增量和增幅均达到阈值时形成候选。</p>
          <dl className="mt-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-muted">
            <dt>规则</dt><dd className="break-words">{rule.id} · {rule.version}</dd>
            <dt>生效区间</dt><dd>{energyTime(rule.from, data.timezone)} 至 {energyTime(rule.to, data.timezone)}（不含结束）</dd>
            <dt>历史样本下限</dt><dd>{energyNumber(rule.minHistory)} 个可比周期</dd>
            <dt>用量增幅阈值</dt><dd>{energyPercent(rule.minChangeRate)}</dd>
            <dt>用量增量阈值</dt><dd><Quantity value={rule.minIncrease} unit={data.unit} /></dd>
            <dt>规则来源</dt><dd className="break-words">{rule.source}</dd>
            <dt>项目</dt><dd className="break-words">{data.project}</dd>
          </dl>
        </>
      ) : <p className="mt-1.5 text-muted">当前范围无适用规则。</p>}
    </section>
  );
}

function ReviewEvidence({ data, clue }: { data: AnomalyReport; clue?: Opportunity }) {
  const period = data.current;
  const isRegional = clue?.kind === 'high_usage_observation';
  const measurement = isRegional ? clue.measurement : period;
  const lineage = {
    from: period.from,
    to: period.to,
    timezone: data.timezone,
    asOf: data.asOf,
    unit: data.unit,
    ruleVersion: isRegional ? clue.measurement?.ruleVersion ?? '未提供' : data.rule?.version ?? '无适用规则',
    meterRuleVersion: measurement?.meterRuleVersion ?? '未提供',
    classificationRuleVersion: measurement?.classificationRuleVersion ?? '未提供',
    catalogVersion: measurement?.catalogVersion ?? '未提供',
    topologyVersion: measurement?.topologyVersion ?? '未提供',
    sources: (clue ? clue.sources : period.sources)?.map((source) => ({ ...source, usage: source.usage ?? null })) ?? null,
  };
  return (
    <EnergyEvidence data={lineage}>
      {!isRegional && <RuleDetails rule={data.rule} data={data} />}
      {!clue && (
        <section>
          <h3 className="font-semibold">当前覆盖</h3>
          <p className="mt-1.5 leading-6 text-muted">
            {energyPercent(period.coverage.rate)}（有效 {energyNumber(period.coverage.valid)} / {energyNumber(period.coverage.expected)} 表小时）
          </p>
        </section>
      )}
      {!clue && data.history.length > 0 && (
        <details className="border-t border-line pt-3">
          <summary className="w-fit cursor-pointer rounded py-1 text-primary">历史样本口径</summary>
          <ul className="mt-1 divide-y divide-line text-xs">
            {data.history.map((item) => (
              <li key={item.from} className="space-y-1 py-2.5">
                <p className="font-medium"><time dateTime={item.from}>{energyTime(item.from, data.timezone)}</time></p>
                <p className="break-words text-muted">计量 {item.meterRuleVersion} · 分类 {item.classificationRuleVersion}</p>
                <p className="break-words text-muted">目录 {item.catalogVersion} · 拓扑 {item.topologyVersion}</p>
              </li>
            ))}
          </ul>
        </details>
      )}
    </EnergyEvidence>
  );
}

function EvidenceLink({ pathname, search, children }: { pathname: string; search: string; children: ReactNode }) {
  return <Link to={{ pathname, search }} className={linkStyle}>{children}<ArrowUpRight className="size-4" aria-hidden /></Link>;
}

function UsageReview({ data, query }: { data: AnomalyReport; query: string }) {
  const finding = data.finding;
  const status = finding.usageStatus;
  const explanation = status === 'usage_candidate'
    ? '当前偏差满足该版本的候选规则。确认异常需现场记录与人工复核。'
    : status === 'no_candidate'
      ? '当前可比周期未达到这组规则的判定条件。'
      : reasons[finding.reason] ?? '当前数据不足以进行用量判定。';
  return (
    <section aria-label="用量判定" className={panel}>
      <div className="border-b border-line px-4 py-4 sm:px-5">
        <EnergySectionHeading title="用量判定" action={<ReviewEvidence key={query} data={data} />} />
        <p className="mt-1 text-sm text-muted"><EnergyInterval window={{ ...data.current, timezone: data.timezone, asOf: data.asOf }} /></p>
      </div>
      <dl className="grid grid-cols-2 gap-x-6 gap-y-4 px-4 py-4 sm:grid-cols-4 sm:px-5">
        <Measure label={finding.status === 'metering_anomaly' ? '已计量用量' : '当前用量'}><Quantity value={data.current.usage} unit={data.unit} /></Measure>
        <Measure label="历史基线"><Quantity value={finding.baseline} unit={data.unit} /></Measure>
        <Measure label="用量偏差"><Quantity value={finding.delta} unit={data.unit} signed /></Measure>
        <Measure label="相对偏差">{energyPercent(finding.changeRate)}</Measure>
      </dl>
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-2 border-t border-line px-4 py-3 sm:px-5">
        <div className="flex flex-wrap items-baseline gap-2 text-sm">
          <StatusBadge label={findings[status]} tone={status === 'no_candidate' ? 'neutral' : 'warning'} />
          <p className="text-muted">{explanation}</p>
        </div>
        {status === 'usage_candidate' && (
          <EvidenceLink pathname={ENERGY_OPPORTUNITIES_PATH} search={query}>查看节能机会</EvidenceLink>
        )}
      </div>
    </section>
  );
}

function MeterQuality({ data }: { data: AnomalyReport }) {
  const coverage = data.current.coverage;
  const isMeteringAnomaly = data.finding.status === 'metering_anomaly';
  return (
    <section className={panel} aria-label="计量数据质量">
      <div className="px-4 py-4 sm:px-5">
        <EnergySectionHeading title="计量数据质量" action={isMeteringAnomaly
          ? <StatusBadge label="计量数据异常" tone="warning" />
          : data.current.usage === 0 && coverage.expected > 0 && coverage.valid === coverage.expected && coverage.missing === 0 && coverage.suspect === 0
            ? <StatusBadge label="有效零值" />
            : undefined} />
        <dl className="mt-3 grid grid-cols-2 gap-x-6 gap-y-3 text-sm sm:grid-cols-4">
          <div><dt className="text-xs text-muted">覆盖率</dt><dd className="mt-1 font-semibold tabular">{energyPercent(coverage.rate)}</dd></div>
          <div><dt className="text-xs text-muted">有效 / 预期</dt><dd className="mt-1 font-semibold tabular">{energyNumber(coverage.valid)} / {energyNumber(coverage.expected)} 表小时</dd></div>
          <div><dt className="text-xs text-muted">缺测</dt><dd className="mt-1 font-semibold tabular">{energyNumber(coverage.missing)} 表小时</dd></div>
          <div><dt className="text-xs text-muted">可疑</dt><dd className="mt-1 font-semibold tabular">{energyNumber(coverage.suspect)} 表小时</dd></div>
        </dl>
        {isMeteringAnomaly && <p className="mt-3 text-sm text-muted">建议核查采集记录和通道质量，补齐证据后再进行用量比较。</p>}
      </div>
    </section>
  );
}

function HistorySamples({ data }: { data: AnomalyReport }) {
  const count = data.finding.baselineCount;
  return (
    <section className={`${panel} overflow-hidden`} aria-label="历史样本">
      <div className="px-4 py-4 sm:px-5">
        <EnergySectionHeading title="历史样本" description={`计入基线 ${energyNumber(count)} 个可比周期。`} />
      </div>
      {data.history.length > 0 ? (
        <table className="w-full table-fixed border-t border-line text-sm">
          <caption className="sr-only">历史样本用量与覆盖</caption>
          <thead className="border-b border-line bg-surface-2 text-xs text-muted">
            <tr>
              <th scope="col" className="w-1/2 px-4 py-2.5 text-left font-medium sm:px-5">统计区间（不含结束）</th>
              <th scope="col" className="px-2 py-2.5 text-right font-medium">用量（{data.unit}）</th>
              <th scope="col" className="px-4 py-2.5 text-right font-medium sm:px-5">覆盖率</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {data.history.map((period) => (
              <tr key={period.from}>
                <th scope="row" className="px-4 py-2.5 text-left font-medium sm:px-5">
                  <time className="block" dateTime={period.from}>{energyTime(period.from, data.timezone)}</time>
                  <span className="text-xs font-normal text-muted">至 <time dateTime={period.to}>{energyTime(period.to, data.timezone)}</time></span>
                </th>
                <td className="break-words px-2 py-2.5 text-right tabular">{energyNumber(period.usage)}</td>
                <td className="px-4 py-2.5 text-right tabular sm:px-5">{energyPercent(period.coverage.rate)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : <p className="border-t border-line px-4 py-3 text-sm text-muted sm:px-5">当前范围暂无历史样本。</p>}
    </section>
  );
}

function ClueCard({ clue, data, query, regionQuery }: { clue: Opportunity; data: AnomalyReport; query: string; regionQuery: string }) {
  return (
    <article className={panel} aria-label={clueLabels[clue.kind]}>
      <div className="border-b border-line px-4 py-4 sm:px-5">
        <EnergySectionHeading title={clueLabels[clue.kind]} description={clue.name}
          action={clue.status === 'pending_manual_review' ? <StatusBadge label="待人工核查" tone="warning" /> : undefined} />
      </div>
      <div className="grid gap-5 px-4 py-4 md:grid-cols-2 sm:px-5">
        <div>
          <h3 className="text-sm font-semibold">已有证据</h3>
          <p className="mt-1 text-sm leading-6 text-muted">{clue.evidence}</p>
          <dl className="mt-3 flex flex-wrap gap-x-6 gap-y-2 text-sm">
            <div><dt className="text-xs text-muted">观测用量</dt><dd className="font-semibold"><Quantity value={clue.usage} unit={data.unit} /></dd></div>
            {clue.baseline !== null && <div><dt className="text-xs text-muted">历史基线</dt><dd className="font-semibold"><Quantity value={clue.baseline} unit={data.unit} /></dd></div>}
            {clue.kind === 'high_usage_observation' && <div><dt className="text-xs text-muted">可比区域</dt><dd className="font-semibold tabular">{energyNumber(clue.comparablePeers)} 个</dd></div>}
          </dl>
          <h3 className="mt-4 text-sm font-semibold">证据局限</h3>
          <ul className="mt-1 list-disc space-y-1 pl-4 text-sm leading-6 text-muted">
            {clue.limitations.map((item) => <li key={item}>{item}</li>)}
          </ul>
        </div>
        <div>
          <h3 className="text-sm font-semibold">待确认项</h3>
          <ul className="mt-1 list-disc space-y-1 pl-4 text-sm leading-6 text-muted">
            {clue.confirm.map((item) => <li key={item}>{item}</li>)}
          </ul>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-2 border-t border-line px-3 py-2 sm:px-4">
        <ReviewEvidence data={data} clue={clue} />
        {clue.kind === 'high_usage_observation'
          ? <EvidenceLink pathname={ENERGY_DIAGNOSIS_PATH} search={regionQuery}>查看区域用能观察</EvidenceLink>
          : <EvidenceLink pathname={ENERGY_ANOMALIES_PATH} search={query}>查看异常识别</EvidenceLink>}
      </div>
    </article>
  );
}

export default function BuildingEnergyAnomaliesPage({ view = 'anomalies' }: { view?: 'anomalies' | 'opportunities' }) {
  const [params, setParams] = useSearchParams();
  const applied = energyFilterValues(filters, params);
  const query = new URLSearchParams(applied).toString();
  const regionQuery = new URLSearchParams({ building: applied.building, energy: applied.energy, period: 'day' }).toString();
  const { data, error, retry } = useEnergyPreview<AnomalyReport>('anomalies', query);
  const label = (filter: typeof filters[number]) => filter.options.find(([value]) => value === applied[filter.key])?.[1];

  return (
    <div className="space-y-4 sm:space-y-5">
      <header className="flex min-w-0 items-center justify-between gap-3 border-b border-line pb-3">
        <div className="min-w-0">
          <p className="text-sm font-semibold">{label(filters[0])} · {label(filters[1])}</p>
          <p className="mt-0.5 text-xs leading-5 text-muted">
            {label(filters[2])}{data && ` · 数据截至 ${energyTime(data.asOf, data.timezone)}（项目时间）`}
          </p>
        </div>
        <EnergyFilterDrawer title="筛选分析范围" fields={filters} applied={applied}
          onApply={(next) => { if (new URLSearchParams(next).toString() !== params.toString()) setParams(next); }} />
      </header>
      {error ? <div role="alert"><ErrorState message={error} onRetry={retry} /></div> : !data ? (
        <div role="status" aria-label="加载分析证据"><Skeleton className="h-52" /></div>
      ) : view === 'opportunities' ? (
        <section className="space-y-4" aria-label="节能机会线索">
          <div className="space-y-1 text-sm leading-6 text-muted">
            <p><EnergyInterval window={{ ...data.current, timezone: data.timezone, asOf: data.asOf }} /></p>
            <p>这些合成线索供人工核查。形成已确认异常或节能结论前，须另行核查现场证据。</p>
          </div>
          {data.opportunities.length > 0
            ? data.opportunities.map((clue) => <ClueCard key={`${query}|${clue.kind}|${clue.object}`} clue={clue} data={data} query={query} regionQuery={regionQuery} />)
            : <div className={panel}>
                <EmptyState title="暂无可核查线索" hint="当前场景没有满足条件的用量候选或区域高用量观察。"
                  action={<EvidenceLink pathname={ENERGY_ANOMALIES_PATH} search={query}>查看异常识别</EvidenceLink>} />
              </div>}
        </section>
      ) : (
        <>
          <UsageReview data={data} query={query} />
          <MeterQuality data={data} />
          <HistorySamples data={data} />
        </>
      )}
    </div>
  );
}
