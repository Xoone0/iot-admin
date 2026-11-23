import { lazy, Suspense } from 'react';
import { Navigate, Route, Routes, useLocation, useParams } from 'react-router-dom';
import { AppShell, ENERGY_LIVE, ENERGY_OVERVIEW_PATH, ENERGY_PREVIEW, ENERGY_STATISTICS_PATH, ENERGY_WORKSPACE_PATH, firstPath } from './components/AppShell';
import MetaGate from './components/MetaGate';
import { useMetaState } from './meta';
import type { ListKind } from './api/client';
import { ListPage } from './pages/ListPage';
import { SnapshotPlanPage } from './pages/SnapshotPlanPage';
import { DeviceTypeDetailPage } from './pages/DeviceTypeDetailPage';
import { TerminalDetailPage } from './pages/TerminalDetailPage';
import { SnapshotQueryListPage } from './pages/SnapshotQueryListPage';
import { SnapshotQueryEditPage } from './pages/SnapshotQueryEditPage';
import { SnapshotQueryResultPage } from './pages/SnapshotQueryResultPage';
import { AnalyticsOverviewPage } from './pages/AnalyticsOverviewPage';
import { AnalyticsTrendsPage } from './pages/AnalyticsTrendsPage';
import { AnalyticsQualityPage } from './pages/AnalyticsQualityPage';
import { AnalyticsHealthPage } from './pages/AnalyticsHealthPage';

const BuildingEnergyPreviewPage = ENERGY_PREVIEW
  ? lazy(() => import('./pages/BuildingEnergyPreviewPage'))
  : null;
const BuildingEnergyClassificationPage = ENERGY_PREVIEW
  ? lazy(() => import('./pages/BuildingEnergyClassificationPage'))
  : null;
const BuildingEnergyStatisticsLayout = ENERGY_PREVIEW
  ? lazy(() => import('./pages/BuildingEnergyStatisticsLayout'))
  : null;
const BuildingEnergyTrendPage = ENERGY_PREVIEW
  ? lazy(() => import('./pages/BuildingEnergyTrendPage'))
  : null;
const BuildingEnergyDimensionsPage = ENERGY_PREVIEW
  ? lazy(() => import('./pages/BuildingEnergyDimensionsPage'))
  : null;
const BuildingEnergyBalancePage = ENERGY_PREVIEW
  ? lazy(() => import('./pages/BuildingEnergyBalancePage'))
  : null;
const BuildingEnergyWorkspace = ENERGY_PREVIEW
  ? lazy(() => import('./pages/BuildingEnergyWorkspace'))
  : null;
const BuildingEnergyEfficiencyPage = ENERGY_PREVIEW
  ? lazy(() => import('./pages/BuildingEnergyEfficiencyPage'))
  : null;
const BuildingEnergyDiagnosisPage = ENERGY_PREVIEW
  ? lazy(() => import('./pages/BuildingEnergyDiagnosisPage'))
  : null;
const BuildingEnergyReportsPage = ENERGY_PREVIEW
  ? lazy(() => import('./pages/BuildingEnergyReportsPage'))
  : null;
const BuildingEnergyTargetsPage = ENERGY_PREVIEW
  ? lazy(() => import('./pages/BuildingEnergyTargetsPage'))
  : null;
const BuildingEnergyAnomaliesPage = ENERGY_PREVIEW
  ? lazy(() => import('./pages/BuildingEnergyAnomaliesPage'))
  : null;
const BuildingEnergyIndustryPage = ENERGY_PREVIEW
  ? lazy(() => import('./pages/BuildingEnergyIndustryPage'))
  : null;
const BuildingEnergyIndustryReportsPage = ENERGY_PREVIEW
  ? lazy(() => import('./pages/BuildingEnergyIndustryReportsPage'))
  : null;
const BuildingEnergyLivePage = ENERGY_LIVE
  ? lazy(() => import('./pages/BuildingEnergyLivePage'))
  : null;

/** 旧路径兼容：/e/device-types[/:id] → /e/thing-models[/:id]。 */
function LegacyDeviceTypes({ detail = false }: { detail?: boolean }) {
  const params = useParams();
  const target = detail && params.id ? `/e/thing-models/${params.id}` : '/e/thing-models';
  return <Navigate to={target} replace />;
}

function LegacyBuildingEnergyClassification() {
  const { search, hash } = useLocation();
  return <Navigate replace to={{ pathname: ENERGY_STATISTICS_PATH, search, hash }} />;
}

/** 路由参数绑到 ListPage 的 props（路由声明仍是 /e/:name、/r/:slot、/x/:slot）。 */
function ListRoute({ kind, param }: { kind: ListKind; param: 'name' | 'slot' }) {
  const params = useParams();
  return <ListPage kind={kind} name={params[param] ?? ''} />;
}

export default function App() {
  const { meta, loading, error, reload } = useMetaState();

  if (!meta) return <MetaGate loading={loading} error={error} onRetry={reload} />;

  const home = firstPath(meta, (ENERGY_PREVIEW || ENERGY_LIVE) ? 'building-energy' : 'technical');

  return (
    <Routes>
      {BuildingEnergyLivePage ? (
        <>
          <Route index element={<Navigate to={home} replace />} />
          <Route element={<AppShell navigation="building-energy" />}>
            <Route path={ENERGY_WORKSPACE_PATH}>
              <Route index element={<Navigate to={ENERGY_OVERVIEW_PATH} replace />} />
              <Route path="overview" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F01 综合能耗概览…</p>}><BuildingEnergyLivePage section="overview" /></Suspense>} />
              <Route path="overview/composition" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F02 能源分类构成…</p>}><BuildingEnergyLivePage section="composition" /></Suspense>} />
              <Route path="overview/objects" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F03 分区分项概览…</p>}><BuildingEnergyLivePage section="objects" /></Suspense>} />
              <Route path="overview/trend" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F04 实时与历史趋势…</p>}><BuildingEnergyLivePage section="realtime" /></Suspense>} />
              <Route path="overview/targets" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F05 年度目标摘要…</p>}><BuildingEnergyLivePage section="target-summary" /></Suspense>} />
              <Route path="statistics" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F06 分类分项统计…</p>}><BuildingEnergyLivePage section="classification" /></Suspense>} />
              <Route path="statistics/dimensions" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F07 多维统计…</p>}><BuildingEnergyLivePage section="dimensions" /></Suspense>} />
              <Route path="statistics/trend" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F08 趋势、同环比与排名…</p>}><BuildingEnergyLivePage section="trend" /></Suspense>} />
              <Route path="statistics/balance" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F09 能流与总分表平衡…</p>}><BuildingEnergyLivePage section="balance" /></Suspense>} />
              <Route path="statistics/anomalies" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F10 异常浪费识别…</p>}><BuildingEnergyLivePage section="anomalies" /></Suspense>} />
              <Route path="efficiency" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F11 通用能效强度…</p>}><BuildingEnergyLivePage section="efficiency" /></Suspense>} />
              <Route path="efficiency/templates" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F12 行业指标模板…</p>}><BuildingEnergyLivePage section="industry-templates" /></Suspense>} />
              <Route path="efficiency/benchmarks" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F13 定额标准对标…</p>}><BuildingEnergyLivePage section="industry-benchmarks" /></Suspense>} />
              <Route path="efficiency/targets" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F14 目标与分期分解…</p>}><BuildingEnergyLivePage section="target-periods" /></Suspense>} />
              <Route path="efficiency/assessment" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F15 组织与区域考核…</p>}><BuildingEnergyLivePage section="assessment" /></Suspense>} />
              <Route path="diagnosis" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F16 高耗能对象识别…</p>}><BuildingEnergyLivePage section="diagnosis" /></Suspense>} />
              <Route path="diagnosis/opportunities" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F17 节能机会分析…</p>}><BuildingEnergyLivePage section="opportunities" /></Suspense>} />
              <Route path="diagnosis/baseline" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F18 改造前基线…</p>}><BuildingEnergyLivePage section="retrofit-baseline" /></Suspense>} />
              <Route path="diagnosis/potential" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F19 潜力与收益测算…</p>}><BuildingEnergyLivePage section="retrofit-potential" /></Suspense>} />
              <Route path="diagnosis/verification" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F20 改造后节能核验…</p>}><BuildingEnergyLivePage section="retrofit-verification" /></Suspense>} />
              <Route path="reports" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F21 综合与分类分项报表…</p>}><BuildingEnergyLivePage section="report-consumption" /></Suspense>} />
              <Route path="reports/metrics" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F22 指标与定额报表…</p>}><BuildingEnergyLivePage section="report-metrics" /></Suspense>} />
              <Route path="reports/assessment" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F23 组织考核报表…</p>}><BuildingEnergyLivePage section="report-assessment" /></Suspense>} />
              <Route path="reports/retrofit" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F24 诊断与改造成效报告…</p>}><BuildingEnergyLivePage section="report-retrofit" /></Suspense>} />
              <Route path="reports/custom" element={<Suspense fallback={<p role="status" className="text-sm text-muted">加载 F25 行业专项与自定义报表…</p>}><BuildingEnergyLivePage section="report-custom" /></Suspense>} />
              <Route path="classification" element={<LegacyBuildingEnergyClassification />} />
              <Route path="*" element={<p className="p-6 text-sm text-muted">该功能处于未开放阶段（PRD P1/P2），严禁混入合成样本数据。</p>} />
            </Route>
          </Route>
        </>
      ) : null}
      {BuildingEnergyWorkspace && BuildingEnergyPreviewPage && BuildingEnergyStatisticsLayout &&
        BuildingEnergyClassificationPage && BuildingEnergyTrendPage && BuildingEnergyDimensionsPage && BuildingEnergyBalancePage &&
        BuildingEnergyEfficiencyPage && BuildingEnergyDiagnosisPage && BuildingEnergyReportsPage && BuildingEnergyTargetsPage &&
        BuildingEnergyAnomaliesPage && BuildingEnergyIndustryPage && BuildingEnergyIndustryReportsPage ? (
        <>
          <Route index element={<Navigate to={home} replace />} />
          <Route element={<AppShell navigation="building-energy" />}>
            <Route path={ENERGY_WORKSPACE_PATH} element={
              <Suspense fallback={<p role="status" className="text-sm text-muted">加载能耗工作台…</p>}>
                <BuildingEnergyWorkspace />
              </Suspense>
            }>
              <Route index element={<Navigate to={ENERGY_OVERVIEW_PATH} replace />} />
              <Route path="overview" element={<BuildingEnergyPreviewPage />} />
              <Route path="overview/composition" element={<BuildingEnergyPreviewPage section="composition" />} />
              <Route path="overview/objects" element={<BuildingEnergyPreviewPage section="objects" />} />
              <Route path="overview/trend" element={<BuildingEnergyPreviewPage section="trend" />} />
              <Route path="overview/targets" element={<BuildingEnergyTargetsPage />} />
              <Route path="statistics" element={<BuildingEnergyStatisticsLayout />}>
                <Route index element={<BuildingEnergyClassificationPage />} />
                <Route path="dimensions" element={<BuildingEnergyDimensionsPage />} />
                <Route path="trend" element={<BuildingEnergyTrendPage />} />
                <Route path="balance" element={<BuildingEnergyBalancePage />} />
                <Route path="anomalies" element={<BuildingEnergyAnomaliesPage />} />
              </Route>
              <Route path="efficiency" element={<BuildingEnergyEfficiencyPage />} />
              <Route path="efficiency/templates" element={<BuildingEnergyIndustryPage />} />
              <Route path="efficiency/benchmarks" element={<BuildingEnergyIndustryPage view="benchmarks" />} />
              <Route path="efficiency/targets" element={<BuildingEnergyTargetsPage view="periods" />} />
              <Route path="diagnosis" element={<BuildingEnergyDiagnosisPage />} />
              <Route path="diagnosis/opportunities" element={<BuildingEnergyAnomaliesPage view="opportunities" />} />
              <Route path="reports" element={<BuildingEnergyReportsPage />} />
              <Route path="reports/metrics" element={<BuildingEnergyIndustryReportsPage />} />
              <Route path="reports/custom" element={<BuildingEnergyIndustryReportsPage view="custom" />} />
              <Route path="classification" element={<LegacyBuildingEnergyClassification />} />
              <Route path="*" element={<p className="text-sm text-muted">页面不存在。</p>} />
            </Route>
          </Route>
        </>
      ) : null}
      <Route element={<AppShell />}>
        {!(ENERGY_PREVIEW || ENERGY_LIVE) ? <Route
          index
          element={
            home ? (
              <Navigate to={home} replace />
            ) : (
              <p className="text-sm text-muted">当前没有可访问的页面。</p>
            )
          }
        /> : null}
        {/* 快照计划是固定六档的配置页，不进通用列表（分组 + 开关 + 固定操作列）。 */}
        <Route path="e/snapshot-slots" element={<SnapshotPlanPage />} />
        {/* 快照查询：模板卡片 → 编辑 → 结果（取代旧的粗表只读页） */}
        <Route path="e/snapshot-queries" element={<SnapshotQueryListPage />} />
        <Route path="e/snapshot-queries/new" element={<SnapshotQueryEditPage />} />
        <Route path="e/snapshot-queries/:id/edit" element={<SnapshotQueryEditPage />} />
        <Route path="e/snapshot-queries/:id/query" element={<SnapshotQueryResultPage />} />
        {/* 分析模块：能耗总览、能耗统计与报表、用能告警与质量分析、设备与网关运行健康分析 */}
        <Route path="a/overview" element={<AnalyticsOverviewPage />} />
        <Route path="a/trends" element={<AnalyticsTrendsPage />} />
        <Route path="a/quality" element={<AnalyticsQualityPage />} />
        <Route path="a/health" element={<AnalyticsHealthPage />} />
        {/* 物模型详情：字段绑定 + 终端实例；旧 device-types 路径做重定向 */}
        <Route path="e/device-types" element={<LegacyDeviceTypes />} />
        <Route path="e/device-types/:id" element={<LegacyDeviceTypes detail />} />
        <Route path="e/thing-models/:id" element={<DeviceTypeDetailPage />} />
        {/* 终端详情：基础信息 + 类型字段 + 最新值 */}
        <Route path="e/terminals/:id" element={<TerminalDetailPage />} />
        <Route path="e/:name" element={<ListRoute kind="entity" param="name" />} />
        <Route path="r/:slot" element={<ListRoute kind="readonly" param="slot" />} />
        <Route path="x/:slot" element={<ListRoute kind="exception" param="slot" />} />
        <Route path="*" element={<p className="text-sm text-muted">页面不存在。</p>} />
      </Route>
    </Routes>
  );
}
