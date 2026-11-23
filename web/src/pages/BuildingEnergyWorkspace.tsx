import { Suspense, useEffect, useId, useRef, useState } from 'react';
import type { FormEvent, ReactNode } from 'react';
import { SlidersHorizontal } from 'lucide-react';
import { Outlet, useLocation, useNavigationType, useSearchParams } from 'react-router-dom';
import { currentEnergyItem } from '../components/BuildingEnergyNavigation';
import { Drawer } from '../components/Drawer';
import { cn } from '../lib/cn';

export const ENERGY_PREVIEW_API = '/admin/v1/analytics/building-energy/preview';

const fields = {
  building: {
    label: '建筑',
    choices: [['office-a', '办公楼 A'], ['office-b', '办公楼 B']],
  },
  energy: {
    label: '能源',
    choices: [['electricity', '用电'], ['water', '用水']],
  },
  period: {
    label: '时间',
    choices: [['day', '10月15日'], ['7', '截至 10月31日 · 7天'], ['14', '截至 10月31日 · 14天'], ['30', '10月 · 末30天']],
  },
} as const;

type ScopeKey = keyof typeof fields;
type EnergyScope = Record<ScopeKey, string>;
const scopeKeys: ScopeKey[] = ['building', 'energy', 'period'];
const focus = 'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-canvas';
const number = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 6 });

export function energyNumber(value: number | null | undefined) {
  return value == null ? '—' : number.format(value);
}

function supportedValue(key: ScopeKey, value: string | null) {
  return fields[key].choices.find(([id]) => id === value);
}

export function useEnergyScope() {
  const [params] = useSearchParams();
  const scope = {
    building: supportedValue('building', params.get('building'))?.[0] ?? 'office-a',
    energy: supportedValue('energy', params.get('energy'))?.[0] ?? 'electricity',
    period: supportedValue('period', params.get('period'))?.[0] ?? 'day',
  };
  return { scope, query: new URLSearchParams(scope).toString() };
}

export function EnergySectionHeading({ title, description, action }: { title: string; description?: string; action?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-start justify-between gap-x-5 gap-y-2">
      <div className="min-w-0">
        <h2 className="text-base font-semibold text-fg">{title}</h2>
        {description && <p className="mt-1 text-sm leading-relaxed text-muted">{description}</p>}
      </div>
      {action}
    </div>
  );
}

export function EnergyScopeBar({ action }: { action?: ReactNode }) {
  const [params, setParams] = useSearchParams();
  const { scope } = useEnergyScope();
  function apply(draft: EnergyScope) {
    const next = new URLSearchParams(params);
    for (const key of scopeKeys) next.set(key, draft[key]);
    if (next.toString() !== params.toString()) setParams(next);
  }

  return (
    <div className="flex min-w-0 items-center justify-between gap-3 border-b border-line pb-3">
      <div className="min-w-0">
        <p className="text-sm font-semibold text-fg">{supportedValue('building', scope.building)?.[1]}</p>
        <p className="mt-0.5 text-xs leading-relaxed text-muted">
          {supportedValue('energy', scope.energy)?.[1]} · {supportedValue('period', scope.period)?.[1]}
        </p>
      </div>
      <div className="flex shrink-0 flex-wrap items-center justify-end gap-2">
        {action}
        <EnergyFilterDrawer title="筛选统计范围" applied={scope} onApply={apply}
          fields={scopeKeys.map((key) => ({ key, label: fields[key].label, options: fields[key].choices }))} />
      </div>
    </div>
  );
}

type FilterField<K extends string> = { key: K; label: string; options: readonly (readonly [string, string])[] };

export function energyFilterValues<K extends string>(filterFields: readonly FilterField<K>[], params: URLSearchParams) {
  return Object.fromEntries(filterFields.map(({ key, options }) => [
    key, options.find(([value]) => params.get(key) === value)?.[0] ?? options[0][0],
  ])) as Record<K, string>;
}

export function EnergyFilterDrawer<K extends string>({
  title, fields: filterFields, applied, onApply, normalize, disabled,
}: {
  title: string;
  fields: readonly FilterField<K>[];
  applied: Record<K, string>;
  onApply: (value: Record<K, string>) => void;
  normalize?: (value: Record<K, string>) => Record<K, string>;
  disabled?: (key: K, value: Record<K, string>) => boolean;
}) {
  const id = useId();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState(applied);
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setOpen(false);
    onApply(draft);
  }
  return (
    <Drawer open={open} onOpenChange={(show) => { if (show) setDraft(applied); setOpen(show); }}
      title={title} description="应用后按所选条件更新统计结果。"
      trigger={
        <button type="button" className={cn('inline-flex min-h-10 shrink-0 items-center gap-1.5 rounded-md border border-line-strong bg-surface px-3 text-sm font-medium text-fg hover:bg-surface-2', focus)}>
          <SlidersHorizontal className="size-4 text-primary" aria-hidden />筛选
        </button>
      }
      footer={
        <>
          <button type="button" className={cn('min-h-10 rounded-md px-3 text-sm text-muted hover:bg-surface-2', focus)}
            onClick={() => setOpen(false)}>取消</button>
          <button type="submit" form={id}
            className={cn('min-h-10 rounded-md bg-primary px-4 text-sm font-semibold text-white hover:bg-primary-hover dark:text-canvas', focus)}>应用筛选</button>
        </>
      }
    >
      <form id={id} className="grid grid-cols-2 gap-x-4 gap-y-5" onSubmit={submit}>
        {filterFields.map(({ key, label, options }) => (
          <label key={key} className="flex min-w-0 flex-col gap-1.5 text-sm font-medium">
            {label}
            <select value={draft[key]} disabled={disabled?.(key, draft)}
              onChange={(event) => {
                const next = { ...draft, [key]: event.target.value };
                setDraft(normalize ? normalize(next) : next);
              }}
              className={cn('min-h-11 w-full rounded-md border border-line bg-surface px-2.5 text-sm font-normal text-fg disabled:bg-surface-2 disabled:text-muted', focus)}>
              {options.map(([value, name]) => <option key={value} value={value}>{name}</option>)}
            </select>
          </label>
        ))}
      </form>
    </Drawer>
  );
}

export default function BuildingEnergyWorkspace() {
  const { pathname, hash, state } = useLocation();
  const navigationType = useNavigationType();
  const section = useRef<HTMLDivElement>(null);
  const current = currentEnergyItem(pathname);
  const resets: string[] | undefined = state?.energyScopeResets;

  useEffect(() => {
    if (!hash) {
      if (navigationType === 'PUSH') window.scrollTo({ top: 0 });
      return;
    }
    const content = section.current;
    if (!content) return;
    const anchor = hash === '#category-tree' ? 'f06-table' : hash.slice(1);
    const scrollToAnchor = () => {
      const target = document.getElementById(anchor);
      if (!target || !content.contains(target)) return false;
      window.scrollBy({ top: target.getBoundingClientRect().top - 72 });
      return true;
    };
    if (scrollToAnchor()) return;
    const observer = new MutationObserver(() => {
      if (scrollToAnchor()) observer.disconnect();
    });
    observer.observe(content, { childList: true, subtree: true });
    return () => observer.disconnect();
  }, [hash, pathname, navigationType]);

  return (
    <div ref={section} className="mx-auto min-w-0 max-w-[1440px] space-y-4">
      <div className="flex items-start justify-between gap-3">
        <h1 className="min-w-0 text-xl leading-tight font-semibold tracking-tight sm:text-2xl">{current?.label ?? '建筑能耗监测分析'}</h1>
        <span className="shrink-0 whitespace-nowrap rounded-md bg-warning-soft px-2 py-1 text-xs font-medium text-warning">合成数据</span>
      </div>
      {resets?.length ? <p role="status" className="text-xs leading-relaxed text-muted">{resets.join('，')}。</p> : null}
      <Suspense fallback={<p role="status" className="py-8 text-sm text-muted">加载栏目…</p>}>
        <Outlet />
      </Suspense>
    </div>
  );
}
