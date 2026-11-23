import { useEffect, useRef } from 'react';
import * as echarts from 'echarts/core';
import { SankeyChart, TreemapChart } from 'echarts/charts';
import { TooltipComponent } from 'echarts/components';
import { CanvasRenderer } from 'echarts/renderers';

echarts.use([TreemapChart, SankeyChart, TooltipComponent, CanvasRenderer]);

const numFmt = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 });
const pctFmt = new Intl.NumberFormat('zh-CN', { style: 'percent', maximumFractionDigits: 1 });

function fmtVal(v: number | null | undefined): string {
  return v == null || !Number.isFinite(v) ? '—' : numFmt.format(v);
}

// Restrained editorial industrial energy palette (zero loud/neon colors)
const EDITORIAL_PALETTE = [
  '#2563EB', // architectural cobalt
  '#0F766E', // deep muted teal
  '#3B82F6', // clear slate-blue
  '#4338CA', // restrained indigo
  '#047857', // forest emerald
  '#475569', // neutral steel slate
];

const SANKEY_PARENT_COLOR = '#1E40AF';
const SANKEY_DIFF_COLOR = '#D97706';

// ── 1. F02 Interactive Composition Treemap (Apache ECharts) ──────────────────

export type TreemapMeterLeaf = {
  id: string;
  name: string;
  terminalId: string;
  sourceTable: string;
  value: number;
  weightPct: number;
  valid: number;
  expected: number;
};

export type TreemapCategoryNode = {
  id: string;
  name: string;
  value: number;
  share: number | null;
  shareDiffPp: number | null;
  usageDelta: number | null;
  coverageRate: number | null;
  isUnclassified?: boolean;
  /** Strictly populated ONLY from real confirmed inventory category bindings. */
  children: TreemapMeterLeaf[];
};

export function EnergyTreemapChart({
  nodes,
  unit,
  energyLabel,
  focusedCategoryId,
  onSelectCategory,
  onSelectMeter,
  height = 520,
}: {
  nodes: TreemapCategoryNode[];
  unit: string;
  energyLabel: string;
  focusedCategoryId: string | null;
  onSelectCategory: (catId: string | null) => void;
  onSelectMeter?: (catId: string) => void;
  height?: number;
}) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const chartRef = useRef<echarts.ECharts | null>(null);

  useEffect(() => {
    const el = hostRef.current;
    if (!el) return;
    const chart = echarts.init(el, undefined, { renderer: 'canvas' });
    chartRef.current = chart;

    const ro = new ResizeObserver(() => {
      chart.resize();
    });
    ro.observe(el);

    return () => {
      ro.disconnect();
      chart.dispose();
      chartRef.current = null;
    };
  }, []);

  useEffect(() => {
    const chart = chartRef.current;
    if (!chart) return;

    const focusedNode = focusedCategoryId ? nodes.find((n) => n.id === focusedCategoryId) ?? null : null;
    const isDrilled = Boolean(focusedNode && focusedNode.children.length > 0);

    const seriesData =
      isDrilled && focusedNode
        ? focusedNode.children.map((leaf, idx) => ({
            id: leaf.id,
            name: leaf.name,
            value: Math.max(leaf.value, 0.0001),
            itemStyle: {
              color: EDITORIAL_PALETTE[idx % EDITORIAL_PALETTE.length],
              borderColor: '#FFFFFF',
              borderWidth: 2,
              gapWidth: 2,
            },
            rawMeta: {
              kind: 'meter' as const,
              categoryId: focusedNode.id,
              categoryName: focusedNode.name,
              terminalId: leaf.terminalId,
              sourceTable: leaf.sourceTable,
              usage: leaf.value,
              weightPct: leaf.weightPct,
              share: focusedNode.value > 0 ? leaf.value / focusedNode.value : null,
              valid: leaf.valid,
              expected: leaf.expected,
            },
          }))
        : nodes
            .filter((n) => n.value > 0)
            .map((n, idx) => ({
              id: n.id,
              name: n.name,
              value: n.value,
              itemStyle: {
                color: n.isUnclassified ? '#64748B' : EDITORIAL_PALETTE[idx % EDITORIAL_PALETTE.length],
                borderColor: '#FFFFFF',
                borderWidth: 2,
                gapWidth: 2,
              },
              rawMeta: {
                kind: 'category' as const,
                categoryId: n.id,
                usage: n.value,
                share: n.share,
                shareDiffPp: n.shareDiffPp,
                usageDelta: n.usageDelta,
                coverageRate: n.coverageRate,
                childCount: n.children.length,
                isUnclassified: Boolean(n.isUnclassified),
              },
            }));

    chart.setOption(
      {
        animation: false,
        tooltip: {
          trigger: 'item',
          backgroundColor: 'rgba(15, 23, 42, 0.94)',
          borderColor: 'rgba(148, 163, 184, 0.25)',
          borderWidth: 1,
          padding: [10, 12],
          textStyle: { color: '#F8FAFC', fontSize: 12, fontFamily: 'Inter, system-ui, sans-serif' },
          formatter: (params: unknown) => {
            const p = params as { name?: string; data?: { rawMeta?: Record<string, unknown> } };
            const m = p.data?.rawMeta;
            if (!m) return p.name ?? '';
            if (m.kind === 'meter') {
              const shareTxt = m.share != null ? pctFmt.format(Number(m.share)) : '—';
              return [
                `<div style="font-weight:600;margin-bottom:4px">${p.name}</div>`,
                `<div style="color:#CBD5E1;font-size:11px;margin-bottom:6px">${m.terminalId} · ${m.sourceTable}</div>`,
                `<div>归属分项用量：<b>${fmtVal(Number(m.usage))} ${unit}</b>（占分项 ${shareTxt}）</div>`,
                `<div>绑定分摊权重：${Number(m.weightPct).toFixed(0)}% · 有效槽位 ${m.valid}/${m.expected}</div>`,
                `<div style="margin-top:6px;color:#93C5FD;font-size:11px">点击查看完整计量溯源抽屉 →</div>`,
              ].join('');
            }
            const shareTxt = m.share != null ? pctFmt.format(Number(m.share)) : '—';
            const pp = m.shareDiffPp != null ? Number(m.shareDiffPp) : null;
            const ppTxt = pp != null ? `${pp > 0 ? '+' : ''}${pp.toFixed(1)} pp` : '无可比前日';
            const delta = m.usageDelta != null ? Number(m.usageDelta) : null;
            const deltaTxt = delta != null ? `${delta > 0 ? '+' : ''}${fmtVal(delta)} ${unit}` : '无可比前日';
            const drillHint =
              Number(m.childCount) > 0
                ? `<div style="margin-top:6px;color:#93C5FD;font-size:11px">点击下钻查看 ${m.childCount} 块关联计量点 →</div>`
                : `<div style="margin-top:6px;color:#94A3B8;font-size:11px">无已绑定下级计量点（点击查看溯源说明）</div>`;
            return [
              `<div style="font-weight:600;margin-bottom:4px">${p.name}</div>`,
              `<div>实测用量：<b>${fmtVal(Number(m.usage))} ${unit}</b>（占${energyLabel} ${shareTxt}）</div>`,
              `<div>较前日变化：${deltaTxt} · 结构变动：${ppTxt}</div>`,
              `<div>覆盖率：${m.coverageRate != null ? pctFmt.format(Number(m.coverageRate)) : '—'}</div>`,
              drillHint,
            ].join('');
          },
        },
        series: [
          {
            type: 'treemap',
            left: 0,
            right: 0,
            top: 0,
            bottom: 0,
            roam: false,
            nodeClick: false,
            breadcrumb: { show: false },
            visualDimension: 0,
            label: {
              show: true,
              position: 'insideTopLeft',
              padding: [12, 14],
              formatter: (params: unknown) => {
                const p = params as { name?: string; data?: { rawMeta?: Record<string, unknown> } };
                const m = p.data?.rawMeta;
                if (!m) return p.name ?? '';
                const usageStr = `${fmtVal(Number(m.usage))} ${unit}`;
                const shareStr = m.share != null ? pctFmt.format(Number(m.share)) : '';
                if (m.kind === 'meter') {
                  return `{title|${p.name}}\n{val|${usageStr}}\n{sub|占分项 ${shareStr} · 权重 ${Number(m.weightPct).toFixed(0)}%}\n{meta|${m.terminalId} · 有效 ${m.valid}/${m.expected} · 点击查看溯源}`;
                }
                const pp = m.shareDiffPp != null ? Number(m.shareDiffPp) : null;
                const ppTag = pp != null && Math.abs(pp) >= 0.05 ? ` · ${pp > 0 ? '+' : ''}${pp.toFixed(1)} pp` : '';
                const subTag = Number(m.childCount) > 0 ? `${m.childCount} 块关联表计` : '无下级绑定';
                return `{title|${p.name}}\n{val|${usageStr}}\n{sub|${shareStr}${ppTag}}\n{meta|${subTag}}`;
              },
              rich: {
                title: {
                  color: '#FFFFFF',
                  fontSize: 14,
                  fontWeight: 600,
                  lineHeight: 22,
                },
                val: {
                  color: '#FFFFFF',
                  fontSize: 19,
                  fontWeight: 600,
                  lineHeight: 28,
                  fontFamily: 'Inter, system-ui, sans-serif',
                },
                sub: {
                  color: 'rgba(255,255,255,0.9)',
                  fontSize: 12,
                  fontWeight: 500,
                  lineHeight: 22,
                },
                meta: {
                  color: 'rgba(255,255,255,0.74)',
                  fontSize: 11,
                  lineHeight: 18,
                },
              },
            },
            itemStyle: {
              borderRadius: 4,
              borderColor: '#FFFFFF',
              borderWidth: 2,
              gapWidth: 2,
            },
            emphasis: {
              itemStyle: {
                shadowBlur: 12,
                shadowColor: 'rgba(15, 23, 42, 0.16)',
              },
            },
            data: seriesData,
          },
        ],
      },
      true,
    );

    const handleClick = (params: unknown) => {
      const p = params as { data?: { rawMeta?: { kind?: string; categoryId?: string } } };
      const m = p.data?.rawMeta;
      if (!m?.categoryId) return;
      if (m.kind === 'category') {
        onSelectCategory(m.categoryId);
      } else if (m.kind === 'meter' && onSelectMeter) {
        onSelectMeter(m.categoryId);
      }
    };

    chart.off('click');
    chart.on('click', handleClick);
  }, [nodes, unit, energyLabel, focusedCategoryId, onSelectCategory, onSelectMeter]);

  if (nodes.filter((n) => n.value > 0).length === 0) {
    return (
      <div
        style={{ height }}
        className="flex flex-col items-center justify-center rounded-[4px] border border-dashed border-line bg-fill-1/40 text-[13px] text-muted"
      >
        <span>当前能源暂无有效分项实测构成数据</span>
      </div>
    );
  }

  return (
    <div
      ref={hostRef}
      data-echarts-treemap
      style={{ height }}
      className="w-full"
      aria-label={`${energyLabel}分项构成交互式 Treemap`}
    />
  );
}

// ── 2. F09 Professional Energy Flow Sankey (Apache ECharts) ──────────────────

export type SankeyMeterNodeMeta = {
  id: string;
  name: string;
  /** Shortened display label (strips redundant building prefix already shown in toolbar) */
  displayName?: string;
  role: 'parent' | 'child' | 'diff';
  terminalId: string;
  itemCode: string;
  zone: string;
  usage: number | null;
  share: number | null;
  status: string;
  statusLabel: string;
  topologyVersion?: string;
  note?: string;
};

export function EnergySankeyChart({
  parentNode,
  childNodes,
  diffNode,
  unit,
  zoom = 1,
  selectedNodeId,
  onSelectNode,
  height = 'clamp(540px, calc(100vh - 185px), 780px)',
}: {
  parentNode: SankeyMeterNodeMeta;
  childNodes: SankeyMeterNodeMeta[];
  diffNode: SankeyMeterNodeMeta | null;
  unit: string;
  zoom?: number;
  selectedNodeId?: string | null;
  onSelectNode: (node: SankeyMeterNodeMeta) => void;
  height?: number | string;
}) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const chartRef = useRef<echarts.ECharts | null>(null);

  useEffect(() => {
    const el = hostRef.current;
    if (!el) return;
    const chart = echarts.init(el, undefined, { renderer: 'canvas' });
    chartRef.current = chart;

    const ro = new ResizeObserver(() => {
      chart.resize();
    });
    ro.observe(el);

    return () => {
      ro.disconnect();
      chart.dispose();
      chartRef.current = null;
    };
  }, []);

  useEffect(() => {
    const chart = chartRef.current;
    if (!chart) return;

    // Sort child sub-meters strictly descending by usage; keep unassigned diff strictly at bottom (zero flow crossing)
    const sortedChildren = [...childNodes].sort(
      (a, b) => Math.abs(b.usage ?? 0) - Math.abs(a.usage ?? 0),
    );
    const targets: SankeyMeterNodeMeta[] = [...sortedChildren, ...(diffNode ? [diffNode] : [])];

    const metaMap = new Map<string, SankeyMeterNodeMeta>();
    metaMap.set(parentNode.id, parentNode);
    for (const t of targets) metaMap.set(t.id, t);

    const parentShort = parentNode.displayName || parentNode.name;

    const nodes = [
      {
        name: parentNode.id,
        depth: 0,
        itemStyle: {
          color: SANKEY_PARENT_COLOR,
          borderColor: selectedNodeId === parentNode.id ? '#0F172A' : 'transparent',
          borderWidth: selectedNodeId === parentNode.id ? 2 : 0,
          borderRadius: 2,
        },
        label: {
          position: 'left' as const,
          align: 'right' as const,
          distance: 16,
          formatter: () =>
            `{title|${parentShort}}\n{val|${fmtVal(parentNode.usage)} ${unit}}\n{tag|父总表 · 100%}`,
        },
      },
      ...targets.map((t, i) => {
        const isDiff = t.role === 'diff';
        const color = isDiff ? SANKEY_DIFF_COLOR : EDITORIAL_PALETTE[i % EDITORIAL_PALETTE.length];
        const shareStr = t.share != null ? pctFmt.format(Math.abs(t.share)) : '—';
        const shortName = t.displayName || t.name;
        const metaLine = isDiff
          ? `待核查差额（非线损）· ${shareStr}`
          : `${t.zone !== '—' ? t.zone : t.terminalId} · 占总表 ${shareStr}`;
        return {
          name: t.id,
          depth: 1,
          itemStyle: {
            color,
            borderColor: selectedNodeId === t.id ? '#0F172A' : 'transparent',
            borderWidth: selectedNodeId === t.id ? 2 : 0,
            borderRadius: 2,
          },
          label: {
            position: 'right' as const,
            align: 'left' as const,
            distance: 16,
            formatter: () =>
              `{title|${shortName}}  {${isDiff ? 'valWarn' : 'val'}|${fmtVal(t.usage)} ${unit}}\n{meta|${metaLine}}`,
          },
        };
      }),
    ];

    const links = targets
      .filter((t) => t.usage != null && Math.abs(t.usage) > 0.0001)
      .map((t) => ({
        source: parentNode.id,
        target: t.id,
        value: Math.abs(t.usage ?? 0),
        lineStyle:
          t.role === 'diff'
            ? { color: SANKEY_DIFF_COLOR, opacity: 0.24, curveness: 0.5 }
            : { color: 'gradient', opacity: 0.22, curveness: 0.5 },
      }));

    // Clean outside-label margins so ZERO text overlaps flow ribbons at both 1440px and 1920px
    const clampedZoom = Math.min(1.4, Math.max(0.75, zoom));
    const leftPct = Math.max(14, Math.min(24, Math.round(19 / clampedZoom)));
    const rightPct = Math.max(20, Math.min(32, Math.round(27 / clampedZoom)));
    const vPadPct = Math.max(3, Math.round(5 / clampedZoom));

    chart.setOption(
      {
        animationDuration: 360,
        animationEasing: 'cubicOut',
        tooltip: {
          trigger: 'item',
          triggerOn: 'mousemove',
          backgroundColor: 'rgba(15, 23, 42, 0.94)',
          borderColor: 'rgba(148, 163, 184, 0.25)',
          borderWidth: 1,
          padding: [10, 12],
          textStyle: { color: '#F8FAFC', fontSize: 12, fontFamily: 'Inter, system-ui, sans-serif' },
          formatter: (params: unknown) => {
            const p = params as {
              dataType?: string;
              name?: string;
              data?: { source?: string; target?: string; value?: number };
            };
            if (p.dataType === 'edge' && p.data?.source && p.data?.target) {
              const src = metaMap.get(p.data.source);
              const tgt = metaMap.get(p.data.target);
              const shareStr = tgt?.share != null ? pctFmt.format(Math.abs(tgt.share)) : '—';
              return [
                `<div style="font-weight:600;margin-bottom:4px">${src?.name ?? ''} → ${tgt?.name ?? ''}</div>`,
                `<div>计量流量：<b>${fmtVal(tgt?.usage)} ${unit}</b>（占总表 ${shareStr}）</div>`,
                `<div style="margin-top:4px;color:#93C5FD;font-size:11px">点击节点或流带查看计量溯源详情</div>`,
              ].join('');
            }
            const meta = p.name ? metaMap.get(p.name) : null;
            if (!meta) return '';
            const shareStr = meta.share != null ? pctFmt.format(Math.abs(meta.share)) : '—';
            return [
              `<div style="font-weight:600;margin-bottom:4px">${meta.name}</div>`,
              `<div style="color:#CBD5E1;font-size:11px;margin-bottom:4px">${meta.role === 'parent' ? '父总表' : meta.role === 'diff' ? '总分表差额项（非线损）' : `直接分表 · ${meta.terminalId}`}</div>`,
              `<div>实测用量：<b>${fmtVal(meta.usage)} ${unit}</b>${meta.role !== 'parent' ? `（占总表 ${shareStr}）` : ''}</div>`,
              `<div>状态：${meta.statusLabel}</div>`,
              `<div style="margin-top:4px;color:#93C5FD;font-size:11px">点击打开右侧计量溯源抽屉 →</div>`,
            ].join('');
          },
        },
        series: [
          {
            type: 'sankey',
            left: `${leftPct}%`,
            right: `${rightPct}%`,
            top: `${vPadPct}%`,
            bottom: `${vPadPct}%`,
            nodeWidth: Math.round(16 * Math.min(1.15, clampedZoom)),
            nodeGap: Math.round(30 * clampedZoom),
            nodeAlign: 'justify',
            layoutIterations: 0,
            draggable: false,
            orient: 'horizontal',
            emphasis: {
              focus: 'adjacency',
              lineStyle: { opacity: 0.56 },
            },
            blur: {
              itemStyle: { opacity: 0.32 },
              lineStyle: { opacity: 0.06 },
            },
            label: {
              show: true,
              rich: {
                title: {
                  color: '#0F172A',
                  fontSize: 13.5,
                  fontWeight: 600,
                  lineHeight: 21,
                },
                val: {
                  color: '#1E40AF',
                  fontSize: 13.5,
                  fontWeight: 600,
                  lineHeight: 21,
                  fontFamily: 'Inter, system-ui, sans-serif',
                },
                valWarn: {
                  color: '#B45309',
                  fontSize: 13.5,
                  fontWeight: 600,
                  lineHeight: 21,
                  fontFamily: 'Inter, system-ui, sans-serif',
                },
                tag: {
                  color: '#475569',
                  backgroundColor: '#F1F5F9',
                  borderRadius: 2,
                  padding: [2, 6],
                  fontSize: 11,
                  lineHeight: 18,
                },
                meta: {
                  color: '#475569',
                  fontSize: 12,
                  lineHeight: 19,
                },
              },
            },
            data: nodes,
            links,
          },
        ],
      },
      true,
    );

    chart.off('click');
    chart.on('click', (params: unknown) => {
      const p = params as { dataType?: string; name?: string; data?: { target?: string } };
      const id = p.dataType === 'edge' ? p.data?.target : p.name;
      if (!id) return;
      const hit = metaMap.get(id);
      if (hit) onSelectNode(hit);
    });
  }, [parentNode, childNodes, diffNode, unit, zoom, selectedNodeId, onSelectNode]);

  return (
    <div
      ref={hostRef}
      data-echarts-sankey
      style={{ height }}
      className="w-full"
      aria-label="物理总分表能流拓扑图（Apache ECharts Sankey）"
    />
  );
}
