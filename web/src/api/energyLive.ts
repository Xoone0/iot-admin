export const LIVE_API_PREFIX = '/admin/v1/energy-analysis';
export const LIVE_TOKEN_KEY = 'iot-energy-live-bearer';

export type EnergyType = 'electricity' | 'water' | 'gas' | 'heat' | 'cooling' | 'steam';
export type MeasurementKind = 'cumulative' | 'interval' | 'instantaneous';
export type SourceTable = 'snapshot_coarse' | 'raw_item';
export type ChannelStatus = 'configured' | 'disabled' | 'unconfigured' | 'excluded_non_energy';

export type Coverage = {
  expected: number;
  valid: number;
  missing: number;
  suspect: number;
  rate: number | null;
};

export type SourceTrace = {
  terminalId: string;
  terminalName: string;
  itemCode: string;
  sourceTable: SourceTable;
  sourceSystem: string;
  measurementKind: MeasurementKind;
  usage: number | null;
  unit: string;
  multiplier: number;
  cadenceSeconds: number;
  registerEpoch?: string;
  registerModulus?: number;
  valid: number;
  expected: number;
  missing: number;
  suspect: number;
  profileVersion: string;
  objectBindingVersion: string;
  categoryBindingVersion?: string;
  mappingGeneration: number;
};

export type CategoryRow = {
  id: string;
  name: string;
  usage: number | null;
  share: number | null;
  coverage: Coverage;
  children?: CategoryRow[];
};

export type BucketPoint = {
  from: string;
  to: string;
  usage: number | null;
  valid: number;
  expected: number;
};

export type ExceptionRow = {
  terminalId: string;
  errorType: string;
  count: number;
};

export type BuildingProfile = {
  buildingId: string;
  revision: string;
  name: string;
  buildingType: string;
  region?: string;
  grossAreaSqm?: number;
  occupiedHeadcount?: number;
  timezone: string;
  effectiveFrom: string;
  effectiveTo?: string;
  legacyBuildingId?: number;
  sourceSystem: string;
};

export type SpaceProfile = {
  spaceId: string;
  revision: string;
  buildingId: string;
  parentSpaceId?: string;
  name: string;
  areaSqm?: number;
  effectiveFrom: string;
  effectiveTo?: string;
  sourceSystem: string;
};

export type OrganizationProfile = {
  orgId: string;
  revision: string;
  parentOrgId?: string;
  name: string;
  headcount?: number;
  effectiveFrom: string;
  effectiveTo?: string;
  sourceSystem: string;
};

export type MeterCandidate = {
  terminalId: string;
  terminalName: string;
  gatewayId: string;
  domainCode: string;
  domainName: string;
  typeCode: string;
  typeName: string;
  itemCode: string;
  itemName: string;
  unit: string;
  channelStatus: ChannelStatus;
  exclusionReason?: string;
  suggestedEnergy?: EnergyType;
  suggestedKind?: MeasurementKind;
  configured: boolean;
  pointEnabled: boolean;
  statusReason?: string;
  generation?: number;
  profileVersion?: string;
  sourceTable?: SourceTable;
  energy?: EnergyType;
  measurementKind?: MeasurementKind;
  multiplier?: number;
  registerModulus?: number;
  registerEpoch?: string;
  cadenceSeconds?: number;
  effectiveFrom?: string;
  effectiveTo?: string;
  enabled: boolean;
  online: boolean;
  buildingName?: string;
  areaName?: string;
  boundBuildingId?: string;
  boundSpaceId?: string;
  boundOrgId?: string;
  hourRows: number;
  rawRows: number;
  maxHourStart?: string;
  maxRawAt?: string;
};

export type CategoryNodeRecord = {
  categoryId: string;
  parentId?: string;
  name: string;
  energy: EnergyType;
};

export type CategoryAllocation = {
  categoryId: string;
  weightPpm: number;
};

export type PointCategoryBinding = {
  terminalId: string;
  itemCode: string;
  revision: string;
  effectiveFrom: string;
  effectiveTo?: string;
  allocations: CategoryAllocation[];
};

export type TopologyEdge = {
  topologyVersion: string;
  effectiveFrom: string;
  effectiveTo?: string;
  buildingId: string;
  energy: EnergyType;
  parentTerminalId: string;
  parentItemCode: string;
  childTerminalId: string;
  childItemCode: string;
  zoneCode: string;
};

export type RegisterEventRecord = {
  id: number;
  terminalId: string;
  itemCode: string;
  eventKind: string;
  effectiveAt: string;
  newEpoch?: string;
  reason: string;
  actor: string;
};

export type TargetConfigRecord = {
  objectKind: string;
  objectId: string;
  energy: EnergyType;
  year: number;
  revision: string;
  unit: string;
  annualMilli: number;
  method: 'linear' | 'weighted';
};

export type AnomalyRuleConfigRecord = {
  objectKind: string;
  objectId: string;
  energy: EnergyType;
  revision: string;
  minimumHistory: number;
  minimumAbsoluteIncrease: number;
  minimumRelativeIncrease: number;
};

export type DenominatorConfigRecord = {
  objectKind: string;
  objectId: string;
  denominatorKind: string;
  revision: string;
  periodFrom: string;
  periodTo: string;
  value: number;
  unit: string;
  basis: string;
};

export type BenchmarkConfigRecord = {
  benchmarkId: string;
  revision: string;
  name: string;
  publisher: string;
  region: string;
  metricId: string;
  templateVersion: string;
  unit: string;
  kind: 'demo' | 'external';
  buildingType: string;
  periodGranularity: string;
  maximumValue: number;
};

export type DatasetMeta = {
  datasetId: string;
  datasetName: string;
  datasetVersion: string;
  seed: number;
  isSimulated: boolean;
  generatedAt: string;
  dataFrom: string;
  dataTo: string;
  hourlyFrom: string;
  rawFrom: string;
  buildingScope: string;
  energyScope: string;
};

export type InventoryData = {
  dataMode: string;
  dataOriginNotice: string;
  dataset?: DatasetMeta;
  timezone: string;
  energyDomainReady: boolean;
  configuredMeterCount: number;
  unconfiguredCount: number;
  excludedChannelCount: number;
  disabledMeterCount: number;
  buildings: BuildingProfile[];
  buildingHistory: BuildingProfile[];
  spaces: SpaceProfile[];
  spaceHistory: SpaceProfile[];
  organizations: OrganizationProfile[];
  organizationHistory: OrganizationProfile[];
  meters: MeterCandidate[];
  categories: CategoryNodeRecord[];
  categoryBindings: PointCategoryBinding[];
  topology: TopologyEdge[];
  registerEvents: RegisterEventRecord[];
  targets: TargetConfigRecord[];
  anomalyRules: AnomalyRuleConfigRecord[];
  denominators: DenominatorConfigRecord[];
  benchmarks: BenchmarkConfigRecord[];
  exceptions: ExceptionRow[];
};

export type MeteringPointHistory = {
  terminalId: string;
  itemCode: string;
  currentGeneration: number;
  enabled: boolean;
  statusReason?: string;
  generations: Array<{
    generation: number;
    previousGeneration?: number;
    recordedAt: string;
    publishedBy: string;
    reason: string;
    profiles: Array<{
      revision: string;
      effectiveFrom: string;
      effectiveTo?: string;
      sourceTable: SourceTable;
      energy: EnergyType;
      measurementKind: MeasurementKind;
      unit: string;
      multiplier: number;
      registerModulus?: number;
      registerEpoch?: string;
      cadenceSeconds: number;
    }>;
    bindings: Array<{
      viewKind: string;
      revision: string;
      effectiveFrom: string;
      effectiveTo?: string;
      allocations: Array<{ targetId: string; targetRevision: string; weightPpm: number }>;
    }>;
  }>;
  categoryBindings: PointCategoryBinding[];
  registerEvents: RegisterEventRecord[];
};

export type ReportColumn = { key: string; label: string };
export type LiveReportData = {
  reportCode: string;
  reportTitle: string;
  dataMode: string;
  dataOriginNotice: string;
  buildingId: string;
  buildingName: string;
  energy: string;
  timezone: string;
  from: string;
  to: string;
  asOf: string;
  generatedAt: string;
  allowedFields?: ReportColumn[];
  selectedFields?: string[];
  columns: ReportColumn[];
  rows: Array<Record<string, string>>;
  disclaimer?: string;
};

export async function liveGet<T>(
  path: string,
  token: string,
  signal?: AbortSignal,
): Promise<{ data?: T; status: number; error?: string }> {
  const res = await fetch(`${LIVE_API_PREFIX}${path}`, {
    signal,
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  });
  let body: { code?: number; message?: string; data?: T } | null = null;
  try {
    body = await res.json();
  } catch {
    body = null;
  }
  if (!res.ok || !body || body.code !== 0) {
    return { status: res.status, error: body?.message ?? `HTTP ${res.status}` };
  }
  return { status: res.status, data: body.data };
}

export async function livePost<T>(
  path: string,
  token: string,
  payload: unknown,
): Promise<{ data?: T; status: number; error?: string }> {
  const res = await fetch(`${LIVE_API_PREFIX}${path}`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    body: JSON.stringify(payload),
  });
  let body: { code?: number; message?: string; data?: T } | null = null;
  try {
    body = await res.json();
  } catch {
    body = null;
  }
  if (!res.ok || !body || body.code !== 0) {
    return { status: res.status, error: body?.message ?? `HTTP ${res.status}` };
  }
  return { status: res.status, data: body.data };
}

export async function downloadLiveCsv(path: string, token: string, filename: string): Promise<string | null> {
  const sep = path.includes('?') ? '&' : '?';
  const res = await fetch(`${LIVE_API_PREFIX}${path}${sep}format=csv`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  });
  if (!res.ok) {
    try {
      const body = (await res.json()) as { message?: string };
      return body.message ?? `CSV 下载失败（HTTP ${res.status}）`;
    } catch {
      return `CSV 下载失败（HTTP ${res.status}）`;
    }
  }
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
  return null;
}
