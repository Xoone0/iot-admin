import { useEffect, useMemo, useState } from 'react';
import { CheckCircle2, Lock, Plus, ShieldAlert, SlidersHorizontal, Wrench } from 'lucide-react';
import { liveGet, livePost, type Coverage, type EnergyType, type InventoryData } from '../api/energyLive';
import {
  BaselineVsActualChart,
  RetrofitEconomicsChart,
  RetrofitMVWaterfallChart,
  RetrofitScenarioChart,
  TrendAreaChart,
  type TrendPoint,
} from './EnergyCharts';
import { ChineseDateInput, CollapsibleSection, fmtShanghaiDate } from './EnergyWorkspaceChrome';

export type RetrofitBaselineRevision = {
  baselineVersion: string;
  buildingId: string;
  spaceId?: string;
  energy: string;
  baselineFrom: string;
  baselineTo: string;
  baselineModel: string;
  baselineFactors: string;
  baselineR2?: number | null;
  baselineCoverage: number;
  baselineLocked: boolean;
  baselineDailyAvg: number;
  baselineActualUsage?: number | null;
  changeReason: string;
  actorSub: string;
  recordedAt: string;
};

export type RetrofitVerificationRecord = {
  id: number;
  baselineVersion: string;
  reportFrom: string;
  reportTo: string;
  adjustedBaselineUse?: number | null;
  reportActualUsage?: number | null;
  reportCoverageRate?: number | null;
  nonRoutineAdjustment: number;
  verifiedSavings?: number | null;
  verifiedSavingsRate?: number | null;
  verifiedCarbonKgCO2e?: number | null;
  weatherDataComplete: boolean;
  mvStatus: string;
  mvNote: string;
  actorSub: string;
  recordedAt: string;
};

export type RetrofitProject = {
  retrofitId: string;
  buildingId: string;
  buildingName: string;
  spaceId?: string;
  spaceName?: string;
  energy: string;
  name: string;
  measureSummary: string;
  unit: string;
  baselineVersion: string;
  baselineFrom: string;
  baselineTo: string;
  baselineModel: string;
  baselineFactors: string;
  baselineR2?: number | null;
  baselineCoverage: number;
  baselineActualCoverage?: Coverage;
  baselineLocked: boolean;
  baselineDailyAvg: number;
  baselineActualUsage?: number | null;
  annualizedBaselineUsage?: number | null;
  estSavingsLow: number;
  estSavingsBase: number;
  estSavingsHigh: number;
  estSavingsRateLow?: number | null;
  estSavingsRateBase?: number | null;
  estSavingsRateHigh?: number | null;
  refUnitPrice: number;
  priceConfigured?: boolean;
  estAnnualCostLow?: number | null;
  estAnnualCostBase?: number | null;
  estAnnualCostHigh?: number | null;
  capexBudget: number;
  staticPaybackLowYears?: number | null;
  staticPaybackYears?: number | null;
  staticPaybackHighYears?: number | null;
  reportFrom: string;
  reportTo: string;
  adjustedBaselineUse?: number | null;
  reportActualUsage?: number | null;
  reportActualCoverage?: Coverage;
  minReportCoverageRate?: number;
  nonRoutineAdjustment: number;
  verifiedSavings?: number | null;
  verifiedSavingsRate?: number | null;
  verifiedCostSavings?: number | null;
  verifiedCarbonReductionKgCO2?: number | null;
  weatherDataComplete: boolean;
  mvStandardRef: string;
  mvStatus: string;
  mvNote: string;
  baselineHistory?: RetrofitBaselineRevision[];
  verificationLogs?: RetrofitVerificationRecord[];
};

export type RetrofitData = {
  section: string;
  dataOriginNotice: string;
  projects: RetrofitProject[];
  disclaimer: string;
};

const numFmt = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 3 });
const pctFmt = new Intl.NumberFormat('zh-CN', { style: 'percent', maximumFractionDigits: 1 });

function fmtNum(v: number | null | undefined) {
  return v == null ? '—' : numFmt.format(v);
}

function fmtPct(v: number | null | undefined) {
  return v == null ? '—' : pctFmt.format(v);
}

function toShanghaiMidnightISO(dateStr: string): string {
  return `${dateStr}T00:00:00+08:00`;
}

const energyLabels: Record<string, string> = {
  electricity: '用电',
  water: '用水',
  gas: '燃气',
  heat: '供热',
  cooling: '供冷',
  steam: '蒸汽',
};

const mvStatusShort: Record<string, string> = {
  verified_simulated: '模拟核验完成',
  pending_verification: '待核验',
  estimated_only: '仅模型估算',
};

const mvStatusFull: Record<string, string> = {
  verified_simulated: '模拟核验完成（非现场实测）',
  pending_verification: '待核验（未达门禁或待确认）',
  estimated_only: '仅模型估算（未进入核验）',
};

const baselineModelPresets = [
  '多变量回归（CDD/HDD + 营业日系数）',
  '营业时长分段基线模型',
  '采暖度日数 HDD 线性基线',
  '历史同口径日均基线模型',
];

export function RetrofitWorkspace({
  data,
  mode,
  token,
  inventory,
  initialBuilding,
  initialEnergy,
  onUpdated,
}: {
  data: RetrofitData;
  mode: 'retrofit-baseline' | 'retrofit-potential' | 'retrofit-verification';
  token: string;
  inventory: InventoryData | null;
  initialBuilding?: string;
  initialEnergy?: string;
  onUpdated: () => void;
}) {
  const projects = data.projects ?? [];
  // Respect incoming building/energy drilldown (e.g. F17 -> F18), otherwise prefer verified project on F20.
  const defaultId = useMemo(() => {
    if (initialBuilding) {
      const exact = projects.find(
        (p) => p.buildingId === initialBuilding && (!initialEnergy || p.energy === initialEnergy),
      );
      if (exact) return exact.retrofitId;
      const byBldg = projects.find((p) => p.buildingId === initialBuilding);
      if (byBldg) return byBldg.retrofitId;
    }
    if (mode === 'retrofit-verification') {
      return projects.find((p) => p.mvStatus === 'verified_simulated')?.retrofitId ?? projects[0]?.retrofitId ?? '';
    }
    return projects[0]?.retrofitId ?? '';
  }, [projects, mode, initialBuilding, initialEnergy]);

  const [selectedId, setSelectedId] = useState<string>(defaultId);
  const [creating, setCreating] = useState<boolean>(false);
  const [showMvForm, setShowMvForm] = useState<boolean>(false);
  const [busy, setBusy] = useState<boolean>(false);
  const [feedback, setFeedback] = useState<{ ok: boolean; text: string } | null>(null);

  const active = useMemo(
    () => projects.find((p) => p.retrofitId === selectedId) ?? projects.find((p) => p.retrofitId === defaultId) ?? projects[0] ?? null,
    [projects, selectedId, defaultId],
  );

  // Create Form State (F18)
  const [newId, setNewId] = useState('');
  const [newName, setNewName] = useState('');
  const [newMeasure, setNewMeasure] = useState('');
  const [newBldg, setNewBldg] = useState(() => initialBuilding || inventory?.buildings?.[0]?.buildingId || 'bldg-a');
  const [newSpace, setNewSpace] = useState('');
  const [newEnergy, setNewEnergy] = useState<EnergyType>(() =>
    initialEnergy && ['electricity', 'water', 'gas', 'heat', 'cooling'].includes(initialEnergy)
      ? (initialEnergy as EnergyType)
      : 'electricity',
  );
  const [newBaseFrom, setNewBaseFrom] = useState('2025-06-01');
  const [newBaseTo, setNewBaseTo] = useState('2025-11-01');
  const [newModel, setNewModel] = useState(baselineModelPresets[0]);
  const [newFactors, setNewFactors] = useState('日均室外温度、工作日/周末占用率');
  const [newR2, setNewR2] = useState('0.89');
  const [newSavLow, setNewSavLow] = useState('40000');
  const [newSavBase, setNewSavBase] = useState('55000');
  const [newSavHigh, setNewSavHigh] = useState('70000');
  const [newPrice, setNewPrice] = useState('0.88');
  const [newCapex, setNewCapex] = useState('180000');

  // Edit Form State (F18)
  const [edName, setEdName] = useState('');
  const [edMeasure, setEdMeasure] = useState('');
  const [edBldg, setEdBldg] = useState('');
  const [edSpace, setEdSpace] = useState('');
  const [edEnergy, setEdEnergy] = useState<EnergyType>('electricity');
  const [edBaseFrom, setEdBaseFrom] = useState('');
  const [edBaseTo, setEdBaseTo] = useState('');
  const [edModel, setEdModel] = useState('');
  const [edFactors, setEdFactors] = useState('');
  const [edR2, setEdR2] = useState('');
  const [edNewVerCheck, setEdNewVerCheck] = useState(false);
  const [edNewVerName, setEdNewVerName] = useState('');
  const [edChangeReason, setEdChangeReason] = useState('');
  const [lockReason, setLockReason] = useState('核对基线周期与覆盖率无误后正式锁定');

  // F19 Scenario Edit State
  const [scLow, setScLow] = useState('');
  const [scBase, setScBase] = useState('');
  const [scHigh, setScHigh] = useState('');
  const [scPrice, setScPrice] = useState('');
  const [scCapex, setScCapex] = useState('');

  // F20 Verification Form State
  const [vfFrom, setVfFrom] = useState('');
  const [vfTo, setVfTo] = useState('');
  const [vfAdjBase, setVfAdjBase] = useState('');
  const [vfNonRoutine, setVfNonRoutine] = useState('0');
  const [vfWeatherOK, setVfWeatherOK] = useState(true);
  const [vfNote, setVfNote] = useState('');

  useEffect(() => {
    if (!active) return;
    setEdName(active.name);
    setEdMeasure(active.measureSummary);
    setEdBldg(active.buildingId);
    setEdSpace(active.spaceId ?? '');
    setEdEnergy(active.energy as EnergyType);
    setEdBaseFrom(fmtShanghaiDate(active.baselineFrom));
    setEdBaseTo(fmtShanghaiDate(active.baselineTo));
    setEdModel(active.baselineModel);
    setEdFactors(active.baselineFactors);
    setEdR2(active.baselineR2 != null ? String(active.baselineR2) : '');
    setEdNewVerCheck(false);
    setEdNewVerName('');
    setEdChangeReason('');

    setScLow(String(active.estSavingsLow));
    setScBase(String(active.estSavingsBase));
    setScHigh(String(active.estSavingsHigh));
    setScPrice(active.refUnitPrice > 0 ? String(active.refUnitPrice) : '');
    setScCapex(active.capexBudget > 0 ? String(active.capexBudget) : '');

    setVfFrom(fmtShanghaiDate(active.reportFrom));
    setVfTo(fmtShanghaiDate(active.reportTo));
    setVfAdjBase(active.adjustedBaselineUse != null ? String(active.adjustedBaselineUse) : '');
    setVfNonRoutine(String(active.nonRoutineAdjustment ?? 0));
    setVfWeatherOK(Boolean(active.weatherDataComplete));
    setVfNote(active.mvNote ?? '');
  }, [active]);

  // F18: Fetch real monthly energy series for the baseline year so users see baseline trend & chosen window.
  const [baseTrendPoints, setBaseTrendPoints] = useState<TrendPoint[]>([]);
  const [baseTrendYear, setBaseTrendYear] = useState<string>('');
  const [showBaseEditForm, setShowBaseEditForm] = useState<boolean>(false);

  useEffect(() => {
    if (mode !== 'retrofit-baseline' || !active) {
      setBaseTrendPoints([]);
      return;
    }
    const yStr = fmtShanghaiDate(active.baselineFrom).slice(0, 4);
    if (!/^\d{4}$/.test(yStr)) {
      setBaseTrendPoints([]);
      return;
    }
    setBaseTrendYear(yStr);
    const ctrl = new AbortController();
    const qs = new URLSearchParams({ building: active.buildingId, energy: active.energy, year: yStr });
    liveGet<{
      months?: Array<{ month: number; from: string; to: string; usage: number | null; coverage?: Coverage }>;
    }>(`/targets?${qs}`, token, ctrl.signal)
      .then((res) => {
        if (ctrl.signal.aborted) return;
        const pts: TrendPoint[] = (res.data?.months ?? []).map((m) => ({
          from: m.from,
          to: m.to,
          usage: m.usage,
          valid: m.coverage?.valid,
          expected: m.coverage?.expected,
        }));
        setBaseTrendPoints(pts);
      })
      .catch(() => {
        if (!ctrl.signal.aborted) setBaseTrendPoints([]);
      });
    return () => ctrl.abort();
  }, [mode, active?.retrofitId, active?.buildingId, active?.energy, active?.baselineFrom, token]);

  const buildings = inventory?.buildings ?? [];
  const spacesFor = (bldgId: string) => (inventory?.spaces ?? []).filter((sp) => sp.buildingId === bldgId);

  const runAction = async (fn: () => Promise<{ data?: RetrofitProject; error?: string }>, successMsg: string) => {
    setBusy(true);
    setFeedback(null);
    try {
      const res = await fn();
      if (res.error) {
        setFeedback({ ok: false, text: res.error });
      } else {
        setFeedback({ ok: true, text: successMsg });
        if (res.data?.retrofitId) setSelectedId(res.data.retrofitId);
        onUpdated();
      }
    } finally {
      setBusy(false);
    }
  };

  const handleCreate = (e: React.FormEvent) => {
    e.preventDefault();
    runAction(
      () =>
        livePost<RetrofitProject>('/management/retrofits', token, {
          retrofitId: newId.trim(),
          buildingId: newBldg,
          spaceId: newSpace,
          energy: newEnergy,
          name: newName.trim(),
          measureSummary: newMeasure.trim(),
          baselineFrom: toShanghaiMidnightISO(newBaseFrom),
          baselineTo: toShanghaiMidnightISO(newBaseTo),
          baselineModel: newModel,
          baselineFactors: newFactors.trim(),
          baselineR2: newR2.trim() ? Number(newR2) : null,
          estSavingsLow: Number(newSavLow || 0),
          estSavingsBase: Number(newSavBase || 0),
          estSavingsHigh: Number(newSavHigh || 0),
          refUnitPrice: Number(newPrice || 0),
          capexBudget: Number(newCapex || 0),
          reportFrom: toShanghaiMidnightISO('2026-10-01'),
          reportTo: toShanghaiMidnightISO('2026-11-01'),
          weatherDataComplete: true,
        }),
      '新改造项目已创建并生成初始基线版本（未锁定）',
    ).then(() => {
      setCreating(false);
      setNewId('');
      setNewName('');
      setNewMeasure('');
    });
  };

  const handleUpdateBaseline = (e: React.FormEvent) => {
    e.preventDefault();
    if (!active) return;
    runAction(
      () =>
        livePost<RetrofitProject>('/management/retrofits/update', token, {
          retrofitId: active.retrofitId,
          buildingId: edBldg,
          spaceId: edSpace,
          energy: edEnergy,
          name: edName.trim(),
          measureSummary: edMeasure.trim(),
          baselineFrom: toShanghaiMidnightISO(edBaseFrom),
          baselineTo: toShanghaiMidnightISO(edBaseTo),
          baselineModel: edModel.trim(),
          baselineFactors: edFactors.trim(),
          baselineR2: edR2.trim() ? Number(edR2) : null,
          createNewBaselineVersion: edNewVerCheck,
          newBaselineVersion: edNewVerName.trim(),
          changeReason: edChangeReason.trim(),
        }),
      edNewVerCheck ? '已生成新基线版本并保留原历史基线版本' : '改造项目与基线信息已保存',
    );
  };

  const handleLockBaseline = () => {
    if (!active) return;
    runAction(
      () =>
        livePost<RetrofitProject>('/management/retrofits/baseline/lock', token, {
          retrofitId: active.retrofitId,
          changeReason: lockReason.trim(),
        }),
      `基线版本 ${active.baselineVersion} 已正式锁定，不可直接覆盖`,
    );
  };

  const handleUpdateScenarios = (e: React.FormEvent) => {
    e.preventDefault();
    if (!active) return;
    runAction(
      () =>
        livePost<RetrofitProject>('/management/retrofits/update', token, {
          retrofitId: active.retrofitId,
          estSavingsLow: Number(scLow || 0),
          estSavingsBase: Number(scBase || 0),
          estSavingsHigh: Number(scHigh || 0),
          refUnitPrice: scPrice.trim() ? Number(scPrice) : 0,
          capexBudget: scCapex.trim() ? Number(scCapex) : 0,
        }),
      'F19 节能情景、能源单价与投资预算已更新',
    );
  };

  const handleVerify = (markVerified: boolean) => {
    if (!active) return;
    runAction(
      () =>
        livePost<RetrofitProject>('/management/retrofits/verify', token, {
          retrofitId: active.retrofitId,
          reportFrom: toShanghaiMidnightISO(vfFrom),
          reportTo: toShanghaiMidnightISO(vfTo),
          adjustedBaselineUse: vfAdjBase.trim() ? Number(vfAdjBase) : null,
          nonRoutineAdjustment: Number(vfNonRoutine || 0),
          weatherDataComplete: vfWeatherOK,
          markVerified,
          mvNote: vfNote.trim(),
        }),
      markVerified ? 'M&V 核验满足全部门禁（含 ≥90% 覆盖率），已记录为模拟核验完成' : '已保存待核验评估记录',
    );
  };

  const minCov = active?.minReportCoverageRate ?? 0.9;
  const repCovRate = active?.reportActualCoverage?.rate ?? null;
  const baseCovRate = active?.baselineActualCoverage?.rate ?? null;
  const gateLocked = Boolean(active?.baselineLocked);
  const gateWeather = vfWeatherOK;
  const gateAdj = vfAdjBase.trim() !== '' && !Number.isNaN(Number(vfAdjBase));
  const gateRepCov = repCovRate != null && repCovRate >= minCov;
  const gateBaseCov = active?.baselineActualUsage == null || (baseCovRate != null && baseCovRate >= minCov);

  return (
    <div className="space-y-4">
      {feedback && (
        <div
          role="status"
          className={`rounded-[4px] border px-4 py-2.5 text-xs ${
            feedback.ok
              ? 'border-positive/40 bg-positive-soft text-positive'
              : 'border-negative/40 bg-negative-soft text-negative'
          }`}
        >
          {feedback.text}
        </div>
      )}

      {/* Compact Project Switcher Bar (replaces oversized table pushing analytics off-screen) */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-2" role="tablist" aria-label="切换改造项目">
          {projects.map((p) => {
            const isSel = active?.retrofitId === p.retrofitId;
            const dotColor =
              p.mvStatus === 'verified_simulated'
                ? 'bg-positive'
                : p.mvStatus === 'pending_verification'
                ? 'bg-warning'
                : 'bg-muted';
            return (
              <button
                key={p.retrofitId}
                type="button"
                role="tab"
                aria-selected={isSel}
                onClick={() => {
                  setSelectedId(p.retrofitId);
                  setFeedback(null);
                }}
                className={`inline-flex items-center gap-2 rounded-[4px] border px-3 py-1.5 text-xs transition-colors ${
                  isSel
                    ? 'border-primary bg-primary-soft font-semibold text-primary'
                    : 'border-line bg-surface text-fg hover:bg-surface-2'
                }`}
              >
                <span className={`h-2 w-2 rounded-full ${dotColor}`} aria-hidden="true" />
                <span className="max-w-[220px] truncate">{p.name}</span>
                <span className="hidden text-[11px] opacity-70 sm:inline">({p.retrofitId})</span>
              </button>
            );
          })}
        </div>

        <div className="flex items-center gap-2">
          {mode === 'retrofit-baseline' && (
            <button
              type="button"
              onClick={() => {
                setCreating((v) => !v);
                setFeedback(null);
              }}
              className="inline-flex items-center gap-1.5 rounded-[4px] bg-primary px-3 py-1.5 text-xs font-medium text-white hover:bg-primary-hover"
            >
              <Plus className="h-3.5 w-3.5" aria-hidden="true" />
              {creating ? '收起新建' : '新建改造项目'}
            </button>
          )}
          {mode === 'retrofit-verification' && (
            <button
              type="button"
              onClick={() => setShowMvForm((v) => !v)}
              className="inline-flex items-center gap-1.5 rounded-[4px] border border-line bg-surface px-3 py-1.5 text-xs font-medium text-fg hover:bg-surface-2"
            >
              <SlidersHorizontal className="h-3.5 w-3.5 text-primary" aria-hidden="true" />
              {showMvForm ? '收起核验表单' : '调整周期并执行核验'}
            </button>
          )}
        </div>
      </div>

      {/* F18 Create Form */}
      {mode === 'retrofit-baseline' && creating && (
        <form onSubmit={handleCreate} className="space-y-4 rounded-[4px] border border-primary/40 bg-surface p-5 text-xs">
          <div className="flex items-center justify-between border-b border-line pb-2.5">
            <h3 className="text-sm font-semibold text-fg">新建节能改造项目与初始基线</h3>
            <span className="text-muted">默认未锁定，核对实算覆盖率后一键锁定</span>
          </div>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <label className="flex flex-col gap-1 text-muted">
              项目名称 *
              <input value={newName} onChange={(e) => setNewName(e.target.value)} required placeholder="如：A2 机房精密空调变频改造" className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg" />
            </label>
            <label className="flex flex-col gap-1 text-muted">
              项目编号（留空自动生成）
              <input value={newId} onChange={(e) => setNewId(e.target.value)} placeholder="如 ret-a-chiller-02" className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg" />
            </label>
            <label className="flex flex-col gap-1 text-muted">
              所属建筑 *
              <select value={newBldg} onChange={(e) => { setNewBldg(e.target.value); setNewSpace(''); }} className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg">
                {buildings.map((b) => <option key={b.buildingId} value={b.buildingId}>{b.name} ({b.buildingId})</option>)}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-muted">
              改造区域（可选空间）
              <select value={newSpace} onChange={(e) => setNewSpace(e.target.value)} className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg">
                <option value="">整栋建筑范围</option>
                {spacesFor(newBldg).map((sp) => <option key={sp.spaceId} value={sp.spaceId}>{sp.name} ({sp.spaceId})</option>)}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-muted">
              能源类型 *
              <select value={newEnergy} onChange={(e) => setNewEnergy(e.target.value as EnergyType)} className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg">
                <option value="electricity">用电 (kWh)</option>
                <option value="water">用水 (m³)</option>
                <option value="gas">燃气 (Nm³)</option>
                <option value="heat">供热 (GJ)</option>
                <option value="cooling">供冷 (GJ)</option>
              </select>
            </label>
            <label className="flex flex-col gap-1 text-muted">
              基线开始日期 *
              <ChineseDateInput value={newBaseFrom} onChange={setNewBaseFrom} required placeholder="年-月-日" ariaLabel="基线开始日期" className="w-full" />
            </label>
            <label className="flex flex-col gap-1 text-muted">
              基线结束日期 *
              <ChineseDateInput value={newBaseTo} onChange={setNewBaseTo} required placeholder="年-月-日" ariaLabel="基线结束日期" className="w-full" />
            </label>
            <label className="flex flex-col gap-1 text-muted">
              基线计算方式 *
              <select value={newModel} onChange={(e) => setNewModel(e.target.value)} className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg">
                {baselineModelPresets.map((m) => <option key={m} value={m}>{m}</option>)}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-muted sm:col-span-2">
              改造措施说明 *
              <input value={newMeasure} onChange={(e) => setNewMeasure(e.target.value)} required placeholder="简述改造设备、控制策略与节能边界" className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg" />
            </label>
            <label className="flex flex-col gap-1 text-muted">
              自变量与修正因子
              <input value={newFactors} onChange={(e) => setNewFactors(e.target.value)} className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg" />
            </label>
            <label className="flex flex-col gap-1 text-muted">
              拟合度 R²
              <input type="number" step="0.001" min="0" max="1" value={newR2} onChange={(e) => setNewR2(e.target.value)} className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg tabular" />
            </label>
            <label className="flex flex-col gap-1 text-muted">
              预计年节能（保守）
              <input type="number" step="any" min="0" value={newSavLow} onChange={(e) => setNewSavLow(e.target.value)} className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg tabular" />
            </label>
            <label className="flex flex-col gap-1 text-muted">
              预计年节能（基准）
              <input type="number" step="any" min="0" value={newSavBase} onChange={(e) => setNewSavBase(e.target.value)} className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg tabular" />
            </label>
            <label className="flex flex-col gap-1 text-muted">
              预计年节能（乐观）
              <input type="number" step="any" min="0" value={newSavHigh} onChange={(e) => setNewSavHigh(e.target.value)} className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg tabular" />
            </label>
            <div className="grid grid-cols-2 gap-2">
              <label className="flex flex-col gap-1 text-muted">
                参考单价
                <input type="number" step="any" min="0" value={newPrice} onChange={(e) => setNewPrice(e.target.value)} placeholder="未配置" className="rounded-[4px] border border-line bg-surface-2 px-2 py-1.5 text-fg tabular" />
              </label>
              <label className="flex flex-col gap-1 text-muted">
                投资额（元）
                <input type="number" step="any" min="0" value={newCapex} onChange={(e) => setNewCapex(e.target.value)} placeholder="未配置" className="rounded-[4px] border border-line bg-surface-2 px-2 py-1.5 text-fg tabular" />
              </label>
            </div>
          </div>
          <div className="flex justify-end gap-2">
            <button type="button" onClick={() => setCreating(false)} className="rounded-[4px] border border-line px-3.5 py-1.5 text-fg hover:bg-surface-2">取消</button>
            <button type="submit" disabled={busy} className="rounded-[4px] bg-primary px-4 py-1.5 font-medium text-white">创建项目并写入 PostgreSQL</button>
          </div>
        </form>
      )}

      {/* ═══════════════════ F20 FLAGSHIP: M&V VERIFICATION ═══════════════════ */}
      {active && mode === 'retrofit-verification' && (() => {
        const repStartMs = Date.parse(active.reportFrom);
        const repEndMs = Date.parse(active.reportTo);
        const reportDays =
          Number.isFinite(repStartMs) && Number.isFinite(repEndMs) && repEndMs > repStartMs
            ? Math.max(1, Math.round((repEndMs - repStartMs) / 86_400_000))
            : 31;

        return (
          <div className="space-y-4">
            {/* ── 1. Unified Hero M&V Metric Band (Baseline, Actual, Verified Savings, Carbon/Cost) ── */}
            <section className="arco-card overflow-hidden">
              <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line bg-fill-1/40 px-5 py-3">
                <div className="min-w-0">
                  <h2 className="truncate text-[15px] font-medium text-fg">
                    {active.name}
                    <span className="ml-2 text-[12px] font-normal text-muted">
                      {active.buildingName}
                      {active.spaceName ? ` · ${active.spaceName}` : ''} · {energyLabels[active.energy] ?? active.energy}（{active.unit}）
                    </span>
                  </h2>
                </div>
                <span
                  className={`rounded-[2px] px-2 py-0.5 text-[11px] font-medium ${
                    active.mvStatus === 'verified_simulated'
                      ? 'bg-positive-soft text-positive'
                      : active.mvStatus === 'pending_verification'
                        ? 'bg-warning-soft text-warning'
                        : 'bg-fill-2 text-fg-2'
                  }`}
                >
                  {mvStatusFull[active.mvStatus] ?? active.mvStatus}
                </span>
              </div>

              <div className="grid divide-y divide-line sm:grid-cols-2 sm:divide-y-0 lg:grid-cols-4 lg:divide-x tabular">
                <div className="p-4 lg:p-5">
                  <div className="flex items-center justify-between gap-2 text-[12px]">
                    <span className="font-medium text-fg-2">M&amp;V 核验节能量</span>
                    {active.verifiedSavingsRate != null && (
                      <span className="rounded-[2px] bg-positive-soft px-1.5 py-0 text-[11px] font-medium leading-[18px] text-positive">
                        节能率 {fmtPct(active.verifiedSavingsRate)}
                      </span>
                    )}
                  </div>
                  <p
                    className={`mt-2 text-[26px] font-semibold leading-[32px] tracking-[-0.02em] ${
                      active.verifiedSavings != null ? 'text-positive' : 'text-fg'
                    }`}
                  >
                    {active.verifiedSavings != null ? fmtNum(active.verifiedSavings) : '未核验'}
                    {active.verifiedSavings != null && (
                      <span className="ml-1 text-[12px] font-normal text-muted">{active.unit}</span>
                    )}
                  </p>
                  <p className="mt-1.5 text-[11px] text-muted">
                    {active.verifiedSavings != null ? `基线版本 ${active.baselineVersion}` : '门禁未通过不输出伪造节能量'}
                  </p>
                </div>

                <div className="p-4 lg:p-5">
                  <p className="text-[12px] font-medium text-fg-2">调整后基线用量</p>
                  <p className="mt-2 text-[24px] font-semibold leading-[32px] tracking-[-0.02em] text-fg">
                    {active.adjustedBaselineUse != null ? fmtNum(active.adjustedBaselineUse) : '未配置'}
                    {active.adjustedBaselineUse != null && (
                      <span className="ml-1 text-[12px] font-normal text-muted">{active.unit}</span>
                    )}
                  </p>
                  <p className="mt-1.5 text-[11px] text-muted">
                    非例行调整 {fmtNum(active.nonRoutineAdjustment)} {active.unit}
                  </p>
                </div>

                <div className="p-4 lg:p-5">
                  <p className="text-[12px] font-medium text-fg-2">报告期实际用量</p>
                  <p className="mt-2 text-[24px] font-semibold leading-[32px] tracking-[-0.02em] text-primary">
                    {active.reportActualUsage != null ? fmtNum(active.reportActualUsage) : '—'}
                    {active.reportActualUsage != null && (
                      <span className="ml-1 text-[12px] font-normal text-muted">{active.unit}</span>
                    )}
                  </p>
                  <p className="mt-1.5 text-[11px] text-muted">
                    {fmtShanghaiDate(active.reportFrom)} ~ {fmtShanghaiDate(active.reportTo)}（{reportDays} 天）
                  </p>
                </div>

                <div className="bg-fill-1/35 p-4 lg:p-5">
                  <p className="text-[12px] font-medium text-fg-2">参考节费 &amp; 模拟碳减排</p>
                  <p className="mt-2 text-[24px] font-semibold leading-[32px] tracking-[-0.02em] text-fg">
                    {active.verifiedCostSavings != null ? `¥${fmtNum(active.verifiedCostSavings)}` : '—'}
                  </p>
                  <p className="mt-1.5 truncate text-[11px] text-muted">
                    {active.verifiedCarbonReductionKgCO2 != null
                      ? `参考碳减排 ${fmtNum(active.verifiedCarbonReductionKgCO2)} kgCO₂e`
                      : 'sim-carbon-factors-v1（非履约凭证）'}
                  </p>
                </div>
              </div>
            </section>

            {/* ── 2. Primary Analytical Stage: Bridge Waterfall (7/8 cols) + Baseline vs Actual Comparison (5/4 cols) ── */}
            <div className="grid gap-4 lg:grid-cols-12">
              <section className="arco-card p-5 lg:col-span-7 2xl:col-span-8">
                <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
                  <div>
                    <h3 className="text-[15px] font-medium leading-[22px] text-fg">核验节能量桥接瀑布图</h3>
                    <p className="mt-0.5 text-[12px] text-muted">
                      直观呈现「调整后基线 → 扣减报告期实算 → 非例行调整 → 净核验节能量」完整桥接过程
                    </p>
                  </div>
                </div>
                <RetrofitMVWaterfallChart
                  adjustedBaseline={active.adjustedBaselineUse}
                  reportActual={active.reportActualUsage}
                  nonRoutineAdjustment={active.nonRoutineAdjustment}
                  verifiedSavings={active.verifiedSavings}
                  savingsRate={active.verifiedSavingsRate}
                  unit={active.unit}
                  isVerified={active.mvStatus === 'verified_simulated' && active.verifiedSavings != null}
                />
              </section>

              <section className="arco-card p-5 lg:col-span-5 2xl:col-span-4">
                <div className="mb-3">
                  <h3 className="text-[15px] font-medium leading-[22px] text-fg">基线期与报告期实际用量对比</h3>
                  <p className="mt-0.5 text-[12px] text-muted">
                    周期总量与日均用能强度双维度对照（单位：{active.unit}）
                  </p>
                </div>
                <BaselineVsActualChart
                  adjustedBaseline={active.adjustedBaselineUse}
                  baselineActual={active.baselineActualUsage}
                  baselineDailyAvg={active.baselineDailyAvg}
                  reportActual={active.reportActualUsage}
                  reportDays={reportDays}
                  unit={active.unit}
                />
              </section>
            </div>

            {/* ── 3. Auxiliary Evidence & Compliance Region (Coverage, M&V Gates, Methodology & Provenance) ── */}
            <section aria-label="覆盖率、核验条件与证据来源" className="arco-card overflow-hidden">
              <div className="grid divide-y divide-line lg:grid-cols-12 lg:divide-y-0 lg:divide-x">
                <div className="p-5 lg:col-span-7">
                  <div className="mb-3 flex items-baseline justify-between gap-2">
                    <h3 className="text-[14px] font-medium text-fg">核验条件门禁与数据覆盖率（PRD §7.2–§7.3）</h3>
                    <span className="text-[11px] text-muted tabular">最低覆盖率门禁 ≥ {fmtPct(minCov)}</span>
                  </div>
                  <div className="grid gap-2.5 text-[12px] sm:grid-cols-2 tabular">
                    {[
                      { ok: gateLocked, label: '基线版本锁定', detail: active.baselineLocked ? active.baselineVersion : '未锁定（请先在 F18 锁定）' },
                      { ok: gateWeather, label: '气象与运行修正因子', detail: active.weatherDataComplete ? '已确认完整' : '缺失（阻断已核验）' },
                      { ok: gateAdj, label: '调整后基线确认', detail: active.adjustedBaselineUse != null ? `${fmtNum(active.adjustedBaselineUse)} ${active.unit}` : '尚未配置' },
                      { ok: gateRepCov && gateBaseCov, label: '双周期覆盖率门禁', detail: `报告期 ${fmtPct(repCovRate)} / 基线期 ${fmtPct(baseCovRate)}` },
                    ].map((g) => (
                      <div key={g.label} className="flex items-center justify-between gap-2 rounded-[2px] bg-fill-1/60 px-3 py-2">
                        <span className="flex items-center gap-2 font-medium text-fg">
                          <span className={`inline-block size-2 rounded-full ${g.ok ? 'bg-positive' : 'bg-warning'}`} />
                          {g.label}
                        </span>
                        <span className={`truncate ${g.ok ? 'text-fg-2' : 'font-medium text-warning'}`}>{g.detail}</span>
                      </div>
                    ))}
                  </div>
                </div>

                <div className="flex flex-col justify-between bg-fill-1/30 p-5 text-[12px] lg:col-span-5">
                  <div className="space-y-1.5 tabular">
                    <h3 className="text-[14px] font-medium text-fg">核验依据与证据来源</h3>
                    <div className="flex justify-between gap-2 text-fg-2">
                      <span className="text-muted">核算标准</span>
                      <span className="font-medium text-fg">{active.mvStandardRef}</span>
                    </div>
                    <div className="flex justify-between gap-2 text-fg-2">
                      <span className="text-muted">基线区间</span>
                      <span>{fmtShanghaiDate(active.baselineFrom)} ~ {fmtShanghaiDate(active.baselineTo)}</span>
                    </div>
                    <div className="flex justify-between gap-2 text-fg-2">
                      <span className="text-muted">报告区间</span>
                      <span>{fmtShanghaiDate(active.reportFrom)} ~ {fmtShanghaiDate(active.reportTo)}</span>
                    </div>
                  </div>
                  <p className="mt-3 border-t border-line pt-2.5 text-[11px] leading-relaxed text-muted">
                    {active.mvNote}
                  </p>
                </div>
              </div>
            </section>

          {/* Expandable M&V Execution Form */}
          {showMvForm && (
            <section className="space-y-4 rounded-[4px] border border-primary/35 bg-surface p-5 text-xs">
              <div className="flex items-center justify-between border-b border-line pb-2.5">
                <h3 className="text-sm font-semibold text-fg">提交或更新 F20 M&V 核验参数</h3>
                <span className="text-muted">未达 90% 覆盖率、未锁定或缺少气象因子时后端强制拦截</span>
              </div>
              <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                <label className="flex flex-col gap-1 text-muted">
                  报告期开始日期
                  <ChineseDateInput value={vfFrom} onChange={setVfFrom} placeholder="年-月-日" ariaLabel="报告期开始日期" className="w-full" />
                </label>
                <label className="flex flex-col gap-1 text-muted">
                  报告期结束日期
                  <ChineseDateInput value={vfTo} onChange={setVfTo} placeholder="年-月-日" ariaLabel="报告期结束日期" className="w-full" />
                </label>
                <label className="flex flex-col gap-1 text-muted">
                  调整后基线用量 ({active.unit})
                  <input type="number" step="any" value={vfAdjBase} onChange={(e) => setVfAdjBase(e.target.value)} placeholder="如 96500" className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg tabular" />
                </label>
                <label className="flex flex-col gap-1 text-muted">
                  非例行调整量 ({active.unit})
                  <input type="number" step="any" value={vfNonRoutine} onChange={(e) => setVfNonRoutine(e.target.value)} className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg tabular" />
                </label>
                <label className="flex flex-col gap-1 text-muted sm:col-span-2 lg:col-span-3">
                  核验说明与边界备注
                  <input value={vfNote} onChange={(e) => setVfNote(e.target.value)} className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg" />
                </label>
                <label className="flex items-center gap-2 self-end pb-1.5 font-medium text-fg">
                  <input type="checkbox" checked={vfWeatherOK} onChange={(e) => setVfWeatherOK(e.target.checked)} />
                  气象与运行因子完整
                </label>
              </div>
              <div className="flex flex-wrap justify-end gap-2">
                <button type="button" disabled={busy} onClick={() => handleVerify(false)} className="rounded-[4px] border border-line px-3.5 py-1.5 font-medium text-fg hover:bg-surface-2">
                  保存为待核验记录
                </button>
                <button type="button" disabled={busy} onClick={() => handleVerify(true)} className="inline-flex items-center gap-1.5 rounded-[4px] bg-primary px-4 py-1.5 font-medium text-white">
                  <CheckCircle2 className="h-3.5 w-3.5" aria-hidden="true" />
                  执行 M&V 核验并标记已核验
                </button>
              </div>
            </section>
          )}

          {/* Collapsible Verification Log History */}
          <CollapsibleSection
            title="M&V 核验记录台账"
            badge={`${active.verificationLogs?.length ?? 0} 条`}
            subtitle="查看每次核验提交的基线、实算与覆盖率快照"
            defaultOpen={(active.verificationLogs?.length ?? 0) > 0}
          >
            <div className="overflow-x-auto">
              <table className="w-full text-left text-xs">
                <thead className="border-b border-line bg-surface-2 text-muted">
                  <tr>
                    <th className="whitespace-nowrap px-3.5 py-2">核验时间</th>
                    <th className="whitespace-nowrap px-3.5 py-2">基线版本</th>
                    <th className="whitespace-nowrap px-3.5 py-2">报告期</th>
                    <th className="whitespace-nowrap px-3.5 py-2 text-right">调整基线 / 实算</th>
                    <th className="whitespace-nowrap px-3.5 py-2 text-right">覆盖率</th>
                    <th className="whitespace-nowrap px-3.5 py-2 text-right">核验节能量 / 率</th>
                    <th className="whitespace-nowrap px-3.5 py-2 text-right">参考碳减排</th>
                    <th className="px-3.5 py-2">核验状态与备注</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-line tabular">
                  {(active.verificationLogs ?? []).length === 0 ? (
                    <tr><td colSpan={8} className="px-4 py-5 text-center text-muted">尚无核验提交记录，点击右上角「调整周期并执行核验」归档。</td></tr>
                  ) : (
                    (active.verificationLogs ?? []).map((lg) => (
                      <tr key={lg.id}>
                        <td className="whitespace-nowrap px-3.5 py-2 text-muted">{lg.recordedAt.slice(0, 19).replace('T', ' ')}</td>
                        <td className="whitespace-nowrap px-3.5 py-2 text-fg">{lg.baselineVersion}</td>
                        <td className="whitespace-nowrap px-3.5 py-2 text-muted">{fmtShanghaiDate(lg.reportFrom)} ~ {fmtShanghaiDate(lg.reportTo)}</td>
                        <td className="whitespace-nowrap px-3.5 py-2 text-right text-fg">{fmtNum(lg.adjustedBaselineUse)} / {fmtNum(lg.reportActualUsage)} {active.unit}</td>
                        <td className="whitespace-nowrap px-3.5 py-2 text-right text-muted">{fmtPct(lg.reportCoverageRate)}</td>
                        <td className="whitespace-nowrap px-3.5 py-2 text-right font-medium text-fg">
                          {lg.verifiedSavings != null ? `${fmtNum(lg.verifiedSavings)} ${active.unit} (${fmtPct(lg.verifiedSavingsRate)})` : '未核验'}
                        </td>
                        <td className="whitespace-nowrap px-3.5 py-2 text-right text-muted">{lg.verifiedCarbonKgCO2e != null ? `${fmtNum(lg.verifiedCarbonKgCO2e)} kgCO₂e` : '—'}</td>
                        <td className="min-w-[240px] px-3.5 py-2 text-muted">
                          <span className="font-medium text-fg">{mvStatusShort[lg.mvStatus] ?? lg.mvStatus}</span>
                          {lg.mvNote ? ` · ${lg.mvNote}` : ''}（{lg.actorSub}）
                        </td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
          </CollapsibleSection>
        </div>
        );
      })()}

      {/* ═══════════════════ F18: PRE-RETROFIT BASELINE WORKBENCH ═══════════════════ */}
      {active && mode === 'retrofit-baseline' && (() => {
        const bFromYmd = fmtShanghaiDate(active.baselineFrom);
        const bToYmd = fmtShanghaiDate(active.baselineTo);
        const bStartMs = Date.parse(active.baselineFrom);
        const bEndMs = Date.parse(active.baselineTo);
        const bDays =
          Number.isFinite(bStartMs) && Number.isFinite(bEndMs) && bEndMs > bStartMs
            ? Math.max(1, Math.round((bEndMs - bStartMs) / 86_400_000))
            : null;
        const bCovOk = baseCovRate != null && baseCovRate >= minCov;
        const bDataOk = active.baselineActualUsage != null && active.baselineActualUsage > 0;
        const bModelOk = Boolean(active.baselineModel && active.baselineFactors) && (active.baselineR2 == null || active.baselineR2 >= 0.75);
        const readyForMV = bDataOk && bCovOk && bModelOk && active.baselineLocked;

        // Highlight months that overlap [baselineFrom, baselineTo) on the baseline year trend chart.
        const baselineWindowIndices = baseTrendPoints
          .map((pt, idx) => {
            const pStart = pt.from.slice(0, 10);
            const pEnd = (pt.to ?? pt.from).slice(0, 10);
            return pEnd > bFromYmd && pStart < bToYmd ? idx : -1;
          })
          .filter((i) => i >= 0);

        const monthlyAvgRef = active.baselineDailyAvg > 0 ? active.baselineDailyAvg * 30 : null;
        const history = active.baselineHistory ?? [];

        // Compute version-to-version effective diffs for historical baseline revisions.
        const revDiffs = history.map((rev, idx) => {
          const prev = history[idx + 1] ?? null;
          const diffs: string[] = [];
          if (!prev) {
            diffs.push('初始建立版本');
          } else {
            if (fmtShanghaiDate(rev.baselineFrom) !== fmtShanghaiDate(prev.baselineFrom) || fmtShanghaiDate(rev.baselineTo) !== fmtShanghaiDate(prev.baselineTo)) {
              diffs.push(`周期调整为 ${fmtShanghaiDate(rev.baselineFrom)} ~ ${fmtShanghaiDate(rev.baselineTo)}`);
            }
            if (rev.baselineModel !== prev.baselineModel) {
              diffs.push(`模型变更为 ${rev.baselineModel}`);
            }
            if (rev.baselineFactors !== prev.baselineFactors) {
              diffs.push('修正因子更新');
            }
            if (rev.baselineR2 !== prev.baselineR2) {
              diffs.push(`R² ${prev.baselineR2 ?? '—'} → ${rev.baselineR2 ?? '—'}`);
            }
            if (rev.baselineLocked !== prev.baselineLocked) {
              diffs.push(rev.baselineLocked ? '完成正式锁定' : '解锁为草稿');
            }
            if (rev.baselineActualUsage != null && prev.baselineActualUsage != null && Math.abs(rev.baselineActualUsage - prev.baselineActualUsage) > 0.01) {
              const d = rev.baselineActualUsage - prev.baselineActualUsage;
              diffs.push(`实算 ${d > 0 ? '+' : ''}${fmtNum(d)} ${active.unit}`);
            }
            if (diffs.length === 0) diffs.push('元数据或备注更新');
          }
          return { rev, diffs };
        });

        return (
          <div className="space-y-4">
            {/* ── 1. Project Context, Verifiability Verdict & Baseline Hero Strip ── */}
            <section className="arco-card overflow-hidden">
              <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line bg-fill-1/50 px-5 py-3">
                <div className="flex min-w-0 flex-wrap items-center gap-2">
                  <h2 className="truncate text-[15px] font-medium text-fg">{active.name}</h2>
                  <span className="rounded-[2px] bg-fill-2 px-1.5 py-0 text-[11px] text-fg-2">
                    {active.buildingName}{active.spaceName ? ` · ${active.spaceName}` : '（整栋）'}
                  </span>
                  <span className="rounded-[2px] bg-primary-soft px-1.5 py-0 text-[11px] font-medium text-primary">
                    {energyLabels[active.energy] ?? active.energy} ({active.unit})
                  </span>
                  <span className="font-mono text-[11px] text-muted">版本 {active.baselineVersion}</span>
                  <span
                    className={`rounded-[2px] px-2 py-0.5 text-[11px] font-medium ${
                      readyForMV ? 'bg-positive-soft text-positive' : 'bg-warning-soft text-warning'
                    }`}
                  >
                    {readyForMV ? '具备可核验条件' : '暂未完全满足核验门禁'}
                  </span>
                </div>

                <div className="flex flex-wrap items-center gap-2">
                  {!active.baselineLocked ? (
                    <>
                      <input
                        value={lockReason}
                        onChange={(e) => setLockReason(e.target.value)}
                        placeholder="锁定确认说明"
                        aria-label="锁定确认说明"
                        className="arco-input !h-7 w-48 text-[12px]"
                      />
                      <button
                        type="button"
                        data-lock-baseline
                        disabled={busy}
                        onClick={handleLockBaseline}
                        className="inline-flex h-7 items-center gap-1.5 rounded-[2px] bg-positive px-3 text-[12px] font-medium text-white hover:opacity-95"
                      >
                        <Lock className="h-3.5 w-3.5" aria-hidden="true" />
                        锁定当前基线版本
                      </button>
                    </>
                  ) : (
                    <span className="inline-flex h-7 items-center gap-1 rounded-[2px] bg-positive-soft px-2.5 text-[11px] font-medium text-positive">
                      <Lock className="h-3 w-3" aria-hidden="true" />
                      已锁定 · 禁止直接覆盖
                    </span>
                  )}
                  <button
                    type="button"
                    data-toggle-base-edit
                    onClick={() => setShowBaseEditForm((v) => !v)}
                    className="arco-btn !h-7 text-[12px]"
                  >
                    <Wrench className="h-3.5 w-3.5 text-primary" aria-hidden="true" />
                    <span>{showBaseEditForm ? '收起编辑表单' : active.baselineLocked ? '新建基线版本 / 编辑' : '编辑基线参数'}</span>
                  </button>
                </div>
              </div>

              <div className="grid divide-y divide-line sm:grid-cols-2 sm:divide-y-0 lg:grid-cols-4 lg:divide-x tabular">
                <div className="p-4 lg:p-5">
                  <p className="text-[12px] font-medium text-fg-2">基线期数据库实算总量</p>
                  <p className="mt-1.5 text-[24px] font-semibold leading-[30px] text-fg">
                    {active.baselineActualUsage != null ? fmtNum(active.baselineActualUsage) : '无数据'}
                    {active.baselineActualUsage != null && <span className="ml-1 text-[12px] font-normal text-muted">{active.unit}</span>}
                  </p>
                  <p className="mt-1 text-[11px] text-muted">
                    年化参考 {active.annualizedBaselineUsage != null ? `${fmtNum(active.annualizedBaselineUsage)} ${active.unit}/年` : '—'}
                    {bDays ? ` · 共 ${bDays} 天` : ''}
                  </p>
                </div>

                <div className="p-4 lg:p-5">
                  <div className="flex items-center justify-between gap-2 text-[12px]">
                    <span className="font-medium text-fg-2">基线统计范围与覆盖率</span>
                    <span
                      className={`rounded-[2px] px-1.5 py-0 text-[10px] font-medium ${
                        bCovOk ? 'bg-positive-soft text-positive' : 'bg-warning-soft text-warning'
                      }`}
                    >
                      {bCovOk ? `达标 ≥${fmtPct(minCov)}` : '覆盖不足'}
                    </span>
                  </div>
                  <p className="mt-1.5 text-[24px] font-semibold leading-[30px] text-fg">
                    {fmtPct(active.baselineActualCoverage?.rate)}
                  </p>
                  <p className="mt-1 text-[11px] text-muted">
                    有效 {active.baselineActualCoverage?.valid ?? 0}/{active.baselineActualCoverage?.expected ?? 0} 槽 · 缺 {active.baselineActualCoverage?.missing ?? 0} · 疑 {active.baselineActualCoverage?.suspect ?? 0}
                  </p>
                </div>

                <div className="p-4 lg:p-5">
                  <div className="flex items-center justify-between gap-2 text-[12px]">
                    <span className="font-medium text-fg-2">基线计算模型与拟合度</span>
                    {active.baselineR2 != null && (
                      <span
                        className={`rounded-[2px] px-1.5 py-0 text-[10px] font-medium ${
                          active.baselineR2 >= 0.75 ? 'bg-positive-soft text-positive' : 'bg-warning-soft text-warning'
                        }`}
                      >
                        R² = {active.baselineR2.toFixed(3)}
                      </span>
                    )}
                  </div>
                  <p className="mt-1.5 truncate text-[14px] font-semibold leading-[30px] text-fg" title={active.baselineModel}>
                    {active.baselineModel}
                  </p>
                  <p className="mt-1 text-[11px] text-muted">
                    窗口 {bFromYmd} ~ {bToYmd} · 日均 {fmtNum(active.baselineDailyAvg)} {active.unit}
                  </p>
                </div>

                <div className="bg-fill-1/35 p-4 lg:p-5">
                  <p className="text-[12px] font-medium text-fg-2">调整条件与版本状态</p>
                  <p className="mt-1.5 truncate text-[13px] font-medium leading-[30px] text-fg" title={active.baselineFactors}>
                    {active.baselineFactors || '未填写修正因子'}
                  </p>
                  <p className="mt-1 text-[11px] text-muted">
                    当前 {active.baselineVersion} · 历史归档 {history.length} 个版本
                  </p>
                </div>
              </div>
            </section>

            {/* ── 2. Primary Analytical Stage: Baseline Period Trend + Selection Rationale (8 cols) + Verifiability Gates & Version Diffs (4 cols) ── */}
            <div className="grid items-start gap-4 lg:grid-cols-12">
              <section className="arco-card space-y-4 p-5 lg:col-span-8">
                <div className="flex flex-wrap items-baseline justify-between gap-2">
                  <div>
                    <h3 className="text-[15px] font-medium leading-[22px] text-fg">
                      {baseTrendYear || '基线年度'}年{energyLabels[active.energy] ?? active.energy}月度趋势与所选基线区间
                    </h3>
                    <p className="mt-0.5 text-[12px] text-muted tabular">
                      琥珀色高亮区间为本改造项目选定的基线期（{bFromYmd} ~ {bToYmd}），直观呈现该区间负荷特征与代表性
                    </p>
                  </div>
                </div>

                <TrendAreaChart
                  points={baseTrendPoints}
                  unit={active.unit}
                  wide
                  height={258}
                  seriesLabel={`${baseTrendYear} 年月度实测 (${active.unit})`}
                  referenceValue={monthlyAvgRef}
                  referenceLabel={monthlyAvgRef != null ? `基线折算月均 ${fmtNum(monthlyAvgRef)}` : undefined}
                  highlightIndices={baselineWindowIndices}
                  highlightLabel="选定基线期"
                  xLabelFormatter={(pt) => `${Number(pt.from.slice(5, 7))}月`}
                />

                {/* Why This Baseline Was Chosen + Adjustment Basis */}
                <div className="grid gap-3 border-t border-line pt-4 text-[12px] md:grid-cols-2">
                  <div className="rounded-[4px] bg-fill-1/60 p-3.5">
                    <h4 className="text-[12px] font-medium text-fg">改造边界与基线选择依据</h4>
                    <p className="mt-1.5 text-[12px] leading-relaxed text-fg-2">
                      {active.measureSummary || '暂无改造措施说明。'}
                    </p>
                    <p className="mt-2 text-[11px] text-muted tabular">
                      范围：{active.buildingName}{active.spaceName ? ` / ${active.spaceName}` : '（整栋）'} · 区间 {bFromYmd} ~ {bToYmd}
                    </p>
                  </div>
                  <div className="rounded-[4px] bg-fill-1/60 p-3.5">
                    <h4 className="text-[12px] font-medium text-fg">基线计算模型与调整条件</h4>
                    <dl className="mt-1.5 space-y-1 text-[11px] tabular">
                      <div className="flex justify-between gap-2">
                        <dt className="text-muted">模型方式</dt>
                        <dd className="text-right font-medium text-fg">{active.baselineModel}</dd>
                      </div>
                      <div className="flex justify-between gap-2">
                        <dt className="text-muted">自变量 / 修正因子</dt>
                        <dd className="text-right text-fg-2">{active.baselineFactors}</dd>
                      </div>
                      <div className="flex justify-between gap-2">
                        <dt className="text-muted">回归拟合度 R²</dt>
                        <dd className="text-right font-medium text-fg">
                          {active.baselineR2 != null ? `${active.baselineR2.toFixed(3)}（参考门槛 ≥ 0.75）` : '待建模'}
                        </dd>
                      </div>
                    </dl>
                  </div>
                </div>
              </section>

              {/* Right 4 Cols: Verifiability Readiness Gates + Version Evolution & Diffs */}
              <aside aria-label="可核验门禁与历史版本差异" className="arco-card space-y-4 p-5 text-[12px] tabular lg:col-span-4">
                <div>
                  <div className="mb-2.5 flex items-center justify-between border-b border-line pb-2">
                    <h3 className="text-[14px] font-medium text-fg">M&amp;V 可核验条件核查</h3>
                    <span className={`text-[11px] font-medium ${readyForMV ? 'text-positive' : 'text-warning'}`}>
                      {[bDataOk, bCovOk, bModelOk, active.baselineLocked].filter(Boolean).length} / 4 通过
                    </span>
                  </div>
                  <div className="space-y-1.5 text-[11px]">
                    {[
                      {
                        ok: bDataOk,
                        title: '基线期有效计量实算',
                        detail: bDataOk ? `${fmtNum(active.baselineActualUsage)} ${active.unit}` : '缺失基线期数据',
                      },
                      {
                        ok: bCovOk,
                        title: `基线覆盖率 ≥ ${fmtPct(minCov)}`,
                        detail: fmtPct(baseCovRate),
                      },
                      {
                        ok: bModelOk,
                        title: '基线模型与调整因子',
                        detail: active.baselineR2 != null ? `R² ${active.baselineR2.toFixed(3)}` : '已配置因子',
                      },
                      {
                        ok: active.baselineLocked,
                        title: '基线版本正式锁定',
                        detail: active.baselineLocked ? `已锁定 (${active.baselineVersion})` : '未锁定（请先锁定）',
                      },
                    ].map((g) => (
                      <div key={g.title} className="flex items-center justify-between gap-2 rounded-[2px] bg-fill-1/60 px-2.5 py-2">
                        <span className="flex items-center gap-2 font-medium text-fg">
                          <span className={`size-2 rounded-full ${g.ok ? 'bg-positive' : 'bg-warning'}`} />
                          {g.title}
                        </span>
                        <span className={g.ok ? 'text-fg-2' : 'font-medium text-warning'}>{g.detail}</span>
                      </div>
                    ))}
                  </div>
                </div>

                {/* Version Evolution & Effective Diffs */}
                <div className="border-t border-line pt-3.5">
                  <div className="mb-2 flex items-center justify-between">
                    <h3 className="text-[14px] font-medium text-fg">历史版本演进与有效差异</h3>
                    <span className="text-[11px] text-muted">{history.length} 个版本</span>
                  </div>
                  <div className="space-y-2">
                    {revDiffs.map(({ rev, diffs }, idx) => (
                      <div
                        key={rev.baselineVersion}
                        className={`rounded-[4px] border p-2.5 text-[11px] ${
                          idx === 0 ? 'border-primary/35 bg-primary-soft/25' : 'border-line bg-surface'
                        }`}
                      >
                        <div className="flex items-center justify-between gap-2">
                          <span className="font-mono font-semibold text-fg">{rev.baselineVersion}</span>
                          <span className={rev.baselineLocked ? 'text-positive' : 'text-warning'}>
                            {rev.baselineLocked ? '已锁定' : '未锁定'}
                          </span>
                        </div>
                        <p className="mt-1 text-muted">
                          {fmtShanghaiDate(rev.baselineFrom)} ~ {fmtShanghaiDate(rev.baselineTo)} · 覆盖 {fmtPct(rev.baselineCoverage)}
                        </p>
                        <div className="mt-1.5 flex flex-wrap gap-1">
                          {diffs.map((d, di) => (
                            <span key={di} className="rounded-[2px] bg-fill-2 px-1.5 py-0 text-[10px] text-fg-2">
                              {d}
                            </span>
                          ))}
                        </div>
                        {rev.changeReason && (
                          <p className="mt-1 text-[10px] text-muted">
                            原因：{rev.changeReason}（{rev.actorSub}）
                          </p>
                        )}
                      </div>
                    ))}
                  </div>
                </div>
              </aside>
            </div>

            {/* ── 3. Expandable Edit / New Baseline Version Form (Full Locked-Baseline Protection Preserved!) ── */}
            {showBaseEditForm && (
              <form onSubmit={handleUpdateBaseline} className="arco-card space-y-4 border-primary/35 p-5 text-xs">
                <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line pb-2.5">
                  <h3 className="inline-flex items-center gap-1.5 text-sm font-semibold text-fg">
                    <Wrench className="h-4 w-4 text-primary" aria-hidden="true" />
                    编辑项目信息与基线定义
                  </h3>
                  {active.baselineLocked && (
                    <span className="inline-flex items-center gap-1 text-warning">
                      <ShieldAlert className="h-3.5 w-3.5" aria-hidden="true" />
                      基线 {active.baselineVersion} 已锁定：修改基线参数须勾选下方“创建新基线版本”
                    </span>
                  )}
                </div>
                <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                  <label className="flex flex-col gap-1 text-muted">项目名称<input value={edName} onChange={(e) => setEdName(e.target.value)} required className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg" /></label>
                  <label className="flex flex-col gap-1 text-muted">
                    所属建筑
                    <select value={edBldg} onChange={(e) => { setEdBldg(e.target.value); setEdSpace(''); }} className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg">
                      {buildings.map((b) => <option key={b.buildingId} value={b.buildingId}>{b.name} ({b.buildingId})</option>)}
                    </select>
                  </label>
                  <label className="flex flex-col gap-1 text-muted">
                    改造区域（空间）
                    <select value={edSpace} onChange={(e) => setEdSpace(e.target.value)} className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg">
                      <option value="">整栋建筑范围</option>
                      {spacesFor(edBldg).map((sp) => <option key={sp.spaceId} value={sp.spaceId}>{sp.name} ({sp.spaceId})</option>)}
                    </select>
                  </label>
                  <label className="flex flex-col gap-1 text-muted">
                    能源类型
                    <select value={edEnergy} onChange={(e) => setEdEnergy(e.target.value as EnergyType)} className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg">
                      <option value="electricity">用电 (kWh)</option><option value="water">用水 (m³)</option><option value="gas">燃气 (Nm³)</option><option value="heat">供热 (GJ)</option><option value="cooling">供冷 (GJ)</option>
                    </select>
                  </label>
                  <label className="flex flex-col gap-1 text-muted">基线开始日期<ChineseDateInput value={edBaseFrom} onChange={setEdBaseFrom} required placeholder="年-月-日" ariaLabel="基线开始日期" className="w-full" /></label>
                  <label className="flex flex-col gap-1 text-muted">基线结束日期<ChineseDateInput value={edBaseTo} onChange={setEdBaseTo} required placeholder="年-月-日" ariaLabel="基线结束日期" className="w-full" /></label>
                  <label className="flex flex-col gap-1 text-muted">基线计算方式<input value={edModel} onChange={(e) => setEdModel(e.target.value)} required className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg" /></label>
                  <label className="flex flex-col gap-1 text-muted">拟合度 R²<input type="number" step="0.001" min="0" max="1" value={edR2} onChange={(e) => setEdR2(e.target.value)} className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg tabular" /></label>
                  <label className="flex flex-col gap-1 text-muted sm:col-span-2">改造措施说明<input value={edMeasure} onChange={(e) => setEdMeasure(e.target.value)} required className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg" /></label>
                  <label className="flex flex-col gap-1 text-muted sm:col-span-2">自变量与修正因子<input value={edFactors} onChange={(e) => setEdFactors(e.target.value)} className="rounded-[4px] border border-line bg-surface-2 px-2.5 py-1.5 text-fg" /></label>
                </div>
                {active.baselineLocked && (
                  <div className="space-y-2 rounded-[4px] border border-warning/35 bg-warning-soft/50 p-3">
                    <label className="inline-flex cursor-pointer items-center gap-2 font-medium text-fg">
                      <input type="checkbox" checked={edNewVerCheck} onChange={(e) => setEdNewVerCheck(e.target.checked)} />
                      基于已锁定基线创建新基线版本（保留原版本 {active.baselineVersion} 历史快照）
                    </label>
                    {edNewVerCheck && (
                      <div className="grid gap-2 pt-1 sm:grid-cols-2">
                        <input value={edNewVerName} onChange={(e) => setEdNewVerName(e.target.value)} placeholder="新版本号（留空自动递增）" className="rounded-[4px] border border-line bg-surface px-2.5 py-1.5 text-fg" />
                        <input value={edChangeReason} onChange={(e) => setEdChangeReason(e.target.value)} required placeholder="变更原因（必填，记入历史台账）" className="rounded-[4px] border border-line bg-surface px-2.5 py-1.5 text-fg" />
                      </div>
                    )}
                  </div>
                )}
                <div className="flex justify-end gap-2">
                  <button type="button" onClick={() => setShowBaseEditForm(false)} className="rounded-[4px] border border-line px-3.5 py-1.5 text-fg hover:bg-surface-2">收起</button>
                  <button type="submit" disabled={busy} className="rounded-[4px] bg-primary px-4 py-1.5 font-medium text-white">
                    {edNewVerCheck ? '发布新基线版本' : '保存修改'}
                  </button>
                </div>
              </form>
            )}

            {/* ── 4. Collapsible Historical Baseline Version Ledger ── */}
            <CollapsibleSection title="历史基线版本完整记录" badge={`${history.length} 条`} subtitle="按需展开查看全字段归档快照与审计操作人">
              <div className="overflow-x-auto">
                <table className="w-full text-left text-xs">
                  <thead className="border-b border-line bg-surface-2 text-muted">
                    <tr>
                      <th className="px-3.5 py-2">版本号</th>
                      <th className="px-3.5 py-2">统计周期</th>
                      <th className="px-3.5 py-2">计算方式</th>
                      <th className="px-3.5 py-2 text-right">基线实算 / 覆盖率</th>
                      <th className="px-3.5 py-2">锁定状态</th>
                      <th className="px-3.5 py-2">变更原因与操作人</th>
                      <th className="px-3.5 py-2">记录时间</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-line tabular">
                    {history.map((rev) => (
                      <tr key={rev.baselineVersion}>
                        <td className="whitespace-nowrap px-3.5 py-2 font-mono font-medium text-fg">{rev.baselineVersion}</td>
                        <td className="whitespace-nowrap px-3.5 py-2 text-muted">{fmtShanghaiDate(rev.baselineFrom)} ~ {fmtShanghaiDate(rev.baselineTo)}</td>
                        <td className="px-3.5 py-2 text-muted">{rev.baselineModel}</td>
                        <td className="whitespace-nowrap px-3.5 py-2 text-right text-fg">{rev.baselineActualUsage != null ? `${fmtNum(rev.baselineActualUsage)} ${active.unit}` : '—'} · {fmtPct(rev.baselineCoverage)}</td>
                        <td className="whitespace-nowrap px-3.5 py-2"><span className={rev.baselineLocked ? 'text-positive' : 'text-warning'}>{rev.baselineLocked ? '已锁定' : '未锁定'}</span></td>
                        <td className="px-3.5 py-2 text-muted">{rev.changeReason || '—'}（{rev.actorSub}）</td>
                        <td className="whitespace-nowrap px-3.5 py-2 text-muted">{rev.recordedAt.slice(0, 19).replace('T', ' ')}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </CollapsibleSection>
          </div>
        );
      })()}

      {/* ═══════════════════ F19: 3-SCENARIO POTENTIAL & ECONOMICS WORKBENCH ═══════════════════ */}
      {active && mode === 'retrofit-potential' && (() => {
        // Live interactive parameter parsing so charts & KPIs react immediately as parameters are adjusted.
        const pLow = Math.max(0, Number(scLow) || 0);
        const pBase = Math.max(0, Number(scBase) || 0);
        const pHigh = Math.max(0, Number(scHigh) || 0);
        const pPrice = scPrice.trim() !== '' && Number(scPrice) > 0 ? Number(scPrice) : 0;
        const pCapex = scCapex.trim() !== '' && Number(scCapex) > 0 ? Number(scCapex) : 0;
        const pPriceConfigured = pPrice > 0;

        const isDirty =
          pLow !== active.estSavingsLow ||
          pBase !== active.estSavingsBase ||
          pHigh !== active.estSavingsHigh ||
          pPrice !== (active.refUnitPrice || 0) ||
          pCapex !== (active.capexBudget || 0);

        const annBase = active.annualizedBaselineUsage != null && active.annualizedBaselineUsage > 0 ? active.annualizedBaselineUsage : null;
        const calcRate = (sav: number, serverVal?: number | null) =>
          !isDirty && serverVal != null ? serverVal : annBase != null ? sav / annBase : null;
        const calcCost = (sav: number, serverVal?: number | null) =>
          !isDirty && active.priceConfigured && serverVal != null ? serverVal : pPriceConfigured && sav > 0 ? sav * pPrice : null;
        const calcPayback = (cost: number | null, serverVal?: number | null) =>
          !isDirty && serverVal != null ? serverVal : pCapex > 0 && cost != null && cost > 0 ? Number((pCapex / cost).toFixed(2)) : null;

        const rateLow = calcRate(pLow, active.estSavingsRateLow);
        const rateBase = calcRate(pBase, active.estSavingsRateBase);
        const rateHigh = calcRate(pHigh, active.estSavingsRateHigh);

        const costLow = calcCost(pLow, active.estAnnualCostLow);
        const costBase = calcCost(pBase, active.estAnnualCostBase);
        const costHigh = calcCost(pHigh, active.estAnnualCostHigh);

        const pbLow = calcPayback(costLow, active.staticPaybackLowYears);
        const pbBase = calcPayback(costBase, active.staticPaybackYears);
        const pbHigh = calcPayback(costHigh, active.staticPaybackHighYears);

        const postBaseUsage = annBase != null ? Math.max(0, annBase - pBase) : null;

        return (
          <div className="space-y-4">
            {/* ── 1. Simulated Economics Governance Banner + 4-Col Synchronized KPI Strip ── */}
            <section className="arco-card overflow-hidden">
              <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line bg-fill-1/50 px-5 py-2.5 text-[12px] tabular">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="rounded-[2px] bg-warning-soft px-2 py-0.5 text-[11px] font-medium text-warning">
                    模拟测算结果 · 非实际核验收益
                  </span>
                  <span className="font-medium text-fg">
                    {active.name} · {active.buildingName} · {energyLabels[active.energy] ?? active.energy} ({active.unit})
                  </span>
                  {isDirty && (
                    <span className="rounded-[2px] bg-primary-soft px-2 py-0.5 text-[11px] font-medium text-primary">
                      参数实时预览中（点击下方保存持久化）
                    </span>
                  )}
                </div>
                <span className="text-[11px] text-muted">
                  公式：年参考节费 = 预计年节能量 × 能源单价 · 静态回收期 = 改造投资 ÷ 年参考节费
                </span>
              </div>

              <div className="grid divide-y divide-line sm:grid-cols-2 sm:divide-y-0 lg:grid-cols-4 lg:divide-x tabular">
                <div className="p-4 lg:p-5">
                  <p className="text-[12px] font-medium text-fg-2">改造前年化基线用能</p>
                  <p className="mt-1.5 text-[24px] font-semibold leading-[30px] text-fg">
                    {annBase != null ? fmtNum(annBase) : <span className="text-[16px] text-warning">缺少有效基线</span>}
                    {annBase != null && <span className="ml-1 text-[12px] font-normal text-muted">{active.unit}/年</span>}
                  </p>
                  <p className="mt-1 text-[11px] text-muted">
                    基线版本 {active.baselineVersion} · {fmtShanghaiDate(active.baselineFrom)} ~ {fmtShanghaiDate(active.baselineTo)}
                  </p>
                </div>

                <div className="p-4 lg:p-5">
                  <div className="flex items-center justify-between gap-2 text-[12px]">
                    <span className="font-medium text-fg-2">基准情景预计年节能量</span>
                    {rateBase != null && (
                      <span className="rounded-[2px] bg-positive-soft px-1.5 py-0 text-[10px] font-medium text-positive">
                        节能率 {fmtPct(rateBase)}
                      </span>
                    )}
                  </div>
                  <p className="mt-1.5 text-[24px] font-semibold leading-[30px] text-primary">
                    {fmtNum(pBase)}
                    <span className="ml-1 text-[12px] font-normal text-muted">{active.unit}/年</span>
                  </p>
                  <p className="mt-1 text-[11px] text-muted">
                    {postBaseUsage != null
                      ? `改造后预计年用能 ${fmtNum(postBaseUsage)} ${active.unit}`
                      : '缺少有效基线，不计算节能率'}
                  </p>
                </div>

                <div className="p-4 lg:p-5">
                  <p className="text-[12px] font-medium text-fg-2">基准年参考节费（模拟测算）</p>
                  <p className="mt-1.5 text-[24px] font-semibold leading-[30px] text-fg">
                    {costBase != null ? `¥${fmtNum(Math.round(costBase))}` : <span className="text-[16px] text-warning">不可计算</span>}
                  </p>
                  <p className="mt-1 text-[11px] text-muted">
                    {pPriceConfigured ? `参考单价 ¥${pPrice}/${active.unit}` : '缺少能源单价（不以 0 代替）'}
                    {' · '}投资 {pCapex > 0 ? `¥${fmtNum(pCapex)}` : '未配置'}
                  </p>
                </div>

                <div className="bg-fill-1/35 p-4 lg:p-5">
                  <p className="text-[12px] font-medium text-fg-2">静态投资回收期（模拟）</p>
                  <p className="mt-1.5 text-[24px] font-semibold leading-[30px] text-fg">
                    {pbBase != null ? (
                      <>
                        {pbBase.toFixed(2)} <span className="text-[13px] font-normal text-muted">年</span>
                      </>
                    ) : (
                      <span className="text-[16px] text-warning">不可计算</span>
                    )}
                  </p>
                  <p className="mt-1 text-[11px] text-muted">
                    {pbHigh != null && pbLow != null
                      ? `乐观 ${pbHigh.toFixed(2)} 年 ~ 保守 ${pbLow.toFixed(2)} 年`
                      : '需配置单价、投资额及节能量'}
                  </p>
                </div>
              </div>
            </section>

            {/* ── 2. Primary Stage: 3-Scenario Visuals (8 cols) + Live Synchronized Parameter Controls (4 cols) ── */}
            <div className="grid items-start gap-4 lg:grid-cols-12">
              <section className="arco-card space-y-5 p-5 lg:col-span-8">
                <div>
                  <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
                    <div>
                      <h3 className="text-[15px] font-medium leading-[22px] text-fg">三情景累计参考节费与静态投资回收期曲线</h3>
                      <p className="mt-0.5 text-[12px] text-muted">
                        保守 / 基准 / 乐观三条累计节费曲线与改造投资门槛（橙色虚线）的交点即为各情景静态回收期
                      </p>
                    </div>
                  </div>
                  <RetrofitEconomicsChart
                    capex={pCapex}
                    costLow={costLow}
                    costBase={costBase}
                    costHigh={costHigh}
                    paybackLow={pbLow}
                    paybackBase={pbBase}
                    paybackHigh={pbHigh}
                    priceConfigured={pPriceConfigured}
                    height={244}
                  />
                </div>

                <div className="border-t border-line pt-4">
                  <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
                    <h3 className="text-[14px] font-medium text-fg">三情景年节能量区间对照（{active.unit}/年）</h3>
                    <span className="text-[11px] text-muted">同步响应右侧参数调整</span>
                  </div>
                  <RetrofitScenarioChart
                    low={pLow}
                    base={pBase}
                    high={pHigh}
                    costLow={costLow}
                    costBase={costBase}
                    costHigh={costHigh}
                    paybackLow={pbLow}
                    paybackBase={pbBase}
                    paybackHigh={pbHigh}
                    unit={active.unit}
                  />
                </div>
              </section>

              {/* Right 4 Cols: Live Synchronized Parameter Sensitivity Panel (Always Accessible!) */}
              <aside aria-label="关键测算参数联动调整" className="arco-card p-5 text-[12px] lg:col-span-4">
                <form onSubmit={handleUpdateScenarios} className="space-y-3.5 tabular">
                  <div className="border-b border-line pb-2.5">
                    <h3 className="text-[14px] font-medium text-fg">关键测算参数实时调整</h3>
                    <p className="mt-0.5 text-[11px] text-muted">
                      修改参数后左侧图表与指标立即联动预览；留空单价或投资额将如实显示“不可计算”
                    </p>
                  </div>

                  <div className="space-y-2.5">
                    <label className="flex flex-col gap-1 text-[11px] text-fg-2">
                      <span>保守情景年节能量 ({active.unit})</span>
                      <input
                        data-sc-input="low"
                        type="number"
                        step="any"
                        min="0"
                        value={scLow}
                        onChange={(e) => setScLow(e.target.value)}
                        className="arco-input w-full tabular"
                      />
                    </label>
                    <label className="flex flex-col gap-1 text-[11px] text-fg-2">
                      <span>基准情景年节能量 ({active.unit})</span>
                      <input
                        data-sc-input="base"
                        type="number"
                        step="any"
                        min="0"
                        value={scBase}
                        onChange={(e) => setScBase(e.target.value)}
                        className="arco-input w-full tabular"
                      />
                    </label>
                    <label className="flex flex-col gap-1 text-[11px] text-fg-2">
                      <span>乐观情景年节能量 ({active.unit})</span>
                      <input
                        data-sc-input="high"
                        type="number"
                        step="any"
                        min="0"
                        value={scHigh}
                        onChange={(e) => setScHigh(e.target.value)}
                        className="arco-input w-full tabular"
                      />
                    </label>
                    <div className="grid grid-cols-2 gap-2.5 border-t border-line pt-2.5">
                      <label className="flex flex-col gap-1 text-[11px] text-fg-2">
                        <span>能源单价 (元/{active.unit})</span>
                        <input
                          data-sc-input="price"
                          type="number"
                          step="any"
                          min="0"
                          value={scPrice}
                          onChange={(e) => setScPrice(e.target.value)}
                          placeholder="留空不可计算"
                          className="arco-input w-full tabular"
                        />
                      </label>
                      <label className="flex flex-col gap-1 text-[11px] text-fg-2">
                        <span>改造投资预算 (元)</span>
                        <input
                          data-sc-input="capex"
                          type="number"
                          step="any"
                          min="0"
                          value={scCapex}
                          onChange={(e) => setScCapex(e.target.value)}
                          placeholder="留空不可计算"
                          className="arco-input w-full tabular"
                        />
                      </label>
                    </div>
                  </div>

                  {/* Transparent Formula Breakdown */}
                  <div className="rounded-[4px] bg-fill-1/60 p-3 text-[11px] text-muted space-y-1">
                    <div className="font-medium text-fg-2">基准情景计算链路（模拟）</div>
                    <div>
                      预计年节费 = {fmtNum(pBase)} × {pPriceConfigured ? `¥${pPrice}` : '缺单价'} ={' '}
                      <strong className="text-fg">{costBase != null ? `¥${fmtNum(Math.round(costBase))}` : '不可计算'}</strong>
                    </div>
                    <div>
                      静态回收期 = {pCapex > 0 ? `¥${fmtNum(pCapex)}` : '缺投资'} ÷{' '}
                      {costBase != null ? `¥${fmtNum(Math.round(costBase))}` : '缺节费'} ={' '}
                      <strong className="text-fg">{pbBase != null ? `${pbBase.toFixed(2)} 年` : '不可计算'}</strong>
                    </div>
                  </div>

                  <div className="flex items-center justify-end gap-2 pt-1">
                    {isDirty && (
                      <button
                        type="button"
                        onClick={() => {
                          setScLow(String(active.estSavingsLow));
                          setScBase(String(active.estSavingsBase));
                          setScHigh(String(active.estSavingsHigh));
                          setScPrice(active.refUnitPrice > 0 ? String(active.refUnitPrice) : '');
                          setScCapex(active.capexBudget > 0 ? String(active.capexBudget) : '');
                        }}
                        className="arco-btn !h-8 text-[12px]"
                      >
                        重置
                      </button>
                    )}
                    <button
                      type="submit"
                      data-sc-save
                      disabled={busy}
                      className="h-8 rounded-[2px] bg-primary px-4 text-[12px] font-medium text-white hover:bg-primary/90 disabled:opacity-50"
                    >
                      保存测算参数并写入
                    </button>
                  </div>
                </form>
              </aside>
            </div>

            {/* ── 3. Detailed 3-Scenario Comparison Matrix ── */}
            <section className="arco-card overflow-x-auto">
              <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line px-5 py-3">
                <h3 className="text-[14px] font-medium text-fg">三情景差异分析明细表（模拟测算）</h3>
                <span className="text-[11px] text-muted">所有经济性测算结果均为模型估算，正式核验节能量以 F20 M&amp;V 为准</span>
              </div>
              <table className="w-full text-left text-[12px]">
                <thead className="border-b border-line bg-fill-1 text-fg-2">
                  <tr>
                    <th className="px-4 py-2.5 font-medium">评估情景</th>
                    <th className="px-4 py-2.5 text-right font-medium">预计年节能量 ({active.unit})</th>
                    <th className="px-4 py-2.5 text-right font-medium">改造后预计年用能 ({active.unit})</th>
                    <th className="px-4 py-2.5 text-right font-medium">预计节能率</th>
                    <th className="px-4 py-2.5 text-right font-medium">年度参考节省费用</th>
                    <th className="px-4 py-2.5 text-right font-medium">静态投资回收期</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-line tabular">
                  {[
                    { label: '保守情景 (Low)', sav: pLow, rate: rateLow, cost: costLow, pb: pbLow, accent: false },
                    { label: '基准情景 (Base · 推荐)', sav: pBase, rate: rateBase, cost: costBase, pb: pbBase, accent: true },
                    { label: '乐观情景 (High)', sav: pHigh, rate: rateHigh, cost: costHigh, pb: pbHigh, accent: false },
                  ].map((sc) => (
                    <tr key={sc.label} className={sc.accent ? 'bg-primary-soft/25' : ''}>
                      <td className="px-4 py-2.5 font-medium text-fg">{sc.label}</td>
                      <td className="px-4 py-2.5 text-right font-semibold text-fg">{fmtNum(sc.sav)}</td>
                      <td className="px-4 py-2.5 text-right text-fg-2">
                        {annBase != null ? fmtNum(Math.max(0, annBase - sc.sav)) : <span className="text-warning">缺少有效基线</span>}
                      </td>
                      <td className="px-4 py-2.5 text-right text-fg-2">
                        {sc.rate != null ? fmtPct(sc.rate) : <span className="text-warning">缺少基线</span>}
                      </td>
                      <td className="px-4 py-2.5 text-right font-medium text-fg">
                        {pPriceConfigured && sc.cost != null ? `¥${fmtNum(Math.round(sc.cost))}` : <span className="text-warning">不可计算（缺单价）</span>}
                      </td>
                      <td className="px-4 py-2.5 text-right font-semibold text-primary">
                        {sc.pb != null ? `${sc.pb.toFixed(2)} 年` : <span className="font-normal text-warning">不可计算</span>}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </section>
          </div>
        );
      })()}

      {/* Collapsible Full Project Ledger (accessible on all 3 retrofit pages without crowding hero) */}
      <CollapsibleSection title="全部改造项目台账" badge={`${projects.length} 项`} subtitle="点击行快速切换当前分析对象">
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs">
            <thead className="border-b border-line bg-surface-2 text-muted">
              <tr>
                <th className="whitespace-nowrap px-3.5 py-2">改造项目</th>
                <th className="whitespace-nowrap px-3.5 py-2">建筑 / 区域</th>
                <th className="whitespace-nowrap px-3.5 py-2">能源</th>
                <th className="whitespace-nowrap px-3.5 py-2">基线版本</th>
                <th className="whitespace-nowrap px-3.5 py-2 text-right">基线实算</th>
                <th className="whitespace-nowrap px-3.5 py-2 text-right">基线 / 报告覆盖率</th>
                <th className="whitespace-nowrap px-3.5 py-2">锁定</th>
                <th className="whitespace-nowrap px-3.5 py-2">M&V 状态</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line tabular">
              {projects.map((p) => (
                <tr
                  key={p.retrofitId}
                  onClick={() => setSelectedId(p.retrofitId)}
                  className={`cursor-pointer ${active?.retrofitId === p.retrofitId ? 'bg-primary-soft/60' : 'hover:bg-surface-2'}`}
                >
                  <td className="px-3.5 py-2 font-medium text-fg">{p.name} <span className="text-muted">({p.retrofitId})</span></td>
                  <td className="whitespace-nowrap px-3.5 py-2 text-muted">{p.buildingName}{p.spaceName ? ` / ${p.spaceName}` : ''}</td>
                  <td className="whitespace-nowrap px-3.5 py-2 text-fg">{energyLabels[p.energy] ?? p.energy}</td>
                  <td className="whitespace-nowrap px-3.5 py-2 text-muted">{p.baselineVersion}</td>
                  <td className="whitespace-nowrap px-3.5 py-2 text-right text-fg">{fmtNum(p.baselineActualUsage)} {p.unit}</td>
                  <td className="whitespace-nowrap px-3.5 py-2 text-right text-muted">{fmtPct(p.baselineActualCoverage?.rate)} / {fmtPct(p.reportActualCoverage?.rate)}</td>
                  <td className="whitespace-nowrap px-3.5 py-2">{p.baselineLocked ? <span className="text-positive">已锁定</span> : <span className="text-warning">未锁定</span>}</td>
                  <td className="whitespace-nowrap px-3.5 py-2 text-muted">{mvStatusShort[p.mvStatus] ?? p.mvStatus}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </CollapsibleSection>
    </div>
  );
}
