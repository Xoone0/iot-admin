import { useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import { ArrowUpRight } from 'lucide-react';
import type { Envelope } from '../api/types';
import { Drawer } from '../components/Drawer';
import { ENERGY_PREVIEW_API, energyNumber } from './BuildingEnergyWorkspace';

export interface EnergyCoverage {
  expected: number;
  valid: number;
  missing: number;
  suspect: number;
  rate: number | null;
}

export interface EnergyValue {
  usage: number | null;
  coverage: EnergyCoverage;
}

export interface EnergyWindow {
  timezone: string;
  from: string;
  to: string;
  asOf: string;
}

export interface EnergySource {
  name: string;
  source: string;
  usage: number | null;
  valid: number;
  missing: number;
  suspect: number;
  profileVersion: string;
  bindingVersion: string;
  categoryBindingVersion?: string;
  mappingGeneration: number;
}

export interface EnergyEvidenceData extends EnergyWindow {
  unit: string;
  ruleVersion: string;
  meterRuleVersion: string;
  classificationRuleVersion: string;
  catalogVersion: string;
  topologyVersion: string;
  sources: EnergySource[] | null;
}

export type EnergyQuality = 'valid' | 'valid_zero' | 'missing' | 'suspect' | 'partial';

export const energyQualityLabel: Record<EnergyQuality, string> = {
  valid: '覆盖完整',
  valid_zero: '有效零',
  missing: '缺测',
  suspect: '可疑',
  partial: '部分覆盖',
};

const percentFormatter = new Intl.NumberFormat('zh-CN', { style: 'percent', maximumFractionDigits: 1 });

export function energyPercent(value: number | null) {
  return value === null ? '—' : percentFormatter.format(value);
}

export function energyTime(value: string, timeZone: string) {
  return new Intl.DateTimeFormat('zh-CN', { timeZone, dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value));
}

export function EnergyInterval({ window }: { window: EnergyWindow }) {
  return (
    <span>
      <time dateTime={window.from}>{energyTime(window.from, window.timezone)}</time>
      {' 至 '}
      <time dateTime={window.to}>{energyTime(window.to, window.timezone)}</time>
      {'（项目时间，结束时刻不计入）'}
    </span>
  );
}

export function useEnergyPreview<T extends { sample: boolean }>(path: string, query: string) {
  const [attempt, setAttempt] = useState(0);
  const [response, setResponse] = useState<{ key: string; data?: T; error?: string }>();
  const url = `${ENERGY_PREVIEW_API}/${path}?${query}`;
  const key = `${url}|${attempt}`;
  const current = response?.key === key ? response : undefined;

  useEffect(() => {
    const controller = new AbortController();
    fetch(url, { signal: controller.signal })
      .then(async (response) => {
        const payload = await response.json().catch(() => undefined) as Envelope<T> | undefined;
        if (!response.ok || payload?.code !== 0 || !payload.data?.sample) {
          throw new Error(payload?.message || '样本服务暂不可用，请重试。');
        }
        if (!controller.signal.aborted) setResponse({ key, data: payload.data });
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) {
          setResponse({ key, error: error instanceof Error ? error.message : '样本加载失败，请重试。' });
        }
      });
    return () => controller.abort();
  }, [url, key]);

  return { data: current?.data, error: current?.error, retry: () => setAttempt((value) => value + 1) };
}

export function EnergyEvidence({ data, children }: { data: EnergyEvidenceData; children?: ReactNode }) {
  const [open, setOpen] = useState(false);
  return (
    <Drawer open={open} onOpenChange={setOpen} title="计量来源与口径" description="当前统计范围内的合成计量来源。"
      trigger={(
        <button type="button" className="inline-flex min-h-10 items-center gap-1.5 rounded-md px-2 text-sm font-medium text-primary hover:bg-primary-soft">
          查看计量来源 <ArrowUpRight className="size-4" aria-hidden />
        </button>
      )}
    >
      <div className="space-y-5 text-sm">
        <div>
          <h3 className="font-semibold">统计范围</h3>
          <p className="mt-1.5 leading-6 text-muted"><EnergyInterval window={data} /></p>
          <p className="leading-6 text-muted">数据截至 {energyTime(data.asOf, data.timezone)}</p>
        </div>
        {children}
        <div>
          <h3 className="font-semibold">计量点</h3>
          {data.sources?.length ? (
            <ul className="mt-1 divide-y divide-line">
              {data.sources.map((source) => (
                <li key={source.source} className="py-3">
                  <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5">
                    <span className="font-medium">{source.name}</span>
                    <span className="tabular">{energyNumber(source.usage)}{source.usage === null ? '' : ` ${data.unit}`}</span>
                  </div>
                  <p className="mt-1 text-xs leading-5 text-muted">
                    有效 {energyNumber(source.valid)} · 缺测 {energyNumber(source.missing)} · 可疑 {energyNumber(source.suspect)} 表小时
                  </p>
                  <details className="mt-1.5 text-xs text-muted">
                    <summary className="w-fit cursor-pointer rounded py-1 text-primary">通道及归属版本</summary>
                    <dl className="mt-1 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 break-all">
                      <dt>计量通道</dt><dd>{source.source}</dd>
                      <dt>计量点版本</dt><dd>{source.profileVersion}</dd>
                      <dt>归属版本</dt><dd>{source.bindingVersion}</dd>
                      <dt>分类映射</dt><dd>{source.categoryBindingVersion || '未分项'}</dd>
                      <dt>映射代次</dt><dd>{source.mappingGeneration}</dd>
                    </dl>
                  </details>
                </li>
              ))}
            </ul>
          ) : <p className="mt-2 text-sm text-muted">此范围暂无计量点。</p>}
        </div>
        <details className="border-t border-line pt-3">
          <summary className="w-fit cursor-pointer rounded py-1 text-primary">计算与目录版本</summary>
          <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 break-all text-xs text-muted">
            <dt>当前计算</dt><dd>{data.ruleVersion}</dd>
            <dt>计量规则</dt><dd>{data.meterRuleVersion}</dd>
            <dt>分类规则</dt><dd>{data.classificationRuleVersion}</dd>
            <dt>分类目录</dt><dd>{data.catalogVersion}</dd>
            <dt>计量拓扑</dt><dd>{data.topologyVersion}</dd>
            <dt>项目时区</dt><dd>{data.timezone}</dd>
          </dl>
        </details>
      </div>
    </Drawer>
  );
}
