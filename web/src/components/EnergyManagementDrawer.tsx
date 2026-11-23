import { useState } from 'react';
import * as Dialog from '@radix-ui/react-dialog';
import { History, Power, X } from 'lucide-react';
import { ChineseDateInput } from './EnergyWorkspaceChrome';
import {
  liveGet,
  livePost,
  type ChannelStatus,
  type EnergyType,
  type InventoryData,
  type MeasurementKind,
  type MeterCandidate,
  type MeteringPointHistory,
  type SourceTable,
} from '../api/energyLive';

type TabKey = 'meters' | 'categories' | 'objects' | 'topology' | 'events' | 'governance';

const statusBadge: Record<ChannelStatus, { label: string; cls: string }> = {
  configured: { label: '已配置·启用', cls: 'border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300' },
  disabled: { label: '已停用', cls: 'border-slate-400/40 bg-slate-500/10 text-muted' },
  unconfigured: { label: '待配置', cls: 'border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300' },
  excluded_non_energy: { label: '已排除非能耗项', cls: 'border-red-500/40 bg-red-500/10 text-red-700 dark:text-red-300' },
};

const energyText: Record<string, string> = {
  electricity: '用电',
  water: '用水',
  gas: '燃气',
  heat: '供热',
  cooling: '供冷',
  steam: '蒸汽',
};

const kindText: Record<string, string> = {
  cumulative: '累计量',
  interval: '区间量',
  instantaneous: '瞬时量',
};

export function EnergyManagementDrawer({
  open,
  onOpenChange,
  token,
  inventory,
  onChanged,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  token: string;
  inventory: InventoryData | null;
  onChanged: () => void;
}) {
  const [tab, setTab] = useState<TabKey>('meters');
  const [busy, setBusy] = useState(false);
  const [feedback, setFeedback] = useState<{ ok: boolean; text: string } | null>(null);

  // Tab 1: Metering Point Publish Form
  const [mpTerminal, setMpTerminal] = useState('');
  const [mpItem, setMpItem] = useState('total_usage');
  const [mpSourceTable, setMpSourceTable] = useState<SourceTable>('snapshot_coarse');
  const [mpEnergy, setMpEnergy] = useState<EnergyType>('electricity');
  const [mpKind, setMpKind] = useState<MeasurementKind>('cumulative');
  const [mpUnit, setMpUnit] = useState('kWh');
  const [mpMult, setMpMult] = useState('1');
  const [mpCadence, setMpCadence] = useState('3600');
  const [mpModulus, setMpModulus] = useState('');
  const [mpEpoch, setMpEpoch] = useState('epoch-v1');
  const [mpEffFrom, setMpEffFrom] = useState('2025-01-01');
  const [mpEffTo, setMpEffTo] = useState('');
  const [mpBldg, setMpBldg] = useState('');
  const [mpSpace, setMpSpace] = useState('');
  const [mpOrg, setMpOrg] = useState('');
  const [mpPreserve, setMpPreserve] = useState(true);
  const [mpReason, setMpReason] = useState('前端配置中心发布计量点口径');
  const [historyData, setHistoryData] = useState<MeteringPointHistory | null>(null);

  // Tab 2: Category & Category Binding
  const [catId, setCatId] = useState('');
  const [catParent, setCatParent] = useState('');
  const [catName, setCatName] = useState('');
  const [catEnergy, setCatEnergy] = useState<EnergyType>('electricity');

  const [cbTerminal, setCbTerminal] = useState('');
  const [cbItem, setCbItem] = useState('total_usage');
  const [cbRev, setCbRev] = useState('');
  const [cbEffFrom, setCbEffFrom] = useState('2025-01-01');
  const [cbAllocText, setCbAllocText] = useState('elec-lighting:1000000');

  // Tab 3: Building / Space / Org
  const [bldgId, setBldgId] = useState('bldg-a');
  const [bldgRev, setBldgRev] = useState('');
  const [bldgName, setBldgName] = useState('');
  const [bldgType, setBldgType] = useState('office');
  const [bldgRegion, setBldgRegion] = useState('上海');
  const [bldgArea, setBldgArea] = useState('');
  const [bldgPeople, setBldgPeople] = useState('');
  const [bldgEffFrom, setBldgEffFrom] = useState('2025-01-01');
  const [bldgEffTo, setBldgEffTo] = useState('');

  const [spId, setSpId] = useState('');
  const [spBldg, setSpBldg] = useState('bldg-a');
  const [spParent, setSpParent] = useState('');
  const [spName, setSpName] = useState('');
  const [spArea, setSpArea] = useState('');

  const [orgId, setOrgId] = useState('');
  const [orgParent, setOrgParent] = useState('');
  const [orgName, setOrgName] = useState('');
  const [orgHeadcount, setOrgHeadcount] = useState('');

  // Tab 4: Topology
  const [topoVer, setTopoVer] = useState('');
  const [topoBldg, setTopoBldg] = useState('bldg-a');
  const [topoEnergy, setTopoEnergy] = useState<EnergyType>('electricity');
  const [topoEffFrom, setTopoEffFrom] = useState('2025-01-01');
  const [topoParent, setTopoParent] = useState('');
  const [topoChildrenText, setTopoChildrenText] = useState('');

  // Tab 5: Events
  const [evTerminal, setEvTerminal] = useState('');
  const [evItem, setEvItem] = useState('total_usage');
  const [evKind, setEvKind] = useState('meter_swap');
  const [evAt, setEvAt] = useState('2026-11-17T00:00');
  const [evEpoch, setEvEpoch] = useState('');
  const [evReason, setEvReason] = useState('');

  // Tab 6: Targets / Rules / Denominators / Benchmarks / F15 Assessment
  const [tgtBldg, setTgtBldg] = useState('bldg-a');
  const [tgtEnergy, setTgtEnergy] = useState<EnergyType>('electricity');
  const [tgtYear, setTgtYear] = useState('2026');
  const [tgtUnit, setTgtUnit] = useState('kWh');
  const [tgtValue, setTgtValue] = useState('1250000');

  const [arBldg, setArBldg] = useState('bldg-a');
  const [arEnergy, setArEnergy] = useState<EnergyType>('electricity');
  const [arHist, setArHist] = useState('2');
  const [arAbs, setArAbs] = useState('200');
  const [arRel, setArRel] = useState('0.10');

  const [dnBldg, setDnBldg] = useState('bldg-a');
  const [dnKind, setDnKind] = useState('revenue');
  const [dnVal, setDnVal] = useState('');
  const [dnUnit, setDnUnit] = useState('元');
  const [dnFrom, setDnFrom] = useState('2026-11-17');
  const [dnTo, setDnTo] = useState('2026-11-18');

  const [bmId, setBmId] = useState('demo-office-area-day');
  const [bmName, setBmName] = useState('演示参考阈值');
  const [bmRegion, setBmRegion] = useState('上海');
  const [bmMetricId, setBmMetricId] = useState('office-electricity-area');
  const [bmUnit, setBmUnit] = useState('kWh/m²');
  const [bmKind, setBmKind] = useState<'demo' | 'external'>('demo');
  const [bmMax, setBmMax] = useState('0.30');

  const [asId, setAsId] = useState('assess-custom-1');
  const [asObjKind, setAsObjKind] = useState('organization');
  const [asObjId, setAsObjId] = useState('org-a-rd');
  const [asEnergy, setAsEnergy] = useState<EnergyType>('electricity');
  const [asLabel, setAsLabel] = useState('自定义组织日用电考核');
  const [asTarget, setAsTarget] = useState('1200');
  const [asWeight, setAsWeight] = useState('40');
  const [asFormula, setAsFormula] = useState('实际用电 ≤ 目标值获满分，每超 1% 扣 2 分');

  const runAction = async (fn: () => Promise<{ error?: string }>, successText: string) => {
    setBusy(true);
    setFeedback(null);
    try {
      const res = await fn();
      if (res.error) {
        setFeedback({ ok: false, text: res.error });
      } else {
        setFeedback({ ok: true, text: successText });
        onChanged();
      }
    } finally {
      setBusy(false);
    }
  };

  const selectMeterToEdit = (m: MeterCandidate) => {
    setMpTerminal(m.terminalId);
    setMpItem(m.itemCode);
    setMpSourceTable(m.sourceTable ?? (m.hourRows > 0 ? 'snapshot_coarse' : 'raw_item'));
    setMpEnergy(m.energy ?? m.suggestedEnergy ?? 'electricity');
    setMpKind(m.measurementKind && m.measurementKind !== 'instantaneous' ? m.measurementKind : (m.suggestedKind === 'interval' ? 'interval' : 'cumulative'));
    setMpUnit(m.unit || 'kWh');
    setMpMult(String(m.multiplier && m.multiplier > 0 ? m.multiplier : 1));
    setMpCadence(String(m.cadenceSeconds && m.cadenceSeconds > 0 ? m.cadenceSeconds : 3600));
    setMpModulus(m.registerModulus ? String(m.registerModulus) : '');
    setMpEpoch(m.registerEpoch || 'epoch-v1');
    setMpEffFrom(m.effectiveFrom ? m.effectiveFrom.slice(0, 10) : '2025-01-01');
    setMpEffTo(m.effectiveTo ? m.effectiveTo.slice(0, 10) : '');
    setMpBldg(m.boundBuildingId ?? '');
    setMpSpace(m.boundSpaceId ?? '');
    setMpOrg(m.boundOrgId ?? '');
  };

  const loadHistory = async (terminalId: string, itemCode: string) => {
    const res = await liveGet<MeteringPointHistory>(
      `/management/metering-points/history?terminalId=${encodeURIComponent(terminalId)}&itemCode=${encodeURIComponent(itemCode)}`,
      token,
    );
    if (res.data) setHistoryData(res.data);
    else if (res.error) setFeedback({ ok: false, text: res.error });
  };

  const toISO = (d: string) => (d.trim() ? new Date(`${d.trim()}T00:00:00Z`).toISOString() : undefined);

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/40" />
        <Dialog.Content
          className="fixed inset-y-0 right-0 z-50 flex h-full w-full flex-col border-l border-line bg-surface shadow-xl outline-none lg:w-[860px]"
          aria-describedby={undefined}
        >
          <div className="flex items-start justify-between gap-4 border-b border-line px-5 py-4">
            <div className="min-w-0">
              <Dialog.Title className="text-base font-semibold text-fg">
                建筑能耗配置中心（计量点 · 分类 · 建筑/空间/组织 · 总分表 · 目标定额）
              </Dialog.Title>
              <p className="mt-1 text-xs text-muted">
                {inventory?.energyDomainReady
                  ? `energy_domain 已就绪 · 已配置启用 ${inventory.configuredMeterCount} / 待配置 ${inventory.unconfiguredCount} / 停用 ${inventory.disabledMeterCount} / 已排除非能耗项 ${inventory.excludedChannelCount}`
                  : 'energy_domain 增量表尚未在当前库执行，仅展示只读台账；写入管理操作需在本地开发库执行迁移后启用。'}
              </p>
            </div>
            <Dialog.Close asChild>
              <button type="button" aria-label="关闭配置中心" className="rounded-md p-1.5 text-muted hover:bg-canvas hover:text-fg">
                <X className="h-4 w-4" />
              </button>
            </Dialog.Close>
          </div>

          <div className="flex flex-wrap gap-1 border-b border-line bg-canvas px-4 py-2 text-xs">
            {(
              [
                ['meters', '1. 计量点配置'],
                ['categories', '2. 能源分类与分摊'],
                ['objects', '3. 建筑·空间·组织'],
                ['topology', '4. 总分表拓扑'],
                ['events', '5. 换表与质量事件'],
                ['governance', '6. 目标·规则·分母·定额'],
              ] as Array<[TabKey, string]>
            ).map(([k, label]) => (
              <button
                key={k}
                type="button"
                onClick={() => { setTab(k); setFeedback(null); }}
                className={`rounded-md px-3 py-1.5 font-medium ${tab === k ? 'bg-primary text-white' : 'text-muted hover:bg-surface hover:text-fg'}`}
              >
                {label}
              </button>
            ))}
          </div>

          {feedback && (
            <div
              role="status"
              className={`mx-5 mt-3 rounded-[4px] border px-3 py-2 text-xs ${
                feedback.ok
                  ? 'border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
                  : 'border-red-500/40 bg-red-500/10 text-red-700 dark:text-red-300'
              }`}
            >
              {feedback.text}
            </div>
          )}

          <div className="flex-1 space-y-4 overflow-y-auto p-5 text-xs">
            {/* TAB 1: METERING POINTS */}
            {tab === 'meters' && (
              <>
                <div className="space-y-2">
                  <h3 className="text-sm font-semibold text-fg">物模型通道与计量点台账（点击“配置”载入编辑表单）</h3>
                  <div className="overflow-x-auto rounded-[4px] border border-line">
                    <table className="w-full text-left">
                      <thead className="border-b border-line bg-canvas text-muted">
                        <tr>
                          <th className="px-3 py-2">终端 / 通道</th>
                          <th className="px-3 py-2">P0 状态</th>
                          <th className="px-3 py-2">口径与来源表</th>
                          <th className="px-3 py-2">归属（建/区/组）</th>
                          <th className="px-3 py-2 text-right">操作</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-line tabular-nums">
                        {(inventory?.meters ?? []).map((m) => {
                          const badge = statusBadge[m.channelStatus] ?? statusBadge.unconfigured;
                          return (
                            <tr key={`${m.terminalId}:${m.itemCode}`}>
                              <td className="px-3 py-2">
                                <div className="font-medium text-fg">{m.terminalName}</div>
                                <div className="text-[11px] text-muted">{m.terminalId} / {m.itemCode}（{m.itemName}）</div>
                              </td>
                              <td className="px-3 py-2">
                                <span className={`inline-block rounded-full border px-2 py-0.5 text-[11px] ${badge.cls}`}>{badge.label}</span>
                                {m.exclusionReason && <div className="mt-0.5 text-[11px] text-red-600">{m.exclusionReason}</div>}
                              </td>
                              <td className="px-3 py-2 text-muted">
                                {m.configured ? (
                                  <>
                                    <div>{energyText[m.energy ?? ''] ?? m.energy} · {kindText[m.measurementKind ?? ''] ?? m.measurementKind} · {m.unit} · {m.multiplier}×</div>
                                    <div className="text-[11px]">{m.sourceTable} · {m.cadenceSeconds}s · gen {m.generation}</div>
                                  </>
                                ) : (
                                  <div>未配置正式口径（禁止入账）· 小时 {m.hourRows} / 流水 {m.rawRows}</div>
                                )}
                              </td>
                              <td className="px-3 py-2 text-muted">
                                {m.boundBuildingId || '—'} / {m.boundSpaceId || '—'} / {m.boundOrgId || '—'}
                              </td>
                              <td className="px-3 py-2 text-right">
                                <div className="inline-flex items-center gap-1">
                                  {m.channelStatus !== 'excluded_non_energy' && (
                                    <button
                                      type="button"
                                      onClick={() => selectMeterToEdit(m)}
                                      className="rounded border border-line bg-canvas px-2 py-1 text-[11px] font-medium text-fg hover:bg-surface"
                                    >
                                      配置
                                    </button>
                                  )}
                                  {m.configured && (
                                    <>
                                      <button
                                        type="button"
                                        title="切换启用/停用"
                                        disabled={busy}
                                        onClick={() =>
                                          runAction(
                                            () =>
                                              livePost('/management/metering-points/status', token, {
                                                terminalId: m.terminalId,
                                                itemCode: m.itemCode,
                                                enabled: !m.pointEnabled,
                                                reason: m.pointEnabled ? '配置中心停用' : '配置中心重新启用',
                                              }),
                                            `${m.terminalId}/${m.itemCode} 已${m.pointEnabled ? '停用' : '启用'}`,
                                          )
                                        }
                                        className="rounded border border-line bg-canvas p-1 text-muted hover:text-fg"
                                      >
                                        <Power className="h-3.5 w-3.5" />
                                      </button>
                                      <button
                                        type="button"
                                        title="查看历史版本"
                                        onClick={() => loadHistory(m.terminalId, m.itemCode)}
                                        className="rounded border border-line bg-canvas p-1 text-muted hover:text-fg"
                                      >
                                        <History className="h-3.5 w-3.5" />
                                      </button>
                                    </>
                                  )}
                                </div>
                              </td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </div>
                </div>

                {historyData && (
                  <div className="rounded-[4px] border border-line bg-canvas p-3">
                    <div className="flex items-center justify-between">
                      <span className="font-semibold text-fg">
                        点位版本历史：{historyData.terminalId} / {historyData.itemCode}（当前代次 g{historyData.currentGeneration}，{historyData.enabled ? '启用' : '停用'}）
                      </span>
                      <button type="button" onClick={() => setHistoryData(null)} className="text-muted hover:text-fg">收起</button>
                    </div>
                    <ul className="mt-2 space-y-1.5">
                      {(historyData.generations ?? []).map((g) => (
                        <li key={g.generation} className="rounded border border-line bg-surface p-2 text-[11px]">
                          <div className="font-medium text-fg">
                            代次 g{g.generation} · {g.recordedAt.slice(0, 19)} · {g.publishedBy} · 原因：{g.reason}
                          </div>
                          {g.profiles.map((p) => (
                            <div key={p.revision} className="text-muted">
                              口径 {p.revision}：[{p.effectiveFrom.slice(0, 10)}, {p.effectiveTo ? p.effectiveTo.slice(0, 10) : '∞'}) · {p.sourceTable} · {p.energy}/{p.measurementKind} · {p.unit} · 倍率 {p.multiplier}× · 周期 {p.cadenceSeconds}s{p.registerModulus ? ` · 满量程 ${p.registerModulus}` : ''}
                            </div>
                          ))}
                        </li>
                      ))}
                    </ul>
                  </div>
                )}

                <form
                  className="space-y-3 rounded-[4px] border border-line bg-canvas p-4"
                  onSubmit={(e) => {
                    e.preventDefault();
                    runAction(
                      () =>
                        livePost('/management/metering-points', token, {
                          terminalId: mpTerminal,
                          itemCode: mpItem,
                          sourceTable: mpSourceTable,
                          energy: mpEnergy,
                          measurementKind: mpKind,
                          unit: mpUnit,
                          multiplier: Number(mpMult),
                          cadenceSeconds: Number(mpCadence),
                          registerModulus: mpModulus.trim() ? Number(mpModulus) : undefined,
                          registerEpoch: mpEpoch,
                          effectiveFrom: toISO(mpEffFrom),
                          effectiveTo: toISO(mpEffTo),
                          buildingAllocations: mpBldg ? [{ targetId: mpBldg, weightPpm: 1000000 }] : [],
                          spaceAllocations: mpSpace ? [{ targetId: mpSpace, weightPpm: 1000000 }] : [],
                          organizationAllocations: mpOrg ? [{ targetId: mpOrg, weightPpm: 1000000 }] : [],
                          preserveHistory: mpPreserve,
                          reason: mpReason,
                        }),
                      `计量点 ${mpTerminal}/${mpItem} 新代次已发布`,
                    );
                  }}
                >
                  <h4 className="font-semibold text-fg">发布计量点新代次（P1 完整口径与归属配置）</h4>
                  <div className="grid gap-2 sm:grid-cols-3">
                    <label className="flex flex-col gap-1">终端 ID
                      <input value={mpTerminal} onChange={(e) => setMpTerminal(e.target.value)} required className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    </label>
                    <label className="flex flex-col gap-1">物模型项编码
                      <input value={mpItem} onChange={(e) => setMpItem(e.target.value)} required className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    </label>
                    <label className="flex flex-col gap-1">数据来源表
                      <select value={mpSourceTable} onChange={(e) => setMpSourceTable(e.target.value as SourceTable)} className="rounded border border-line bg-surface px-2 py-1 text-fg">
                        <option value="snapshot_coarse">snapshot_coarse（小时快照）</option>
                        <option value="raw_item">raw_item（原始流水）</option>
                      </select>
                    </label>
                    <label className="flex flex-col gap-1">能源类型
                      <select value={mpEnergy} onChange={(e) => setMpEnergy(e.target.value as EnergyType)} className="rounded border border-line bg-surface px-2 py-1 text-fg">
                        <option value="electricity">用电 (electricity)</option>
                        <option value="water">用水 (water)</option>
                        <option value="gas">燃气 (gas)</option>
                        <option value="heat">供热 (heat)</option>
                        <option value="cooling">供冷 (cooling)</option>
                        <option value="steam">蒸汽 (steam)</option>
                      </select>
                    </label>
                    <label className="flex flex-col gap-1">计量方式（禁止瞬时量）
                      <select value={mpKind} onChange={(e) => setMpKind(e.target.value as MeasurementKind)} className="rounded border border-line bg-surface px-2 py-1 text-fg">
                        <option value="cumulative">累计量 (cumulative)</option>
                        <option value="interval">区间量 (interval)</option>
                      </select>
                    </label>
                    <label className="flex flex-col gap-1">单位
                      <input value={mpUnit} onChange={(e) => setMpUnit(e.target.value)} required className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    </label>
                    <label className="flex flex-col gap-1">倍率 (multiplier)
                      <input type="number" step="any" value={mpMult} onChange={(e) => setMpMult(e.target.value)} required className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    </label>
                    <label className="flex flex-col gap-1">采样周期 (秒)
                      <input type="number" value={mpCadence} onChange={(e) => setMpCadence(e.target.value)} required className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    </label>
                    <label className="flex flex-col gap-1">满量程回卷值（选填）
                      <input type="number" step="any" value={mpModulus} onChange={(e) => setMpModulus(e.target.value)} placeholder="如 999999.9" className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    </label>
                    <label className="flex flex-col gap-1">表计代次 (register_epoch)
                      <input value={mpEpoch} onChange={(e) => setMpEpoch(e.target.value)} className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    </label>
                    <label className="flex flex-col gap-1">生效开始日期
                      <ChineseDateInput value={mpEffFrom} onChange={setMpEffFrom} required placeholder="年-月-日" ariaLabel="生效开始日期" className="w-full" />
                    </label>
                    <label className="flex flex-col gap-1">生效结束日期（留空长期）
                      <ChineseDateInput value={mpEffTo} onChange={setMpEffTo} allowClear placeholder="长期有效" ariaLabel="生效结束日期" className="w-full" />
                    </label>
                    <label className="flex flex-col gap-1">建筑归属
                      <select value={mpBldg} onChange={(e) => setMpBldg(e.target.value)} className="rounded border border-line bg-surface px-2 py-1 text-fg">
                        <option value="">不绑定</option>
                        {(inventory?.buildings ?? []).map((b) => <option key={b.buildingId} value={b.buildingId}>{b.name} ({b.buildingId})</option>)}
                      </select>
                    </label>
                    <label className="flex flex-col gap-1">空间归属
                      <select value={mpSpace} onChange={(e) => setMpSpace(e.target.value)} className="rounded border border-line bg-surface px-2 py-1 text-fg">
                        <option value="">不绑定</option>
                        {(inventory?.spaces ?? []).map((sp) => <option key={sp.spaceId} value={sp.spaceId}>{sp.name} ({sp.spaceId})</option>)}
                      </select>
                    </label>
                    <label className="flex flex-col gap-1">组织归属
                      <select value={mpOrg} onChange={(e) => setMpOrg(e.target.value)} className="rounded border border-line bg-surface px-2 py-1 text-fg">
                        <option value="">不绑定</option>
                        {(inventory?.organizations ?? []).map((o) => <option key={o.orgId} value={o.orgId}>{o.name} ({o.orgId})</option>)}
                      </select>
                    </label>
                  </div>
                  <div className="flex flex-wrap items-center justify-between gap-2">
                    <label className="inline-flex items-center gap-1.5 text-muted">
                      <input type="checkbox" checked={mpPreserve} onChange={(e) => setMpPreserve(e.target.checked)} />
                      保留生效日前历史口径切片（支持历史时段回算）
                    </label>
                    <input value={mpReason} onChange={(e) => setMpReason(e.target.value)} placeholder="变更原因" className="min-w-[200px] flex-1 rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <button type="submit" disabled={busy} className="rounded bg-primary px-3 py-1.5 font-medium text-white">发布新代次</button>
                  </div>
                </form>
              </>
            )}

            {/* TAB 2: CATEGORIES & WEIGHTED BINDINGS */}
            {tab === 'categories' && (
              <div className="space-y-4">
                <div className="grid gap-4 md:grid-cols-2">
                  <form
                    className="space-y-2 rounded-[4px] border border-line bg-canvas p-3"
                    onSubmit={(e) => {
                      e.preventDefault();
                      runAction(
                        () => livePost('/management/categories', token, { categoryId: catId, parentId: catParent, name: catName, energy: catEnergy }),
                        `分类节点 ${catId} 已保存`,
                      );
                    }}
                  >
                    <h4 className="font-semibold text-fg">维护正式能源分类树（energy_category）</h4>
                    <input placeholder="分类 ID（如 elec-hvac）" value={catId} onChange={(e) => setCatId(e.target.value)} required className="w-full rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input placeholder="父分类 ID（留空为根节点）" value={catParent} onChange={(e) => setCatParent(e.target.value)} className="w-full rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input placeholder="分类名称（如 暖通空调）" value={catName} onChange={(e) => setCatName(e.target.value)} required className="w-full rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <select value={catEnergy} onChange={(e) => setCatEnergy(e.target.value as EnergyType)} className="w-full rounded border border-line bg-surface px-2 py-1 text-fg">
                      <option value="electricity">用电</option><option value="water">用水</option><option value="gas">燃气</option><option value="heat">供热</option><option value="cooling">供冷</option>
                    </select>
                    <button type="submit" disabled={busy} className="rounded bg-primary px-3 py-1 font-medium text-white">保存分类节点</button>
                  </form>

                  <form
                    className="space-y-2 rounded-[4px] border border-line bg-canvas p-3"
                    onSubmit={(e) => {
                      e.preventDefault();
                      const allocations = cbAllocText
                        .split(',')
                        .map((part) => part.trim())
                        .filter(Boolean)
                        .map((part) => {
                          const [cid, w] = part.split(':');
                          return { categoryId: (cid ?? '').trim(), weightPpm: Number((w ?? '1000000').trim()) };
                        });
                      runAction(
                        () =>
                          livePost('/management/category-bindings', token, {
                            terminalId: cbTerminal,
                            itemCode: cbItem,
                            revision: cbRev,
                            effectiveFrom: toISO(cbEffFrom),
                            allocations,
                          }),
                        `点位 ${cbTerminal}/${cbItem} 分类分摊版本已发布`,
                      );
                    }}
                  >
                    <h4 className="font-semibold text-fg">发布点位多分类分摊（支持权重与历史版本）</h4>
                    <div className="grid grid-cols-2 gap-2">
                      <input placeholder="终端 ID" value={cbTerminal} onChange={(e) => setCbTerminal(e.target.value)} required className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                      <input placeholder="项编码" value={cbItem} onChange={(e) => setCbItem(e.target.value)} required className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    </div>
                    <div className="grid grid-cols-2 gap-2">
                      <input placeholder="版本号（留空自动生成）" value={cbRev} onChange={(e) => setCbRev(e.target.value)} className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                      <ChineseDateInput value={cbEffFrom} onChange={setCbEffFrom} placeholder="生效日期（年-月-日）" ariaLabel="分类绑定生效日期" className="w-full" />
                    </div>
                    <input
                      placeholder="格式: catA:700000,catB:300000 (总和<=1000000)"
                      value={cbAllocText}
                      onChange={(e) => setCbAllocText(e.target.value)}
                      required
                      className="w-full rounded border border-line bg-surface px-2 py-1 text-fg"
                    />
                    <button type="submit" disabled={busy} className="rounded bg-primary px-3 py-1 font-medium text-white">发布分类分配</button>
                  </form>
                </div>

                <div>
                  <h4 className="font-semibold text-fg">当前分类节点 ({inventory?.categories?.length ?? 0}) 与分配记录 ({inventory?.categoryBindings?.length ?? 0})</h4>
                  <div className="mt-2 grid gap-2 sm:grid-cols-2">
                    {(inventory?.categories ?? []).map((c) => (
                      <div key={c.categoryId} className="rounded border border-line p-2">
                        <span className="font-medium text-fg">{c.name}</span> <span className="text-muted">({c.categoryId} · {energyText[c.energy] ?? c.energy}{c.parentId ? ` · 父:${c.parentId}` : ''})</span>
                      </div>
                    ))}
                  </div>
                  <div className="mt-2 space-y-1">
                    {(inventory?.categoryBindings ?? []).map((b) => (
                      <div key={`${b.terminalId}:${b.itemCode}:${b.revision}`} className="rounded border border-line p-2 tabular-nums text-muted">
                        <span className="font-medium text-fg">{b.terminalId}/{b.itemCode}</span> · 版本 {b.revision} · [{b.effectiveFrom.slice(0, 10)}, {b.effectiveTo ? b.effectiveTo.slice(0, 10) : '∞'}) ·{' '}
                        {b.allocations.map((a) => `${a.categoryId} (${(a.weightPpm / 10000).toFixed(1)}%)`).join(' + ')}
                      </div>
                    ))}
                  </div>
                </div>
              </div>
            )}

            {/* TAB 3: BUILDINGS / SPACES / ORGS */}
            {tab === 'objects' && (
              <div className="space-y-4">
                <form
                  className="space-y-2 rounded-[4px] border border-line bg-canvas p-3"
                  onSubmit={(e) => {
                    e.preventDefault();
                    runAction(
                      () =>
                        livePost('/management/buildings', token, {
                          buildingId: bldgId,
                          revision: bldgRev,
                          name: bldgName,
                          buildingType: bldgType,
                          region: bldgRegion,
                          grossAreaSqm: bldgArea.trim() ? Number(bldgArea) : undefined,
                          occupiedHeadcount: bldgPeople.trim() ? Number(bldgPeople) : undefined,
                          effectiveFrom: toISO(bldgEffFrom),
                          effectiveTo: toISO(bldgEffTo),
                        }),
                      `建筑 ${bldgId} 生效版本已保存`,
                    );
                  }}
                >
                  <h4 className="font-semibold text-fg">发布建筑档案版本（按统计时段自动匹配对应历史面积/人数）</h4>
                  <div className="grid gap-2 sm:grid-cols-4">
                    <input placeholder="建筑 ID" value={bldgId} onChange={(e) => setBldgId(e.target.value)} required className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input placeholder="版本号（留空自动）" value={bldgRev} onChange={(e) => setBldgRev(e.target.value)} className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input placeholder="建筑名称" value={bldgName} onChange={(e) => setBldgName(e.target.value)} required className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <select value={bldgType} onChange={(e) => setBldgType(e.target.value)} className="rounded border border-line bg-surface px-2 py-1 text-fg">
                      <option value="office">办公建筑 (office)</option>
                      <option value="commercial">商业综合体 (commercial)</option>
                      <option value="school">学校建筑 (school)</option>
                      <option value="hospital">医院建筑 (hospital)</option>
                      <option value="hotel">酒店建筑 (hotel)</option>
                    </select>
                    <input placeholder="地区（如 上海）" value={bldgRegion} onChange={(e) => setBldgRegion(e.target.value)} className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input type="number" step="any" placeholder="建筑面积 (m²)" value={bldgArea} onChange={(e) => setBldgArea(e.target.value)} className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input type="number" step="any" placeholder="常驻人数 (人)" value={bldgPeople} onChange={(e) => setBldgPeople(e.target.value)} className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <div className="flex gap-1">
                      <ChineseDateInput value={bldgEffFrom} onChange={setBldgEffFrom} placeholder="生效起" ariaLabel="建筑生效开始日期" className="min-w-0 flex-1" />
                      <ChineseDateInput value={bldgEffTo} onChange={setBldgEffTo} allowClear placeholder="长期" ariaLabel="建筑生效结束日期" className="min-w-0 flex-1" />
                    </div>
                  </div>
                  <button type="submit" disabled={busy} className="rounded bg-primary px-3 py-1 font-medium text-white">发布建筑版本</button>
                </form>

                <div className="grid gap-4 md:grid-cols-2">
                  <form
                    className="space-y-2 rounded-[4px] border border-line bg-canvas p-3"
                    onSubmit={(e) => {
                      e.preventDefault();
                      runAction(
                        () =>
                          livePost('/management/spaces', token, {
                            spaceId: spId,
                            buildingId: spBldg,
                            parentSpaceId: spParent,
                            name: spName,
                            areaSqm: spArea.trim() ? Number(spArea) : undefined,
                          }),
                        `空间 ${spId} 已保存`,
                      );
                    }}
                  >
                    <h4 className="font-semibold text-fg">维护空间层级</h4>
                    <input placeholder="空间 ID" value={spId} onChange={(e) => setSpId(e.target.value)} required className="w-full rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input placeholder="所属建筑 ID" value={spBldg} onChange={(e) => setSpBldg(e.target.value)} required className="w-full rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input placeholder="父空间 ID（选填）" value={spParent} onChange={(e) => setSpParent(e.target.value)} className="w-full rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input placeholder="空间名称" value={spName} onChange={(e) => setSpName(e.target.value)} required className="w-full rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input type="number" step="any" placeholder="面积 m²（选填）" value={spArea} onChange={(e) => setSpArea(e.target.value)} className="w-full rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <button type="submit" disabled={busy} className="rounded bg-primary px-3 py-1 font-medium text-white">保存空间</button>
                  </form>

                  <form
                    className="space-y-2 rounded-[4px] border border-line bg-canvas p-3"
                    onSubmit={(e) => {
                      e.preventDefault();
                      runAction(
                        () =>
                          livePost('/management/organizations', token, {
                            orgId,
                            parentOrgId: orgParent,
                            name: orgName,
                            headcount: orgHeadcount.trim() ? Number(orgHeadcount) : undefined,
                          }),
                        `组织 ${orgId} 已保存`,
                      );
                    }}
                  >
                    <h4 className="font-semibold text-fg">维护组织层级</h4>
                    <input placeholder="组织 ID" value={orgId} onChange={(e) => setOrgId(e.target.value)} required className="w-full rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input placeholder="上级组织 ID（选填）" value={orgParent} onChange={(e) => setOrgParent(e.target.value)} className="w-full rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input placeholder="组织名称" value={orgName} onChange={(e) => setOrgName(e.target.value)} required className="w-full rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input type="number" step="any" placeholder="人数（选填）" value={orgHeadcount} onChange={(e) => setOrgHeadcount(e.target.value)} className="w-full rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <button type="submit" disabled={busy} className="rounded bg-primary px-3 py-1 font-medium text-white">保存组织</button>
                  </form>
                </div>

                <div>
                  <h4 className="font-semibold text-fg">建筑历史版本台账 ({inventory?.buildingHistory?.length ?? 0})</h4>
                  <div className="mt-1 space-y-1">
                    {(inventory?.buildingHistory ?? []).map((b) => (
                      <div key={`${b.buildingId}:${b.revision}`} className="rounded border border-line p-2 tabular-nums text-muted">
                        <span className="font-medium text-fg">{b.name} ({b.buildingId})</span> · 版本 {b.revision} · [{b.effectiveFrom.slice(0, 10)}, {b.effectiveTo ? b.effectiveTo.slice(0, 10) : '∞'}) · 类型 {b.buildingType} · 地区 {b.region || '未设'} · 面积 {b.grossAreaSqm ?? '未配置'} m² · 人数 {b.occupiedHeadcount ?? '未配置'}
                      </div>
                    ))}
                  </div>
                </div>
              </div>
            )}

            {/* TAB 4: TOPOLOGY */}
            {tab === 'topology' && (
              <div className="space-y-4">
                <form
                  className="space-y-2 rounded-[4px] border border-line bg-canvas p-3"
                  onSubmit={(e) => {
                    e.preventDefault();
                    const [pt, pi] = topoParent.split(':');
                    const children = topoChildrenText
                      .split('\n')
                      .map((l) => l.trim())
                      .filter(Boolean)
                      .map((l) => {
                        const [ct, ci, z] = l.split(':');
                        return { terminalId: (ct ?? '').trim(), itemCode: (ci ?? 'total_usage').trim(), zoneCode: (z ?? 'zone-1').trim() };
                      });
                    runAction(
                      () =>
                        livePost('/management/topology', token, {
                          topologyVersion: topoVer,
                          buildingId: topoBldg,
                          energy: topoEnergy,
                          effectiveFrom: toISO(topoEffFrom),
                          parentTerminalId: (pt ?? '').trim(),
                          parentItemCode: (pi ?? 'total_usage').trim(),
                          children,
                        }),
                      '总分表物理拓扑版本已发布',
                    );
                  }}
                >
                  <h4 className="font-semibold text-fg">发布总分表历史拓扑（energy_domain.meter_topology）</h4>
                  <div className="grid gap-2 sm:grid-cols-4">
                    <input placeholder="拓扑版本号" value={topoVer} onChange={(e) => setTopoVer(e.target.value)} className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input placeholder="建筑 ID" value={topoBldg} onChange={(e) => setTopoBldg(e.target.value)} required className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <select value={topoEnergy} onChange={(e) => setTopoEnergy(e.target.value as EnergyType)} className="rounded border border-line bg-surface px-2 py-1 text-fg">
                      <option value="electricity">用电</option><option value="water">用水</option><option value="gas">燃气</option><option value="heat">供热</option><option value="cooling">供冷</option>
                    </select>
                    <ChineseDateInput value={topoEffFrom} onChange={setTopoEffFrom} placeholder="生效日期（年-月-日）" ariaLabel="拓扑生效日期" className="w-full" />
                  </div>
                  <input placeholder="总表（格式 terminalId:itemCode）" value={topoParent} onChange={(e) => setTopoParent(e.target.value)} required className="w-full rounded border border-line bg-surface px-2 py-1 text-fg" />
                  <textarea
                    rows={3}
                    placeholder="直接分表，每行一条，格式: terminalId:itemCode:zoneCode"
                    value={topoChildrenText}
                    onChange={(e) => setTopoChildrenText(e.target.value)}
                    required
                    className="w-full rounded border border-line bg-surface px-2 py-1 text-fg"
                  />
                  <button type="submit" disabled={busy} className="rounded bg-primary px-3 py-1 font-medium text-white">发布拓扑版本</button>
                </form>

                <div>
                  <h4 className="font-semibold text-fg">已配置拓扑边 ({inventory?.topology?.length ?? 0})</h4>
                  <div className="mt-1 space-y-1">
                    {(inventory?.topology ?? []).map((e, idx) => (
                      <div key={idx} className="rounded border border-line p-2 tabular-nums text-muted">
                        版本 {e.topologyVersion} · {e.buildingId} · {e.energy} · [{e.effectiveFrom.slice(0, 10)}, {e.effectiveTo ? e.effectiveTo.slice(0, 10) : '∞'}) · 总表 {e.parentTerminalId}/{e.parentItemCode} → 分表 {e.childTerminalId}/{e.childItemCode}（区域 {e.zoneCode}）
                      </div>
                    ))}
                  </div>
                </div>
              </div>
            )}

            {/* TAB 5: REGISTER EVENTS */}
            {tab === 'events' && (
              <div className="space-y-4">
                <form
                  className="space-y-2 rounded-[4px] border border-line bg-canvas p-3"
                  onSubmit={(e) => {
                    e.preventDefault();
                    runAction(
                      () =>
                        livePost('/management/events', token, {
                          terminalId: evTerminal,
                          itemCode: evItem,
                          eventKind: evKind,
                          effectiveAt: new Date(evAt).toISOString(),
                          newEpoch: evEpoch,
                          reason: evReason,
                        }),
                      '换表/回卷/质量事件已登记',
                    );
                  }}
                >
                  <h4 className="font-semibold text-fg">登记换表、满量程回卷或质量核查事件</h4>
                  <div className="grid gap-2 sm:grid-cols-3">
                    <input placeholder="终端 ID" value={evTerminal} onChange={(e) => setEvTerminal(e.target.value)} required className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input placeholder="项编码" value={evItem} onChange={(e) => setEvItem(e.target.value)} required className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <select value={evKind} onChange={(e) => setEvKind(e.target.value)} className="rounded border border-line bg-surface px-2 py-1 text-fg">
                      <option value="meter_swap">换表 (meter_swap)</option>
                      <option value="rollover">满量程回卷 (rollover)</option>
                      <option value="quality_suspect">可疑标记 (quality_suspect)</option>
                      <option value="quality_corrected">人工修正 (quality_corrected)</option>
                    </select>
                    <input type="datetime-local" value={evAt} onChange={(e) => setEvAt(e.target.value)} required className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input placeholder="新表代次（换表必填）" value={evEpoch} onChange={(e) => setEvEpoch(e.target.value)} className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input placeholder="事件说明与工单号" value={evReason} onChange={(e) => setEvReason(e.target.value)} required className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                  </div>
                  <button type="submit" disabled={busy} className="rounded bg-primary px-3 py-1 font-medium text-white">登记事件</button>
                </form>

                <div className="space-y-1">
                  {(inventory?.registerEvents ?? []).map((ev) => (
                    <div key={ev.id} className="rounded border border-line p-2 tabular-nums text-muted">
                      #{ev.id} · <span className="font-medium text-fg">{ev.terminalId}/{ev.itemCode}</span> · {ev.eventKind} · {ev.effectiveAt.slice(0, 19)}{ev.newEpoch ? ` · 新代次 ${ev.newEpoch}` : ''} · {ev.reason}（{ev.actor}）
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* TAB 6: TARGETS / ANOMALY RULES / DENOMINATORS / BENCHMARKS */}
            {tab === 'governance' && (
              <div className="grid gap-4 md:grid-cols-2">
                <form
                  className="space-y-2 rounded-[4px] border border-line bg-canvas p-3"
                  onSubmit={(e) => {
                    e.preventDefault();
                    runAction(
                      () =>
                        livePost('/management/targets', token, {
                          objectKind: 'building',
                          objectId: tgtBldg,
                          energy: tgtEnergy,
                          year: Number(tgtYear),
                          unit: tgtUnit,
                          annualValue: Number(tgtValue),
                          method: 'linear',
                        }),
                      `年度目标（${tgtBldg} ${tgtYear}）已发布`,
                    );
                  }}
                >
                  <h4 className="font-semibold text-fg">F05/F14 年度目标配置</h4>
                  <div className="grid grid-cols-2 gap-2">
                    <input value={tgtBldg} onChange={(e) => setTgtBldg(e.target.value)} placeholder="建筑 ID" className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input type="number" value={tgtYear} onChange={(e) => setTgtYear(e.target.value)} placeholder="年度" className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <select value={tgtEnergy} onChange={(e) => setTgtEnergy(e.target.value as EnergyType)} className="rounded border border-line bg-surface px-2 py-1 text-fg">
                      <option value="electricity">用电</option><option value="water">用水</option><option value="gas">燃气</option><option value="heat">供热</option><option value="cooling">供冷</option>
                    </select>
                    <input value={tgtUnit} onChange={(e) => setTgtUnit(e.target.value)} placeholder="单位" className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                  </div>
                  <input type="number" step="any" value={tgtValue} onChange={(e) => setTgtValue(e.target.value)} placeholder="年度目标值" className="w-full rounded border border-line bg-surface px-2 py-1 text-fg" />
                  <button type="submit" disabled={busy} className="rounded bg-primary px-3 py-1 font-medium text-white">发布年度目标</button>
                </form>

                <form
                  className="space-y-2 rounded-[4px] border border-line bg-canvas p-3"
                  onSubmit={(e) => {
                    e.preventDefault();
                    runAction(
                      () =>
                        livePost('/management/anomaly-rules', token, {
                          objectKind: 'building',
                          objectId: arBldg,
                          energy: arEnergy,
                          minimumHistory: Number(arHist),
                          minimumAbsoluteIncrease: Number(arAbs),
                          minimumRelativeIncrease: Number(arRel),
                        }),
                      `异常识别规则（${arBldg}）已发布`,
                    );
                  }}
                >
                  <h4 className="font-semibold text-fg">F10/F17 异常识别规则</h4>
                  <div className="grid grid-cols-2 gap-2">
                    <input value={arBldg} onChange={(e) => setArBldg(e.target.value)} placeholder="建筑 ID" className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <select value={arEnergy} onChange={(e) => setArEnergy(e.target.value as EnergyType)} className="rounded border border-line bg-surface px-2 py-1 text-fg">
                      <option value="electricity">用电</option><option value="water">用水</option><option value="gas">燃气</option><option value="heat">供热</option><option value="cooling">供冷</option>
                    </select>
                    <input type="number" value={arHist} onChange={(e) => setArHist(e.target.value)} placeholder="最少历史日(>=2)" className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input type="number" step="any" value={arAbs} onChange={(e) => setArAbs(e.target.value)} placeholder="绝对增幅门槛" className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                  </div>
                  <input type="number" step="any" value={arRel} onChange={(e) => setArRel(e.target.value)} placeholder="相对增幅比例(如0.05)" className="w-full rounded border border-line bg-surface px-2 py-1 text-fg" />
                  <button type="submit" disabled={busy} className="rounded bg-primary px-3 py-1 font-medium text-white">发布异常规则</button>
                </form>

                <form
                  className="space-y-2 rounded-[4px] border border-line bg-canvas p-3"
                  onSubmit={(e) => {
                    e.preventDefault();
                    runAction(
                      () =>
                        livePost('/management/denominators', token, {
                          objectKind: 'building',
                          objectId: dnBldg,
                          denominatorKind: dnKind,
                          periodFrom: toISO(dnFrom),
                          periodTo: toISO(dnTo),
                          value: Number(dnVal),
                          unit: dnUnit,
                          basis: 'manual_archive',
                        }),
                      `业务分母（${dnKind}）已发布`,
                    );
                  }}
                >
                  <h4 className="font-semibold text-fg">F12 行业业务分母（床日/间夜/营收/学生）</h4>
                  <div className="grid grid-cols-2 gap-2">
                    <input value={dnBldg} onChange={(e) => setDnBldg(e.target.value)} placeholder="建筑 ID" className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <select value={dnKind} onChange={(e) => setDnKind(e.target.value)} className="rounded border border-line bg-surface px-2 py-1 text-fg">
                      <option value="revenue">营收 (元)</option>
                      <option value="bed_days">床日</option>
                      <option value="room_nights">间夜</option>
                      <option value="students">学生数 (人)</option>
                      <option value="area">面积 (m²)</option>
                      <option value="people">人数 (人)</option>
                    </select>
                    <input type="number" step="any" value={dnVal} onChange={(e) => setDnVal(e.target.value)} placeholder="分母数值" required className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input value={dnUnit} onChange={(e) => setDnUnit(e.target.value)} placeholder="单位" required className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <ChineseDateInput value={dnFrom} onChange={setDnFrom} placeholder="开始日期（年-月-日）" ariaLabel="分母开始日期" className="w-full" />
                    <ChineseDateInput value={dnTo} onChange={setDnTo} placeholder="结束日期（年-月-日）" ariaLabel="分母结束日期" className="w-full" />
                  </div>
                  <button type="submit" disabled={busy} className="rounded bg-primary px-3 py-1 font-medium text-white">发布分母</button>
                </form>

                <form
                  className="space-y-2 rounded-[4px] border border-line bg-canvas p-3"
                  onSubmit={(e) => {
                    e.preventDefault();
                    runAction(
                      () =>
                        livePost('/management/benchmarks', token, {
                          benchmarkId: bmId,
                          name: bmName,
                          publisher: bmKind === 'demo' ? '本地开发演示' : '外部定额发布方',
                          region: bmRegion,
                          metricId: bmMetricId,
                          templateVersion: 'tmpl-office-v1',
                          unit: bmUnit,
                          kind: bmKind,
                          buildingType: 'office',
                          objectKind: 'building',
                          periodGranularity: 'day',
                          maximumValue: Number(bmMax),
                        }),
                      `定额标准 ${bmId} 已发布`,
                    );
                  }}
                >
                  <h4 className="font-semibold text-fg">F13 定额标准/演示阈值</h4>
                  <div className="grid grid-cols-2 gap-2">
                    <input value={bmId} onChange={(e) => setBmId(e.target.value)} placeholder="标准 ID" className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input value={bmName} onChange={(e) => setBmName(e.target.value)} placeholder="标准名称" className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input value={bmRegion} onChange={(e) => setBmRegion(e.target.value)} placeholder="适用地区" className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input value={bmMetricId} onChange={(e) => setBmMetricId(e.target.value)} placeholder="指标 ID" className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <select value={bmKind} onChange={(e) => setBmKind(e.target.value as 'demo' | 'external')} className="rounded border border-line bg-surface px-2 py-1 text-fg">
                      <option value="demo">演示参考阈值 (demo)</option>
                      <option value="external">外部定额标准 (external)</option>
                    </select>
                    <input value={bmUnit} onChange={(e) => setBmUnit(e.target.value)} placeholder="单位 (如 kWh/m²)" className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input type="number" step="any" value={bmMax} onChange={(e) => setBmMax(e.target.value)} placeholder="上限值" className="col-span-2 rounded border border-line bg-surface px-2 py-1 text-fg" />
                  </div>
                  <button type="submit" disabled={busy} className="rounded bg-primary px-3 py-1 font-medium text-white">发布标准</button>
                </form>

                <form
                  className="space-y-2 rounded-[4px] border border-line bg-canvas p-3 md:col-span-2"
                  onSubmit={(e) => {
                    e.preventDefault();
                    runAction(
                      () =>
                        livePost('/management/assessments', token, {
                          ruleId: asId,
                          objectKind: asObjKind,
                          objectId: asObjId,
                          energy: asEnergy,
                          metricLabel: asLabel,
                          targetValue: Number(asTarget),
                          weightPct: Number(asWeight),
                          publicSharePct: 0,
                          exempt: false,
                          scoringFormula: asFormula,
                          minCoverageRate: 0.9,
                        }),
                      `F15 考核规则 ${asId} 已保存`,
                    );
                  }}
                >
                  <h4 className="font-semibold text-fg">F15 组织与区域考核规则配置</h4>
                  <div className="grid gap-2 sm:grid-cols-4">
                    <input value={asId} onChange={(e) => setAsId(e.target.value)} placeholder="规则 ID" required className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <select value={asObjKind} onChange={(e) => setAsObjKind(e.target.value)} className="rounded border border-line bg-surface px-2 py-1 text-fg">
                      <option value="organization">组织 (organization)</option>
                      <option value="space">空间 (space)</option>
                      <option value="building">建筑 (building)</option>
                    </select>
                    <input value={asObjId} onChange={(e) => setAsObjId(e.target.value)} placeholder="对象 ID（如 org-a-rd）" required className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <select value={asEnergy} onChange={(e) => setAsEnergy(e.target.value as EnergyType)} className="rounded border border-line bg-surface px-2 py-1 text-fg">
                      <option value="electricity">用电</option><option value="water">用水</option><option value="gas">燃气</option><option value="heat">供热</option><option value="cooling">供冷</option>
                    </select>
                    <input value={asLabel} onChange={(e) => setAsLabel(e.target.value)} placeholder="考核指标名称" required className="sm:col-span-2 rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input type="number" step="any" value={asTarget} onChange={(e) => setAsTarget(e.target.value)} placeholder="目标值" required className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input type="number" step="any" value={asWeight} onChange={(e) => setAsWeight(e.target.value)} placeholder="权重 (%)" required className="rounded border border-line bg-surface px-2 py-1 text-fg" />
                    <input value={asFormula} onChange={(e) => setAsFormula(e.target.value)} placeholder="评分公式（含 每超 1% 扣 1/2/3 分）" className="sm:col-span-4 rounded border border-line bg-surface px-2 py-1 text-fg" />
                  </div>
                  <button type="submit" disabled={busy} className="rounded bg-primary px-3 py-1 font-medium text-white">保存考核规则</button>
                </form>
              </div>
            )}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
