import { useRef, useState, type ReactNode } from 'react';
import * as Dialog from '@radix-ui/react-dialog';
import { Calendar, ChevronDown, ChevronRight, Database, Filter, Info, X } from 'lucide-react';
import type { DatasetMeta } from '../api/energyLive';

const shanghaiDateFmt = new Intl.DateTimeFormat('en-CA', {
  timeZone: 'Asia/Shanghai',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
});

export function fmtShanghaiDate(iso: string | undefined): string {
  if (!iso) return '—';
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso.slice(0, 10) : shanghaiDateFmt.format(d);
}

/**
 * ChineseDateInput wraps a native <input type="date"> so browsers never display
 * English "mm/dd/yyyy" placeholders; displays YYYY-MM-DD when chosen or a clean
 * Chinese placeholder when empty, while keeping native calendar picking intact.
 */
export function ChineseDateInput({
  value,
  onChange,
  placeholder = '年-月-日',
  ariaLabel,
  allowClear = false,
  required = false,
  className = '',
}: {
  value: string;
  onChange: (next: string) => void;
  placeholder?: string;
  ariaLabel?: string;
  allowClear?: boolean;
  required?: boolean;
  className?: string;
}) {
  const inputRef = useRef<HTMLInputElement>(null);

  const openPicker = () => {
    const el = inputRef.current;
    if (!el) return;
    el.focus();
    if (typeof el.showPicker === 'function') {
      try {
        el.showPicker();
      } catch {
        // Fallback: native input click handles picker when showPicker is restricted.
      }
    }
  };

  return (
    <span
      onClick={openPicker}
      className={`arco-input relative inline-flex min-w-[144px] cursor-pointer items-center justify-between gap-2 select-none tabular ${className}`}
    >
      <span className={value ? 'text-fg' : 'text-muted'}>{value || placeholder}</span>
      <span className="relative z-20 flex shrink-0 items-center gap-1">
        {allowClear && value && (
          <button
            type="button"
            aria-label="清除日期"
            onClick={(e) => {
              e.stopPropagation();
              onChange('');
            }}
            className="inline-flex size-4 items-center justify-center rounded-full text-muted hover:bg-fill-3 hover:text-fg"
          >
            <X className="size-3" aria-hidden="true" />
          </button>
        )}
        <Calendar className="pointer-events-none size-3.5 text-muted" aria-hidden="true" />
      </span>
      <input
        ref={inputRef}
        type="date"
        lang="zh-CN"
        aria-label={ariaLabel}
        required={required}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="absolute inset-0 z-10 h-full w-full cursor-pointer opacity-0"
      />
    </span>
  );
}

/**
 * DatasetInfoButton opens a clean Arco Modal containing PostgreSQL source,
 * seed, version, generation timestamp, scope, and origin notice.
 */
export function DatasetInfoButton({
  dataset,
  originNotice,
}: {
  dataset?: DatasetMeta;
  originNotice?: string;
}) {
  const [open, setOpen] = useState(false);

  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Dialog.Trigger asChild>
        <button
          type="button"
          className="inline-flex h-6 items-center gap-1 rounded-[2px] px-1.5 text-[12px] text-muted transition-colors hover:bg-fill-2 hover:text-fg"
          title="查看数据集版本、Seed、时间范围与 PostgreSQL 数据源详情"
        >
          <Info className="h-3.5 w-3.5" aria-hidden="true" />
          <span className="tabular">{dataset ? `${dataset.datasetVersion} · 数据集详情` : '数据源详情'}</span>
        </button>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-black/45" />
        <Dialog.Content
          className="fixed top-1/2 left-1/2 z-50 w-[min(92vw,520px)] -translate-x-1/2 -translate-y-1/2 rounded-[4px] border border-line bg-surface p-5 shadow-panel outline-none"
          aria-describedby={undefined}
        >
          <div className="flex items-start justify-between gap-3 border-b border-line pb-3">
            <div>
              <Dialog.Title className="text-[15px] font-medium text-fg">
                数据集与技术来源详情
              </Dialog.Title>
              <p className="mt-0.5 text-[12px] text-muted">
                全模块 25 项功能与 CSV 导出共享同一 PostgreSQL 后端数据源
              </p>
            </div>
            <Dialog.Close asChild>
              <button
                type="button"
                aria-label="关闭"
                className="rounded-[2px] p-1 text-muted hover:bg-fill-2 hover:text-fg"
              >
                <X className="h-4 w-4" />
              </button>
            </Dialog.Close>
          </div>

          <dl className="mt-3 divide-y divide-line text-[13px] tabular">
            <div className="flex justify-between gap-4 py-2">
              <dt className="text-muted">数据属性</dt>
              <dd className="font-medium text-warning">
                {dataset?.isSimulated ? '模拟数据（非现场真实遥测）' : '数据库实测'}
              </dd>
            </div>
            {dataset && (
              <>
                <div className="flex justify-between gap-4 py-2">
                  <dt className="text-muted">数据集名称</dt>
                  <dd className="text-right font-medium text-fg">{dataset.datasetName}</dd>
                </div>
                <div className="flex justify-between gap-4 py-2">
                  <dt className="text-muted">版本与固定种子</dt>
                  <dd className="font-medium text-fg">
                    {dataset.datasetVersion} · Seed {dataset.seed}
                  </dd>
                </div>
                <div className="flex justify-between gap-4 py-2">
                  <dt className="text-muted">生成时间</dt>
                  <dd className="text-fg">{dataset.generatedAt.slice(0, 19).replace('T', ' ')}</dd>
                </div>
                <div className="flex justify-between gap-4 py-2">
                  <dt className="text-muted">建筑范围</dt>
                  <dd className="text-right text-fg">{dataset.buildingScope}</dd>
                </div>
                <div className="flex justify-between gap-4 py-2">
                  <dt className="text-muted">能源覆盖</dt>
                  <dd className="text-right text-fg">{dataset.energyScope}</dd>
                </div>
                <div className="flex justify-between gap-4 py-2">
                  <dt className="text-muted">数据时间区间</dt>
                  <dd className="text-fg">
                    {fmtShanghaiDate(dataset.dataFrom)} ~ {fmtShanghaiDate(dataset.dataTo)}
                  </dd>
                </div>
              </>
            )}
            <div className="flex justify-between gap-4 py-2">
              <dt className="text-muted">存储与计算引擎</dt>
              <dd className="inline-flex items-center gap-1 text-fg">
                <Database className="h-3.5 w-3.5 text-muted" aria-hidden="true" />
                PostgreSQL · Go 正式计算服务
              </dd>
            </div>
          </dl>

          {originNotice && (
            <p className="mt-3 rounded-[4px] bg-fill-1 px-3 py-2 text-[12px] leading-relaxed text-fg-2">
              {originNotice}
            </p>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

/**
 * MobileFilterDrawer tucks filter controls into a bottom sheet on mobile (<768px)
 * while showing a 1-line summary bar.
 */
export function MobileFilterDrawer({
  summary,
  children,
}: {
  summary: string;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);

  return (
    <div className="md:hidden">
      <div className="flex items-center justify-between gap-2 rounded-[4px] border border-line bg-surface px-3 py-2 text-[12px]">
        <span className="min-w-0 truncate text-fg-2 tabular">{summary || '全部默认范围'}</span>
        <Dialog.Root open={open} onOpenChange={setOpen}>
          <Dialog.Trigger asChild>
            <button
              type="button"
              className="inline-flex h-7 shrink-0 items-center gap-1 rounded-[2px] border border-line bg-fill-1 px-2.5 font-medium text-fg"
            >
              <Filter className="h-3.5 w-3.5 text-primary" aria-hidden="true" />
              筛选
            </button>
          </Dialog.Trigger>
          <Dialog.Portal>
            <Dialog.Overlay className="fixed inset-0 z-50 bg-black/45" />
            <Dialog.Content
              className="fixed inset-x-0 bottom-0 z-50 max-h-[82vh] overflow-y-auto rounded-t-[8px] border-t border-line bg-surface p-5 shadow-panel outline-none"
              aria-describedby={undefined}
            >
              <div className="flex items-center justify-between border-b border-line pb-3">
                <Dialog.Title className="text-[15px] font-medium text-fg">统计范围筛选</Dialog.Title>
                <Dialog.Close asChild>
                  <button type="button" aria-label="关闭筛选" className="rounded-[2px] p-1 text-muted hover:bg-fill-2">
                    <X className="h-4 w-4" />
                  </button>
                </Dialog.Close>
              </div>
              <div className="mt-4 flex flex-col gap-3">{children}</div>
              <div className="mt-5">
                <Dialog.Close asChild>
                  <button type="button" className="arco-btn arco-btn-primary w-full">
                    应用并返回视图
                  </button>
                </Dialog.Close>
              </div>
            </Dialog.Content>
          </Dialog.Portal>
        </Dialog.Root>
      </div>
    </div>
  );
}

/**
 * CollapsibleSection implements Arco Collapse progressive disclosure for technical evidence.
 */
export function CollapsibleSection({
  title,
  subtitle,
  defaultOpen = false,
  badge,
  children,
}: {
  title: string;
  subtitle?: string;
  defaultOpen?: boolean;
  badge?: string;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <section className="overflow-hidden rounded-[4px] border border-line bg-surface">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="flex w-full items-center justify-between gap-3 px-4 py-3 text-left transition-colors hover:bg-fill-1"
      >
        <div className="flex min-w-0 items-center gap-2">
          {open ? (
            <ChevronDown className="h-4 w-4 shrink-0 text-muted" aria-hidden="true" />
          ) : (
            <ChevronRight className="h-4 w-4 shrink-0 text-muted" aria-hidden="true" />
          )}
          <span className="truncate text-[14px] font-medium text-fg">{title}</span>
          {badge && (
            <span className="shrink-0 rounded-[2px] bg-fill-2 px-1.5 py-0.5 text-[11px] font-normal text-fg-2 tabular">
              {badge}
            </span>
          )}
        </div>
        {subtitle && <span className="hidden shrink-0 text-[12px] text-muted sm:inline">{subtitle}</span>}
      </button>
      {open && <div className="border-t border-line">{children}</div>}
    </section>
  );
}
