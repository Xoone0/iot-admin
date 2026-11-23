import { useEffect, useId, useRef, useState } from 'react';
import * as Dialog from '@radix-ui/react-dialog';
import { ChartPie, ChevronDown, FileText, Gauge, Menu, ScanSearch, Target, X } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { Link, useLocation, useNavigationType } from 'react-router-dom';
import { cn } from '../lib/cn';

export const ENERGY_WORKSPACE_PATH = '/a/energy';
export const ENERGY_OVERVIEW_PATH = `${ENERGY_WORKSPACE_PATH}/overview`;
export const ENERGY_STATISTICS_PATH = `${ENERGY_WORKSPACE_PATH}/statistics`;
export const ENERGY_TARGET_SUMMARY_PATH = `${ENERGY_OVERVIEW_PATH}/targets`;
export const ENERGY_TARGET_PERIODS_PATH = `${ENERGY_WORKSPACE_PATH}/efficiency/targets`;
export const ENERGY_INDUSTRY_TEMPLATES_PATH = `${ENERGY_WORKSPACE_PATH}/efficiency/templates`;
export const ENERGY_INDUSTRY_BENCHMARKS_PATH = `${ENERGY_WORKSPACE_PATH}/efficiency/benchmarks`;
export const ENERGY_ANOMALIES_PATH = `${ENERGY_STATISTICS_PATH}/anomalies`;
export const ENERGY_DIAGNOSIS_PATH = `${ENERGY_WORKSPACE_PATH}/diagnosis`;
export const ENERGY_OPPORTUNITIES_PATH = `${ENERGY_DIAGNOSIS_PATH}/opportunities`;
const ENERGY_REPORTS_PATH = `${ENERGY_WORKSPACE_PATH}/reports`;

export type EnergyItem = {
  id: string;
  label: string;
  path?: string;
  partial?: boolean;
};

export type EnergyNavParent = {
  key: string;
  label: string;
  icon: LucideIcon;
  items: readonly EnergyItem[];
};

const LIVE_ALL_25 = import.meta.env.VITE_ENERGY_LIVE === '1';

/**
 * 建筑能耗监测分析：完整五父级、25 子功能左侧树形导航（Arco Design Pro Menu 样式）。
 */
export const ENERGY_NAV_PARENTS: readonly EnergyNavParent[] = [
  {
    key: 'overview',
    label: '能耗总览',
    icon: Gauge,
    items: [
      { id: 'F01', label: '综合能耗概览', path: ENERGY_OVERVIEW_PATH, partial: !LIVE_ALL_25 },
      { id: 'F02', label: '能源分类构成', path: `${ENERGY_OVERVIEW_PATH}/composition` },
      { id: 'F03', label: '分区分项概览', path: `${ENERGY_OVERVIEW_PATH}/objects` },
      { id: 'F04', label: '实时与历史趋势', path: `${ENERGY_OVERVIEW_PATH}/trend`, partial: !LIVE_ALL_25 },
      { id: 'F05', label: '年度目标摘要', path: ENERGY_TARGET_SUMMARY_PATH },
    ],
  },
  {
    key: 'statistics',
    label: '能耗统计分析',
    icon: ChartPie,
    items: [
      { id: 'F06', label: '分类分项统计', path: ENERGY_STATISTICS_PATH },
      { id: 'F07', label: '多维统计', path: `${ENERGY_STATISTICS_PATH}/dimensions` },
      { id: 'F08', label: '趋势、同环比与排名', path: `${ENERGY_STATISTICS_PATH}/trend` },
      { id: 'F09', label: '能流与总分表平衡', path: `${ENERGY_STATISTICS_PATH}/balance` },
      { id: 'F10', label: '异常浪费识别', path: ENERGY_ANOMALIES_PATH },
    ],
  },
  {
    key: 'efficiency',
    label: '能效指标与考核',
    icon: Target,
    items: [
      { id: 'F11', label: '通用能效强度', path: `${ENERGY_WORKSPACE_PATH}/efficiency` },
      { id: 'F12', label: '行业指标模板', path: ENERGY_INDUSTRY_TEMPLATES_PATH },
      { id: 'F13', label: '定额标准对标', path: ENERGY_INDUSTRY_BENCHMARKS_PATH },
      { id: 'F14', label: '目标与分期分解', path: ENERGY_TARGET_PERIODS_PATH },
      { id: 'F15', label: '组织与区域考核', path: LIVE_ALL_25 ? `${ENERGY_WORKSPACE_PATH}/efficiency/assessment` : undefined },
    ],
  },
  {
    key: 'diagnosis',
    label: '节能诊断与改造评估',
    icon: ScanSearch,
    items: [
      { id: 'F16', label: '高耗能对象识别', path: ENERGY_DIAGNOSIS_PATH, partial: !LIVE_ALL_25 },
      { id: 'F17', label: '节能机会分析', path: ENERGY_OPPORTUNITIES_PATH },
      { id: 'F18', label: '改造前基线', path: LIVE_ALL_25 ? `${ENERGY_DIAGNOSIS_PATH}/baseline` : undefined },
      { id: 'F19', label: '潜力与收益测算', path: LIVE_ALL_25 ? `${ENERGY_DIAGNOSIS_PATH}/potential` : undefined },
      { id: 'F20', label: '改造后节能核验', path: LIVE_ALL_25 ? `${ENERGY_DIAGNOSIS_PATH}/verification` : undefined },
    ],
  },
  {
    key: 'reports',
    label: '能耗报表',
    icon: FileText,
    items: [
      { id: 'F21', label: '综合与分类分项报表', path: ENERGY_REPORTS_PATH },
      { id: 'F22', label: '指标与定额报表', path: `${ENERGY_REPORTS_PATH}/metrics` },
      { id: 'F23', label: '组织考核报表', path: LIVE_ALL_25 ? `${ENERGY_REPORTS_PATH}/assessment` : undefined },
      { id: 'F24', label: '诊断与改造成效报告', path: LIVE_ALL_25 ? `${ENERGY_REPORTS_PATH}/retrofit` : undefined },
      { id: 'F25', label: '行业专项与自定义报表', path: `${ENERGY_REPORTS_PATH}/custom` },
    ],
  },
];

export function currentEnergyItem(pathname: string): EnergyItem | undefined {
  const path = pathname.replace(/\/$/, '');
  const match = path === `${ENERGY_WORKSPACE_PATH}/classification` ? ENERGY_STATISTICS_PATH : path;
  return ENERGY_NAV_PARENTS.flatMap(({ items }) => items).find((item) => item.path === match);
}

export function currentEnergyHierarchy(pathname: string): {
  parent?: EnergyNavParent;
  item?: EnergyItem;
} {
  const item = currentEnergyItem(pathname);
  if (!item) return {};
  const parent = ENERGY_NAV_PARENTS.find((p) => p.items.some((it) => it.id === item.id));
  return { parent, item };
}

const samplePeriods = ['day', '7', '14', '30'];
const naturalPeriods = ['day', 'hour', 'month'];
const graphPeriods = ['day', 'hour', 'month', '7', '14', '30'];
const industryItems = ['F12', 'F13', 'F22', 'F25'];

export function energyItemLocation(destination: string, pathname: string, search: string, hash = '') {
  const to = currentEnergyItem(destination)?.id;
  const from = currentEnergyItem(pathname)?.id;
  if (to && to === from) return { to: { pathname: destination, search, hash } };

  const input = new URLSearchParams(search);

  // In live PostgreSQL mode (VITE_ENERGY_LIVE=1), preserve real building/5-energy/date/year
  // parameters cleanly without injecting legacy synthetic preview IDs like "office-a".
  if (LIVE_ALL_25) {
    const liveParams = new URLSearchParams();
    const b = input.get('building');
    const legacySynthetic =
      b === 'office-a' ||
      b === 'office-b' ||
      b === 'hospital-a' ||
      b === 'school-a' ||
      b === 'hotel-a' ||
      b === 'all';
    if (b && !legacySynthetic) liveParams.set('building', b);

    const e = input.get('energy');
    if (e && ['electricity', 'water', 'gas', 'heat', 'cooling'].includes(e)) {
      liveParams.set('energy', e);
    }

    const d = input.get('date');
    if (d && to !== 'F05' && to !== 'F14') liveParams.set('date', d);

    const y = input.get('year');
    if (to === 'F05' || to === 'F14') {
      if (y) liveParams.set('year', y);
      else if (d && /^\d{4}/.test(d)) liveParams.set('year', d.slice(0, 4));
    }

    return { to: { pathname: destination, search: liveParams.toString(), hash } };
  }

  const params = new URLSearchParams();
  const resets: string[] = [];
  const toIndustry = industryItems.includes(to ?? '');
  const fromIndustry = industryItems.includes(from ?? '');
  const building = input.get('building') ?? (from === 'F07' ? 'all' : 'office-a');
  const industryBuilding = building === 'hospital-a' || building === 'school-a' || building === 'hotel-a';
  if (building === 'all' && to !== 'F07') resets.push('建筑已改为办公楼 A');
  if (fromIndustry && industryBuilding && !toIndustry) resets.push('已从行业专用样本切换为办公楼 A');
  params.set(
    'building',
    building === 'office-b' || (building === 'all' && to === 'F07') || (toIndustry && industryBuilding)
      ? building
      : 'office-a',
  );
  params.set('energy', input.get('energy') === 'water' ? 'water' : 'electricity');

  const fromTargets = from === 'F05' || from === 'F14';
  const fromDiagnostics = from === 'F10' || from === 'F17';
  const scenario = input.get('scenario');
  const sourceKey = from === 'F08' ? 'granularity' : 'period';

  if (to === 'F05' || to === 'F14') {
    const year = input.get('year');
    if (year) params.set('year', year);
    if (!fromTargets) resets.push('时间条件已切换为考核年度');
    if (scenario && scenario !== 'standard') resets.push('样本场景已恢复为标准');
    return {
      to: { pathname: destination, search: params.toString() },
      state: resets.length > 0 ? { energyScopeResets: resets } : undefined,
    };
  }
  if (to === 'F10' || to === 'F17' || toIndustry) {
    const sameSampleGroup = toIndustry ? fromIndustry : fromDiagnostics;
    if (sameSampleGroup && scenario) {
      const compatible =
        scenario === 'no-template'
          ? to === 'F25'
          : scenario !== 'display-stress' || to === 'F22' || to === 'F25';
      if (compatible) params.set('scenario', scenario);
      else resets.push('样本场景已恢复为标准');
    }
    const project = input.get('project');
    if (project && (from === 'F22' || from === 'F25') && (to === 'F22' || to === 'F25')) params.set('project', project);
    if (!sameSampleGroup) {
      const selectedPeriod = input.get(sourceKey) ?? (from === 'F06' ? '7' : 'day');
      if (
        fromTargets ||
        selectedPeriod !== 'day' ||
        input.get('mode') === 'to-date' ||
        (from === 'F01' && input.has('granularity'))
      ) {
        resets.push('分析周期已改为样本自然日');
      }
      if (scenario && scenario !== 'standard') resets.push('样本场景已恢复为标准');
    }
    return {
      to: { pathname: destination, search: params.toString() },
      state: resets.length > 0 ? { energyScopeResets: resets } : undefined,
    };
  }

  const targetKey = to === 'F08' ? 'granularity' : 'period';
  if (fromTargets && input.has('year')) resets.push('已从目标年度切换到当前用量样本周期');
  const fallbackPeriod = from === 'F06' || (fromTargets && to === 'F06') ? '7' : 'day';
  const sourcePeriod = (fromTargets || fromDiagnostics || fromIndustry ? null : input.get(sourceKey)) ?? fallbackPeriod;
  const targetPeriods = to === 'F07' ? graphPeriods : to === 'F08' ? naturalPeriods : samplePeriods;
  const partial = input.get('mode') === 'to-date';
  const legacyMeter = from === 'F01' && input.has('granularity');
  const compatible =
    !legacyMeter &&
    targetPeriods.includes(sourcePeriod) &&
    (!partial || ((to === 'F07' || to === 'F08') && (sourcePeriod === 'day' || sourcePeriod === 'month')));
  const period = compatible ? sourcePeriod : to === 'F06' ? '7' : 'day';
  if (!compatible) resets.push(period === '7' ? '统计周期已改为样本末 7 天' : '统计周期已改为样本自然日');
  params.set(targetKey, period);
  if (compatible && partial) params.set('mode', 'to-date');
  if (scenario && scenario !== 'standard') resets.push('样本场景已恢复为标准');

  return {
    to: { pathname: destination, search: params.toString() },
    state: resets.length > 0 ? { energyScopeResets: resets } : undefined,
  };
}

export function BuildingEnergyNavigation({
  onSelect,
  collapsed = false,
}: {
  onSelect?: () => void;
  collapsed?: boolean;
}) {
  const { pathname, search, hash, key: locationKey } = useLocation();
  const navigationType = useNavigationType();
  const domID = useId();
  const { parent: activeParentObj, item: current } = currentEnergyHierarchy(pathname);
  const activeParent = activeParentObj?.key;
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});

  useEffect(() => {
    if (activeParent) {
      setExpanded((value) => (value[activeParent] ? value : { ...value, [activeParent]: true }));
    }
  }, [pathname, activeParent]);

  useEffect(() => {
    if (navigationType === 'POP' && activeParent) {
      setExpanded((value) => (value[activeParent] ? value : { ...value, [activeParent]: true }));
    }
  }, [locationKey, navigationType, activeParent]);

  if (collapsed) {
    return (
      <nav aria-label="建筑能耗导航" className="min-h-0 flex-1 overflow-y-auto px-2 py-2.5">
        <ul className="space-y-1.5">
          {ENERGY_NAV_PARENTS.map((parent) => {
            const Icon = parent.icon;
            const active = activeParent === parent.key;
            const firstPath = parent.items.find((i) => i.path)?.path;
            return (
              <li key={parent.key}>
                {firstPath ? (
                  <Link
                    {...energyItemLocation(firstPath, pathname, search, hash)}
                    title={parent.label}
                    aria-label={parent.label}
                    aria-current={active ? 'page' : undefined}
                    className={cn(
                      'mx-auto flex h-10 w-10 items-center justify-center rounded-[4px] transition-colors',
                      active
                        ? 'bg-primary-soft text-primary'
                        : 'text-fg-2 hover:bg-fill-2 hover:text-fg',
                    )}
                    onClick={(event) => {
                      if (!event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) onSelect?.();
                    }}
                  >
                    <Icon className="size-[18px]" aria-hidden />
                  </Link>
                ) : null}
              </li>
            );
          })}
        </ul>
      </nav>
    );
  }

  return (
    <nav aria-label="建筑能耗导航" className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-2 py-2">
      <ul className="space-y-0.5">
        {ENERGY_NAV_PARENTS.map(({ key, label, icon: Icon, items }) => {
          const active = activeParent === key;
          const open = expanded[key] ?? active;
          const childID = `${domID}-${key}`;
          return (
            <li key={key}>
              <button
                type="button"
                aria-expanded={open}
                aria-controls={childID}
                className={cn(
                  'group flex h-[36px] w-full items-center gap-2.5 rounded-[4px] px-2.5 text-left text-[13px] transition-colors motion-reduce:transition-none',
                  active
                    ? 'font-medium text-primary hover:bg-fill-2'
                    : 'font-normal text-fg hover:bg-fill-2',
                )}
                onClick={() => setExpanded((prev) => ({ ...prev, [key]: !open }))}
              >
                <Icon
                  className={cn(
                    'size-[15px] shrink-0 transition-colors',
                    active ? 'text-primary' : 'text-fg-2 group-hover:text-fg',
                  )}
                  aria-hidden
                />
                <span className="min-w-0 flex-1 truncate">{label}</span>
                <ChevronDown
                  aria-hidden
                  className={cn(
                    'size-3.5 shrink-0 text-muted transition-transform duration-150 motion-reduce:transition-none',
                    open && 'rotate-180',
                  )}
                />
              </button>

              <ul id={childID} hidden={!open} className="mt-0.5 space-y-0.5 pb-1">
                {items.map((item) => {
                  const selected = current?.id === item.id;
                  const row = cn(
                    'flex h-[32px] items-center gap-1.5 rounded-[4px] pl-8 pr-2.5 text-[13px] transition-colors',
                    selected
                      ? 'bg-primary-soft font-medium text-primary'
                      : 'font-normal text-fg-2 hover:bg-fill-2 hover:text-fg',
                  );
                  const body = (
                    <>
                      <span className="min-w-0 flex-1 truncate">{item.label}</span>
                      {!item.path && <span className="shrink-0 text-[11px] text-muted">未开放</span>}
                      {item.partial && <span className="shrink-0 text-[11px] font-normal text-muted">部分</span>}
                    </>
                  );
                  return (
                    <li key={item.id} data-energy-feature={item.id}>
                      {item.path ? (
                        <Link
                          {...energyItemLocation(item.path, pathname, search, hash)}
                          aria-current={selected ? 'page' : undefined}
                          title={`${item.id} ${item.label}`}
                          className={row}
                          onClick={(event) => {
                            if (!event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) onSelect?.();
                          }}
                        >
                          {body}
                        </Link>
                      ) : (
                        <span className={cn(row, 'cursor-not-allowed text-muted')} aria-disabled="true">
                          {body}
                        </span>
                      )}
                    </li>
                  );
                })}
              </ul>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}

export function BuildingEnergyMobileNavigation() {
  const [open, setOpen] = useState(false);
  const panel = useRef<HTMLDivElement>(null);
  const { pathname } = useLocation();
  useEffect(() => setOpen(false), [pathname]);
  useEffect(() => {
    const desktop = window.matchMedia('(min-width: 1024px)');
    const closeOnDesktop = () => {
      if (desktop.matches) setOpen(false);
    };
    desktop.addEventListener('change', closeOnDesktop);
    return () => desktop.removeEventListener('change', closeOnDesktop);
  }, []);

  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Dialog.Trigger asChild>
        <button
          type="button"
          aria-label="打开导航"
          className="-ml-1 inline-flex size-8 items-center justify-center rounded-[4px] text-fg-2 hover:bg-fill-2 hover:text-fg lg:hidden"
        >
          <Menu className="size-[18px]" aria-hidden />
        </button>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-30 bg-black/45" />
        <Dialog.Content
          ref={panel}
          aria-describedby={undefined}
          className="fixed inset-y-0 left-0 z-40 flex w-[240px] max-w-[calc(100vw-2.5rem)] flex-col border-r border-line bg-surface text-fg outline-none"
          onOpenAutoFocus={(event) => {
            const active = panel.current?.querySelector<HTMLElement>('[aria-current="page"]');
            if (active) {
              event.preventDefault();
              active.focus();
            }
          }}
        >
          <div className="flex h-[56px] shrink-0 items-center justify-between border-b border-line px-4">
            <Dialog.Title className="text-[14px] font-semibold tracking-tight text-fg">
              建筑能耗监测分析
            </Dialog.Title>
            <Dialog.Close asChild>
              <button
                type="button"
                aria-label="关闭导航"
                className="inline-flex size-8 items-center justify-center rounded-[4px] text-muted hover:bg-fill-2 hover:text-fg"
              >
                <X className="size-4" aria-hidden />
              </button>
            </Dialog.Close>
          </div>
          <BuildingEnergyNavigation onSelect={() => setOpen(false)} />
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
