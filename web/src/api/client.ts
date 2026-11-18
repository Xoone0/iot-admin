import type {
  CursorMeta,
  Envelope,
  ListMeta,
  MetaPayload,
  OptionItem,
  RowData,
} from './types';

/** SPA 与 Go 进程同源：dev 期由 vite proxy 转发 /admin。 */
const BASE = '/admin/v1';

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly detail: string;

  constructor(status: number, message: string) {
    // message 形如 "invalid_query: 未知排序字段 ..."，前半段当 code，整段当 detail。
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.code = message.replace(/:.*$/, '') || `http_${status}`;
    this.detail = message;
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<Envelope<T>> {
  const response = await fetch(`${BASE}${path}`, {
    ...init,
    headers: {
      Accept: 'application/json',
      ...(init.body ? { 'Content-Type': 'application/json' } : {}),
      ...init.headers,
    },
  });
  let payload: Envelope<T> | null = null;
  try {
    payload = (await response.json()) as Envelope<T>;
  } catch {
    payload = null;
  }
  if (!response.ok || (payload && payload.code !== 0)) {
    throw new ApiError(response.status, payload?.message ?? `HTTP ${response.status}`);
  }
  return payload ?? { code: 0, message: 'ok' };
}

export async function fetchMeta(): Promise<MetaPayload> {
  const payload = await request<MetaPayload>('/_meta');
  return payload.data as MetaPayload;
}

export type ListKind = 'entity' | 'readonly' | 'exception';

export function listPath(kind: ListKind, name: string): string {
  switch (kind) {
    case 'readonly':
      return `/readonly/${name}`;
    case 'exception':
      return `/exceptions/${name}`;
    default:
      return `/${name}`;
  }
}

export interface ListResult<T = RowData> {
  rows: T[];
  meta: ListMeta;
  cursor?: CursorMeta;
}

/** 列表查询：分页/筛选/排序全部交给服务端（白名单在 Go 侧）。 */
export async function fetchList<T = RowData>(
  kind: ListKind,
  name: string,
  params: URLSearchParams | Record<string, string | number>,
  signal?: AbortSignal,
): Promise<ListResult<T>> {
  const searchParams =
    params instanceof URLSearchParams
      ? params
      : new URLSearchParams(
          Object.entries(params).map(([k, v]) => [k, String(v)]),
        );
  const suffix = searchParams.toString();
  const payload = await request<T[]>(`${listPath(kind, name)}${suffix ? `?${suffix}` : ''}`, {
    signal,
  });
  const meta = payload.meta;
  return {
    rows: Array.isArray(payload.data) ? payload.data : [],
    meta: {
      total: meta?.total ?? 0,
      page: meta?.page ?? 1,
      pageSize: meta?.pageSize ?? 10,
      sort: meta?.sort ?? '',
      order: (meta?.order as 'asc' | 'desc') ?? 'desc',
    },
    cursor: meta && typeof meta.hasMore === 'boolean'
      ? {
          hasMore: meta.hasMore,
          nextCursor: meta.nextCursor ?? null,
          limit: meta.limit ?? 0,
        }
      : undefined,
  };
}

export interface SnapshotQueryColumn {
  key: string;
  label: string;
  unit?: string;
}

export interface SnapshotQueryCell {
  num?: number;
  text?: string;
  unit?: string;
  time?: string;
  quality?: number;
}

export interface SnapshotQueryRow {
  terminalId: string;
  terminalName: string;
  typeId: number;
  values: Record<string, SnapshotQueryCell>;
}

export interface SnapshotQueryPayload {
  template: { id: number; name: string; description: string };
  granularity: string;
  from: string;
  to: string;
  columns: SnapshotQueryColumn[];
  rows: SnapshotQueryRow[];
}

/** 快照查询：模板 + 档位 + 时间范围 → 终端 × 字段的透视结果。 */
export async function runSnapshotQuery(params: {
  template: number;
  granularity: 'hour' | 'day' | 'month' | 'year';
  from: string;
  to: string;
}): Promise<SnapshotQueryPayload> {
  const search = new URLSearchParams({
    template: String(params.template),
    granularity: params.granularity,
    from: params.from,
    to: params.to,
  });
  const payload = await request<SnapshotQueryPayload>(`/snapshot-query?${search.toString()}`);
  return payload.data as SnapshotQueryPayload;
}

export async function fetchOne(
  entity: string,
  id: string,
  signal?: AbortSignal,
): Promise<RowData> {
  const payload = await request<RowData>(`/${entity}/${encodeURIComponent(id)}`, { signal });
  return (payload.data ?? {}) as RowData;
}

export async function createRow(entity: string, body: RowData): Promise<RowData> {
  const payload = await request<RowData>(`/${entity}`, {
    method: 'POST',
    body: JSON.stringify(body),
  });
  return (payload.data ?? {}) as RowData;
}

export async function updateRow(entity: string, id: string, body: RowData): Promise<RowData> {
  const payload = await request<RowData>(`/${entity}/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: JSON.stringify(body),
  });
  return (payload.data ?? {}) as RowData;
}

export type BatchScope =
  | { mode: 'ids'; ids: string[] }
  | { mode: 'filter'; q: string; filters: { field: string; op: string; values: string[] }[] };

export interface BatchResult {
  action: 'enable' | 'disable' | 'delete';
  requested: number;
  succeeded: number;
  failed: { id: string; reason: string }[];
  credentials?: { id: string; password: string }[];
}

/** 业务组批量操作：按显式 ID 或当前筛选条件执行，返回逐项结果。 */
export async function batchAction(
  entity: string,
  action: BatchResult['action'],
  scope: BatchScope,
): Promise<BatchResult> {
  const payload = await request<BatchResult>(`/${entity}/batch`, {
    method: 'POST',
    body: JSON.stringify({ action, scope }),
  });
  return (payload.data ?? { action, requested: 0, succeeded: 0, failed: [] }) as BatchResult;
}

export interface RawItemRow {
  id: number;
  message_id: number;
  received_at: string;
  item_code: string;
  value_num: number | null;
  value_text: string | null;
  unit: string | null;
  value_time: string | null;
  quality: number;
  quality_reason: string | null;
  status: 'written' | 'dropped' | 'ignored';
  reason: string | null;
}

/** 异常现场回放：某终端在时间窗内的接入流水。 */
export async function fetchRawItems(terminalID: string, from: string, to: string): Promise<RawItemRow[]> {
  const search = new URLSearchParams({ terminal_id: terminalID, from, to });
  const payload = await request<RawItemRow[]>(`/raw-items?${search.toString()}`);
  return (payload.data ?? []) as RawItemRow[];
}

export interface GatewayCredential {
  gateway_id: string;
  mqtt_topic: string;
  mqtt_username: string;
  mqtt_password?: string;
  password_saved: boolean;
}

/** 读取网关 MQTT 凭据（密码需服务端密钥解密）。 */
export async function fetchGatewayCredential(id: string): Promise<GatewayCredential> {
  const payload = await request<GatewayCredential>(`/gateways/${encodeURIComponent(id)}/credential`);
  return payload.data as GatewayCredential;
}

/** 重置网关 MQTT 密码：Broker 侧重建账号，旧密码立即失效。 */
export async function resetGatewayCredential(id: string): Promise<GatewayCredential> {
  const payload = await request<GatewayCredential>(`/gateways/${encodeURIComponent(id)}/credential/reset`, {
    method: 'POST',
  });
  return payload.data as GatewayCredential;
}

export async function deleteRow(entity: string, id: string): Promise<void> {
  await request<null>(`/${entity}/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

/** 关联选项：ids 精确回填，q 模糊搜索，二者互斥（服务端约束）。 */
export async function fetchOptions(
  entity: string,
  params: { q?: string; ids?: string[]; limit?: number } = {},
  signal?: AbortSignal,
): Promise<OptionItem[]> {
  const search = new URLSearchParams();
  if (params.ids?.length) search.set('ids', params.ids.join(','));
  else {
    if (params.q) search.set('q', params.q);
    if (params.limit) search.set('limit', String(params.limit));
  }
  const suffix = search.toString();
  const payload = await request<OptionItem[]>(
    `/options/${entity}${suffix ? `?${suffix}` : ''}`,
    { signal },
  );
  return Array.isArray(payload.data) ? payload.data : [];
}

// ── 分析模块 API ─────────────────────────────────────────────────────────────

export type AnalyticsSource = 'raw' | 'hour' | 'day' | 'month' | 'year';
export type AnalyticsMode = 'delta' | 'reading';
export type AnalyticsCompare = 'none' | 'mom' | 'yoy';

export interface AnalyticsPropertyMeta {
  typeId: number;
  typeName: string;
  typeCode: string;
  domainName?: string;
  itemCode: string;
  itemName: string;
  valueType: string;
  unit?: string;
  precision?: number;
  minValue?: number;
  maxValue?: number;
}

export interface AnalyticsPoint {
  time: string;
  num?: number;
  text?: string;
  quality: number;
  qualityReason?: string;
  delta?: number;
  rawDelta?: number;
  rollback?: boolean;
  spikeAnomaly?: boolean;
  touPeriod?: 'sharp' | 'peak' | 'flat' | 'valley';
}

export interface AnalyticsTouBreakdown {
  sharpUsage: number;
  peakUsage: number;
  flatUsage: number;
  valleyUsage: number;
}

export interface AnalyticsSeriesStats {
  count: number;
  numericCount: number;
  badQualityCount: number;
  first?: number;
  last?: number;
  min?: number;
  max?: number;
  avg?: number;
  sum?: number;
  stdDev?: number;
  delta?: number;
  changeRate?: number;
  usageSum?: number;
  usageAvg?: number;
  usageMax?: number;
  usageMaxTime?: string;
  spikeCount: number;
  rollbackCount: number;
  offHoursUsage?: number;
  prevUsageSum?: number;
  compareRate?: number;
  touBreakdown?: AnalyticsTouBreakdown;
}

export interface AnalyticsTerminalSeries {
  terminalId: string;
  terminalName: string;
  gatewayId: string;
  online: boolean;
  enabled: boolean;
  stats: AnalyticsSeriesStats;
  points: AnalyticsPoint[];
}

export interface AnalyticsFleetStats {
  terminalCount: number;
  totalPoints: number;
  badQualityCount: number;
  min?: number;
  max?: number;
  avg?: number;
  totalUsage?: number;
  prevTotalUsage?: number;
  compareRate?: number;
  spikeCount: number;
  rollbackCount: number;
  touBreakdown?: AnalyticsTouBreakdown;
}

export interface AnalyticsAggBucket {
  time: string;
  usage: number;
  readingAvg?: number;
  readingSum?: number;
  prevUsage?: number;
  changeRate?: number;
  touPeriod: 'sharp' | 'peak' | 'flat' | 'valley';
  spikeAnomaly?: boolean;
}

export interface AnalyticsSeriesPayload {
  property: AnalyticsPropertyMeta;
  source: AnalyticsSource;
  mode: AnalyticsMode;
  compare?: AnalyticsCompare;
  from: string;
  to: string;
  prevFrom?: string;
  prevTo?: string;
  fleetStats: AnalyticsFleetStats;
  aggregatedBuckets?: AnalyticsAggBucket[];
  series: AnalyticsTerminalSeries[];
}

export async function fetchAnalyticsSeries(
  params: {
    typeId: number;
    itemCode: string;
    source: AnalyticsSource;
    mode?: AnalyticsMode;
    compare?: AnalyticsCompare;
    from: string;
    to: string;
    terminalIds?: string[];
  },
  signal?: AbortSignal,
): Promise<AnalyticsSeriesPayload> {
  const search = new URLSearchParams({
    type_id: String(params.typeId),
    item_code: params.itemCode,
    source: params.source,
    from: params.from,
    to: params.to,
  });
  if (params.mode) search.set('mode', params.mode);
  if (params.compare && params.compare !== 'none') search.set('compare', params.compare);
  if (params.terminalIds && params.terminalIds.length > 0) {
    search.set('terminal_ids', params.terminalIds.join(','));
  }
  const payload = await request<AnalyticsSeriesPayload>(`/analytics/series?${search.toString()}`, {
    signal,
  });
  return payload.data as AnalyticsSeriesPayload;
}

// ── 能耗总览与桑基能流图 API ─────────────────────────────────────────────────

export interface AnalyticsOverviewBucket {
  time: string;
  currentVal: number;
  prevVal: number;
}

export interface AnalyticsDomainOverview {
  domainId: number;
  domainCode: string;
  domainName: string;
  unit: string;
  coalFactor: number;
  carbonFactor: number;
  currentUsage: number;
  prevUsage: number;
  changeRate?: number;
  standardCoalKgce: number;
  carbonKgCO2: number;
  sharePercent: number;
  terminalCount: number;
  onlineCount: number;
  buckets: AnalyticsOverviewBucket[];
}

export interface AnalyticsTypeEnergySummary {
  typeId: number;
  typeCode: string;
  typeName: string;
  domainName: string;
  primaryItemCode: string;
  primaryItemName: string;
  unit: string;
  terminalCount: number;
  onlineCount: number;
  currentUsage: number;
  prevUsage: number;
  changeRate?: number;
  standardCoalKgce: number;
  sharePercent: number;
}

export interface AnalyticsGatewayEnergySummary {
  gatewayId: string;
  gatewayName: string;
  online: boolean;
  terminalCount: number;
  onlineCount: number;
  currentUsage: number;
  standardCoalKgce: number;
  sharePercent: number;
}

export interface AnalyticsTerminalEnergyRank {
  terminalId: string;
  terminalName: string;
  gatewayId: string;
  typeId: number;
  typeName: string;
  domainName: string;
  itemCode: string;
  unit: string;
  online: boolean;
  firstReading?: number;
  lastReading?: number;
  usage: number;
  prevUsage: number;
  changeRate?: number;
  standardCoalKgce: number;
  sharePercent: number;
}

export interface AnalyticsSankeyNode {
  id: string;
  name: string;
  category: 'domain' | 'device_type' | 'gateway' | 'terminal';
  value: number;
  rawValue: number;
  unit: string;
}

export interface AnalyticsSankeyLink {
  source: string;
  target: string;
  value: number;
  rawValue: number;
  unit: string;
}

export interface AnalyticsOverviewAlarmSummary {
  spike50PctCount: number;
  rollbackCount: number;
  qualityIssueCount: number;
  offlineTerminalCount: number;
}

export interface AnalyticsOverviewPayload {
  from: string;
  to: string;
  prevFrom: string;
  prevTo: string;
  granularity: 'hour' | 'day' | 'month' | 'year';
  totalStandardCoalKgce: number;
  prevStandardCoalKgce: number;
  coalChangeRate?: number;
  totalCarbonKgCO2: number;
  prevCarbonKgCO2: number;
  carbonChangeRate?: number;
  totalTerminals: number;
  onlineTerminals: number;
  terminalOnlineRate: number;
  domains: AnalyticsDomainOverview[];
  byDeviceType: AnalyticsTypeEnergySummary[];
  byGateway: AnalyticsGatewayEnergySummary[];
  topTerminals: AnalyticsTerminalEnergyRank[];
  sankeyNodes: AnalyticsSankeyNode[];
  sankeyLinks: AnalyticsSankeyLink[];
  alarmSummary: AnalyticsOverviewAlarmSummary;
}

export async function fetchAnalyticsOverview(
  params: {
    from: string;
    to: string;
    granularity?: 'hour' | 'day' | 'month' | 'year';
  },
  signal?: AbortSignal,
): Promise<AnalyticsOverviewPayload> {
  const search = new URLSearchParams({ from: params.from, to: params.to });
  if (params.granularity) search.set('granularity', params.granularity);
  const payload = await request<AnalyticsOverviewPayload>(
    `/analytics/overview?${search.toString()}`,
    { signal },
  );
  return payload.data as AnalyticsOverviewPayload;
}

// ── 用能异常监测与跑冒滴漏平衡分析 API ───────────────────────────────────────

export interface AnalyticsEnergyAlarmSummary {
  overLimitCount: number;
  spike50PctCount: number;
  offHoursAnomalyCount: number;
  leakageAnomalyCount: number;
  rollbackCount: number;
  rangeOutCount: number;
}

export interface AnalyticsEnergyAlarmEvent {
  alarmType: 'over_limit' | 'spike_50pct' | 'off_hours' | 'leakage' | 'meter_rollback' | 'range_out';
  label: string;
  tone: 'danger' | 'warn' | 'info';
  terminalId: string;
  terminalName: string;
  gatewayId: string;
  typeName: string;
  itemCode: string;
  itemName: string;
  unit: string;
  time: string;
  actualValue: number;
  baselineValue: number;
  deviationPct: number;
  detail: string;
}

export interface AnalyticsBalanceBucket {
  time: string;
  masterUsage: number;
  subUsageSum: number;
  diffUsage: number;
  diffPercent: number;
  leakageAlarm: boolean;
}

export interface AnalyticsBalanceResult {
  masterTerminalId: string;
  masterTerminalName: string;
  subTerminalIds: string[];
  unit: string;
  thresholdPct: number;
  masterTotalUsage: number;
  subTotalUsage: number;
  totalDiffUsage: number;
  totalDiffPercent: number;
  alarmSlotCount: number;
  buckets: AnalyticsBalanceBucket[];
}

export interface AnalyticsAnomaliesPayload {
  from: string;
  to: string;
  granularity: 'hour' | 'day' | 'month';
  offHoursSpec: string;
  summary: AnalyticsEnergyAlarmSummary;
  events: AnalyticsEnergyAlarmEvent[];
  balanceAnalysis?: AnalyticsBalanceResult;
}

export async function fetchAnalyticsAnomalies(
  params: {
    from: string;
    to: string;
    granularity?: 'hour' | 'day' | 'month';
    typeId?: number;
    gatewayId?: string;
    itemCode?: string;
    dailyLimit?: number;
    offHoursLimit?: number;
    offHours?: string;
    masterTerminalId?: string;
    subTerminalIds?: string[];
    leakageThresholdPct?: number;
  },
  signal?: AbortSignal,
): Promise<AnalyticsAnomaliesPayload> {
  const search = new URLSearchParams({ from: params.from, to: params.to });
  if (params.granularity) search.set('granularity', params.granularity);
  if (params.typeId && params.typeId > 0) search.set('type_id', String(params.typeId));
  if (params.gatewayId) search.set('gateway_id', params.gatewayId);
  if (params.itemCode) search.set('item_code', params.itemCode);
  if (params.dailyLimit && params.dailyLimit > 0) search.set('daily_limit', String(params.dailyLimit));
  if (params.offHoursLimit && params.offHoursLimit > 0) {
    search.set('off_hours_limit', String(params.offHoursLimit));
  }
  if (params.offHours) search.set('off_hours', params.offHours);
  if (params.masterTerminalId) search.set('master_terminal_id', params.masterTerminalId);
  if (params.subTerminalIds && params.subTerminalIds.length > 0) {
    search.set('sub_terminal_ids', params.subTerminalIds.join(','));
  }
  if (params.leakageThresholdPct !== undefined) {
    search.set('leakage_threshold_pct', String(params.leakageThresholdPct));
  }
  const payload = await request<AnalyticsAnomaliesPayload>(
    `/analytics/anomalies?${search.toString()}`,
    { signal },
  );
  return payload.data as AnalyticsAnomaliesPayload;
}

export interface AnalyticsRawSummary {
  totalItems: number;
  writtenCount: number;
  cleanPassCount: number;
  qualityViolationCount: number;
  droppedCount: number;
  ignoredCount: number;
  rangeOutCount: number;
  ruleHitCount: number;
  unitOutCount: number;
  passRate?: number;
  effectiveWriteRate?: number;
}

export interface AnalyticsExceptionDist {
  errorType: string;
  label: string;
  tone: 'neutral' | 'positive' | 'negative' | 'warning';
  count: number;
  terminalCount: number;
  lastSeen?: string;
}

export interface AnalyticsQualityTrendBucket {
  bucket: string;
  totalExceptions: number;
  qualityViolationCount: number;
  cleanFailedCount: number;
  lateDroppedCount: number;
}

export interface AnalyticsTopTerminal {
  terminalId: string;
  terminalName: string;
  gatewayId: string;
  typeName: string;
  totalCount: number;
  qualityCount: number;
  cleanFailedCount: number;
  lateDroppedCount: number;
  primaryErrorType: string;
  primaryErrorLabel: string;
  sampleDigest?: string;
  lastSeen?: string;
}

export interface AnalyticsTopItem {
  itemCode: string;
  droppedCount: number;
  qualityViolationCount: number;
  totalIssues: number;
  sampleReason?: string;
}

export interface AnalyticsQualityPayload {
  from: string;
  to: string;
  bucketUnit: 'hour' | 'day';
  totalExceptionCount: number;
  affectedTerminals: number;
  rawSummary: AnalyticsRawSummary;
  exceptionsByType: AnalyticsExceptionDist[];
  trend: AnalyticsQualityTrendBucket[];
  topTerminals: AnalyticsTopTerminal[];
  topItems: AnalyticsTopItem[];
}

export async function fetchAnalyticsQuality(
  params: {
    from: string;
    to: string;
    gatewayId?: string;
    typeId?: number;
  },
  signal?: AbortSignal,
): Promise<AnalyticsQualityPayload> {
  const search = new URLSearchParams({ from: params.from, to: params.to });
  if (params.gatewayId) search.set('gateway_id', params.gatewayId);
  if (params.typeId && params.typeId > 0) search.set('type_id', String(params.typeId));
  const payload = await request<AnalyticsQualityPayload>(`/analytics/quality?${search.toString()}`, {
    signal,
  });
  return payload.data as AnalyticsQualityPayload;
}

export interface AnalyticsHealthOverview {
  totalGateways: number;
  enabledGateways: number;
  onlineGateways: number;
  gatewayOnlineRate: number;
  totalTerminals: number;
  enabledTerminals: number;
  onlineTerminals: number;
  terminalOnlineRate: number;
  activeTerminalsInWindow: number;
  silentOnlineTerminals: number;
  totalMessagesInWindow: number;
  totalBytesInWindow: number;
  totalItemsInWindow: number;
  lateDroppedInWindow: number;
}

export interface AnalyticsGatewayHealth {
  gatewayId: string;
  name: string;
  enabled: boolean;
  online: boolean;
  lastSeenAt?: string;
  terminalTotal: number;
  terminalEnabled: number;
  terminalOnline: number;
  messageCount: number;
  byteSize: number;
  exceptionCount: number;
  healthStatus: 'healthy' | 'warning' | 'offline' | 'disabled';
}

export interface AnalyticsDeviceTypeHealth {
  typeId: number;
  typeCode: string;
  typeName: string;
  domainName: string;
  terminalTotal: number;
  terminalEnabled: number;
  terminalOnline: number;
  onlineRate: number;
}

export interface AnalyticsTerminalHealth {
  terminalId: string;
  name: string;
  typeId: number;
  typeName: string;
  gatewayId: string;
  enabled: boolean;
  online: boolean;
  lastSeenAt?: string;
  silenceSeconds?: number;
  writtenCount: number;
  droppedCount: number;
  lateDroppedCount: number;
  exceptionCount: number;
  avgIntervalSeconds?: number;
  healthStatus: 'healthy' | 'silent' | 'warning' | 'offline' | 'disabled';
}

export interface AnalyticsHealthPayload {
  from: string;
  to: string;
  overview: AnalyticsHealthOverview;
  gateways: AnalyticsGatewayHealth[];
  byDeviceType: AnalyticsDeviceTypeHealth[];
  terminals: AnalyticsTerminalHealth[];
}

export async function fetchAnalyticsHealth(
  params: {
    from: string;
    to: string;
    gatewayId?: string;
  },
  signal?: AbortSignal,
): Promise<AnalyticsHealthPayload> {
  const search = new URLSearchParams({ from: params.from, to: params.to });
  if (params.gatewayId) search.set('gateway_id', params.gatewayId);
  const payload = await request<AnalyticsHealthPayload>(`/analytics/health?${search.toString()}`, {
    signal,
  });
  return payload.data as AnalyticsHealthPayload;
}
