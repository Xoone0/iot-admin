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

// Restrained industrial energy palette (aligned with Arco Design tokens — zero neon/rainbow noise)
const TREEMAP_PALETTE = [
  '#165DFF', // primary cobalt
  '#0FC6C2', // teal
  '#3491FA', // azure
  '#551DB0', // deep indigo
  '#00B42A', // emerald
  '#722ED1', // violet
];

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
  height = 500,
}: {
  nodes: TreemapCategoryNode[];
  unit: string;
  energyLabel: string;
  focusedCategoryId: string | null;
  onSelectCategory: (catId: string | null) => void;
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

    const seriesData = isDrilled && focusedNode
      ? focusedNode.children.map((leaf, idx) => ({
          id: leaf.id,
          name: leaf.name,
          value: Math.max(leaf.value, 0.0001),
          itemStyle: {
            color: TREEMAP_PALETTE[idx % TREEMAP_PALETTE.length],
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
          .map((n, idx) => {
            const baseColor = n.isUnclassified ? '#86909C' : TREEMAP_PALETTE[idx % TREEMAP_PALETTE.length];
            return {
              id: n.id,
              name: n.name,
              value: n.value,
              itemStyle: {
                color: baseColor,
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
            };
          });

    chart.setOption(
      {
        animationDuration: 320,
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
              ].join('');
            }
            const shareTxt = m.share != null ? pctFmt.format(Number(m.share)) : '—';
            const pp = m.shareDiffPp != null ? Number(m.shareDiffPp) : null;
            const ppTxt = pp != null ? `${pp > 0 ? '+' : ''}${pp.toFixed(1)} pp` : '无可比前日';
            const delta = m.usageDelta != null ? Number(m.usageDelta) : null;
            const deltaTxt = delta != null ? `${delta > 0 ? '+' : ''}${fmtVal(delta)} ${unit}` : '—';
            const drillHint =
              Number(m.childCount) > 0
                ? `<div style="margin-top:6px;color:#93C5FD;font-size:11px">点击下钻查看 ${m.childCount} 块关联计量点 →</div>`
                : `<div style="margin-top:6px;color:#94A3B8;font-size:11px">无已绑定下级计量点</div>`;
            return [
              `<div style="font-weight:600;margin-bottom:4px">${p.name}</div>`,
              `<div>实测用量：<b>${fmtVal(Number(m.usage))} ${unit}</b>（占${energyLabel} ${shareTxt}）</div>`,
              `<div>较前日用量变化：${deltaTxt} · 结构变动：${ppTxt}</div>`,
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
              padding: [10, 12],
              formatter: (params: unknown) => {
                const p = params as { name?: string; data?: { rawMeta?: Record<string, unknown> } };
                const m = p.data?.rawMeta;
                if (!m) return p.name ?? '';
                const usageStr = `${fmtVal(Number(m.usage))} ${unit}`;
                const shareStr = m.share != null ? pctFmt.format(Number(m.share)) : '';
                if (m.kind === 'meter') {
                  return `{title|${p.name}}\n{val|${usageStr}}\n{sub|${shareStr}}\n{meta|权重 ${Number(m.weightPct).toFixed(0)}% · ${m.terminalId}}`;
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
                  fontSize: 18,
                  fontWeight: 600,
                  lineHeight: 26,
                  fontFamily: 'Inter, system-ui, sans-serif',
                },
                sub: {
                  color: 'rgba(255,255,255,0.88)',
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
      const p = params as { data?: { rawMeta?: { kind?: string; categoryId?: string; childCount?: number } } };
      const m = p.data?.rawMeta;
      if (!m?.categoryId) return;
      if (m.kind === 'category') {
        onSelectCategory(m.categoryId);
      }
    };

    chart.off('click');
    chart.on('click', handleClick);
  }, [nodes, unit, energyLabel, focusedCategoryId, onSelectCategory]);

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
  height = 580,
}: {
  parentNode: SankeyMeterNodeMeta;
  childNodes: SankeyMeterNodeMeta[];
  diffNode: SankeyMeterNodeMeta | null;
  unit: string;
  zoom?: number;
  selectedNodeId?: string | null;
  onSelectNode: (node: SankeyMeterNodeMeta) => void;
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

    const metaMap = new Map<string, SankeyMeterNodeMeta>();
    metaMap.set(parentNode.id, parentNode);
    for (const c of childNodes) metaMap.set(c.id, c);
    if (diffNode) metaMap.set(diffNode.id, diffNode);

    const targets: SankeyMeterNodeMeta[] = [...childNodes, ...(diffNode ? [diffNode] : [])];

    const nodes = [
      {
        name: parentNode.id,
        depth: 0,
        itemStyle: {
          color: '#165DFF',
          borderColor: selectedNodeId === parentNode.id ? '#0E42D2' : 'transparent',
          borderWidth: selectedNodeId === parentNode.id ? 2 : 0,
          borderRadius: 2,
        },
        label: {
          position: 'right' as const,
          distance: 12,
          formatter: () =>
            `{title|${parentNode.name}}\n{val|${fmtVal(parentNode.usage)} ${unit}}  {tag|父总表}`,
        },
      },
      ...targets.map((t, i) => {
        const isDiff = t.role === 'diff';
        const color = isDiff ? '#FF7D00' : TREEMAP_PALETTE[(i + 1) % TREEMAP_PALETTE.length];
        const shareStr = t.share != null ? pctFmt.format(Math.abs(t.share)) : '—';
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
            position: 'left' as const,
            distance: 12,
            formatter: () =>
              `{title|${t.name}}  {val|${fmtVal(t.usage)} ${unit}}\n{meta|${isDiff ? '未分配/待核查差额（非线损）' : t.zone !== '—' ? t.zone : t.terminalId} · 占总表 ${shareStr}}`,
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
        lineStyle: t.role === 'diff'
          ? { color: '#FF7D00', opacity: 0.26, curveness: 0.52 }
          : { color: 'gradient', opacity: 0.28, curveness: 0.52 },
      }));

    // Apply clean proportional padding scaled by zoom (1 = fit canvas, >1 = zoom in, <1 = zoom out)
    const clampedZoom = Math.min(1.5, Math.max(0.7, zoom));
    const hPadPct = Math.max(3, Math.round(6 / clampedZoom));
    const vPadPct = Math.max(4, Math.round(7 / clampedZoom));

    chart.setOption(
      {
        animationDuration: 420,
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
                `<div style="margin-top:4px;color:#93C5FD;font-size:11px">点击节点查看计量点编码、状态与溯源详情</div>`,
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
            left: `${hPadPct}%`,
            right: `${hPadPct}%`,
            top: `${vPadPct}%`,
            bottom: `${vPadPct}%`,
            nodeWidth: Math.round(14 * Math.min(1.2, clampedZoom)),
            nodeGap: Math.round(24 * clampedZoom),
            nodeAlign: 'justify',
            layoutIterations: 0,
            draggable: false,
            orient: 'horizontal',
            emphasis: {
              focus: 'adjacency',
              lineStyle: { opacity: 0.62 },
            },
            blur: {
              itemStyle: { opacity: 0.35 },
              lineStyle: { opacity: 0.08 },
            },
            label: {
              show: true,
              rich: {
                title: {
                  color: '#0F172A',
                  fontSize: 13,
                  fontWeight: 600,
                  lineHeight: 20,
                },
                val: {
                  color: '#165DFF',
                  fontSize: 13,
                  fontWeight: 600,
                  lineHeight: 20,
                  fontFamily: 'Inter, system-ui, sans-serif',
                },
                tag: {
                  color: '#4E5969',
                  backgroundColor: '#F2F3F5',
                  borderRadius: 2,
                  padding: [1, 5],
                  fontSize: 10,
                  lineHeight: 16,
                },
                meta: {
                  color: '#64748B',
                  fontSize: 11,
                  lineHeight: 17,
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
