import { useId, useMemo, useState } from 'react';

const numFmt = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 3 });
const compactFmt = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 });
const pctFmt = new Intl.NumberFormat('zh-CN', { style: 'percent', maximumFractionDigits: 1 });

function fmtVal(v: number | null | undefined) {
  return v == null ? '—' : numFmt.format(v);
}

function fmtCompact(v: number) {
  const abs = Math.abs(v);
  if (abs >= 1_000_000) return `${compactFmt.format(v / 1_000_000)}M`;
  if (abs >= 10_000) return `${compactFmt.format(v / 1_000)}k`;
  return compactFmt.format(v);
}

function niceScale(rawMax: number, ticks = 4): { max: number; steps: number[] } {
  if (rawMax <= 0 || !Number.isFinite(rawMax)) {
    return { max: 1, steps: [0, 0.25, 0.5, 0.75, 1] };
  }
  const roughStep = rawMax / ticks;
  const pow10 = Math.pow(10, Math.floor(Math.log10(roughStep)));
  const norm = roughStep / pow10;
  let stepMult = 1;
  if (norm <= 1) stepMult = 1;
  else if (norm <= 2) stepMult = 2;
  else if (norm <= 2.5) stepMult = 2.5;
  else if (norm <= 5) stepMult = 5;
  else stepMult = 10;
  const step = stepMult * pow10;
  const max = Math.ceil(rawMax / step) * step;
  const steps: number[] = [];
  for (let i = 0; i <= Math.round(max / step); i++) {
    steps.push(Number((i * step).toPrecision(6)));
  }
  return { max, steps };
}

// Official Arco Design restrained industrial data palette (zero violet/lavender slop).
export const CHART_PALETTE = [
  '#165DFF', // Arco Blue 6 (Primary)
  '#0FC6C2', // Arco Cyan 6 (HVAC / Secondary)
  '#00B42A', // Arco Green 6 (Positive / Verified)
  '#FF7D00', // Arco Orange 6 (Warning / Thermal)
  '#3491FA', // Arco Arcoblue 5 (Auxiliary)
  '#86909C', // Arco Gray 6 (Unclassified / Neutral)
];

// ── 1. TrendAreaChart (F01 / F04 / F08 / F10) ───────────────────────────────
// High-precision SVG area + line chart with gradient fill, horizontal dashed grid,
// missing-data diagonal hatch bands (never plotted as 0), mean/baseline reference lines,
// peak callout, and interactive vertical crosshair tooltip.

export type TrendPoint = {
  from: string;
  to?: string;
  usage: number | null;
  valid?: number;
  expected?: number;
};

export function TrendAreaChart({
  points,
  unit,
  seriesLabel,
  comparisonPoints,
  comparisonLabel,
  highlightIndices,
  highlightLabel,
  referenceValue,
  referenceLabel,
  secondaryReferenceValue,
  secondaryReferenceLabel,
  xLabelFormatter,
  onPointClick,
  highlightPeak = true,
  height = 292,
  wide = false,
}: {
  points: TrendPoint[];
  unit: string;
  seriesLabel?: string;
  comparisonPoints?: TrendPoint[];
  comparisonLabel?: string;
  highlightIndices?: number[];
  highlightLabel?: string;
  referenceValue?: number | null;
  referenceLabel?: string;
  secondaryReferenceValue?: number | null;
  secondaryReferenceLabel?: string;
  xLabelFormatter?: (pt: TrendPoint, idx: number) => string;
  onPointClick?: (idx: number) => void;
  highlightPeak?: boolean;
  height?: number;
  wide?: boolean;
}) {
  const uid = useId().replace(/:/g, '');
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);
  const highlightSet = useMemo(() => new Set(highlightIndices ?? []), [highlightIndices]);

  const validEntries = useMemo(
    () =>
      points
        .map((p, idx) => ({ p, idx }))
        .filter((x): x is { p: TrendPoint & { usage: number }; idx: number } => x.p.usage != null),
    [points],
  );

  const compMax = useMemo(() => {
    const vals = (comparisonPoints ?? []).map((p) => p.usage ?? 0);
    return vals.length ? Math.max(...vals) : 0;
  }, [comparisonPoints]);

  const stats = useMemo(() => {
    const vals = validEntries.map((x) => x.p.usage);
    const maxVal = vals.length ? Math.max(...vals) : 0;
    const minVal = vals.length ? Math.min(...vals) : null;
    const avgVal = vals.length ? vals.reduce((a, b) => a + b, 0) / vals.length : null;
    const peakIdx =
      validEntries.reduce((best, cur) => (cur.p.usage > (best?.p.usage ?? -Infinity) ? cur : best), validEntries[0] ?? null)?.idx ??
      null;
    const missingCount = points.length - vals.length;
    const partialCount = points.filter(
      (p) => p.usage != null && p.expected != null && p.expected > 0 && (p.valid ?? 0) < p.expected,
    ).length;
    const ceiling = Math.max(maxVal, compMax, referenceValue ?? 0, secondaryReferenceValue ?? 0, avgVal ?? 0) * 1.16;
    const scale = niceScale(ceiling, 4);
    return { maxVal, minVal, avgVal, peakIdx, missingCount, partialCount, scale };
  }, [validEntries, points, compMax, referenceValue, secondaryReferenceValue]);

  const W = wide ? 1120 : 760;
  const H = height;
  const pad = { top: 24, right: 20, bottom: 32, left: 58 };
  const plotW = W - pad.left - pad.right;
  const plotH = H - pad.top - pad.bottom;

  const n = points.length;
  const xAt = (i: number) => (n <= 1 ? pad.left + plotW / 2 : pad.left + (i / (n - 1)) * plotW);
  const yAt = (v: number) => pad.top + plotH - (Math.max(0, v) / stats.scale.max) * plotH;

  // Split into contiguous valid segments so missing slots cleanly break the line (never connect across gaps or drop to 0)
  const segments = useMemo(() => {
    const segs: Array<Array<{ x: number; y: number; idx: number; val: number }>> = [];
    let cur: Array<{ x: number; y: number; idx: number; val: number }> = [];
    points.forEach((p, idx) => {
      if (p.usage != null) {
        cur.push({ x: xAt(idx), y: yAt(p.usage), idx, val: p.usage });
      } else if (cur.length > 0) {
        segs.push(cur);
        cur = [];
      }
    });
    if (cur.length > 0) segs.push(cur);
    return segs;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [points, stats.scale.max, height]);

  const compSegments = useMemo(() => {
    if (!comparisonPoints?.length) return [];
    const segs: Array<Array<{ x: number; y: number; idx: number; val: number }>> = [];
    let cur: Array<{ x: number; y: number; idx: number; val: number }> = [];
    const limit = Math.min(n, comparisonPoints.length);
    for (let idx = 0; idx < limit; idx++) {
      const p = comparisonPoints[idx];
      if (p?.usage != null) {
        cur.push({ x: xAt(idx), y: yAt(p.usage), idx, val: p.usage });
      } else if (cur.length > 0) {
        segs.push(cur);
        cur = [];
      }
    }
    if (cur.length > 0) segs.push(cur);
    return segs;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [comparisonPoints, n, stats.scale.max, height]);

  const missingIndices = useMemo(
    () => points.map((p, idx) => (p.usage == null ? idx : -1)).filter((i) => i >= 0),
    [points],
  );

  const isHourly = points.length >= 2 && points[0].from.slice(0, 10) === points[points.length - 1].from.slice(0, 10);
  const formatTick = (iso: string) => (isHourly ? iso.slice(11, 16) : iso.slice(5, 10));

  const xTickIndices = useMemo(() => {
    if (n <= 1) return n === 1 ? [0] : [];
    const maxTicks = n <= 7 ? n : 6;
    const step = Math.max(1, Math.ceil((n - 1) / (maxTicks - 1)));
    const out: number[] = [];
    for (let i = 0; i < n; i += step) out.push(i);
    if (out[out.length - 1] !== n - 1) out.push(n - 1);
    return out;
  }, [n]);

  if (n === 0) {
    return (
      <div className="flex h-[220px] items-center justify-center rounded-[4px] border border-dashed border-line bg-fill-1/40 text-[13px] text-muted">
        当前统计窗口暂无时序监测数据
      </div>
    );
  }

  const refVal = referenceValue !== undefined ? referenceValue : (comparisonPoints?.length ? null : stats.avgVal);
  const refLbl = referenceLabel ?? (stats.avgVal != null ? `时段均值 ${fmtCompact(stats.avgVal)}` : '');
  const hovered = hoverIdx != null ? points[hoverIdx] : null;
  const hoveredComp = hoverIdx != null && comparisonPoints ? comparisonPoints[hoverIdx] : null;
  const slotStepW = n > 1 ? plotW / (n - 1) : plotW;

  return (
    <div className="space-y-2">
      {/* Compact Analytical Legend + Stat Strip */}
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 text-[12px] tabular">
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-fg-2">
          <span className="inline-flex items-center gap-1.5">
            <span className="h-[2px] w-3.5 rounded-full bg-primary" />
            <span>{seriesLabel ?? `本期实测 (${unit})`}</span>
          </span>
          {compSegments.length > 0 && (
            <span className="inline-flex items-center gap-1.5">
              <span className="h-0 w-3.5 border-t-[2px] border-dashed border-[#0FC6C2]" />
              <span>{comparisonLabel ?? '可比上期'}</span>
            </span>
          )}
          {refVal != null && (
            <span className="inline-flex items-center gap-1.5">
              <span className="h-0 w-3.5 border-t border-dashed border-warning" />
              <span>{refLbl}</span>
            </span>
          )}
          {highlightSet.size > 0 && (
            <span className="inline-flex items-center gap-1.5 text-warning">
              <span className="size-2.5 rounded-[2px] border border-warning/70 bg-warning/25" />
              <span>{highlightLabel ?? '规则命中候选区间'}</span>
            </span>
          )}
          {secondaryReferenceValue != null && secondaryReferenceLabel && (
            <span className="inline-flex items-center gap-1.5">
              <span className="h-0 w-3.5 border-t border-dashed border-positive" />
              <span>{secondaryReferenceLabel}</span>
            </span>
          )}
          {stats.missingCount > 0 ? (
            <span className="inline-flex items-center gap-1 rounded-[2px] bg-warning-soft px-1.5 py-0 text-[11px] font-medium text-warning">
              {stats.missingCount} 个缺测时段（不计为 0）
            </span>
          ) : stats.partialCount > 0 ? (
            <span className="inline-flex items-center gap-1 rounded-[2px] bg-warning-soft px-1.5 py-0 text-[11px] font-medium text-warning">
              {n - stats.partialCount}/{n} 时段完整 · {stats.partialCount} 个部分缺测
            </span>
          ) : (
            <span className="text-[11px] text-positive">
              {validEntries.length}/{n} 时段完整
            </span>
          )}
        </div>
        <div className="flex items-center gap-3 text-[12px] text-muted">
          <span>
            峰值 <strong className="font-medium text-fg">{fmtVal(stats.maxVal)}</strong>
          </span>
          {stats.minVal != null && (
            <span>
              谷值 <strong className="font-medium text-fg">{fmtVal(stats.minVal)}</strong>
            </span>
          )}
        </div>
      </div>

      {/* Main SVG Plot */}
      <div className="relative select-none" onMouseLeave={() => setHoverIdx(null)}>
        <svg viewBox={`0 0 ${W} ${H}`} className="h-auto max-h-[360px] w-full overflow-visible" role="img" aria-label={`能耗趋势图（单位 ${unit}）`}>
          <defs>
            <linearGradient id={`area-${uid}`} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="#165DFF" stopOpacity="0.20" />
              <stop offset="85%" stopColor="#165DFF" stopOpacity="0.03" />
              <stop offset="100%" stopColor="#165DFF" stopOpacity="0.00" />
            </linearGradient>
            <pattern id={`hatch-${uid}`} width="6" height="6" patternTransform="rotate(45 0 0)" patternUnits="userSpaceOnUse">
              <line x1="0" y1="0" x2="0" y2="6" stroke="#FF7D00" strokeOpacity="0.28" strokeWidth="1.5" />
            </pattern>
          </defs>

          {/* Y-axis unit label */}
          <text x={pad.left - 8} y={12} textAnchor="end" className="fill-muted text-[10px]">
            {unit}
          </text>

          {/* Horizontal dashed gridlines & Y ticks */}
          {stats.scale.steps.map((step) => {
            const y = yAt(step);
            return (
              <g key={step}>
                <line
                  x1={pad.left}
                  x2={W - pad.right}
                  y1={y}
                  y2={y}
                  stroke="currentColor"
                  className={step === 0 ? 'text-line-strong' : 'text-line'}
                  strokeDasharray={step === 0 ? undefined : '3 3'}
                  strokeWidth={1}
                />
                <text x={pad.left - 8} y={y + 3.5} textAnchor="end" className="fill-muted text-[11px] tabular">
                  {fmtCompact(step)}
                </text>
              </g>
            );
          })}

          {/* Missing-data hatched gap zones (explicit visual honesty: missing != 0) */}
          {missingIndices.map((idx) => {
            const cx = xAt(idx);
            const bandW = Math.max(10, Math.min(28, slotStepW * 0.72));
            return (
              <g key={`miss-${idx}`}>
                <rect
                  x={Math.max(pad.left, cx - bandW / 2)}
                  y={pad.top}
                  width={Math.min(bandW, W - pad.right - Math.max(pad.left, cx - bandW / 2))}
                  height={plotH}
                  fill={`url(#hatch-${uid})`}
                />
              </g>
            );
          })}

          {/* Reference Line (Average or Primary Baseline) */}
          {refVal != null && refVal <= stats.scale.max && (
            <g>
              <line
                x1={pad.left}
                x2={W - pad.right}
                y1={yAt(refVal)}
                y2={yAt(refVal)}
                stroke="#FF7D00"
                strokeWidth={1.25}
                strokeDasharray="4 4"
              />
              <rect
                x={W - pad.right - 98}
                y={Math.max(pad.top, yAt(refVal) - 18)}
                width={98}
                height={16}
                rx={2}
                fill="var(--surface)"
                fillOpacity={0.9}
              />
              <text
                x={W - pad.right - 4}
                y={Math.max(pad.top + 11, yAt(refVal) - 6)}
                textAnchor="end"
                className="fill-warning text-[10px] font-medium tabular"
              >
                {refLbl}
              </text>
            </g>
          )}

          {/* Secondary Reference Line (e.g. Prior Period Baseline) */}
          {secondaryReferenceValue != null && secondaryReferenceValue <= stats.scale.max && (
            <g>
              <line
                x1={pad.left}
                x2={W - pad.right}
                y1={yAt(secondaryReferenceValue)}
                y2={yAt(secondaryReferenceValue)}
                stroke="#00B42A"
                strokeWidth={1.2}
                strokeDasharray="2 3"
              />
            </g>
          )}

          {/* Rule-triggered anomaly or baseline highlight bands on timeline */}
          {(() => {
            const hlArr = Array.from(highlightSet).filter((i) => i >= 0 && i < n).sort((a, b) => a - b);
            const labelTargetIdx =
              hlArr.length > 0
                ? hlArr.reduce((best, cur) => ((points[cur]?.usage ?? -Infinity) > (points[best]?.usage ?? -Infinity) ? cur : best), hlArr[0])
                : -1;
            return hlArr.map((idx) => {
              const cx = xAt(idx);
              const bandW = Math.max(16, Math.min(44, slotStepW * 0.52));
              const left = Math.max(pad.left, cx - bandW / 2);
              const width = Math.min(bandW, W - pad.right - left);
              const ptVal = points[idx]?.usage;
              return (
                <g key={`anom-band-${idx}`}>
                  <rect
                    x={left}
                    y={pad.top}
                    width={Math.max(6, width)}
                    height={plotH}
                    fill="#FF7D00"
                    fillOpacity={0.14}
                    stroke="#FF7D00"
                    strokeOpacity={0.4}
                    strokeDasharray="3 2"
                  />
                  {ptVal != null && (
                    <>
                      <circle cx={cx} cy={yAt(ptVal)} r={7} fill="#FF7D00" fillOpacity={0.22} />
                      <circle cx={cx} cy={yAt(ptVal)} r={4.2} fill="#FF7D00" stroke="var(--surface)" strokeWidth={2} />
                      {hoverIdx == null && idx === labelTargetIdx && (
                        <g>
                          <rect
                            x={Math.min(W - pad.right - 74, Math.max(pad.left + 2, cx - 36))}
                            y={Math.max(4, yAt(ptVal) - 27)}
                            width={72}
                            height={15}
                            rx={2}
                            fill="var(--surface)"
                            fillOpacity={0.92}
                          />
                          <text
                            x={Math.min(W - pad.right - 38, Math.max(pad.left + 38, cx))}
                            y={Math.max(15, yAt(ptVal) - 16)}
                            textAnchor="middle"
                            className="fill-warning text-[10px] font-semibold"
                          >
                            {highlightLabel ?? '规则命中候选'}
                          </text>
                        </g>
                      )}
                    </>
                  )}
                </g>
              );
            });
          })()}

          {/* Comparison series (dashed Arco Cyan line for comparable prior period) */}
          {compSegments.map((seg, si) => {
            const lineD = seg.map((pt, k) => `${k === 0 ? 'M' : 'L'} ${pt.x.toFixed(1)} ${pt.y.toFixed(1)}`).join(' ');
            return (
              <g key={`comp-${si}`}>
                {seg.length > 1 && (
                  <path
                    d={lineD}
                    fill="none"
                    stroke="#0FC6C2"
                    strokeWidth={1.75}
                    strokeDasharray="5 4"
                    strokeLinejoin="round"
                    strokeLinecap="round"
                  />
                )}
                {(seg.length === 1 || n <= 12) &&
                  seg.map((pt) => (
                    <circle
                      key={`cp-${pt.idx}`}
                      cx={pt.x}
                      cy={pt.y}
                      r={hoverIdx === pt.idx ? 4 : 2.5}
                      fill="var(--surface)"
                      stroke="#0FC6C2"
                      strokeWidth={1.75}
                    />
                  ))}
              </g>
            );
          })}

          {/* Area fill + primary line per contiguous valid segment */}
          {segments.map((seg, si) => {
            const lineD = seg.map((pt, k) => `${k === 0 ? 'M' : 'L'} ${pt.x.toFixed(1)} ${pt.y.toFixed(1)}`).join(' ');
            const areaD = `${lineD} L ${seg[seg.length - 1].x.toFixed(1)} ${(pad.top + plotH).toFixed(1)} L ${seg[0].x.toFixed(1)} ${(pad.top + plotH).toFixed(1)} Z`;
            return (
              <g key={si}>
                {seg.length > 1 && <path d={areaD} fill={`url(#area-${uid})`} />}
                {seg.length > 1 && (
                  <path
                    d={lineD}
                    fill="none"
                    stroke="#165DFF"
                    strokeWidth={2.1}
                    strokeLinejoin="round"
                    strokeLinecap="round"
                  />
                )}
                {/* Single isolated point or small series markers */}
                {(seg.length === 1 || n <= 12) &&
                  seg.map((pt) => (
                    <circle
                      key={pt.idx}
                      cx={pt.x}
                      cy={pt.y}
                      r={hoverIdx === pt.idx ? 4.5 : 3}
                      fill="var(--surface)"
                      stroke="#165DFF"
                      strokeWidth={2}
                    />
                  ))}
              </g>
            );
          })}

          {/* Peak highlight marker & subtle callout (suppressed when already annotated by anomaly highlight) */}
          {highlightPeak && stats.peakIdx != null && !highlightSet.has(stats.peakIdx) && points[stats.peakIdx]?.usage != null && (
            <g>
              <circle
                cx={xAt(stats.peakIdx)}
                cy={yAt(points[stats.peakIdx].usage!)}
                r={4.2}
                fill="var(--surface)"
                stroke="#165DFF"
                strokeWidth={2.2}
              />
              {hoverIdx == null && (
                <text
                  x={Math.min(W - pad.right - 34, Math.max(pad.left + 34, xAt(stats.peakIdx)))}
                  y={Math.max(14, yAt(points[stats.peakIdx].usage!) - 9)}
                  textAnchor="middle"
                  className="fill-primary text-[10px] font-medium tabular"
                >
                  峰值 {fmtCompact(points[stats.peakIdx].usage!)}
                </text>
              )}
            </g>
          )}

          {/* Hover vertical crosshair + active dot */}
          {hoverIdx != null && (
            <g>
              <line
                x1={xAt(hoverIdx)}
                x2={xAt(hoverIdx)}
                y1={pad.top}
                y2={pad.top + plotH}
                stroke="#165DFF"
                strokeOpacity={0.45}
                strokeDasharray="3 3"
                strokeWidth={1}
              />
              {hovered?.usage != null && (
                <circle
                  cx={xAt(hoverIdx)}
                  cy={yAt(hovered.usage)}
                  r={5}
                  fill="#165DFF"
                  stroke="var(--surface)"
                  strokeWidth={2}
                />
              )}
              {hoveredComp?.usage != null && (
                <circle
                  cx={xAt(hoverIdx)}
                  cy={yAt(hoveredComp.usage)}
                  r={4.2}
                  fill="#0FC6C2"
                  stroke="var(--surface)"
                  strokeWidth={2}
                />
              )}
            </g>
          )}

          {/* X-axis ticks */}
          {xTickIndices.map((idx) => (
            <text
              key={idx}
              x={xAt(idx)}
              y={H - 8}
              textAnchor={idx === 0 ? 'start' : idx === n - 1 ? 'end' : 'middle'}
              className="fill-muted text-[11px] tabular"
            >
              {xLabelFormatter ? xLabelFormatter(points[idx], idx) : formatTick(points[idx].from)}
            </text>
          ))}

          {/* Full-column hover hit zones */}
          {points.map((pt, idx) => {
            const cx = xAt(idx);
            const left = idx === 0 ? pad.left : cx - slotStepW / 2;
            const width = idx === 0 || idx === n - 1 ? slotStepW / 2 : slotStepW;
            return (
              <rect
                key={`hit-${pt.from}-${idx}`}
                x={Math.max(pad.left, left)}
                y={pad.top}
                width={Math.max(6, width)}
                height={plotH}
                fill="transparent"
                className={onPointClick ? 'cursor-pointer' : undefined}
                onMouseEnter={() => setHoverIdx(idx)}
                onFocus={() => setHoverIdx(idx)}
                onClick={() => onPointClick?.(idx)}
              />
            );
          })}
        </svg>

        {/* Floating Arco Popover Tooltip */}
        {hoverIdx != null && hovered && (
          <div
            className="pointer-events-none absolute top-2 z-20 min-w-[206px] rounded-[4px] border border-line bg-surface px-3 py-2 text-[12px] shadow-panel tabular"
            style={{
              left: `${Math.min(70, Math.max(4, (xAt(hoverIdx) / W) * 100))}%`,
              transform: xAt(hoverIdx) / W > 0.6 ? 'translateX(-105%)' : 'translateX(12px)',
            }}
          >
            <div className="mb-1 border-b border-line pb-1 text-[11px] font-medium text-fg-2">
              {isHourly
                ? `${hovered.from.slice(0, 10)} ${hovered.from.slice(11, 16)}${hovered.to ? ` – ${hovered.to.slice(11, 16)}` : ''}`
                : hovered.to
                  ? `${hovered.from.slice(0, 10)} ~ ${hovered.to.slice(0, 10)}`
                  : hovered.from.slice(0, 10)}
            </div>
            {hovered.usage != null ? (
              <div className="space-y-0.5">
                <div className="flex items-center justify-between gap-4">
                  <span className="inline-flex items-center gap-1.5 text-muted">
                    <span className="size-2 rounded-full bg-primary" />
                    {seriesLabel ? seriesLabel.replace(/\s*\(.*\)$/, '') : '本期实测'}
                  </span>
                  <span className="font-semibold text-fg">
                    {fmtVal(hovered.usage)} {unit}
                  </span>
                </div>
                {hoveredComp && (
                  <div className="flex items-center justify-between gap-4 text-[11px]">
                    <span className="inline-flex items-center gap-1.5 text-muted">
                      <span className="size-2 rounded-full bg-[#0FC6C2]" />
                      {comparisonLabel ?? '可比上期'}
                    </span>
                    <span className="font-medium text-fg-2">
                      {hoveredComp.usage != null ? `${fmtVal(hoveredComp.usage)} ${unit}` : '缺测'}
                    </span>
                  </div>
                )}
                {hoveredComp?.usage != null && hoveredComp.usage > 0 && (
                  <div className="flex items-center justify-between gap-4 text-[11px]">
                    <span className="text-muted">对比差幅</span>
                    <span className={hovered.usage - hoveredComp.usage > 0 ? 'font-medium text-negative' : 'font-medium text-positive'}>
                      {hovered.usage - hoveredComp.usage > 0 ? '+' : ''}
                      {fmtVal(hovered.usage - hoveredComp.usage)} ({hovered.usage - hoveredComp.usage > 0 ? '+' : ''}
                      {pctFmt.format((hovered.usage - hoveredComp.usage) / hoveredComp.usage)})
                    </span>
                  </div>
                )}
                {!hoveredComp && stats.avgVal != null && stats.avgVal > 0 && (
                  <div className="flex items-center justify-between gap-4 text-[11px]">
                    <span className="text-muted">较均值</span>
                    <span className={hovered.usage >= stats.avgVal ? 'text-warning' : 'text-positive'}>
                      {hovered.usage >= stats.avgVal ? '+' : ''}
                      {pctFmt.format((hovered.usage - stats.avgVal) / stats.avgVal)}
                    </span>
                  </div>
                )}
                {hovered.expected != null && hovered.expected > 0 && (
                  <div className="flex items-center justify-between gap-4 text-[11px]">
                    <span className="text-muted">数据覆盖率</span>
                    <span className="text-fg-2">
                      {pctFmt.format((hovered.valid ?? 0) / hovered.expected)} ({hovered.valid ?? 0}/{hovered.expected})
                    </span>
                  </div>
                )}
              </div>
            ) : (
              <div className="font-medium text-warning">缺测时段（严禁按 0 补齐）</div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

// ── 2. EnergyStructureChart (Interactive Donut + Proportional Breakdown) ────
// Strictly single-energy, single-unit structure chart with hover highlight.

export type StructureSlice = {
  id: string;
  name: string;
  usage: number | null;
  share?: number | null;
  isUnclassified?: boolean;
};

export function EnergyStructureChart({
  slices,
  total,
  totalUsage,
  unit,
  layout = 'row',
  selectedId,
  onSelectSlice,
  emptyLabel = '暂无分项计量构成数据',
}: {
  slices: StructureSlice[];
  total?: number | null;
  totalUsage?: number | null;
  unit: string;
  layout?: 'row' | 'stack';
  selectedId?: string | null;
  onSelectSlice?: (id: string) => void;
  emptyLabel?: string;
}) {
  const [activeIdx, setActiveIdx] = useState<number | null>(null);

  const validSlices = useMemo(
    () =>
      slices
        .filter((s): s is StructureSlice & { usage: number } => s.usage != null && s.usage > 0)
        .sort((a, b) => (a.isUnclassified ? 1 : b.isUnclassified ? -1 : b.usage - a.usage)),
    [slices],
  );

  const effectiveTotal = total ?? totalUsage ?? null;
  const computedTotal = useMemo(() => {
    if (effectiveTotal != null && effectiveTotal > 0) return effectiveTotal;
    return validSlices.reduce((acc, s) => acc + s.usage, 0);
  }, [effectiveTotal, validSlices]);

  if (validSlices.length === 0 || computedTotal <= 0) {
    return (
      <div className="flex h-[180px] items-center justify-center rounded-[4px] border border-dashed border-line bg-fill-1/40 text-[12px] text-muted">
        {emptyLabel}
      </div>
    );
  }

  const R = 56;
  const C = 2 * Math.PI * R;
  let offsetAcc = 0;

  const arcs = validSlices.map((slice, i) => {
    const ratio = Math.min(1, Math.max(0, slice.share ?? slice.usage / computedTotal));
    const dashLen = Math.max(0, ratio * C - 1.5);
    const dashOffset = -offsetAcc;
    offsetAcc += ratio * C;
    const color = slice.isUnclassified ? '#C9CDD4' : CHART_PALETTE[i % (CHART_PALETTE.length - 1)];
    return { slice, ratio, dashLen, dashOffset, color };
  });

  const focused =
    activeIdx != null
      ? arcs[activeIdx]
      : selectedId
        ? arcs.find((a) => a.slice.id === selectedId) ?? null
        : null;

  return (
    <div
      className={layout === 'stack' ? 'flex flex-col gap-3.5' : 'grid items-center gap-4 sm:grid-cols-[152px_1fr]'}
      onMouseLeave={() => setActiveIdx(null)}
    >
      {/* Interactive Donut Ring */}
      <div
        className={`relative mx-auto flex shrink-0 items-center justify-center ${
          layout === 'stack' ? 'h-[136px] w-[136px]' : 'h-[148px] w-[148px]'
        }`}
      >
        <svg viewBox="0 0 148 148" className="h-full w-full -rotate-90" role="img" aria-label={`分项结构图（单位 ${unit}）`}>
          <circle cx="74" cy="74" r={R} fill="none" stroke="var(--fill-2)" strokeWidth="16" />
          {arcs.map((a, idx) => {
            const isActive = activeIdx === idx || (activeIdx == null && selectedId === a.slice.id);
            const dimmed = (activeIdx != null || Boolean(selectedId)) && !isActive;
            return (
              <circle
                key={a.slice.id}
                cx="74"
                cy="74"
                r={R}
                fill="none"
                stroke={a.color}
                strokeWidth={isActive ? 20 : 16}
                strokeDasharray={`${a.dashLen.toFixed(2)} ${C.toFixed(2)}`}
                strokeDashoffset={a.dashOffset.toFixed(2)}
                strokeOpacity={dimmed ? 0.35 : 1}
                className="cursor-pointer transition-all duration-150"
                onMouseEnter={() => setActiveIdx(idx)}
                onClick={() => onSelectSlice?.(a.slice.id)}
              />
            );
          })}
        </svg>
        <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center px-4 text-center tabular">
          <span className="max-w-[92px] truncate text-[11px] text-muted">
            {focused ? focused.slice.name : '分项合计'}
          </span>
          <span className="mt-0.5 text-[16px] font-semibold leading-tight text-fg">
            {focused ? pctFmt.format(focused.ratio) : fmtCompact(computedTotal)}
          </span>
          <span className="text-[11px] text-muted">
            {focused ? `${fmtCompact(focused.slice.usage)} ${unit}` : unit}
          </span>
        </div>
      </div>

      {/* Aligned Proportional Breakdown List */}
      <div className="min-w-0 space-y-2.5 tabular">
        {arcs.map((a, idx) => {
          const isActive = activeIdx === idx || selectedId === a.slice.id;
          return (
            <div
              key={a.slice.id}
              onMouseEnter={() => setActiveIdx(idx)}
              onClick={() => onSelectSlice?.(a.slice.id)}
              className={`cursor-pointer rounded-[2px] px-1.5 py-1 transition-colors ${
                selectedId === a.slice.id ? 'bg-primary-soft/60 ring-1 ring-primary/30' : isActive ? 'bg-fill-1' : ''
              }`}
            >
              <div className="flex items-baseline justify-between gap-2 text-[12px]">
                <span className="flex min-w-0 items-center gap-1.5">
                  <span className="h-2.5 w-2.5 shrink-0 rounded-[2px]" style={{ backgroundColor: a.color }} />
                  <span className={`truncate ${isActive ? 'font-medium text-fg' : 'text-fg-2'}`}>
                    {a.slice.name}
                  </span>
                </span>
                <span className="shrink-0 whitespace-nowrap text-right">
                  <strong className="font-medium text-fg">{fmtVal(a.slice.usage)}</strong>
                  <span className="ml-1 text-[11px] text-muted">{unit}</span>
                  <span className="ml-1.5 inline-block w-11 text-right text-[11px] font-medium text-fg-2">
                    {pctFmt.format(a.ratio)}
                  </span>
                </span>
              </div>
              <div className="mt-1 h-[5px] w-full overflow-hidden rounded-full bg-fill-2">
                <div
                  className="h-full rounded-full transition-all duration-150"
                  style={{ width: `${Math.max(2, a.ratio * 100)}%`, backgroundColor: a.color }}
                />
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

// ── 3. HorizontalRankingChart (F03 / F07 / F11 / F16) ───────────────────────
// True axis-scaled ranked horizontal bar chart; non-rankable items clearly hatched
// with inline reason rather than faked into 0 or hidden.

export type RankingItem = {
  id: string;
  name: string;
  value?: number | null;
  usage?: number | null;
  rank: number | null;
  status?: string;
  statusText?: string;
  reason?: string;
  coverageRate?: number | null;
};

export function HorizontalRankingChart({
  items,
  unit,
  threshold,
  thresholdLabel,
  averageValue,
  selectedId,
  onSelect,
  onRowClick,
}: {
  items: RankingItem[];
  unit: string;
  threshold?: number | null;
  thresholdLabel?: string;
  averageValue?: number | null;
  selectedId?: string | null;
  onSelect?: (id: string) => void;
  onRowClick?: (id: string) => void;
}) {
  const [hoverId, setHoverId] = useState<string | null>(null);
  const handleSelect = onSelect ?? onRowClick;

  const normalized = useMemo(
    () =>
      items.map((i) => ({
        ...i,
        val: i.value ?? i.usage ?? null,
        reasonText: i.reason ?? i.statusText,
      })),
    [items],
  );

  const sorted = useMemo(() => {
    const rankable = normalized.filter((i) => i.rank != null && i.val != null).sort((a, b) => (a.rank ?? 999) - (b.rank ?? 999));
    const unranked = normalized.filter((i) => i.rank == null || i.val == null);
    return [...rankable, ...unranked];
  }, [normalized]);

  const maxVal = useMemo(() => {
    const vals = sorted.map((i) => i.val ?? 0);
    const rawMax = Math.max(...vals, threshold ?? 0, averageValue ?? 0);
    return niceScale(rawMax > 0 ? rawMax * 1.14 : 1, 4);
  }, [sorted, threshold, averageValue]);

  if (sorted.length === 0) {
    return (
      <div className="flex h-[180px] items-center justify-center rounded-[4px] border border-dashed border-line bg-fill-1/40 text-[12px] text-muted">
        当前范围暂无可对比对象
      </div>
    );
  }

  return (
    <div className="space-y-2 tabular">
      <div className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-muted">
        <div className="flex flex-wrap items-center gap-3">
          <span>按同口径降序排列（低覆盖/不可比对象保留数值但不出名次）</span>
          {averageValue != null && averageValue > 0 && (
            <span className="inline-flex items-center gap-1 text-warning">
              <span className="h-2.5 w-[1.5px] bg-warning" />
              <span>同口径均值 {fmtVal(averageValue)} {unit}</span>
            </span>
          )}
        </div>
        <span>单位：{unit}</span>
      </div>

      <div className="relative space-y-2">
        {sorted.map((item) => {
          const isRanked = item.rank != null && item.val != null;
          const pct = item.val != null ? Math.min(100, (item.val / maxVal.max) * 100) : 0;
          const isHovered = hoverId === item.id;
          const isSelected = selectedId != null && selectedId === item.id;
          const diffPct =
            isRanked && averageValue != null && averageValue > 0 && item.val != null
              ? (item.val - averageValue) / averageValue
              : null;
          const barColor = !isRanked
            ? '#C9CDD4'
            : item.rank === 1
              ? '#165DFF'
              : item.rank === 2
                ? '#3491FA'
                : '#6AA1FF';

          return (
            <div
              key={item.id}
              onMouseEnter={() => setHoverId(item.id)}
              onMouseLeave={() => setHoverId(null)}
              onClick={() => handleSelect?.(item.id)}
              className={`group rounded-[4px] border px-2.5 py-2 transition-colors ${
                handleSelect ? 'cursor-pointer' : ''
              } ${
                isSelected
                  ? 'border-primary/40 bg-primary-soft/50'
                  : isHovered
                    ? 'border-transparent bg-fill-1'
                    : 'border-transparent'
              }`}
            >
              <div className="flex items-center justify-between gap-2 text-[12px]">
                <div className="flex min-w-0 items-center gap-2">
                  <span
                    className={`inline-flex h-5 w-6 shrink-0 items-center justify-center rounded-[2px] text-[11px] font-medium ${
                      isRanked && item.rank === 1
                        ? 'bg-primary text-white'
                        : isRanked
                          ? 'bg-primary-soft text-primary'
                          : 'bg-fill-2 text-muted'
                    }`}
                  >
                    {isRanked ? `#${item.rank}` : '—'}
                  </span>
                  <span className={`truncate font-medium ${isSelected ? 'text-primary' : 'text-fg'}`}>{item.name}</span>
                  {!isRanked && item.reasonText && (
                    <span className="hidden truncate rounded-[2px] bg-warning-soft px-1.5 py-0 text-[11px] text-warning sm:inline">
                      {item.reasonText}
                    </span>
                  )}
                </div>
                <div className="flex shrink-0 items-baseline gap-2.5">
                  {diffPct != null && (
                    <span
                      className={`hidden text-[11px] font-medium sm:inline ${
                        diffPct > 0.05 ? 'text-warning' : diffPct < -0.05 ? 'text-positive' : 'text-muted'
                      }`}
                    >
                      {diffPct > 0 ? '+' : ''}
                      {pctFmt.format(diffPct)}
                    </span>
                  )}
                  <span className="font-semibold text-fg">
                    {fmtVal(item.val)}
                    <span className="ml-1 text-[11px] font-normal text-muted">{unit}</span>
                  </span>
                  {item.coverageRate != null && (
                    <span className={`w-10 text-right text-[11px] ${item.coverageRate < 0.9 ? 'text-warning' : 'text-muted'}`}>
                      {pctFmt.format(item.coverageRate)}
                    </span>
                  )}
                </div>
              </div>

              <div className="relative mt-1.5 h-[8px] w-full overflow-hidden rounded-[2px] bg-fill-2">
                {item.val != null ? (
                  <div
                    className="h-full rounded-[2px] transition-all duration-200"
                    style={{
                      width: `${Math.max(1.5, pct)}%`,
                      backgroundColor: barColor,
                      backgroundImage: !isRanked
                        ? 'repeating-linear-gradient(45deg, transparent, transparent 4px, rgba(255,255,255,0.45) 4px, rgba(255,255,255,0.45) 8px)'
                        : undefined,
                    }}
                  />
                ) : (
                  <div className="h-full w-full bg-[repeating-linear-gradient(45deg,transparent,transparent_5px,rgba(134,144,156,0.18)_5px,rgba(134,144,156,0.18)_10px)]" />
                )}
                {averageValue != null && averageValue > 0 && averageValue <= maxVal.max && (
                  <div
                    className="pointer-events-none absolute inset-y-0 w-[1.5px] bg-warning"
                    style={{ left: `${(averageValue / maxVal.max) * 100}%` }}
                    title={`同口径均值：${fmtVal(averageValue)} ${unit}`}
                  />
                )}
                {threshold != null && threshold <= maxVal.max && (
                  <div
                    className="pointer-events-none absolute inset-y-0 w-[1.5px] bg-negative"
                    style={{ left: `${(threshold / maxVal.max) * 100}%` }}
                    title={thresholdLabel}
                  />
                )}
              </div>
            </div>
          );
        })}
      </div>

      {/* Explicit X-axis Scale Footer */}
      <div className="flex items-center justify-between border-t border-line pt-1.5 text-[11px] text-muted">
        {maxVal.steps.map((s) => (
          <span key={s}>{maxVal.max < 2 ? fmtVal(s) : fmtCompact(s)}</span>
        ))}
      </div>
    </div>
  );
}

// ── 4. TargetComparisonChart (F01 / F05 / F14) ──────────────────────────────
// 12-month actual consumption columns vs monthly target step markers + missing-month hatching.

export type TargetMonthPoint = {
  month: number;
  label: string;
  target: number | null;
  usage: number | null;
  ratio?: number | null;
  status?: string;
};

export function TargetComparisonChart({
  months,
  unit,
  height = 228,
  wide = false,
}: {
  months: TargetMonthPoint[];
  unit: string;
  height?: number;
  wide?: boolean;
}) {
  const uid = useId().replace(/:/g, '');
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);

  const scale = useMemo(() => {
    const maxVal = Math.max(
      ...months.map((m) => Math.max(m.usage ?? 0, m.target ?? 0)),
      1,
    );
    return niceScale(maxVal * 1.15, 4);
  }, [months]);

  if (months.length === 0) {
    return (
      <div className="flex h-[200px] items-center justify-center rounded-[4px] border border-dashed border-line bg-fill-1/40 text-[12px] text-muted">
        暂无年度分月目标数据
      </div>
    );
  }

  const W = wide ? 960 : 680;
  const H = height;
  const pad = { top: 18, right: 16, bottom: 28, left: 54 };
  const plotW = W - pad.left - pad.right;
  const plotH = H - pad.top - pad.bottom;
  const slotW = plotW / months.length;
  const barW = Math.min(28, Math.max(12, slotW * 0.56));
  const yAt = (v: number) => pad.top + plotH - (Math.max(0, v) / scale.max) * plotH;

  const hovered = hoverIdx != null ? months[hoverIdx] : null;

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-center justify-between gap-2 text-[12px] text-fg-2">
        <div className="flex flex-wrap items-center gap-3.5">
          <span className="inline-flex items-center gap-1.5">
            <span className="size-2.5 rounded-[2px] bg-primary" />
            <span>达标实测</span>
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="size-2.5 rounded-[2px] bg-negative" />
            <span>超目标</span>
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="h-[2px] w-3.5 bg-warning" />
            <span>月度目标上限</span>
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="size-2.5 rounded-[2px] border border-dashed border-line-strong bg-fill-2" />
            <span>未完结/缺测</span>
          </span>
        </div>
        <span className="text-[11px] text-muted">单位：{unit}</span>
      </div>

      <div className="relative select-none" onMouseLeave={() => setHoverIdx(null)}>
        <svg viewBox={`0 0 ${W} ${H}`} className="h-auto max-h-[270px] w-full overflow-visible" role="img" aria-label={`分月目标与实测对比图（单位 ${unit}）`}>
          <defs>
            <pattern id={`t-hatch-${uid}`} width="6" height="6" patternTransform="rotate(45 0 0)" patternUnits="userSpaceOnUse">
              <line x1="0" y1="0" x2="0" y2="6" stroke="#86909C" strokeOpacity="0.26" strokeWidth="1.5" />
            </pattern>
          </defs>

          {scale.steps.map((step) => {
            const y = yAt(step);
            return (
              <g key={step}>
                <line
                  x1={pad.left}
                  x2={W - pad.right}
                  y1={y}
                  y2={y}
                  stroke="currentColor"
                  className={step === 0 ? 'text-line-strong' : 'text-line'}
                  strokeDasharray={step === 0 ? undefined : '3 3'}
                />
                <text x={pad.left - 8} y={y + 3.5} textAnchor="end" className="fill-muted text-[11px] tabular">
                  {fmtCompact(step)}
                </text>
              </g>
            );
          })}

          {months.map((m, i) => {
            const cx = pad.left + i * slotW + slotW / 2;
            const bx = cx - barW / 2;
            const over = m.usage != null && m.target != null && m.usage > m.target;
            const isHovered = hoverIdx === i;

            return (
              <g key={m.label}>
                {isHovered && (
                  <rect x={pad.left + i * slotW} y={pad.top} width={slotW} height={plotH} fill="var(--fill-2)" fillOpacity={0.6} />
                )}

                {m.usage != null ? (
                  <rect
                    x={bx}
                    y={yAt(m.usage)}
                    width={barW}
                    height={Math.max(2, pad.top + plotH - yAt(m.usage))}
                    rx={2}
                    fill={over ? '#F53F3F' : '#165DFF'}
                    fillOpacity={isHovered ? 1 : 0.86}
                  />
                ) : (
                  <rect
                    x={bx}
                    y={m.target != null ? yAt(m.target) : pad.top + plotH - 28}
                    width={barW}
                    height={m.target != null ? Math.max(8, pad.top + plotH - yAt(m.target)) : 28}
                    rx={2}
                    fill={`url(#t-hatch-${uid})`}
                    stroke="var(--line-strong)"
                    strokeDasharray="3 2"
                    strokeWidth={1}
                  />
                )}

                {/* Crisp horizontal target cap line per month */}
                {m.target != null && (
                  <line
                    x1={bx - 4}
                    x2={bx + barW + 4}
                    y1={yAt(m.target)}
                    y2={yAt(m.target)}
                    stroke="#FF7D00"
                    strokeWidth={2}
                    strokeLinecap="round"
                  />
                )}

                <text x={cx} y={H - 8} textAnchor="middle" className="fill-muted text-[11px] tabular">
                  {m.month}月
                </text>

                <rect
                  x={pad.left + i * slotW}
                  y={pad.top}
                  width={slotW}
                  height={plotH}
                  fill="transparent"
                  onMouseEnter={() => setHoverIdx(i)}
                />
              </g>
            );
          })}
        </svg>

        {hoverIdx != null && hovered && (
          <div
            className="pointer-events-none absolute top-2 z-20 min-w-[184px] rounded-[4px] border border-line bg-surface px-3 py-2 text-[12px] shadow-panel tabular"
            style={{
              left: `${Math.min(72, Math.max(4, ((pad.left + hoverIdx * slotW) / W) * 100))}%`,
              transform: hoverIdx >= months.length / 2 ? 'translateX(-102%)' : 'translateX(14px)',
            }}
          >
            <div className="mb-1 border-b border-line pb-1 font-medium text-fg">{hovered.label}</div>
            <div className="space-y-0.5">
              <div className="flex justify-between gap-4">
                <span className="text-muted">月度目标</span>
                <span className="text-fg-2">{fmtVal(hovered.target)} {unit}</span>
              </div>
              <div className="flex justify-between gap-4">
                <span className="text-muted">实际用量</span>
                <span className="font-semibold text-fg">
                  {hovered.usage != null ? `${fmtVal(hovered.usage)} ${unit}` : '未完结 / 缺测'}
                </span>
              </div>
              {hovered.ratio != null && (
                <div className="flex justify-between gap-4">
                  <span className="text-muted">目标占比</span>
                  <span className={hovered.ratio > 1 ? 'font-medium text-negative' : 'font-medium text-positive'}>
                    {pctFmt.format(hovered.ratio)}
                  </span>
                </div>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

// ── 5. EnergyFlowBalanceChart (F09 Sankey Flow Diagram) ─────────────────────

export type FlowNode = {
  id: string;
  name: string;
  zone?: string;
  usage: number | null;
  status?: string;
};

export function EnergyFlowBalanceChart({
  parentLabel,
  parentName,
  parentUsage,
  childrenNodes,
  deltaUsage,
  difference,
  unit,
}: {
  parentLabel?: string;
  parentName?: string;
  parentUsage: number | null;
  childrenNodes: FlowNode[];
  deltaUsage?: number | null;
  difference?: number | null;
  unit: string;
}) {
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);
  const resolvedParent = parentLabel ?? parentName ?? '总表';
  const resolvedDelta = deltaUsage ?? difference ?? null;

  const validChildren = childrenNodes.filter((c) => c.usage != null && c.usage > 0);
  const allTargets: Array<{ id: string; name: string; usage: number; isDelta?: boolean }> = [
    ...validChildren.map((c) => ({ id: c.id, name: c.name, usage: c.usage! })),
    ...(resolvedDelta != null && Math.abs(resolvedDelta) > 0.001
      ? [{ id: '__delta__', name: resolvedDelta > 0 ? '未分配 / 待核查差额' : '子表超父表待核查', usage: Math.abs(resolvedDelta), isDelta: true }]
      : []),
  ];

  const totalFlow = Math.max(
    parentUsage ?? 0,
    allTargets.reduce((a, b) => a + b.usage, 0),
    1,
  );

  if (parentUsage == null || allTargets.length === 0) {
    return (
      <div className="flex h-[200px] items-center justify-center rounded-[4px] border border-dashed border-line bg-fill-1/40 text-[12px] text-muted">
        当前总分表拓扑暂无可绘制的能流数据
      </div>
    );
  }

  const W = 760;
  const H = Math.max(248, allTargets.length * 48 + 44);
  const leftX = 12;
  const leftW = 192;
  const rightW = 248;
  const rightX = W - rightW - 12;
  const flowTop = 24;
  const flowH = H - 48;

  const shortMeterName = (raw: string) => {
    const parts = raw.split(/\s*[·•]\s*/).filter(Boolean);
    const tail = parts.length >= 2 ? parts[parts.length - 1] : raw;
    return tail.length > 18 ? `${tail.slice(0, 17)}…` : tail;
  };

  let srcYAcc = flowTop;
  const rowH = flowH / allTargets.length;

  return (
    <div className="select-none" onMouseLeave={() => setHoverIdx(null)}>
      <svg viewBox={`0 0 ${W} ${H}`} className="w-full overflow-visible" role="img" aria-label={`能流平衡图（单位 ${unit}）`}>
        {/* Left Parent Meter Block */}
        <rect x={leftX} y={flowTop} width={leftW} height={flowH} rx={4} fill="#165DFF" fillOpacity={0.1} stroke="#165DFF" strokeWidth={1.5} />
        <text x={leftX + 12} y={flowTop + flowH / 2 - 8} className="fill-fg text-[12px] font-medium">
          {shortMeterName(resolvedParent)}
        </text>
        <text x={leftX + 12} y={flowTop + flowH / 2 + 12} className="fill-primary text-[14px] font-semibold tabular">
          {fmtVal(parentUsage)} {unit}
        </text>

        {/* Smooth Sankey Ribbons + Right Child Nodes */}
        {allTargets.map((t, idx) => {
          const share = t.usage / totalFlow;
          const srcBandH = Math.max(6, share * flowH);
          const sy1 = srcYAcc;
          const sy2 = srcYAcc + srcBandH;
          srcYAcc += srcBandH;

          const tyCenter = flowTop + idx * rowH + rowH / 2;
          const tBoxH = Math.min(34, Math.max(24, rowH - 8));
          const ty1 = tyCenter - Math.min(srcBandH / 2, tBoxH / 2 - 2);
          const ty2 = tyCenter + Math.min(srcBandH / 2, tBoxH / 2 - 2);

          const x1 = leftX + leftW;
          const x2 = rightX;
          const cx = (x1 + x2) / 2;
          const pathD = `M ${x1} ${sy1} C ${cx} ${sy1}, ${cx} ${ty1}, ${x2} ${ty1} L ${x2} ${ty2} C ${cx} ${ty2}, ${cx} ${sy2}, ${x1} ${sy2} Z`;
          const color = t.isDelta ? '#FF7D00' : CHART_PALETTE[idx % (CHART_PALETTE.length - 1)];
          const isHovered = hoverIdx === idx;
          const dimmed = hoverIdx != null && !isHovered;

          return (
            <g key={t.id} onMouseEnter={() => setHoverIdx(idx)} className="cursor-pointer">
              <path
                d={pathD}
                fill={color}
                fillOpacity={isHovered ? 0.42 : dimmed ? 0.08 : 0.22}
                className="transition-opacity duration-150"
              />
              <rect
                x={rightX}
                y={tyCenter - tBoxH / 2}
                width={rightW}
                height={tBoxH}
                rx={4}
                fill="var(--surface)"
                stroke={isHovered ? color : 'var(--line)'}
                strokeWidth={isHovered ? 1.5 : 1}
              />
              <rect x={rightX} y={tyCenter - tBoxH / 2} width={3.5} height={tBoxH} rx={1} fill={color} />
              <title>{`${t.name}：${fmtVal(t.usage)} ${unit}（${pctFmt.format(share)}）`}</title>
              <text x={rightX + 10} y={tyCenter - 2} className="fill-fg text-[12px] font-medium">
                {shortMeterName(t.name)}
              </text>
              <text x={rightX + 10} y={tyCenter + 12} className="fill-muted text-[11px] tabular">
                {fmtVal(t.usage)} {unit} · {pctFmt.format(share)}
              </text>
            </g>
          );
        })}
      </svg>
    </div>
  );
}

// ── 6. F15 Executive Assessment Charts (Gauge, Ranking, Distribution, Diverging) ──

export type AssessmentChartRow = {
  ruleId: string;
  objectName: string;
  energy: string;
  score: number | null;
  weightedScore?: number | null;
  weightPct: number;
  deviationRate: number | null;
  targetValue?: number | null;
  actualUsage?: number | null;
  unit?: string;
  reviewStatus: string;
  exempt: boolean;
};

/** Semi-circular executive radial gauge for F15 weighted total score (0-100). */
export function ScoreGaugeChart({
  score,
  label = '加权综合得分',
  statusLabel,
}: {
  score: number | null | undefined;
  label?: string;
  statusLabel?: string;
}) {
  const clamped = score == null ? 0 : Math.min(100, Math.max(0, score));
  const tone =
    score == null ? '#86909C' : clamped >= 85 ? '#00B42A' : clamped >= 70 ? '#165DFF' : clamped >= 60 ? '#FF7D00' : '#F53F3F';

  // Semi-circle radius 68 centered at (92, 86), angle 180deg -> 0deg
  const R = 68;
  const semiLen = Math.PI * R;
  const filledLen = score == null ? 0 : (clamped / 100) * semiLen;
  const theta = Math.PI * (1 - clamped / 100);
  const dotX = 92 + R * Math.cos(theta);
  const dotY = 86 - R * Math.sin(theta);

  return (
    <div className="flex flex-col items-center justify-center tabular">
      <svg viewBox="0 0 184 108" className="h-[108px] w-[184px]" role="img" aria-label={`${label} ${score ?? '—'} 分`}>
        {/* Background track */}
        <path
          d="M 24 86 A 68 68 0 0 1 160 86"
          fill="none"
          stroke="var(--fill-2)"
          strokeWidth={12}
          strokeLinecap="round"
        />
        {/* Active score arc */}
        {score != null && (
          <path
            d="M 24 86 A 68 68 0 0 1 160 86"
            fill="none"
            stroke={tone}
            strokeWidth={12}
            strokeLinecap="round"
            strokeDasharray={`${filledLen.toFixed(1)} ${semiLen.toFixed(1)}`}
          />
        )}
        {/* Indicator dot */}
        {score != null && (
          <circle cx={dotX} cy={dotY} r={5} fill="var(--surface)" stroke={tone} strokeWidth={3} />
        )}
        {/* Center score */}
        <text x="92" y="72" textAnchor="middle" className="fill-fg text-[28px] font-semibold">
          {score != null ? score.toFixed(1) : '—'}
        </text>
        <text x="92" y="90" textAnchor="middle" className="fill-muted text-[11px]">
          {statusLabel || `${label} / 100`}
        </text>
        {/* 0 / 60 / 100 scale markers */}
        <text x="20" y="102" textAnchor="middle" className="fill-muted text-[10px]">0</text>
        <text x="164" y="102" textAnchor="middle" className="fill-muted text-[10px]">100</text>
      </svg>
    </div>
  );
}

/** F15 Horizontal Ranked Organization Score Chart with 60/80 threshold reference lines. */
export function AssessmentScoreChart({ rows }: { rows: AssessmentChartRow[] }) {
  const [hoverId, setHoverId] = useState<string | null>(null);

  const sorted = useMemo(() => {
    const scored = rows.filter((r) => !r.exempt && r.score != null).sort((a, b) => (b.score ?? 0) - (a.score ?? 0));
    const others = rows.filter((r) => r.exempt || r.score == null);
    return [...scored, ...others];
  }, [rows]);

  if (sorted.length === 0) {
    return (
      <div className="flex h-[200px] items-center justify-center rounded-[4px] border border-dashed border-line bg-fill-1/40 text-[12px] text-muted">
        暂无组织考核评分数据
      </div>
    );
  }

  return (
    <div className="space-y-3 tabular">
      <div className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-muted">
        <div className="flex flex-wrap items-center gap-3">
          <span className="inline-flex items-center gap-1"><span className="size-2 rounded-[2px] bg-positive" /> 优秀 ≥85</span>
          <span className="inline-flex items-center gap-1"><span className="size-2 rounded-[2px] bg-primary" /> 良好 70-84</span>
          <span className="inline-flex items-center gap-1"><span className="size-2 rounded-[2px] bg-warning" /> 预警 60-69</span>
          <span className="inline-flex items-center gap-1"><span className="size-2 rounded-[2px] bg-negative" /> 不达标 &lt;60</span>
        </div>
        <span>虚线标尺：60 分及格 · 80 分达标</span>
      </div>

      <div className="relative space-y-2.5">
        {sorted.map((r, idx) => {
          const isScored = !r.exempt && r.score != null;
          const sc = r.score ?? 0;
          const color = !isScored
            ? '#C9CDD4'
            : sc >= 85
              ? '#00B42A'
              : sc >= 70
                ? '#165DFF'
                : sc >= 60
                  ? '#FF7D00'
                  : '#F53F3F';
          const isHovered = hoverId === r.ruleId;

          return (
            <div
              key={r.ruleId}
              onMouseEnter={() => setHoverId(r.ruleId)}
              onMouseLeave={() => setHoverId(null)}
              className={`rounded-[4px] px-2 py-1.5 transition-colors ${isHovered ? 'bg-fill-1' : ''}`}
            >
              <div className="flex items-center justify-between gap-2 text-[12px]">
                <div className="flex min-w-0 items-center gap-2">
                  <span
                    className={`inline-flex h-5 w-6 shrink-0 items-center justify-center rounded-[2px] text-[11px] font-medium ${
                      isScored && idx === 0
                        ? 'bg-primary text-white'
                        : isScored
                          ? 'bg-primary-soft text-primary'
                          : 'bg-fill-2 text-muted'
                    }`}
                  >
                    {isScored ? `#${idx + 1}` : '免'}
                  </span>
                  <span className="truncate font-medium text-fg">{r.objectName}</span>
                  <span className="shrink-0 text-[11px] text-muted">权重 {r.weightPct}%</span>
                </div>
                <div className="flex shrink-0 items-baseline gap-2.5">
                  {isScored ? (
                    <>
                      <span className="text-[13px] font-semibold text-fg">{sc.toFixed(1)} 分</span>
                      {r.weightedScore != null && (
                        <span className="w-16 text-right text-[11px] text-muted">加权 {r.weightedScore.toFixed(1)}</span>
                      )}
                    </>
                  ) : (
                    <span className="rounded-[2px] bg-fill-2 px-1.5 py-0 text-[11px] text-muted">
                      {r.exempt ? '已豁免' : '低质量阻断'}
                    </span>
                  )}
                </div>
              </div>

              <div className="relative mt-1.5 h-[10px] w-full overflow-hidden rounded-[2px] bg-fill-2">
                {/* 60 & 80 reference guide lines */}
                <div className="pointer-events-none absolute inset-y-0 left-[60%] w-px border-l border-dashed border-warning/70" />
                <div className="pointer-events-none absolute inset-y-0 left-[80%] w-px border-l border-dashed border-positive/70" />

                {isScored ? (
                  <div
                    className="h-full rounded-[2px] transition-all duration-200"
                    style={{ width: `${Math.max(2, Math.min(100, sc))}%`, backgroundColor: color }}
                  />
                ) : (
                  <div className="h-full w-full bg-[repeating-linear-gradient(45deg,transparent,transparent_5px,rgba(134,144,156,0.22)_5px,rgba(134,144,156,0.22)_10px)]" />
                )}
              </div>
            </div>
          );
        })}
      </div>

      {/* Explicit 0-100 Axis Footer */}
      <div className="relative flex items-center justify-between border-t border-line pt-1.5 text-[11px] text-muted">
        <span>0</span>
        <span>25</span>
        <span>50</span>
        <span className="text-positive">80 达标</span>
        <span>100 分</span>
      </div>
    </div>
  );
}

/** F15 Vertical Score Distribution Histogram across 5 performance tiers. */
export function ScoreDistributionChart({ rows }: { rows: AssessmentChartRow[] }) {
  const [hoverBand, setHoverBand] = useState<string | null>(null);

  const bands = useMemo(() => {
    const defs = [
      { key: 'exc', label: '优秀 ≥90', color: '#00B42A', match: (r: AssessmentChartRow) => !r.exempt && r.score != null && r.score >= 90 },
      { key: 'good', label: '良好 80-89', color: '#165DFF', match: (r: AssessmentChartRow) => !r.exempt && r.score != null && r.score >= 80 && r.score < 90 },
      { key: 'pass', label: '合格 60-79', color: '#FF7D00', match: (r: AssessmentChartRow) => !r.exempt && r.score != null && r.score >= 60 && r.score < 80 },
      { key: 'fail', label: '待整改 <60', color: '#F53F3F', match: (r: AssessmentChartRow) => !r.exempt && r.score != null && r.score < 60 },
      { key: 'other', label: '豁免/阻断', color: '#86909C', match: (r: AssessmentChartRow) => r.exempt || r.score == null },
    ];
    return defs.map((d) => {
      const items = rows.filter(d.match);
      return { ...d, count: items.length, names: items.map((i) => i.objectName) };
    });
  }, [rows]);

  const maxCount = Math.max(...bands.map((b) => b.count), 1);
  const total = Math.max(rows.length, 1);

  const W = 420;
  const H = 212;
  const pad = { top: 26, right: 12, bottom: 34, left: 32 };
  const plotW = W - pad.left - pad.right;
  const plotH = H - pad.top - pad.bottom;
  const slotW = plotW / bands.length;
  const barW = Math.min(40, slotW * 0.56);
  const yAt = (c: number) => pad.top + plotH - (c / maxCount) * plotH;

  const active = bands.find((b) => b.key === hoverBand) ?? null;

  return (
    <div className="space-y-2 tabular">
      <div className="flex items-center justify-between text-[11px] text-muted">
        <span>共 {rows.length} 个考核对象分档分布</span>
        <span>单位：个</span>
      </div>

      <div className="relative select-none" onMouseLeave={() => setHoverBand(null)}>
        <svg viewBox={`0 0 ${W} ${H}`} className="w-full overflow-visible" role="img" aria-label="考核得分分档分布图">
          {Array.from({ length: maxCount + 1 }, (_, tick) => {
            const y = yAt(tick);
            return (
              <g key={tick}>
                <line
                  x1={pad.left}
                  x2={W - pad.right}
                  y1={y}
                  y2={y}
                  stroke="currentColor"
                  className={tick === 0 ? 'text-line-strong' : 'text-line'}
                  strokeDasharray={tick === 0 ? undefined : '3 3'}
                />
                <text x={pad.left - 8} y={y + 3.5} textAnchor="end" className="fill-muted text-[11px]">
                  {tick}
                </text>
              </g>
            );
          })}

          {bands.map((b, i) => {
            const cx = pad.left + i * slotW + slotW / 2;
            const bx = cx - barW / 2;
            const bh = (b.count / maxCount) * plotH;
            const by = pad.top + plotH - bh;
            const isHovered = hoverBand === b.key;

            return (
              <g key={b.key} onMouseEnter={() => setHoverBand(b.key)} className="cursor-pointer">
                {isHovered && (
                  <rect x={pad.left + i * slotW} y={pad.top} width={slotW} height={plotH} fill="var(--fill-2)" fillOpacity={0.55} />
                )}
                <rect
                  x={bx}
                  y={b.count > 0 ? by : pad.top + plotH - 2}
                  width={barW}
                  height={b.count > 0 ? Math.max(4, bh) : 2}
                  rx={2}
                  fill={b.color}
                  fillOpacity={b.count > 0 ? (isHovered ? 1 : 0.86) : 0.25}
                />
                <text
                  x={cx}
                  y={(b.count > 0 ? by : pad.top + plotH) - 7}
                  textAnchor="middle"
                  className="fill-fg text-[12px] font-semibold"
                >
                  {b.count}
                </text>
                <text x={cx} y={H - 10} textAnchor="middle" className="fill-fg-2 text-[11px]">
                  {b.label}
                </text>
              </g>
            );
          })}
        </svg>

        {/* Active Band Member Strip */}
        <div className="mt-1 min-h-[24px] rounded-[2px] bg-fill-1 px-2.5 py-1 text-[11px] text-fg-2">
          {active ? (
            <span>
              <strong className="font-medium text-fg">{active.label}</strong>（{active.count} 个 ·{' '}
              {pctFmt.format(active.count / total)}）：
              {active.names.length > 0 ? active.names.join('、') : '无对象'}
            </span>
          ) : (
            <span className="text-muted">悬停各分档柱体查看对应组织名单</span>
          )}
        </div>
      </div>
    </div>
  );
}

/** F15 Zero-Centered Signed Deviation Diverging Bar Chart. */
export function DeviationDivergingChart({ rows }: { rows: AssessmentChartRow[] }) {
  const [hoverId, setHoverId] = useState<string | null>(null);

  const maxAbs = useMemo(() => {
    const vals = rows.map((r) => (r.deviationRate != null ? Math.abs(r.deviationRate) : 0));
    const raw = Math.max(...vals, 0.25);
    if (raw <= 0.5) return 0.5;
    if (raw <= 1.0) return 1.0;
    return Math.ceil(raw * 2) / 2;
  }, [rows]);

  if (rows.length === 0) return null;

  return (
    <div className="space-y-2.5 tabular">
      <div className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-muted">
        <div className="flex items-center gap-4">
          <span className="inline-flex items-center gap-1.5">
            <span className="size-2.5 rounded-[2px] bg-positive" />
            <span>低于考核目标（节约）</span>
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="size-2.5 rounded-[2px] bg-negative" />
            <span>高于考核目标（超额扣分）</span>
          </span>
        </div>
        <span>基准中轴：0%（实际 = 目标）</span>
      </div>

      <div className="space-y-2">
        {rows.map((r) => {
          const rate = r.deviationRate;
          const isZero = rate != null && Math.abs(rate) < 0.0005;
          const pctLabel =
            r.exempt || rate == null
              ? r.exempt
                ? '豁免'
                : '—'
              : isZero
                ? '0%'
                : `${rate > 0 ? '+' : ''}${pctFmt.format(rate)}`;
          const barWidthPct = rate != null ? Math.min(50, (Math.abs(rate) / maxAbs) * 50) : 0;
          const isHovered = hoverId === r.ruleId;

          return (
            <div
              key={r.ruleId}
              onMouseEnter={() => setHoverId(r.ruleId)}
              onMouseLeave={() => setHoverId(null)}
              className={`grid items-center gap-3 rounded-[4px] px-2 py-1.5 transition-colors sm:grid-cols-[176px_1fr_76px] ${
                isHovered ? 'bg-fill-1' : ''
              }`}
            >
              <div className="min-w-0">
                <p className="truncate text-[12px] font-medium text-fg">{r.objectName}</p>
                {r.targetValue != null && r.actualUsage != null && (
                  <p className="truncate text-[11px] text-muted">
                    实测 {fmtCompact(r.actualUsage)} / 目标 {fmtCompact(r.targetValue)} {r.unit ?? ''}
                  </p>
                )}
              </div>

              {/* Diverging Center-Axis Track */}
              <div className="relative h-5 overflow-hidden rounded-[2px] bg-fill-1">
                {/* Subtle -50% and +50% guide lines */}
                <div className="pointer-events-none absolute inset-y-0 left-1/4 w-px border-l border-dashed border-line" />
                <div className="pointer-events-none absolute inset-y-0 left-3/4 w-px border-l border-dashed border-line" />
                {/* Bold 0% center axis */}
                <div className="pointer-events-none absolute inset-y-0 left-1/2 w-[1.5px] -translate-x-1/2 bg-line-strong" />

                {!r.exempt && rate != null && (
                  <div
                    className={`absolute top-1 bottom-1 rounded-[2px] transition-all duration-200 ${
                      rate > 0.0005 ? 'bg-negative' : 'bg-positive'
                    }`}
                    style={
                      rate > 0.0005
                        ? { left: '50%', width: `${Math.max(1.5, barWidthPct)}%` }
                        : { right: '50%', width: `${Math.max(isZero ? 0.8 : 1.5, barWidthPct)}%` }
                    }
                  />
                )}
              </div>

              <span
                className={`text-right text-[12px] font-semibold ${
                  r.exempt || rate == null
                    ? 'text-muted'
                    : rate > 0.0005
                      ? 'text-negative'
                      : 'text-positive'
                }`}
              >
                {pctLabel}
              </span>
            </div>
          );
        })}
      </div>

      {/* Signed Axis Labels */}
      <div className="grid items-center gap-3 border-t border-line pt-1.5 text-[11px] text-muted sm:grid-cols-[176px_1fr_76px]">
        <span />
        <div className="flex items-center justify-between">
          <span>-{pctFmt.format(maxAbs)}</span>
          <span>-{pctFmt.format(maxAbs / 2)}</span>
          <span className="font-medium text-fg-2">0%</span>
          <span>+{pctFmt.format(maxAbs / 2)}</span>
          <span>+{pctFmt.format(maxAbs)}</span>
        </div>
        <span className="text-right">偏差率</span>
      </div>
    </div>
  );
}

// ── 7. F20 True Floating Bridge Waterfall & Baseline Comparison Charts ──────

/**
 * Authentic M&V Floating Bridge Waterfall:
 *   adjustedBaseline (0 -> B)
 *   - reportActual (floating down from B -> B - A)
 *   - nonRoutineAdjustment (floating step from B - A -> S)
 *   = verifiedSavings (standing from 0 -> S)
 */
export function RetrofitMVWaterfallChart({
  adjustedBaseline,
  reportActual,
  nonRoutine,
  nonRoutineAdjustment,
  verifiedSavings,
  savingsRate,
  unit,
}: {
  adjustedBaseline: number | null | undefined;
  reportActual: number | null | undefined;
  nonRoutine?: number;
  nonRoutineAdjustment?: number;
  verifiedSavings: number | null | undefined;
  savingsRate?: number | null;
  isVerified?: boolean;
  unit: string;
}) {
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);
  const adjVal = nonRoutine ?? nonRoutineAdjustment ?? 0;

  if (adjustedBaseline == null || reportActual == null || verifiedSavings == null) {
    return (
      <div className="flex h-[240px] flex-col items-center justify-center gap-1.5 rounded-[4px] border border-dashed border-line bg-fill-1/40 px-4 text-center">
        <p className="text-[13px] font-medium text-fg-2">尚未完成 M&amp;V 桥接核验</p>
        <p className="text-[12px] text-muted">
          需先锁定改造前基线并补齐报告期调整后基线与实算数据（覆盖率 ≥ 90%），系统方可绘制完整桥接瀑布图。
        </p>
      </div>
    );
  }

  const afterActual = adjustedBaseline - reportActual;
  const afterAdj = afterActual - adjVal;
  const topVal = Math.max(adjustedBaseline, reportActual, verifiedSavings, afterActual, afterAdj, 1);
  const scale = niceScale(topVal * 1.18, 4);

  const W = 640;
  const H = 264;
  const pad = { top: 28, right: 20, bottom: 48, left: 60 };
  const plotW = W - pad.left - pad.right;
  const plotH = H - pad.top - pad.bottom;
  const yAt = (v: number) => pad.top + plotH - (Math.max(0, v) / scale.max) * plotH;

  const steps = [
    {
      key: 'base',
      label: '调整后基线',
      sub: '对照基准',
      from: 0,
      to: adjustedBaseline,
      displayVal: adjustedBaseline,
      signedLabel: `${fmtCompact(adjustedBaseline)} ${unit}`,
      color: '#4E5969',
      connectorOut: adjustedBaseline,
      desc: '按报告期气象与运行条件调整后的基准用能量',
    },
    {
      key: 'actual',
      label: '报告期实算',
      sub: '扣减实际消耗',
      from: Math.max(0, afterActual),
      to: adjustedBaseline,
      displayVal: reportActual,
      signedLabel: `-${fmtCompact(reportActual)} ${unit}`,
      color: '#165DFF',
      connectorOut: Math.max(0, afterActual),
      desc: '报告期内真实计量表实测消耗量（桥接扣减）',
    },
    {
      key: 'adj',
      label: '非例行调整',
      sub: '边界修正项',
      from: Math.max(0, Math.min(afterActual, afterAdj)),
      to: Math.max(0, Math.max(afterActual, afterAdj)),
      displayVal: adjVal,
      signedLabel: `${adjVal >= 0 ? '-' : '+'}${fmtCompact(Math.abs(adjVal))} ${unit}`,
      color: '#FF7D00',
      connectorOut: Math.max(0, verifiedSavings),
      desc: '改造期间面积、班次或非改造设备变更引起的边界调整量',
    },
    {
      key: 'savings',
      label: '核验节能量',
      sub: savingsRate != null ? `节能率 ${pctFmt.format(savingsRate)}` : 'M&V 净节能量',
      from: 0,
      to: Math.max(0, verifiedSavings),
      displayVal: verifiedSavings,
      signedLabel: `${verifiedSavings >= 0 ? '+' : ''}${fmtCompact(verifiedSavings)} ${unit}`,
      color: verifiedSavings >= 0 ? '#00B42A' : '#F53F3F',
      connectorOut: null,
      desc: '核验节能量 = 调整后基线 - 报告期实算 - 非例行调整',
    },
  ];

  const slotW = plotW / steps.length;
  const barW = Math.min(72, slotW * 0.5);
  const hovered = hoverIdx != null ? steps[hoverIdx] : null;

  return (
    <div className="space-y-2 tabular">
      <div className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-muted">
        <span>核验公式：核验节能量 = 调整后基线 − 报告期实算 − 非例行调整</span>
        <span>单位：{unit}</span>
      </div>

      <div className="relative select-none" onMouseLeave={() => setHoverIdx(null)}>
        <svg viewBox={`0 0 ${W} ${H}`} className="h-auto max-h-[300px] w-full overflow-visible" role="img" aria-label={`M&V 桥接瀑布图（单位 ${unit}）`}>
          {scale.steps.map((s) => {
            const y = yAt(s);
            return (
              <g key={s}>
                <line
                  x1={pad.left}
                  x2={W - pad.right}
                  y1={y}
                  y2={y}
                  stroke="currentColor"
                  className={s === 0 ? 'text-line-strong' : 'text-line'}
                  strokeDasharray={s === 0 ? undefined : '3 3'}
                />
                <text x={pad.left - 8} y={y + 3.5} textAnchor="end" className="fill-muted text-[11px]">
                  {fmtCompact(s)}
                </text>
              </g>
            );
          })}

          {/* Horizontal dashed bridge connectors between adjacent waterfall steps */}
          {steps.map((st, idx) => {
            if (idx === steps.length - 1 || st.connectorOut == null) return null;
            const x1 = pad.left + idx * slotW + slotW / 2 + barW / 2;
            const x2 = pad.left + (idx + 1) * slotW + slotW / 2 - barW / 2;
            const y = yAt(st.connectorOut);
            return (
              <line
                key={`conn-${st.key}`}
                x1={x1}
                x2={x2}
                y1={y}
                y2={y}
                stroke="var(--line-strong)"
                strokeDasharray="3 3"
                strokeWidth={1.25}
              />
            );
          })}

          {/* Bridge Bars */}
          {steps.map((st, idx) => {
            const cx = pad.left + idx * slotW + slotW / 2;
            const bx = cx - barW / 2;
            const yTop = yAt(st.to);
            const yBot = yAt(st.from);
            const bh = Math.max(4, yBot - yTop);
            const isHovered = hoverIdx === idx;

            return (
              <g key={st.key} onMouseEnter={() => setHoverIdx(idx)} className="cursor-pointer">
                {isHovered && (
                  <rect x={pad.left + idx * slotW} y={pad.top} width={slotW} height={plotH} fill="var(--fill-2)" fillOpacity={0.45} />
                )}
                <rect
                  x={bx}
                  y={yTop}
                  width={barW}
                  height={bh}
                  rx={2}
                  fill={st.color}
                  fillOpacity={isHovered ? 1 : 0.88}
                />
                <text
                  x={cx}
                  y={Math.max(14, yTop - 7)}
                  textAnchor="middle"
                  className="fill-fg text-[12px] font-semibold"
                >
                  {st.signedLabel}
                </text>
                <text x={cx} y={H - 24} textAnchor="middle" className="fill-fg text-[12px] font-medium">
                  {st.label}
                </text>
                <text x={cx} y={H - 8} textAnchor="middle" className="fill-muted text-[10px]">
                  {st.sub}
                </text>
              </g>
            );
          })}
        </svg>

        {hovered && (
          <div className="mt-1 flex flex-wrap items-center justify-between gap-2 rounded-[2px] bg-fill-1 px-3 py-1.5 text-[12px]">
            <span className="text-fg-2">
              <strong className="font-medium text-fg">{hovered.label}</strong>：{hovered.desc}
            </span>
            <span className="font-semibold text-fg">
              {fmtVal(hovered.displayVal)} {unit}
            </span>
          </div>
        )}
      </div>
    </div>
  );
}

/** F20 Companion Baseline vs Reporting Period Direct Comparison Chart (Total + Daily Intensity). */
export function BaselineVsActualChart({
  adjustedBaseline,
  baselineActual,
  baselineDailyAvg,
  reportActual,
  reportDays,
  unit,
}: {
  adjustedBaseline: number | null | undefined;
  baselineActual: number | null | undefined;
  baselineDailyAvg: number;
  reportActual: number | null | undefined;
  reportDays: number;
  unit: string;
}) {
  const refTotal = adjustedBaseline ?? baselineActual ?? null;
  const reportDailyAvg = reportActual != null && reportDays > 0 ? reportActual / reportDays : null;

  const pairs = [
    {
      label: adjustedBaseline != null ? '周期总量（调整后基线 vs 实算）' : '周期总量（基线实测 vs 报告期实测）',
      unit,
      base: refTotal,
      actual: reportActual ?? null,
    },
    {
      label: '日均用能强度（基线日均 vs 报告期日均）',
      unit: `${unit}/日`,
      base: baselineDailyAvg > 0 ? baselineDailyAvg : null,
      actual: reportDailyAvg,
    },
  ];

  return (
    <div className="space-y-4 tabular">
      {pairs.map((p) => {
        const maxV = Math.max(p.base ?? 0, p.actual ?? 0, 1);
        const deltaPct = p.base != null && p.base > 0 && p.actual != null ? (p.actual - p.base) / p.base : null;

        return (
          <div key={p.label} className="space-y-2 rounded-[4px] bg-fill-1/60 p-3.5">
            <div className="flex items-center justify-between gap-2 text-[12px]">
              <span className="font-medium text-fg">{p.label}</span>
              {deltaPct != null && (
                <span
                  className={`rounded-[2px] px-1.5 py-0 text-[11px] font-medium leading-[18px] ${
                    deltaPct <= 0 ? 'bg-positive-soft text-positive' : 'bg-negative-soft text-negative'
                  }`}
                >
                  {deltaPct > 0 ? '+' : ''}
                  {pctFmt.format(deltaPct)}
                </span>
              )}
            </div>

            <div className="space-y-2">
              <div>
                <div className="mb-1 flex justify-between text-[12px]">
                  <span className="text-fg-2">基线参照</span>
                  <span className="font-medium text-fg">
                    {fmtVal(p.base)} <span className="text-[11px] font-normal text-muted">{p.unit}</span>
                  </span>
                </div>
                <div className="h-[10px] w-full overflow-hidden rounded-[2px] bg-fill-2">
                  <div
                    className="h-full rounded-[2px] bg-[#86909C]"
                    style={{ width: `${p.base != null ? Math.max(2, (p.base / maxV) * 100) : 0}%` }}
                  />
                </div>
              </div>

              <div>
                <div className="mb-1 flex justify-between text-[12px]">
                  <span className="text-fg-2">报告期实测</span>
                  <span className="font-semibold text-primary">
                    {fmtVal(p.actual)} <span className="text-[11px] font-normal text-muted">{p.unit}</span>
                  </span>
                </div>
                <div className="h-[10px] w-full overflow-hidden rounded-[2px] bg-fill-2">
                  <div
                    className="h-full rounded-[2px] bg-primary"
                    style={{ width: `${p.actual != null ? Math.max(2, (p.actual / maxV) * 100) : 0}%` }}
                  />
                </div>
              </div>
            </div>
          </div>
        );
      })}
    </div>
  );
}

// ── 8. RetrofitScenarioChart (F19 3-Scenario Vertical Column Comparison + Physical Baseline Ceiling) ──

export function RetrofitScenarioChart({
  low,
  base,
  high,
  costLow,
  costBase,
  costHigh,
  paybackLow,
  paybackBase,
  paybackHigh,
  annualizedBaseline,
  unit,
}: {
  low: number;
  base: number;
  high: number;
  costLow?: number | null;
  costBase?: number | null;
  costHigh?: number | null;
  paybackLow?: number | null;
  paybackBase?: number | null;
  paybackHigh?: number | null;
  annualizedBaseline?: number | null;
  unit: string;
}) {
  const [hoverKey, setHoverKey] = useState<string | null>(null);
  const rows = [
    { key: 'low', name: '保守情景', savings: low, cost: costLow, payback: paybackLow, color: '#86909C' },
    { key: 'base', name: '基准情景（推荐）', savings: base, cost: costBase, payback: paybackBase, color: '#165DFF' },
    { key: 'high', name: '乐观情景', savings: high, cost: costHigh, payback: paybackHigh, color: '#00B42A' },
  ];

  const topRaw = Math.max(low, base, high, annualizedBaseline ?? 0, 1);
  const scale = niceScale(topRaw * 1.18, 4);
  const W = 680;
  const H = 224;
  const pad = { top: 26, right: 20, bottom: 42, left: 64 };
  const plotW = W - pad.left - pad.right;
  const plotH = H - pad.top - pad.bottom;
  const yAt = (v: number) => pad.top + plotH - (Math.max(0, v) / scale.max) * plotH;
  const slotW = plotW / rows.length;
  const barW = Math.min(72, slotW * 0.42);
  const baseLineY = annualizedBaseline != null && annualizedBaseline > 0 ? yAt(annualizedBaseline) : null;
  const anyExceeds = annualizedBaseline != null && annualizedBaseline > 0 && rows.some((r) => r.savings > annualizedBaseline);
  const hovered = rows.find((r) => r.key === hoverKey) ?? null;

  return (
    <div className="space-y-2 tabular">
      <div className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-muted">
        <span>三情景预计年节能量对照（柱状图 · 单位：{unit}/年）</span>
        {annualizedBaseline != null && annualizedBaseline > 0 && (
          <span className={`inline-flex items-center gap-1.5 ${anyExceeds ? 'font-medium text-negative' : 'text-fg-2'}`}>
            <span className="w-4 border-t-2 border-dashed border-negative" />
            年化基线物理上限 {fmtVal(annualizedBaseline)} {unit}
          </span>
        )}
      </div>

      <div className="relative select-none" onMouseLeave={() => setHoverKey(null)}>
        <svg viewBox={`0 0 ${W} ${H}`} className="h-auto max-h-[244px] w-full overflow-visible" role="img" aria-label={`三情景年节能量柱状对比图（单位 ${unit}/年）`}>
          {scale.steps.map((v) => {
            const y = yAt(v);
            return (
              <g key={v}>
                <line x1={pad.left} x2={W - pad.right} y1={y} y2={y} stroke="var(--line)" strokeDasharray={v === 0 ? undefined : '3 3'} />
                <text x={pad.left - 8} y={y + 3.5} textAnchor="end" className="fill-muted text-[10px]">
                  {fmtCompact(v)}
                </text>
              </g>
            );
          })}

          {rows.map((r, idx) => {
            const cx = pad.left + idx * slotW + slotW / 2;
            const bx = cx - barW / 2;
            const by = yAt(r.savings);
            const bh = Math.max(2, pad.top + plotH - by);
            const exceeds = annualizedBaseline != null && annualizedBaseline > 0 && r.savings > annualizedBaseline;
            const isHov = hoverKey === r.key;
            const fill = exceeds ? '#F53F3F' : r.color;

            return (
              <g key={r.key} className="cursor-pointer" onMouseEnter={() => setHoverKey(r.key)}>
                {isHov && <rect x={pad.left + idx * slotW} y={pad.top} width={slotW} height={plotH} fill="var(--fill-2)" fillOpacity={0.5} />}
                <rect x={bx} y={by} width={barW} height={bh} rx={2} fill={fill} fillOpacity={isHov ? 1 : 0.88} />
                <text x={cx} y={by - 7} textAnchor="middle" className={`text-[11px] font-semibold ${exceeds ? 'fill-negative' : 'fill-fg'}`}>
                  {fmtCompact(r.savings)}{exceeds ? ' (超基线)' : ''}
                </text>
                <text x={cx} y={H - 22} textAnchor="middle" className="fill-fg text-[11px] font-medium">
                  {r.name}
                </text>
                <text x={cx} y={H - 8} textAnchor="middle" className="fill-muted text-[10px]">
                  {r.cost != null ? `节费 ¥${fmtCompact(r.cost)}` : '缺单价'}
                  {r.payback != null ? ` · ${r.payback.toFixed(1)}年` : ''}
                </text>
              </g>
            );
          })}

          {baseLineY != null && (
            <line x1={pad.left} x2={W - pad.right} y1={baseLineY} y2={baseLineY} stroke="#F53F3F" strokeWidth={1.6} strokeDasharray="5 4" />
          )}
        </svg>

        {hovered && (
          <div className="mt-1 flex flex-wrap items-center justify-between gap-2 rounded-[2px] bg-fill-1 px-3 py-1.5 text-[11px]">
            <span className="font-medium text-fg">{hovered.name}</span>
            <div className="flex flex-wrap items-center gap-3">
              <span>预计年节能量 <strong className="text-fg">{fmtVal(hovered.savings)} {unit}</strong></span>
              {annualizedBaseline != null && annualizedBaseline > 0 && (
                hovered.savings > annualizedBaseline ? (
                  <span className="font-semibold text-negative">超出物理基线 +{fmtVal(hovered.savings - annualizedBaseline)} {unit}（无效节能率）</span>
                ) : (
                  <span className="text-positive">占年化基线 {pctFmt.format(hovered.savings / annualizedBaseline)}</span>
                )
              )}
              {hovered.payback != null && <span className="text-primary">静态回收 {hovered.payback.toFixed(2)} 年</span>}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

// ── 9. StackedCategoryTrendChart (F06 Multi-Day Category Trend & Share Evolution) ──

export type CategoryTrendDay = {
  date: string;
  total: number | null;
  values: Record<string, number | null>;
};

export type CategoryTrendSeries = {
  id: string;
  name: string;
  color: string;
  isUnclassified?: boolean;
};

export function StackedCategoryTrendChart({
  days,
  series,
  unit,
  mode = 'value',
  selectedId,
  onSelectCategory,
}: {
  days: CategoryTrendDay[];
  series: CategoryTrendSeries[];
  unit: string;
  mode?: 'value' | 'share';
  selectedId?: string | null;
  onSelectCategory?: (id: string) => void;
}) {
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);

  const visibleSeries = useMemo(
    () => (selectedId ? series.filter((s) => s.id === selectedId) : series),
    [series, selectedId],
  );

  const scale = useMemo(() => {
    if (mode === 'share' && !selectedId) return { max: 1, steps: [0, 0.25, 0.5, 0.75, 1] };
    if (mode === 'share' && selectedId) {
      const shares = days.map((d) => {
        const v = d.values[selectedId] ?? 0;
        return d.total && d.total > 0 ? v / d.total : 0;
      });
      const top = Math.min(1, Math.max(...shares, 0.2) * 1.25);
      return niceScale(top, 4);
    }
    const totals = days.map((d) =>
      visibleSeries.reduce((sum, s) => sum + (d.values[s.id] ?? 0), 0),
    );
    return niceScale(Math.max(...totals, 1) * 1.15, 4);
  }, [days, visibleSeries, mode, selectedId]);

  if (days.length === 0 || visibleSeries.length === 0) {
    return (
      <div className="flex h-[220px] items-center justify-center rounded-[4px] border border-dashed border-line bg-fill-1/40 text-[12px] text-muted">
        暂无分项多日趋势数据
      </div>
    );
  }

  const W = 720;
  const H = 252;
  const pad = { top: 20, right: 16, bottom: 28, left: 54 };
  const plotW = W - pad.left - pad.right;
  const plotH = H - pad.top - pad.bottom;
  const slotW = plotW / days.length;
  const barW = Math.min(44, Math.max(16, slotW * 0.52));
  const yAt = (v: number) => pad.top + plotH - (Math.max(0, v) / scale.max) * plotH;

  const hovered = hoverIdx != null ? days[hoverIdx] : null;

  return (
    <div className="space-y-2 tabular">
      {/* Interactive Legend */}
      <div className="flex flex-wrap items-center justify-between gap-2 text-[12px]">
        <div className="flex flex-wrap items-center gap-3">
          {series.map((s) => {
            const active = !selectedId || selectedId === s.id;
            return (
              <button
                key={s.id}
                type="button"
                onClick={() => onSelectCategory?.(s.id)}
                className={`inline-flex items-center gap-1.5 rounded-[2px] px-1.5 py-0.5 transition-opacity ${
                  active ? 'text-fg-2 opacity-100 hover:bg-fill-1' : 'text-muted opacity-40 hover:opacity-80'
                }`}
              >
                <span className="size-2.5 rounded-[2px]" style={{ backgroundColor: s.color }} />
                <span>{s.name}</span>
              </button>
            );
          })}
        </div>
        <span className="text-[11px] text-muted">
          {mode === 'share' ? '纵轴：占当日总量比例 (%)' : `纵轴：日用量 (${unit})`}
        </span>
      </div>

      <div className="relative select-none" onMouseLeave={() => setHoverIdx(null)}>
        <svg viewBox={`0 0 ${W} ${H}`} className="h-auto max-h-[280px] w-full overflow-visible" role="img" aria-label="分项多日趋势与占比变化图">
          {scale.steps.map((step) => {
            const y = yAt(step);
            return (
              <g key={step}>
                <line
                  x1={pad.left}
                  x2={W - pad.right}
                  y1={y}
                  y2={y}
                  stroke="currentColor"
                  className={step === 0 ? 'text-line-strong' : 'text-line'}
                  strokeDasharray={step === 0 ? undefined : '3 3'}
                />
                <text x={pad.left - 8} y={y + 3.5} textAnchor="end" className="fill-muted text-[11px]">
                  {mode === 'share' ? pctFmt.format(step) : fmtCompact(step)}
                </text>
              </g>
            );
          })}

          {days.map((d, di) => {
            const cx = pad.left + di * slotW + slotW / 2;
            const bx = cx - barW / 2;
            const daySum = visibleSeries.reduce((acc, s) => acc + (d.values[s.id] ?? 0), 0);
            const denom = mode === 'share' ? (selectedId ? (d.total ?? 0) : daySum) : 1;
            let accY = 0;
            const isHovered = hoverIdx === di;

            return (
              <g key={d.date} onMouseEnter={() => setHoverIdx(di)}>
                {isHovered && (
                  <rect x={pad.left + di * slotW} y={pad.top} width={slotW} height={plotH} fill="var(--fill-2)" fillOpacity={0.5} />
                )}
                {visibleSeries.map((s) => {
                  const raw = d.values[s.id] ?? 0;
                  const val = mode === 'share' ? (denom > 0 ? raw / denom : 0) : raw;
                  if (val <= 0) return null;
                  const yBot = yAt(accY);
                  accY += val;
                  const yTop = yAt(accY);
                  const bh = Math.max(1.5, yBot - yTop);
                  return (
                    <rect
                      key={`${d.date}-${s.id}`}
                      x={bx}
                      y={yTop}
                      width={barW}
                      height={bh}
                      fill={s.color}
                      fillOpacity={isHovered ? 1 : 0.88}
                      className="cursor-pointer"
                      onClick={() => onSelectCategory?.(s.id)}
                    />
                  );
                })}
                <text x={cx} y={H - 8} textAnchor="middle" className="fill-muted text-[11px]">
                  {d.date.slice(5)}
                </text>
              </g>
            );
          })}
        </svg>

        {hoverIdx != null && hovered && (
          <div
            className="pointer-events-none absolute top-2 z-20 min-w-[200px] rounded-[4px] border border-line bg-surface px-3 py-2 text-[12px] shadow-panel"
            style={{
              left: `${Math.min(68, Math.max(4, ((pad.left + hoverIdx * slotW) / W) * 100))}%`,
              transform: hoverIdx >= days.length / 2 ? 'translateX(-102%)' : 'translateX(14px)',
            }}
          >
            <div className="mb-1 flex items-center justify-between border-b border-line pb-1 text-[11px] font-medium text-fg">
              <span>{hovered.date}</span>
              <span className="text-muted">合计 {fmtVal(hovered.total)} {unit}</span>
            </div>
            <div className="space-y-1">
              {visibleSeries.map((s) => {
                const v = hovered.values[s.id] ?? null;
                const sh = v != null && hovered.total && hovered.total > 0 ? v / hovered.total : null;
                return (
                  <div key={s.id} className="flex items-center justify-between gap-3 text-[11px]">
                    <span className="inline-flex items-center gap-1.5 text-fg-2">
                      <span className="size-2 rounded-[2px]" style={{ backgroundColor: s.color }} />
                      {s.name}
                    </span>
                    <span className="font-medium text-fg">
                      {fmtVal(v)} {unit}
                      {sh != null && <span className="ml-1 text-muted">({pctFmt.format(sh)})</span>}
                    </span>
                  </div>
                );
              })}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

// ── 10. PeriodVarianceBarChart (F08 Signed Period-over-Period Delta Bars) ───

export type VariancePoint = {
  label: string;
  current: number | null;
  baseline: number | null;
};

export function PeriodVarianceBarChart({
  points,
  unit,
  wide = false,
  increaseLabel = '本期高于上期（增长）',
  decreaseLabel = '本期低于上期（节约）',
  formulaLabel,
}: {
  points: VariancePoint[];
  unit: string;
  wide?: boolean;
  increaseLabel?: string;
  decreaseLabel?: string;
  formulaLabel?: string;
}) {
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);

  const deltas = useMemo(
    () =>
      points.map((p) => {
        const delta = p.current != null && p.baseline != null ? p.current - p.baseline : null;
        const rate = delta != null && p.baseline != null && p.baseline > 0 ? delta / p.baseline : null;
        return { ...p, delta, rate };
      }),
    [points],
  );

  const maxAbs = useMemo(() => {
    const vals = deltas.map((d) => (d.delta != null ? Math.abs(d.delta) : 0));
    return niceScale(Math.max(...vals, 1) * 1.15, 2).max;
  }, [deltas]);

  if (deltas.length === 0) return null;

  const W = wide ? 1120 : 760;
  const H = 136;
  const pad = { top: 14, right: 16, bottom: 24, left: 54 };
  const plotW = W - pad.left - pad.right;
  const plotH = H - pad.top - pad.bottom;
  const midY = pad.top + plotH / 2;
  const slotW = plotW / deltas.length;
  const barW = Math.min(24, Math.max(6, slotW * 0.62));
  const hovered = hoverIdx != null ? deltas[hoverIdx] : null;
  const tickStep = Math.max(1, Math.ceil(deltas.length / 8));

  return (
    <div className="space-y-1.5 tabular">
      <div className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-muted">
        <div className="flex items-center gap-3.5">
          <span className="inline-flex items-center gap-1.5">
            <span className="size-2 rounded-[2px] bg-negative" />
            <span>{increaseLabel}</span>
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="size-2 rounded-[2px] bg-positive" />
            <span>{decreaseLabel}</span>
          </span>
        </div>
        <span>{formulaLabel ?? `差额 = 本期 − 可比上期（${unit}）`}</span>
      </div>

      <div className="relative select-none" onMouseLeave={() => setHoverIdx(null)}>
        <svg viewBox={`0 0 ${W} ${H}`} className="h-auto max-h-[148px] w-full overflow-visible" role="img" aria-label={`分时段差幅图（单位 ${unit}）`}>
          {/* +max, 0, -max gridlines */}
          {[maxAbs, 0, -maxAbs].map((val) => {
            const y = midY - (val / maxAbs) * (plotH / 2);
            return (
              <g key={val}>
                <line
                  x1={pad.left}
                  x2={W - pad.right}
                  y1={y}
                  y2={y}
                  stroke="currentColor"
                  className={val === 0 ? 'text-line-strong' : 'text-line'}
                  strokeDasharray={val === 0 ? undefined : '3 3'}
                />
                <text x={pad.left - 8} y={y + 3.5} textAnchor="end" className="fill-muted text-[10px]">
                  {val === 0 ? '0' : `${val > 0 ? '+' : ''}${fmtCompact(val)}`}
                </text>
              </g>
            );
          })}

          {deltas.map((d, idx) => {
            const cx = pad.left + idx * slotW + slotW / 2;
            const bx = cx - barW / 2;
            const isHovered = hoverIdx === idx;
            const h = d.delta != null ? Math.max(2, (Math.abs(d.delta) / maxAbs) * (plotH / 2)) : 0;
            const y = d.delta != null ? (d.delta >= 0 ? midY - h : midY) : midY;

            return (
              <g key={`${d.label}-${idx}`} onMouseEnter={() => setHoverIdx(idx)}>
                {isHovered && (
                  <rect x={pad.left + idx * slotW} y={pad.top} width={slotW} height={plotH} fill="var(--fill-2)" fillOpacity={0.55} />
                )}
                {d.delta != null && (
                  <rect
                    x={bx}
                    y={y}
                    width={barW}
                    height={h}
                    rx={1.5}
                    fill={d.delta > 0 ? '#F53F3F' : '#00B42A'}
                    fillOpacity={isHovered ? 1 : 0.82}
                  />
                )}
                {(idx % tickStep === 0 || idx === deltas.length - 1) && (
                  <text x={cx} y={H - 6} textAnchor="middle" className="fill-muted text-[10px]">
                    {d.label}
                  </text>
                )}
              </g>
            );
          })}
        </svg>

        {hoverIdx != null && hovered && (
          <div className="mt-1 flex flex-wrap items-center justify-between gap-2 rounded-[2px] bg-fill-1 px-3 py-1 text-[11px] text-fg-2">
            <span>
              时段 <strong className="font-medium text-fg">{hovered.label}</strong>：本期 {fmtVal(hovered.current)} {unit} · 上期{' '}
              {fmtVal(hovered.baseline)} {unit}
            </span>
            {hovered.delta != null ? (
              <span className={hovered.delta > 0 ? 'font-semibold text-negative' : 'font-semibold text-positive'}>
                差额 {hovered.delta > 0 ? '+' : ''}
                {fmtVal(hovered.delta)} {unit}
                {hovered.rate != null ? ` (${hovered.delta > 0 ? '+' : ''}${pctFmt.format(hovered.rate)})` : ''}
              </span>
            ) : (
              <span className="text-muted">该时段不可比</span>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

// ── 11. BenchmarkBulletChart (F13 Standard & Threshold Range Comparison) ───
// Each benchmark metric renders on its own native-unit scale (never mixing units),
// showing Safe/Warning/Exceeds zones, vertical threshold line, signed margin,
// and clear distinction between demo rules, internal targets, and external standards.

export type BenchmarkBulletItem = {
  id: string;
  metricName: string;
  unit: string;
  actual: number | null;
  maximum: number | null;
  status: string;
  statusLabel: string;
  ruleKind?: string;
  ruleName?: string;
  ruleNotice?: string;
  coverageRate?: number | null;
};

export function BenchmarkBulletChart({
  items,
  selectedId,
  onSelect,
}: {
  items: BenchmarkBulletItem[];
  selectedId?: string | null;
  onSelect?: (id: string) => void;
}) {
  if (items.length === 0) {
    return (
      <div className="flex h-[180px] items-center justify-center rounded-[4px] border border-dashed border-line bg-fill-1/40 text-[12px] text-muted">
        当前范围暂无可对标指标项
      </div>
    );
  }

  return (
    <div className="space-y-3 tabular">
      <div className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-muted">
        <div className="flex flex-wrap items-center gap-3.5">
          <span className="inline-flex items-center gap-1.5">
            <span className="size-2.5 rounded-[2px] bg-primary" />
            <span>低于阈值上限</span>
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="size-2.5 rounded-[2px] bg-negative" />
            <span>超出阈值上限</span>
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="h-3 w-[2px] bg-fg" />
            <span>对标阈值上限</span>
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="size-2.5 rounded-[2px] border border-dashed border-line-strong bg-fill-2" />
            <span>无标准 / 不可比</span>
          </span>
        </div>
        <span>各指标按自身单位独立尺度展示（严禁跨单位混比）</span>
      </div>

      <div className="space-y-2.5">
        {items.map((it) => {
          const hasRule = it.maximum != null && it.maximum > 0;
          const comparable =
            hasRule &&
            it.actual != null &&
            (it.status === 'within_threshold' || it.status === 'exceeds_threshold');
          const exceeds = it.status === 'exceeds_threshold';
          const scaleMax = hasRule
            ? Math.max(it.maximum! * 1.35, (it.actual ?? 0) * 1.15)
            : Math.max((it.actual ?? 0) * 1.3, 1);
          const actualPct = it.actual != null ? Math.min(100, (it.actual / scaleMax) * 100) : 0;
          const maxPct = hasRule ? Math.min(96, (it.maximum! / scaleMax) * 100) : null;
          const warnStartPct = maxPct != null ? maxPct * 0.8 : null;
          const delta = comparable ? it.actual! - it.maximum! : null;
          const deltaRate = comparable && it.maximum! > 0 ? delta! / it.maximum! : null;
          const isSel = selectedId === it.id;

          const ruleTierLabel =
            it.ruleKind === 'demo'
              ? '演示规则（非正式认证）'
              : it.ruleKind === 'external'
                ? '外部权威标准'
                : it.ruleKind === 'internal'
                  ? '内部目标定额'
                  : null;

          return (
            <div
              key={it.id}
              onClick={() => onSelect?.(it.id)}
              className={`rounded-[4px] border p-3.5 transition-colors ${
                onSelect ? 'cursor-pointer' : ''
              } ${
                isSel
                  ? 'border-primary/45 bg-primary-soft/40'
                  : 'border-line bg-surface hover:bg-fill-1/50'
              }`}
            >
              {/* Top line: Metric Name + Rule Tier + Status + Numeric Delta */}
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex min-w-0 flex-wrap items-center gap-2">
                  <span className="text-[13px] font-medium text-fg">{it.metricName}</span>
                  {ruleTierLabel && (
                    <span
                      className={`rounded-[2px] px-1.5 py-0 text-[10px] font-medium leading-[17px] ${
                        it.ruleKind === 'demo'
                          ? 'bg-warning-soft text-warning'
                          : it.ruleKind === 'external'
                            ? 'bg-positive-soft text-positive'
                            : 'bg-primary-soft text-primary'
                      }`}
                    >
                      {ruleTierLabel}
                    </span>
                  )}
                  <span
                    className={`rounded-[2px] px-1.5 py-0 text-[10px] font-medium leading-[17px] ${
                      exceeds
                        ? 'bg-negative-soft text-negative'
                        : it.status === 'within_threshold'
                          ? 'bg-positive-soft text-positive'
                          : 'bg-fill-2 text-fg-2'
                    }`}
                  >
                    {it.statusLabel}
                  </span>
                </div>

                <div className="flex shrink-0 items-baseline gap-3 text-[12px]">
                  <span>
                    <span className="text-muted">实测 </span>
                    <strong className="text-[14px] font-semibold text-fg">{fmtVal(it.actual)}</strong>
                    <span className="ml-1 text-[11px] text-muted">{it.unit}</span>
                  </span>
                  {hasRule && (
                    <span>
                      <span className="text-muted">上限 </span>
                      <strong className="font-medium text-fg-2">{fmtVal(it.maximum)}</strong>
                    </span>
                  )}
                  {delta != null && deltaRate != null && (
                    <span className={`font-semibold ${exceeds ? 'text-negative' : 'text-positive'}`}>
                      {delta > 0 ? '+' : ''}
                      {fmtVal(delta)} ({delta > 0 ? '+' : ''}
                      {pctFmt.format(deltaRate)})
                    </span>
                  )}
                </div>
              </div>

              {/* Bullet Range Track */}
              <div className="relative mt-2.5 h-[16px] w-full overflow-hidden rounded-[2px] bg-fill-2">
                {comparable && maxPct != null && warnStartPct != null ? (
                  <>
                    {/* Safe band (0..80% of threshold) */}
                    <div
                      className="absolute inset-y-0 left-0 bg-positive/10"
                      style={{ width: `${warnStartPct}%` }}
                    />
                    {/* Caution band (80%..100% of threshold) */}
                    <div
                      className="absolute inset-y-0 bg-warning/15"
                      style={{ left: `${warnStartPct}%`, width: `${maxPct - warnStartPct}%` }}
                    />
                    {/* Over-limit zone (>100% of threshold) */}
                    <div
                      className="absolute inset-y-0 right-0 bg-negative/10"
                      style={{ left: `${maxPct}%` }}
                    />
                    {/* Actual value bar */}
                    <div
                      className="absolute top-[3px] bottom-[3px] left-0 rounded-r-[2px] transition-all duration-200"
                      style={{
                        width: `${Math.max(2, actualPct)}%`,
                        backgroundColor: exceeds ? '#F53F3F' : '#165DFF',
                      }}
                    />
                    {/* Threshold vertical marker */}
                    <div
                      className="absolute inset-y-0 w-[2px] bg-fg"
                      style={{ left: `${maxPct}%` }}
                      title={`阈值上限 ${fmtVal(it.maximum)} ${it.unit}`}
                    />
                  </>
                ) : (
                  <div className="flex h-full w-full items-center bg-[repeating-linear-gradient(45deg,transparent,transparent_6px,rgba(134,144,156,0.18)_6px,rgba(134,144,156,0.18)_12px)] px-2.5">
                    <span className="text-[10px] text-fg-2">
                      {it.ruleNotice || '未配置适用定额标准或数据不可比，保留实测值但不输出达标结论'}
                    </span>
                  </div>
                )}
              </div>

              {/* Scale Footer for comparable tracks */}
              {comparable && maxPct != null && (
                <div className="relative mt-1 flex items-center justify-between text-[10px] text-muted">
                  <span>0</span>
                  <span>
                    {it.ruleName ? `${it.ruleName} · ` : ''}上限 {fmtVal(it.maximum)} {it.unit}
                  </span>
                  <span>{fmtCompact(scaleMax)} {it.unit}</span>
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

// ── 12. DiscreteSampleCompareChart (F10 Sparse Weekly Sample Comparison) ───
// Designed for discrete multi-week same-weekday samples (3..8 points) where columns
// + horizontal baseline rule + highlighted candidate bar communicate far more clearly
// than a stretched line across a wide canvas.

export function DiscreteSampleCompareChart({
  points,
  unit,
  baseline,
  baselineLabel = '同星期中位基线',
  highlightIndex,
  highlightLabel = '规则命中候选日',
  onBarClick,
}: {
  points: TrendPoint[];
  unit: string;
  baseline?: number | null;
  baselineLabel?: string;
  highlightIndex?: number | null;
  highlightLabel?: string;
  onBarClick?: (idx: number) => void;
}) {
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);

  const stats = useMemo(() => {
    const vals = points.map((p) => p.usage ?? 0);
    const maxV = Math.max(...vals, baseline ?? 0, 1);
    const scale = niceScale(maxV * 1.22, 4);
    const missingCount = points.filter((p) => p.usage == null).length;
    return { scale, missingCount };
  }, [points, baseline]);

  if (points.length === 0) {
    return (
      <div className="flex h-[220px] items-center justify-center rounded-[4px] border border-dashed border-line bg-fill-1/40 text-[12px] text-muted">
        暂无同星期历史对照样本
      </div>
    );
  }

  const W = 780;
  const H = 276;
  const pad = { top: 28, right: 20, bottom: 34, left: 58 };
  const plotW = W - pad.left - pad.right;
  const plotH = H - pad.top - pad.bottom;
  const slotW = plotW / points.length;
  const barW = Math.min(64, Math.max(24, slotW * 0.42));
  const yAt = (v: number) => pad.top + plotH - (Math.max(0, v) / stats.scale.max) * plotH;
  const hovered = hoverIdx != null ? points[hoverIdx] : null;

  return (
    <div className="space-y-2 tabular">
      <div className="flex flex-wrap items-center justify-between gap-2 text-[12px] text-fg-2">
        <div className="flex flex-wrap items-center gap-4">
          <span className="inline-flex items-center gap-1.5">
            <span className="size-2.5 rounded-[2px] bg-primary" />
            <span>同星期历史完整日</span>
          </span>
          {highlightIndex != null && (
            <span className="inline-flex items-center gap-1.5 text-warning">
              <span className="size-2.5 rounded-[2px] bg-warning" />
              <span>{highlightLabel}</span>
            </span>
          )}
          {baseline != null && (
            <span className="inline-flex items-center gap-1.5">
              <span className="h-0 w-4 border-t-[1.5px] border-dashed border-warning" />
              <span>
                {baselineLabel} {fmtVal(baseline)} {unit}
              </span>
            </span>
          )}
        </div>
        <span className="text-[11px] text-muted">
          共 {points.length} 个观测点{stats.missingCount > 0 ? ` · 缺测 ${stats.missingCount}` : ' · 全部完整'} · 单位 {unit}
        </span>
      </div>

      <div className="relative select-none" onMouseLeave={() => setHoverIdx(null)}>
        <svg viewBox={`0 0 ${W} ${H}`} className="h-auto max-h-[300px] w-full overflow-visible" role="img" aria-label={`同星期离散样本柱状对照图（单位 ${unit}）`}>
          {stats.scale.steps.map((step) => {
            const y = yAt(step);
            return (
              <g key={step}>
                <line
                  x1={pad.left}
                  x2={W - pad.right}
                  y1={y}
                  y2={y}
                  stroke="currentColor"
                  className={step === 0 ? 'text-line-strong' : 'text-line'}
                  strokeDasharray={step === 0 ? undefined : '3 3'}
                />
                <text x={pad.left - 8} y={y + 3.5} textAnchor="end" className="fill-muted text-[11px]">
                  {fmtCompact(step)}
                </text>
              </g>
            );
          })}

          {/* Bars */}
          {points.map((pt, idx) => {
            const cx = pad.left + idx * slotW + slotW / 2;
            const bx = cx - barW / 2;
            const isHit = highlightIndex === idx;
            const isHovered = hoverIdx === idx;
            const val = pt.usage;
            const bh = val != null ? Math.max(4, pad.top + plotH - yAt(val)) : 0;
            const by = val != null ? yAt(val) : pad.top + plotH;
            const diffPct = val != null && baseline != null && baseline > 0 ? (val - baseline) / baseline : null;

            return (
              <g
                key={`${pt.from}-${idx}`}
                onMouseEnter={() => setHoverIdx(idx)}
                onClick={() => onBarClick?.(idx)}
                className={onBarClick ? 'cursor-pointer' : undefined}
              >
                {isHovered && (
                  <rect x={pad.left + idx * slotW} y={pad.top} width={slotW} height={plotH} fill="var(--fill-2)" fillOpacity={0.5} />
                )}
                {isHit && (
                  <rect
                    x={bx - 8}
                    y={pad.top}
                    width={barW + 16}
                    height={plotH}
                    rx={3}
                    fill="#FF7D00"
                    fillOpacity={0.09}
                    stroke="#FF7D00"
                    strokeOpacity={0.35}
                    strokeDasharray="3 2"
                  />
                )}
                {val != null ? (
                  <>
                    <rect
                      x={bx}
                      y={by}
                      width={barW}
                      height={bh}
                      rx={2}
                      fill={isHit ? '#FF7D00' : '#165DFF'}
                      fillOpacity={isHovered || isHit ? 0.94 : 0.78}
                    />
                    <text
                      x={cx}
                      y={Math.max(14, by - 7)}
                      textAnchor="middle"
                      className={`text-[11px] font-semibold ${isHit ? 'fill-warning' : 'fill-fg'}`}
                    >
                      {fmtCompact(val)}
                      {isHit && diffPct != null ? ` (${diffPct > 0 ? '+' : ''}${pctFmt.format(diffPct)})` : ''}
                    </text>
                  </>
                ) : (
                  <text x={cx} y={pad.top + plotH - 10} textAnchor="middle" className="fill-warning text-[11px]">
                    缺测
                  </text>
                )}

                <text
                  x={cx}
                  y={H - 12}
                  textAnchor="middle"
                  className={`text-[11px] ${isHit ? 'fill-warning font-semibold' : 'fill-muted'}`}
                >
                  {pt.from.slice(5, 10)}
                  {isHit ? ' (观测日)' : ''}
                </text>
              </g>
            );
          })}

          {/* Baseline horizontal rule drawn cleanly ON TOP of bars */}
          {baseline != null && baseline <= stats.scale.max && (
            <g>
              <line
                x1={pad.left}
                x2={W - pad.right}
                y1={yAt(baseline)}
                y2={yAt(baseline)}
                stroke="#FF7D00"
                strokeWidth={1.75}
                strokeDasharray="5 4"
              />
            </g>
          )}
        </svg>

        {hoverIdx != null && hovered && (
          <div className="mt-1 flex flex-wrap items-center justify-between gap-2 rounded-[2px] bg-fill-1 px-3 py-1.5 text-[12px]">
            <span className="text-fg-2">
              <strong className="font-medium text-fg">{hovered.from.slice(0, 10)}</strong>
              {hovered.expected ? ` · 覆盖 ${hovered.valid ?? 0}/${hovered.expected} 槽` : ''}
            </span>
            <span className="font-semibold text-fg">
              {hovered.usage != null ? `${fmtVal(hovered.usage)} ${unit}` : '缺测'}
              {hovered.usage != null && baseline != null && baseline > 0 && (
                <span className={`ml-2 text-[11px] ${hovered.usage > baseline ? 'text-warning' : 'text-positive'}`}>
                  较基线 {hovered.usage > baseline ? '+' : ''}
                  {pctFmt.format((hovered.usage - baseline) / baseline)}
                </span>
              )}
            </span>
          </div>
        )}
      </div>
    </div>
  );
}

// ── 13. CumulativeTargetBurnupChart (F05 Annual Target Risk Trajectory) ─────
// Distinct from F14 monthly bars: renders 12-month cumulative execution curve
// against cumulative planned target trajectory & annual cap, stopping actuals
// strictly at completed comparable months and shading incomplete/low-cov months.

export type CumulativeBurnupMonth = {
  month: number;
  label: string;
  target: number | null;
  usage: number | null;
  coverageRate?: number | null;
  status: string;
};

export function CumulativeTargetBurnupChart({
  months,
  annualTarget,
  unit,
  height = 286,
  wide = true,
}: {
  months: CumulativeBurnupMonth[];
  annualTarget: number | null;
  unit: string;
  height?: number;
  wide?: boolean;
}) {
  const uid = useId().replace(/:/g, '');
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);

  const computed = useMemo(() => {
    let cumTarget = 0;
    let cumActual = 0;
    let lastCompletedIdx = -1;
    let completedCount = 0;

    const rows = months.map((m, idx) => {
      const formalOk = m.status === 'comparable' && m.usage != null && (m.coverageRate ?? 1) >= 0.9;
      if (m.target != null) cumTarget += m.target;
      if (formalOk && m.usage != null) {
        cumActual += m.usage;
        lastCompletedIdx = idx;
        completedCount += 1;
      }
      return {
        ...m,
        formalOk,
        cumTarget: m.target != null ? cumTarget : null,
        cumActual: formalOk ? cumActual : null,
      };
    });

    const avgMonthlyActual = completedCount > 0 ? cumActual / completedCount : null;
    const projectedEnd =
      avgMonthlyActual != null && lastCompletedIdx >= 0 && lastCompletedIdx < months.length - 1
        ? cumActual + avgMonthlyActual * (months.length - 1 - lastCompletedIdx)
        : null;

    const maxVal = Math.max(
      annualTarget ?? 0,
      cumTarget,
      cumActual,
      projectedEnd ?? 0,
      1,
    );
    const scale = niceScale(maxVal * 1.12, 4);

    return { rows, lastCompletedIdx, completedCount, cumActual, avgMonthlyActual, projectedEnd, scale };
  }, [months, annualTarget]);

  if (months.length === 0) {
    return (
      <div className="flex h-[240px] items-center justify-center rounded-[4px] border border-dashed border-line bg-fill-1/40 text-[12px] text-muted">
        当前年度尚未配置目标分期数据
      </div>
    );
  }

  const W = wide ? 1040 : 760;
  const H = height;
  const pad = { top: 26, right: 24, bottom: 32, left: 66 };
  const plotW = W - pad.left - pad.right;
  const plotH = H - pad.top - pad.bottom;
  const n = computed.rows.length;
  const xAt = (i: number) => (n <= 1 ? pad.left + plotW / 2 : pad.left + (i / (n - 1)) * plotW);
  const yAt = (v: number) => pad.top + plotH - (Math.max(0, v) / computed.scale.max) * plotH;

  // Build cumulative planned target polyline
  const planPts = computed.rows
    .map((r, i) => (r.cumTarget != null ? { x: xAt(i), y: yAt(r.cumTarget), idx: i } : null))
    .filter((p): p is { x: number; y: number; idx: number } => p != null);
  const planPath = planPts.map((p, k) => `${k === 0 ? 'M' : 'L'} ${p.x.toFixed(1)} ${p.y.toFixed(1)}`).join(' ');

  // Build actual cumulative polyline (strictly up to completed comparable months)
  const actualPts = computed.rows
    .map((r, i) => (r.cumActual != null ? { x: xAt(i), y: yAt(r.cumActual), idx: i, val: r.cumActual } : null))
    .filter((p): p is { x: number; y: number; idx: number; val: number } => p != null);
  const actualPath = actualPts.map((p, k) => `${k === 0 ? 'M' : 'L'} ${p.x.toFixed(1)} ${p.y.toFixed(1)}`).join(' ');
  const actualArea =
    actualPts.length > 1
      ? `${actualPath} L ${actualPts[actualPts.length - 1].x.toFixed(1)} ${(pad.top + plotH).toFixed(1)} L ${actualPts[0].x.toFixed(1)} ${(pad.top + plotH).toFixed(1)} Z`
      : '';

  const projectionOver =
    computed.projectedEnd != null && annualTarget != null && computed.projectedEnd > annualTarget;

  const firstIncompleteIdx = computed.rows.findIndex((r, i) => !r.formalOk && i > computed.lastCompletedIdx);
  const hovered = hoverIdx != null ? computed.rows[hoverIdx] : null;
  const slotStep = n > 1 ? plotW / (n - 1) : plotW;

  return (
    <div className="space-y-2 tabular">
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 text-[11px] text-muted">
        <div className="flex flex-wrap items-center gap-3.5">
          <span className="inline-flex items-center gap-1.5 text-fg-2">
            <span className="h-[2.5px] w-4 rounded bg-primary" />
            已完结累计实测（{unit}）
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="w-4 border-t-2 border-dashed border-[#86909C]" />
            累计计划轨迹
          </span>
          {annualTarget != null && (
            <span className="inline-flex items-center gap-1.5">
              <span className="w-4 border-t-2 border-dashed border-[#FF7D00]" />
              年度总配额上限
            </span>
          )}
          {computed.projectedEnd != null && (
            <span className="inline-flex items-center gap-1.5">
              <span
                className="w-4 border-t-2 border-dotted"
                style={{ borderColor: projectionOver ? '#F53F3F' : '#00B42A' }}
              />
              按月均速率参考推演（非正式）
            </span>
          )}
        </div>
        {firstIncompleteIdx >= 0 && (
          <span className="inline-flex items-center gap-1.5 text-[11px] text-muted">
            <span className="inline-block size-2.5 rounded-[2px] border border-line bg-fill-2" />
            未完结/低覆盖月不出完成率
          </span>
        )}
      </div>

      <div className="relative w-full" onMouseLeave={() => setHoverIdx(null)}>
        <svg viewBox={`0 0 ${W} ${H}`} className="w-full overflow-visible" role="img" aria-label={`年度累计目标执行轨迹图，单位 ${unit}`}>
          <defs>
            <linearGradient id={`burn-${uid}`} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="#165DFF" stopOpacity="0.16" />
              <stop offset="100%" stopColor="#165DFF" stopOpacity="0.01" />
            </linearGradient>
            <pattern id={`burn-hatch-${uid}`} width="6" height="6" patternUnits="userSpaceOnUse" patternTransform="rotate(45)">
              <line x1="0" y1="0" x2="0" y2="6" stroke="#C9CDD4" strokeWidth="1.5" strokeOpacity="0.55" />
            </pattern>
          </defs>

          {/* Horizontal Y gridlines */}
          {computed.scale.steps.map((v) => {
            const y = yAt(v);
            return (
              <g key={v}>
                <line
                  x1={pad.left}
                  x2={W - pad.right}
                  y1={y}
                  y2={y}
                  stroke="var(--line)"
                  strokeDasharray={v === 0 ? undefined : '3 3'}
                />
                <text x={pad.left - 8} y={y + 4} textAnchor="end" className="fill-muted text-[11px]">
                  {fmtCompact(v)}
                </text>
              </g>
            );
          })}

          {/* Incomplete / low-coverage month hatched region */}
          {computed.rows.map((r, i) => {
            if (r.formalOk) return null;
            const cx = xAt(i);
            const left = Math.max(pad.left, cx - slotStep * 0.45);
            const right = Math.min(W - pad.right, cx + slotStep * 0.45);
            return (
              <rect
                key={`inc-${r.month}`}
                x={left}
                y={pad.top}
                width={Math.max(4, right - left)}
                height={plotH}
                fill={`url(#burn-hatch-${uid})`}
              />
            );
          })}

          {/* Annual cap horizontal dashed rule */}
          {annualTarget != null && annualTarget <= computed.scale.max && (
            <g>
              <line
                x1={pad.left}
                x2={W - pad.right}
                y1={yAt(annualTarget)}
                y2={yAt(annualTarget)}
                stroke="#FF7D00"
                strokeWidth={1.5}
                strokeDasharray="5 4"
              />
              <text
                x={W - pad.right - 4}
                y={Math.max(pad.top + 11, yAt(annualTarget) - 6)}
                textAnchor="end"
                className="fill-warning text-[10px] font-medium"
              >
                年度总目标 {fmtVal(annualTarget)} {unit}
              </text>
            </g>
          )}

          {/* Planned cumulative trajectory */}
          {planPts.length > 1 && (
            <path
              d={planPath}
              fill="none"
              stroke="#86909C"
              strokeWidth={1.6}
              strokeDasharray="5 4"
              strokeLinejoin="round"
            />
          )}

          {/* Reference pace projection from last completed month to month 12 */}
          {computed.projectedEnd != null && computed.lastCompletedIdx >= 0 && actualPts.length > 0 && (
            <g>
              <line
                x1={xAt(computed.lastCompletedIdx)}
                y1={yAt(computed.cumActual)}
                x2={xAt(n - 1)}
                y2={yAt(Math.min(computed.scale.max, computed.projectedEnd))}
                stroke={projectionOver ? '#F53F3F' : '#00B42A'}
                strokeWidth={1.75}
                strokeDasharray="3 3"
              />
              <circle
                cx={xAt(n - 1)}
                cy={yAt(Math.min(computed.scale.max, computed.projectedEnd))}
                r={3.5}
                fill="var(--surface)"
                stroke={projectionOver ? '#F53F3F' : '#00B42A'}
                strokeWidth={1.8}
              />
              <text
                x={xAt(n - 1) - 6}
                y={Math.max(pad.top + 12, yAt(Math.min(computed.scale.max, computed.projectedEnd)) - 8)}
                textAnchor="end"
                className={`text-[10px] font-medium ${projectionOver ? 'fill-negative' : 'fill-positive'}`}
              >
                参考推演 {fmtVal(Math.round(computed.projectedEnd))}
              </text>
            </g>
          )}

          {/* Actual cumulative area & line */}
          {actualArea && <path d={actualArea} fill={`url(#burn-${uid})`} />}
          {actualPts.length > 1 && (
            <path
              d={actualPath}
              fill="none"
              stroke="#165DFF"
              strokeWidth={2.4}
              strokeLinejoin="round"
              strokeLinecap="round"
            />
          )}
          {actualPts.map((p) => (
            <circle
              key={`act-${p.idx}`}
              cx={p.x}
              cy={p.y}
              r={hoverIdx === p.idx ? 5 : 3.2}
              fill="var(--surface)"
              stroke="#165DFF"
              strokeWidth={2}
            />
          ))}

          {/* Vertical hover guide */}
          {hoverIdx != null && (
            <line
              x1={xAt(hoverIdx)}
              x2={xAt(hoverIdx)}
              y1={pad.top}
              y2={pad.top + plotH}
              stroke="#165DFF"
              strokeWidth={1}
              strokeDasharray="3 3"
              strokeOpacity={0.55}
            />
          )}

          {/* X labels */}
          {computed.rows.map((r, i) => (
            <text
              key={`xl-${r.month}`}
              x={xAt(i)}
              y={H - 10}
              textAnchor="middle"
              className={`text-[11px] ${r.formalOk ? 'fill-muted' : 'fill-muted opacity-65'}`}
            >
              {r.month}月
            </text>
          ))}

          {/* Hit columns */}
          {computed.rows.map((r, i) => {
            const cx = xAt(i);
            const left = i === 0 ? pad.left : cx - slotStep / 2;
            const width = i === 0 || i === n - 1 ? slotStep / 2 : slotStep;
            return (
              <rect
                key={`hit-${r.month}`}
                x={Math.max(pad.left, left)}
                y={pad.top}
                width={Math.max(8, width)}
                height={plotH}
                fill="transparent"
                onMouseEnter={() => setHoverIdx(i)}
                onFocus={() => setHoverIdx(i)}
              />
            );
          })}
        </svg>

        {hoverIdx != null && hovered && (
          <div
            className="pointer-events-none absolute top-2 z-20 min-w-[224px] rounded-[4px] border border-line bg-surface px-3 py-2 text-[12px] shadow-panel"
            style={{
              left: `${Math.min(72, Math.max(4, (xAt(hoverIdx) / W) * 100))}%`,
              transform: xAt(hoverIdx) / W > 0.58 ? 'translateX(-105%)' : 'translateX(12px)',
            }}
          >
            <div className="mb-1 flex items-center justify-between border-b border-line pb-1 text-[11px] font-medium text-fg">
              <span>{hovered.label}</span>
              <span className={hovered.formalOk ? 'text-positive' : 'text-warning'}>
                {hovered.formalOk ? '已完结可比' : '未完结/低覆盖'}
              </span>
            </div>
            <div className="space-y-0.5 text-[11px]">
              <div className="flex justify-between gap-4">
                <span className="text-muted">累计计划目标</span>
                <span className="font-medium text-fg">
                  {fmtVal(hovered.cumTarget)} {unit}
                </span>
              </div>
              <div className="flex justify-between gap-4">
                <span className="text-muted">累计实测用量</span>
                <span className="font-semibold text-primary">
                  {hovered.cumActual != null ? `${fmtVal(hovered.cumActual)} ${unit}` : '不出正式累计'}
                </span>
              </div>
              {hovered.cumActual != null && hovered.cumTarget != null && (
                <div className="flex justify-between gap-4">
                  <span className="text-muted">累计偏差</span>
                  <span className={hovered.cumActual > hovered.cumTarget ? 'font-medium text-negative' : 'font-medium text-positive'}>
                    {hovered.cumActual > hovered.cumTarget ? '+' : ''}
                    {fmtVal(hovered.cumActual - hovered.cumTarget)} {unit}
                  </span>
                </div>
              )}
              {hovered.cumActual != null && annualTarget != null && (
                <div className="flex justify-between gap-4">
                  <span className="text-muted">年度剩余额度</span>
                  <span className={annualTarget - hovered.cumActual < 0 ? 'font-medium text-negative' : 'text-fg-2'}>
                    {fmtVal(annualTarget - hovered.cumActual)} {unit}
                  </span>
                </div>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

// ── 14. RetrofitEconomicsChart (F19 3-Scenario Payback & Breakeven Visual) ──
// Plots 5-year cumulative simulated cost savings for Low / Base / High scenarios
// against the Capex investment threshold line so static payback crossover years
// are visually transparent. Strictly blocks computation if price/capex missing.

export function RetrofitEconomicsChart({
  capex,
  costLow,
  costBase,
  costHigh,
  paybackLow,
  paybackBase,
  paybackHigh,
  priceConfigured,
  height = 248,
}: {
  capex: number;
  costLow: number | null;
  costBase: number | null;
  costHigh: number | null;
  paybackLow: number | null;
  paybackBase: number | null;
  paybackHigh: number | null;
  priceConfigured: boolean;
  height?: number;
}) {
  const [hoverYear, setHoverYear] = useState<number | null>(null);

  if (!priceConfigured || capex <= 0 || (costLow == null && costBase == null && costHigh == null)) {
    return (
      <div className="flex h-[220px] flex-col items-center justify-center gap-1.5 rounded-[4px] border border-dashed border-warning/40 bg-warning-soft/30 px-4 text-center text-[12px]">
        <span className="font-medium text-warning">经济性回收期曲线暂不可计算</span>
        <span className="text-[11px] text-muted">
          {!priceConfigured
            ? '缺少有效能源参考单价（不以 0 代替）'
            : capex <= 0
              ? '缺少有效改造投资预算金额（不以 0 代替）'
              : '缺少有效情景节能量参数'}
        </span>
      </div>
    );
  }

  const maxPayback = Math.max(paybackLow ?? 0, paybackBase ?? 0, paybackHigh ?? 0, 5);
  const horizonYears = Math.min(10, Math.max(5, Math.ceil(maxPayback + 1)));
  const years = Array.from({ length: horizonYears + 1 }, (_, i) => i);

  const maxCumSavings = Math.max(
    capex * 1.15,
    (costHigh ?? 0) * horizonYears,
    (costBase ?? 0) * horizonYears,
    1,
  );
  const scale = niceScale(maxCumSavings, 4);

  const W = 720;
  const H = height;
  const pad = { top: 24, right: 24, bottom: 30, left: 64 };
  const plotW = W - pad.left - pad.right;
  const plotH = H - pad.top - pad.bottom;

  const xAt = (yr: number) => pad.left + (yr / horizonYears) * plotW;
  const yAt = (v: number) => pad.top + plotH - (Math.max(0, v) / scale.max) * plotH;

  const scenarios = [
    { key: 'high', label: '乐观情景', cost: costHigh, payback: paybackHigh, color: '#00B42A', dash: '4 3' },
    { key: 'base', label: '基准情景', cost: costBase, payback: paybackBase, color: '#165DFF', dash: undefined },
    { key: 'low', label: '保守情景', cost: costLow, payback: paybackLow, color: '#86909C', dash: '3 3' },
  ];

  const capexY = yAt(capex);

  return (
    <div className="space-y-2 tabular">
      <div className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-muted">
        <div className="flex flex-wrap items-center gap-3.5">
          <span className="inline-flex items-center gap-1.5 text-fg-2">
            <span className="h-[2.5px] w-4 rounded bg-primary" />基准累计节费
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="w-4 border-t-2 border-dashed border-positive" />乐观情景
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="w-4 border-t-2 border-dotted border-[#86909C]" />保守情景
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="w-4 border-t-2 border-dashed border-warning" />投资额回收线 (¥{fmtVal(capex)})
          </span>
        </div>
        <span>模拟测算 · 静态回收期 = 投资额 ÷ 年参考节费</span>
      </div>

      <div className="relative w-full" onMouseLeave={() => setHoverYear(null)}>
        <svg viewBox={`0 0 ${W} ${H}`} className="w-full overflow-visible" role="img" aria-label="三情景累计参考节费与投资回收期对照图">
          {/* Y gridlines */}
          {scale.steps.map((v) => {
            const y = yAt(v);
            return (
              <g key={v}>
                <line x1={pad.left} x2={W - pad.right} y1={y} y2={y} stroke="var(--line)" strokeDasharray={v === 0 ? undefined : '3 3'} />
                <text x={pad.left - 8} y={y + 4} textAnchor="end" className="fill-muted text-[10px]">
                  ¥{fmtCompact(v)}
                </text>
              </g>
            );
          })}

          {/* Capex horizontal breakeven line */}
          <line x1={pad.left} x2={W - pad.right} y1={capexY} y2={capexY} stroke="#FF7D00" strokeWidth={1.6} strokeDasharray="5 4" />
          <text x={pad.left + 6} y={Math.max(pad.top + 10, capexY - 6)} textAnchor="start" className="fill-warning text-[10px] font-medium">
            投资回收门槛 ¥{fmtVal(capex)}
          </text>

          {/* Scenario cumulative lines + breakeven crossover markers */}
          {scenarios.map((sc) => {
            if (sc.cost == null || sc.cost <= 0) return null;
            const d = years.map((yr, idx) => `${idx === 0 ? 'M' : 'L'} ${xAt(yr).toFixed(1)} ${yAt(sc.cost! * yr).toFixed(1)}`).join(' ');
            const crossX = sc.payback != null && sc.payback <= horizonYears ? xAt(sc.payback) : null;
            return (
              <g key={sc.key}>
                <path
                  d={d}
                  fill="none"
                  stroke={sc.color}
                  strokeWidth={sc.key === 'base' ? 2.4 : 1.7}
                  strokeDasharray={sc.dash}
                  strokeLinecap="round"
                />
                {crossX != null && (
                  <g>
                    <circle cx={crossX} cy={capexY} r={4.2} fill="var(--surface)" stroke={sc.color} strokeWidth={2.2} />
                    <text
                      x={crossX}
                      y={sc.key === 'high' ? capexY - 10 : sc.key === 'base' ? capexY + 16 : capexY - 10}
                      textAnchor="middle"
                      className="text-[10px] font-semibold"
                      fill={sc.color}
                    >
                      {sc.payback!.toFixed(2)}年
                    </text>
                  </g>
                )}
              </g>
            );
          })}

          {/* Hover vertical guide */}
          {hoverYear != null && (
            <line
              x1={xAt(hoverYear)}
              x2={xAt(hoverYear)}
              y1={pad.top}
              y2={pad.top + plotH}
              stroke="#165DFF"
              strokeWidth={1}
              strokeDasharray="3 3"
              strokeOpacity={0.5}
            />
          )}

          {/* X labels */}
          {years.map((yr) => (
            <text key={yr} x={xAt(yr)} y={H - 10} textAnchor="middle" className="fill-muted text-[11px]">
              第{yr}年
            </text>
          ))}

          {/* Hover zones */}
          {years.map((yr) => {
            const cx = xAt(yr);
            const step = plotW / horizonYears;
            return (
              <rect
                key={`hy-${yr}`}
                x={Math.max(pad.left, cx - step / 2)}
                y={pad.top}
                width={step}
                height={plotH}
                fill="transparent"
                onMouseEnter={() => setHoverYear(yr)}
              />
            );
          })}
        </svg>

        {hoverYear != null && (
          <div className="mt-1 flex flex-wrap items-center justify-between gap-2 rounded-[2px] bg-fill-1 px-3 py-1.5 text-[11px]">
            <span className="font-medium text-fg">运行第 {hoverYear} 年末累计模拟参考节费：</span>
            <div className="flex flex-wrap items-center gap-3">
              {costLow != null && <span className="text-fg-2">保守 ¥{fmtVal(Math.round(costLow * hoverYear))}</span>}
              {costBase != null && <span className="font-semibold text-primary">基准 ¥{fmtVal(Math.round(costBase * hoverYear))}</span>}
              {costHigh != null && <span className="font-medium text-positive">乐观 ¥{fmtVal(Math.round(costHigh * hoverYear))}</span>}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

// ── 15. DiagnosticQuadrantChart (F16 Volume × Intensity × Anomaly 2D Matrix) ─
// Visually separates High Volume (X), High Area Intensity (Y), and Anomaly Growth
// (amber outer ring) in one iconic diagnostic scatter plot — completely distinct
// from F07's horizontal ranking bars.

export type QuadrantPoint = {
  id: string;
  name: string;
  subLabel?: string;
  usage: number | null;
  intensity: number | null;
  isAnomalyCandidate?: boolean;
  anomalyRate?: number | null;
  comparable: boolean;
  reason?: string;
};

export function DiagnosticQuadrantChart({
  points,
  unit,
  intensityUnit,
  selectedId,
  onSelect,
  height = 310,
}: {
  points: QuadrantPoint[];
  unit: string;
  intensityUnit: string;
  selectedId?: string | null;
  onSelect?: (id: string) => void;
  height?: number;
}) {
  const [hoverId, setHoverId] = useState<string | null>(null);

  const plottable = useMemo(
    () =>
      points.filter(
        (p): p is QuadrantPoint & { usage: number; intensity: number } =>
          p.comparable && p.usage != null && p.intensity != null,
      ),
    [points],
  );
  const excluded = useMemo(
    () => points.filter((p) => !p.comparable || p.usage == null || p.intensity == null),
    [points],
  );

  if (plottable.length === 0) {
    return (
      <div className="flex h-[240px] flex-col items-center justify-center gap-1 rounded-[4px] border border-dashed border-line bg-fill-1/40 text-[12px] text-muted">
        <span>当前对象缺少有效面积或不可比，无法绘制「用量 × 面积强度」二维诊断象限</span>
      </div>
    );
  }

  const maxX = Math.max(...plottable.map((p) => p.usage), 1);
  const maxY = Math.max(...plottable.map((p) => p.intensity), 0.01);
  const xScale = niceScale(maxX * 1.18, 4);
  const yScale = niceScale(maxY * 1.22, 4);

  const avgX = plottable.reduce((s, p) => s + p.usage, 0) / plottable.length;
  const avgY = plottable.reduce((s, p) => s + p.intensity, 0) / plottable.length;

  const W = 780;
  const H = height;
  const pad = { top: 28, right: 28, bottom: 38, left: 64 };
  const plotW = W - pad.left - pad.right;
  const plotH = H - pad.top - pad.bottom;

  const xAt = (v: number) => pad.left + (Math.max(0, v) / xScale.max) * plotW;
  const yAt = (v: number) => pad.top + plotH - (Math.max(0, v) / yScale.max) * plotH;

  const midX = xAt(avgX);
  const midY = yAt(avgY);
  const activePt = plottable.find((p) => p.id === (hoverId ?? selectedId)) ?? null;

  return (
    <div className="space-y-2 tabular">
      <div className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-muted">
        <div className="flex flex-wrap items-center gap-3.5">
          <span className="inline-flex items-center gap-1.5 text-fg-2">
            <span className="size-2.5 rounded-full bg-primary" />常规高耗对象
          </span>
          <span className="inline-flex items-center gap-1.5 text-warning">
            <span className="size-2.5 rounded-full border-2 border-warning bg-warning/25" />叠加异常增长候选
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="w-3.5 border-t border-dashed border-[#86909C]" />均值象限线
          </span>
        </div>
        <span>点击气泡联动右侧趋势、分项与溯源明细</span>
      </div>

      <div className="relative w-full" onMouseLeave={() => setHoverId(null)}>
        <svg viewBox={`0 0 ${W} ${H}`} className="w-full overflow-visible" role="img" aria-label="高耗能对象用量与面积强度二维诊断象限图">
          {/* Subtle Top-Right Priority Zone Tint */}
          <rect
            x={midX}
            y={pad.top}
            width={Math.max(0, W - pad.right - midX)}
            height={Math.max(0, midY - pad.top)}
            fill="#F53F3F"
            fillOpacity={0.045}
          />
          {/* Subtle Top-Left Efficiency Zone Tint */}
          <rect
            x={pad.left}
            y={pad.top}
            width={Math.max(0, midX - pad.left)}
            height={Math.max(0, midY - pad.top)}
            fill="#FF7D00"
            fillOpacity={0.035}
          />

          {/* Gridlines */}
          {yScale.steps.map((v) => {
            const y = yAt(v);
            return (
              <g key={`y-${v}`}>
                <line x1={pad.left} x2={W - pad.right} y1={y} y2={y} stroke="var(--line)" strokeDasharray={v === 0 ? undefined : '3 3'} />
                <text x={pad.left - 8} y={y + 4} textAnchor="end" className="fill-muted text-[10px]">
                  {v < 1 && v > 0 ? v.toFixed(2) : fmtCompact(v)}
                </text>
              </g>
            );
          })}
          {xScale.steps.map((v) => {
            const x = xAt(v);
            return (
              <g key={`x-${v}`}>
                <line x1={x} x2={x} y1={pad.top} y2={pad.top + plotH} stroke="var(--line)" strokeDasharray={v === 0 ? undefined : '3 3'} />
                <text x={x} y={H - 16} textAnchor="middle" className="fill-muted text-[10px]">
                  {fmtCompact(v)}
                </text>
              </g>
            );
          })}

          {/* Axis Labels */}
          <text x={pad.left - 8} y={14} textAnchor="end" className="fill-muted text-[10px]">
            {intensityUnit}
          </text>
          <text x={W - pad.right} y={H - 2} textAnchor="end" className="fill-muted text-[10px]">
            用能总量 ({unit})
          </text>

          {/* Mean Quadrant Dividers */}
          <line x1={midX} x2={midX} y1={pad.top} y2={pad.top + plotH} stroke="#86909C" strokeWidth={1.2} strokeDasharray="4 4" />
          <line x1={pad.left} x2={W - pad.right} y1={midY} y2={midY} stroke="#86909C" strokeWidth={1.2} strokeDasharray="4 4" />

          {/* Quadrant Corner Labels */}
          <text x={W - pad.right - 6} y={pad.top + 14} textAnchor="end" className="fill-negative text-[10px] font-medium opacity-85">
            高用量 · 高强度（优先治理）
          </text>
          <text x={pad.left + 6} y={pad.top + 14} textAnchor="start" className="fill-warning text-[10px] font-medium opacity-85">
            低用量 · 高强度（能效挖潜）
          </text>
          <text x={W - pad.right - 6} y={pad.top + plotH - 6} textAnchor="end" className="fill-muted text-[10px]">
            高用量 · 低强度（体量驱动）
          </text>
          <text x={pad.left + 6} y={pad.top + plotH - 6} textAnchor="start" className="fill-muted text-[10px]">
            双低常态区
          </text>

          {/* Bubbles */}
          {plottable.map((pt) => {
            const cx = xAt(pt.usage);
            const cy = yAt(pt.intensity);
            const isSel = selectedId === pt.id;
            const isHov = hoverId === pt.id;
            const isPriority = pt.usage >= avgX && pt.intensity >= avgY;
            const fill = pt.isAnomalyCandidate
              ? '#FF7D00'
              : isPriority
                ? '#F53F3F'
                : isSel
                  ? '#165DFF'
                  : '#3491FA';
            const r = isSel || isHov ? 8.5 : isPriority ? 7 : 5.8;
            const shortName = pt.name.length > 10 ? `${pt.name.slice(0, 10)}…` : pt.name;
            const labelLeft = cx > pad.left + plotW * 0.72;
            return (
              <g
                key={pt.id}
                className="cursor-pointer"
                onMouseEnter={() => setHoverId(pt.id)}
                onClick={() => onSelect?.(pt.id)}
              >
                {pt.isAnomalyCandidate && (
                  <circle cx={cx} cy={cy} r={r + 5} fill="#FF7D00" fillOpacity={0.18} stroke="#FF7D00" strokeWidth={1.2} strokeDasharray="2 2" />
                )}
                {isSel && <circle cx={cx} cy={cy} r={r + 3.5} fill="none" stroke="#165DFF" strokeWidth={1.8} />}
                <circle cx={cx} cy={cy} r={r} fill={fill} fillOpacity={isSel || isHov ? 1 : 0.85} stroke="var(--surface)" strokeWidth={1.8} />
                {(isSel || isHov || isPriority || plottable.length <= 6) && (
                  <text
                    x={labelLeft ? cx - r - 6 : cx + r + 6}
                    y={cy + 3.5}
                    textAnchor={labelLeft ? 'end' : 'start'}
                    className={`text-[10px] ${isSel || isHov ? 'fill-fg font-semibold' : 'fill-fg-2'}`}
                  >
                    {shortName}
                  </text>
                )}
              </g>
            );
          })}
        </svg>

        {activePt && (
          <div className="mt-1 flex flex-wrap items-center justify-between gap-2 rounded-[2px] bg-fill-1 px-3 py-1.5 text-[11px]">
            <span className="font-medium text-fg">
              {activePt.name}
              {activePt.subLabel ? <span className="ml-1.5 font-normal text-muted">({activePt.subLabel})</span> : null}
            </span>
            <div className="flex flex-wrap items-center gap-3">
              <span>用量 <strong className="text-fg">{fmtVal(activePt.usage)} {unit}</strong></span>
              <span>面积强度 <strong className="text-primary">{fmtVal(activePt.intensity)} {intensityUnit}</strong></span>
              {activePt.isAnomalyCandidate && activePt.anomalyRate != null && (
                <span className="font-medium text-warning">异常增长 +{pctFmt.format(activePt.anomalyRate)}</span>
              )}
            </div>
          </div>
        )}
      </div>

      {excluded.length > 0 && (
        <p className="text-[11px] text-warning">
          另有 {excluded.length} 个对象因缺少有效面积或覆盖率不足未投点：{excluded.map((e) => e.name).join('、')}
        </p>
      )}
    </div>
  );
}

// ── 16. SpatialHierarchyMap (F03 Nested Floor/Zone Blueprint Canvas) ────────
// Visually embeds child spaces INSIDE their parent container block while
// separating direct parent metering vs child space metering (zero double-count).

export type SpatialMapNode = {
  id: string;
  name: string;
  usage: number | null;
  areaSqm?: number | null;
  coverageRate: number | null;
  comparable: boolean;
  meterCount: number;
  subtreeUsage: number | null;
  children: Array<{
    id: string;
    name: string;
    usage: number | null;
    areaSqm?: number | null;
    coverageRate: number | null;
    comparable: boolean;
    meterCount: number;
  }>;
};

export function SpatialHierarchyMap({
  nodes,
  buildingTotal,
  unit,
  selectedId,
  onSelect,
}: {
  nodes: SpatialMapNode[];
  buildingTotal: number | null;
  unit: string;
  selectedId?: string | null;
  onSelect: (id: string) => void;
}) {
  if (nodes.length === 0) {
    return (
      <div className="flex h-[180px] items-center justify-center rounded-[4px] border border-dashed border-line bg-fill-1/40 text-[12px] text-muted">
        当前建筑尚未配置空间归属拓扑
      </div>
    );
  }

  const totalRef = Math.max(
    buildingTotal ?? 0,
    nodes.reduce((s, n) => s + (n.subtreeUsage ?? n.usage ?? 0), 0),
    1,
  );

  return (
    <div className="space-y-3 tabular">
      <div className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-muted">
        <span>区域色块按用能占比分配视觉权重；子空间嵌套于所属父空间内部，直属表计严格隔离不重复累计</span>
        <span className="inline-flex items-center gap-3">
          <span className="inline-flex items-center gap-1"><span className="size-2 rounded-[1px] bg-primary" />高耗能主区</span>
          <span className="inline-flex items-center gap-1"><span className="size-2 rounded-[1px] bg-[#0FC6C2]" />嵌套子空间</span>
        </span>
      </div>

      {/* 2D Proportional Spatial Grid */}
      <div className="grid gap-3 md:grid-cols-12">
        {nodes.map((node, idx) => {
          const share = (node.subtreeUsage ?? node.usage ?? 0) / totalRef;
          // Allocate 12-col width proportionally (min 4 cols, max 8 cols when 2+ nodes)
          const colSpan =
            nodes.length === 1
              ? 'md:col-span-12'
              : share >= 0.55
                ? 'md:col-span-7'
                : share >= 0.3
                  ? 'md:col-span-5'
                  : 'md:col-span-4';
          const isSel = selectedId === node.id;
          const isHot = idx === 0 && share >= 0.4;
          const directShare = buildingTotal && node.usage != null ? node.usage / buildingTotal : null;

          return (
            <div
              key={node.id}
              className={`${colSpan} flex flex-col justify-between rounded-[4px] border p-4 transition-colors ${
                isSel
                  ? 'border-primary bg-primary-soft/30'
                  : isHot
                    ? 'border-primary/40 bg-primary-soft/15'
                    : 'border-line bg-surface'
              }`}
            >
              {/* Parent Direct Space Clickable Area */}
              <button
                type="button"
                data-space-node={node.id}
                onClick={() => onSelect(node.id)}
                className="w-full text-left"
              >
                <div className="flex flex-wrap items-start justify-between gap-2">
                  <div>
                    <div className="flex items-center gap-1.5">
                      <span className={`inline-block size-2 rounded-full ${isHot ? 'bg-primary' : 'bg-[#3491FA]'}`} />
                      <span className="text-[14px] font-semibold text-fg">{node.name}</span>
                    </div>
                    <p className="mt-0.5 text-[11px] text-muted">
                      {node.children.length > 0 ? '父级直属空间' : '独立空间'}
                      {node.areaSqm ? ` · ${fmtVal(node.areaSqm)} m²` : ''}
                      {` · ${node.meterCount} 块表 · 覆盖 ${node.coverageRate != null ? pctFmt.format(node.coverageRate) : '—'}`}
                    </p>
                  </div>
                  <span className={`rounded-[2px] px-1.5 py-0 text-[11px] font-medium ${isHot ? 'bg-primary text-white' : 'bg-fill-2 text-fg-2'}`}>
                    占建筑 {pctFmt.format(share)}
                  </span>
                </div>

                <div className="mt-3 flex flex-wrap items-baseline gap-2">
                  <span className="text-[24px] font-semibold leading-[30px] text-fg">
                    {fmtVal(node.usage)}
                    <span className="ml-1 text-[12px] font-normal text-muted">{unit}</span>
                  </span>
                  {node.children.length > 0 && directShare != null && (
                    <span className="text-[11px] text-muted">
                      直属占比 {pctFmt.format(directShare)} · 含子区参考 {fmtVal(node.subtreeUsage)} {unit}
                    </span>
                  )}
                </div>
              </button>

              {/* Physically Nested Child Space Tiles inside Parent Boundary */}
              {node.children.length > 0 && (
                <div className="mt-3.5 space-y-2 border-t border-dashed border-line pt-3">
                  <div className="flex items-center justify-between text-[10px] text-muted">
                    <span>下级子空间（独立表计，建筑总计不重复累加）</span>
                    <span>{node.children.length} 个子区</span>
                  </div>
                  <div className="grid gap-2">
                    {node.children.map((ch) => {
                      const chSel = selectedId === ch.id;
                      const chShare = buildingTotal && ch.usage != null ? ch.usage / buildingTotal : null;
                      return (
                        <button
                          key={ch.id}
                          type="button"
                          data-space-node={ch.id}
                          onClick={() => onSelect(ch.id)}
                          className={`rounded-[4px] border px-3 py-2.5 text-left transition-colors ${
                            chSel
                              ? 'border-primary bg-surface ring-1 ring-primary/40'
                              : 'border-[#0FC6C2]/45 bg-[#E8FFFB]/45 hover:bg-[#E8FFFB]/80'
                          }`}
                        >
                          <div className="flex items-center justify-between gap-2">
                            <span className="text-[12px] font-medium text-fg">└ {ch.name}</span>
                            <span className="text-[13px] font-semibold text-fg">
                              {fmtVal(ch.usage)} <span className="text-[10px] font-normal text-muted">{unit}</span>
                            </span>
                          </div>
                          <div className="mt-1 flex items-center justify-between text-[10px] text-muted">
                            <span>
                              子空间独立计量{ch.areaSqm ? ` · ${fmtVal(ch.areaSqm)} m²` : ''} · {ch.meterCount} 块表
                            </span>
                            <span>{chShare != null ? `占建筑 ${pctFmt.format(chShare)}` : '—'}</span>
                          </div>
                        </button>
                      );
                    })}
                  </div>
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

// ── 17. GroupedComparisonBarChart (F07 Object Side-by-Side & F08 Period Side-by-Side) ──

export type GroupedBarDatum = {
  id: string;
  label: string;
  shortLabel?: string;
  current: number | null;
  baseline: number | null;
  comparable?: boolean;
  reason?: string;
};

export function GroupedComparisonBarChart({
  items,
  unit,
  currentLabel = '本期实测',
  baselineLabel = '对照上期',
  averageValue,
  selectedId,
  onSelect,
  height = 264,
  wide = false,
}: {
  items: GroupedBarDatum[];
  unit: string;
  currentLabel?: string;
  baselineLabel?: string;
  averageValue?: number | null;
  selectedId?: string | null;
  onSelect?: (id: string) => void;
  height?: number;
  wide?: boolean;
}) {
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);

  if (items.length === 0) {
    return (
      <div className="flex h-[220px] items-center justify-center rounded-[4px] border border-dashed border-line bg-fill-1/40 text-[12px] text-muted">
        暂无可分组对比的同口径数据
      </div>
    );
  }

  const maxRaw = Math.max(
    ...items.flatMap((it) => [it.current ?? 0, it.baseline ?? 0]),
    averageValue ?? 0,
    1,
  );
  const scale = niceScale(maxRaw * 1.16, 4);

  const W = wide ? 920 : 760;
  const H = height;
  const pad = { top: 24, right: 20, bottom: 44, left: 60 };
  const plotW = W - pad.left - pad.right;
  const plotH = H - pad.top - pad.bottom;
  const yAt = (v: number) => pad.top + plotH - (Math.max(0, v) / scale.max) * plotH;

  const slotW = plotW / items.length;
  const hasBaseline = items.some((it) => it.baseline != null);
  const barGap = hasBaseline ? Math.min(4, slotW * 0.06) : 0;
  const barW = hasBaseline
    ? Math.min(28, Math.max(5, (slotW * 0.68 - barGap) / 2))
    : Math.min(40, Math.max(8, slotW * 0.52));

  const labelStep = items.length > 16 ? Math.ceil(items.length / 10) : items.length > 10 ? 2 : 1;
  const activeIdx = hoverIdx ?? (selectedId ? items.findIndex((x) => x.id === selectedId) : -1);
  const activeItem = activeIdx >= 0 ? items[activeIdx] : null;
  const activeDelta =
    activeItem && activeItem.current != null && activeItem.baseline != null
      ? activeItem.current - activeItem.baseline
      : null;
  const activeRate =
    activeDelta != null && activeItem?.baseline && activeItem.baseline > 0
      ? activeDelta / activeItem.baseline
      : null;

  return (
    <div className="space-y-2 tabular">
      <div className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-muted">
        <div className="flex flex-wrap items-center gap-4">
          <span className="inline-flex items-center gap-1.5 text-fg-2">
            <span className="size-2.5 rounded-[2px] bg-primary" />
            {currentLabel} ({unit})
          </span>
          {hasBaseline && (
            <span className="inline-flex items-center gap-1.5 text-fg-2">
              <span className="size-2.5 rounded-[2px] bg-[#0FC6C2]" />
              {baselineLabel} ({unit})
            </span>
          )}
          {averageValue != null && (
            <span className="inline-flex items-center gap-1.5">
              <span className="w-3.5 border-t-2 border-dashed border-warning" />
              同口径均值 {fmtCompact(averageValue)}
            </span>
          )}
        </div>
        <span>同一能源、同一单位分组柱状对照（缺测留空不补 0）</span>
      </div>

      <div className="relative select-none" onMouseLeave={() => setHoverIdx(null)}>
        <svg viewBox={`0 0 ${W} ${H}`} className="h-auto max-h-[300px] w-full overflow-visible" role="img" aria-label={`分组对比柱状图（单位 ${unit}）`}>
          {scale.steps.map((v) => {
            const y = yAt(v);
            return (
              <g key={v}>
                <line x1={pad.left} x2={W - pad.right} y1={y} y2={y} stroke="var(--line)" strokeDasharray={v === 0 ? undefined : '3 3'} />
                <text x={pad.left - 8} y={y + 3.5} textAnchor="end" className="fill-muted text-[10px]">
                  {fmtCompact(v)}
                </text>
              </g>
            );
          })}

          {averageValue != null && (
            <line
              x1={pad.left}
              x2={W - pad.right}
              y1={yAt(averageValue)}
              y2={yAt(averageValue)}
              stroke="#FF7D00"
              strokeWidth={1.3}
              strokeDasharray="4 4"
            />
          )}

          {items.map((it, i) => {
            const cx = pad.left + i * slotW + slotW / 2;
            const isSel = selectedId === it.id;
            const isHov = hoverIdx === i;
            const groupW = hasBaseline ? barW * 2 + barGap : barW;
            const xCur = cx - groupW / 2;
            const xBase = xCur + barW + barGap;
            const rawLbl = it.shortLabel || it.label;
            const lbl = rawLbl.length > 8 && items.length > 5 ? `${rawLbl.slice(0, 7)}…` : rawLbl;

            return (
              <g
                key={it.id}
                className={onSelect ? 'cursor-pointer' : undefined}
                onMouseEnter={() => setHoverIdx(i)}
                onClick={() => onSelect?.(it.id)}
              >
                {(isSel || isHov) && (
                  <rect
                    x={pad.left + i * slotW + 2}
                    y={pad.top}
                    width={Math.max(4, slotW - 4)}
                    height={plotH}
                    rx={2}
                    fill={isSel ? '#165DFF' : 'var(--fill-2)'}
                    fillOpacity={isSel ? 0.08 : 0.65}
                  />
                )}

                {it.current != null ? (
                  <rect
                    x={xCur}
                    y={yAt(it.current)}
                    width={barW}
                    height={Math.max(2, pad.top + plotH - yAt(it.current))}
                    rx={2}
                    fill={it.comparable === false ? '#FF7D00' : '#165DFF'}
                    fillOpacity={isSel || isHov ? 1 : 0.88}
                  />
                ) : (
                  <rect
                    x={xCur}
                    y={pad.top + plotH - 12}
                    width={barW}
                    height={12}
                    rx={1}
                    fill="none"
                    stroke="#86909C"
                    strokeDasharray="2 2"
                  />
                )}

                {hasBaseline &&
                  (it.baseline != null ? (
                    <rect
                      x={xBase}
                      y={yAt(it.baseline)}
                      width={barW}
                      height={Math.max(2, pad.top + plotH - yAt(it.baseline))}
                      rx={2}
                      fill="#0FC6C2"
                      fillOpacity={isSel || isHov ? 0.95 : 0.72}
                    />
                  ) : (
                    <rect
                      x={xBase}
                      y={pad.top + plotH - 12}
                      width={barW}
                      height={12}
                      rx={1}
                      fill="none"
                      stroke="#C9CDD4"
                      strokeDasharray="2 2"
                    />
                  ))}

                {items.length <= 8 && it.current != null && (
                  <text
                    x={xCur + barW / 2}
                    y={yAt(it.current) - 5}
                    textAnchor="middle"
                    className={`text-[10px] ${isSel || isHov ? 'fill-fg font-semibold' : 'fill-fg-2'}`}
                  >
                    {fmtCompact(it.current)}
                  </text>
                )}

                {i % labelStep === 0 && (
                  <text
                    x={cx}
                    y={H - 16}
                    textAnchor="middle"
                    className={`text-[10px] ${isSel ? 'fill-primary font-semibold' : 'fill-muted'}`}
                  >
                    {lbl}
                  </text>
                )}
              </g>
            );
          })}
        </svg>

        <div className="mt-1 flex min-h-[26px] flex-wrap items-center justify-between gap-2 rounded-[2px] bg-fill-1 px-3 py-1 text-[11px]">
          {activeItem ? (
            <>
              <span className="font-medium text-fg">
                {activeItem.label}
                {activeItem.reason && activeItem.comparable === false ? (
                  <span className="ml-1.5 font-normal text-warning">({activeItem.reason})</span>
                ) : null}
              </span>
              <div className="flex flex-wrap items-center gap-3">
                <span>
                  {currentLabel} <strong className="text-primary">{activeItem.current != null ? `${fmtVal(activeItem.current)} ${unit}` : '缺测'}</strong>
                </span>
                {hasBaseline && (
                  <span>
                    {baselineLabel} <strong className="text-fg-2">{activeItem.baseline != null ? `${fmtVal(activeItem.baseline)} ${unit}` : '缺测'}</strong>
                  </span>
                )}
                {activeDelta != null && (
                  <span className={activeDelta > 0 ? 'font-medium text-negative' : 'font-medium text-positive'}>
                    差额 {activeDelta > 0 ? '+' : ''}{fmtVal(activeDelta)} {unit}
                    {activeRate != null ? ` (${activeDelta > 0 ? '+' : ''}${pctFmt.format(activeRate)})` : ''}
                  </span>
                )}
              </div>
            </>
          ) : (
            <span className="text-muted">悬停或点击任意分组柱体查看精确对比数值与变化幅度</span>
          )}
        </div>
      </div>
    </div>
  );
}

// ── 18. AnomalyDistributionBarChart (F10 Exception Type & Terminal Frequency) ──

export function AnomalyDistributionBarChart({
  items,
  height = 176,
}: {
  items: Array<{ key: string; label: string; count: number; kind: 'usage_candidate' | 'metering_quality' }>;
  height?: number;
}) {
  if (items.length === 0) {
    return (
      <div className="flex h-[130px] items-center justify-center rounded-[4px] border border-dashed border-line bg-fill-1/40 text-[12px] text-muted">
        当前观测窗口无异常事件分布记录
      </div>
    );
  }

  const maxCount = Math.max(...items.map((x) => x.count), 1);
  const scale = niceScale(maxCount * 1.2, 3);
  const W = 540;
  const H = height;
  const pad = { top: 22, right: 16, bottom: 34, left: 36 };
  const plotW = W - pad.left - pad.right;
  const plotH = H - pad.top - pad.bottom;
  const slotW = plotW / items.length;
  const barW = Math.min(44, Math.max(14, slotW * 0.48));
  const yAt = (v: number) => pad.top + plotH - (Math.max(0, v) / scale.max) * plotH;

  return (
    <div className="space-y-1.5 tabular">
      <div className="flex items-center justify-between text-[11px] text-muted">
        <span>异常事件分类频次分布（单位：次）</span>
        <div className="flex items-center gap-3">
          <span className="inline-flex items-center gap-1"><span className="size-2 rounded-[1px] bg-warning" />用能异常候选</span>
          <span className="inline-flex items-center gap-1"><span className="size-2 rounded-[1px] bg-negative" />采集质量异常</span>
        </div>
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} className="h-auto max-h-[190px] w-full overflow-visible" role="img" aria-label="异常事件分布柱状图">
        {scale.steps.map((v) => {
          const y = yAt(v);
          return (
            <g key={v}>
              <line x1={pad.left} x2={W - pad.right} y1={y} y2={y} stroke="var(--line)" strokeDasharray={v === 0 ? undefined : '3 3'} />
              <text x={pad.left - 6} y={y + 3.5} textAnchor="end" className="fill-muted text-[10px]">
                {Math.round(v)}
              </text>
            </g>
          );
        })}
        {items.map((it, i) => {
          const cx = pad.left + i * slotW + slotW / 2;
          const bx = cx - barW / 2;
          const by = yAt(it.count);
          const bh = Math.max(3, pad.top + plotH - by);
          const fill = it.kind === 'usage_candidate' ? '#FF7D00' : '#F53F3F';
          const shortLbl = it.label.length > 14 ? `${it.label.slice(0, 13)}…` : it.label;
          return (
            <g key={it.key}>
              <rect x={bx} y={by} width={barW} height={bh} rx={2} fill={fill} fillOpacity={0.86} />
              <text x={cx} y={by - 5} textAnchor="middle" className="fill-fg text-[11px] font-semibold">
                {it.count}
              </text>
              <text x={cx} y={H - 12} textAnchor="middle" className="fill-fg-2 text-[10px]">
                {shortLbl}
              </text>
            </g>
          );
        })}
      </svg>
    </div>
  );
}
