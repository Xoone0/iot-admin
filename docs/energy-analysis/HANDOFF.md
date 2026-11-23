# 建筑能耗监测分析交付

## 当前阶段

`feature/energy-analysis-prd-v1` 基于 `dev @ 2563cd367bcd333d85a650f1be441f3f9589dcaa`。本轮完成《建筑能耗监测分析｜统一五能源模拟数据工程》全部落地：将整个建筑能耗模块统一至**可复现、可版本化、由 Go 后端与本地 PostgreSQL 17.12（`127.0.0.1:55432 / iot_preview`）唯一驱动的五能源（电/水/气/热/冷）模拟数据集**，打通 **F01–F25 全部 25 项功能**（含本次按 PRD §6.3 / §7.2–§7.3 完整补齐的 **F15 / F18 / F19 / F20 / F23 / F24**），前端零随机造数且全页面、全 CSV 导出均显式标注 **「模拟数据」**（F04 叠加 **「模拟实时」**）与数据集溯源信息。不创建新数据库、不安装 Docker/WSL、不连接 FNOS 生产库，不修改官方根 `migrations/`、`internal/ui/dist/` 或原 `dev` 工作区。依据为《建筑能耗监测分析模块 PRD V1.0》（SHA-256：`5bd2758871770161a81b5e28f1107e15984b8f1577d80d4c3ab4dad8b9a6c643`）。 [仓库规范](../../AGENTS.md) · [视觉基线](../../FRONTEND_VISUAL_BASELINE.md) · [增量迁移 0003](../../internal/buildingenergy/migrations/0003_simulation_and_p2_features.sql) · [模拟数据规范](../../internal/buildingenergy/simgen/spec.go) · [模拟数据生成器](../../internal/buildingenergy/simgen/generator.go) · [生成器入口](../../cmd/energy-simgen/main.go) · [F15/F18–F20/F23–F24 后端](../../internal/buildingenergy/liveapi/p2_features.go) · [实时业务页面](../../web/src/pages/BuildingEnergyLivePage.tsx) · [配置中心](../../web/src/components/EnergyManagementDrawer.tsx)

## 本轮统一五能源模拟数据工程（F01–F25 全链路打通）

### 一、确定性五能源模拟数据生成器（`cmd/energy-simgen`）

- **固定种子与版本管理**：执行 `go run ./cmd/energy-simgen --seed 20261118 --version sim-v1.0.0 --reset` 可在本地开发库 `127.0.0.1:55432 / iot_preview` 幂等重建相同数据集（内置安全守卫，拒绝非本机或非 `iot_preview` 库写入）；数据集元信息写入 `energy_domain.dataset_metadata`。
- **三栋标准建筑 × 五大独立能源（电/水/气/热/冷）**：
  - `bldg-a` **A · 星云办公园区**（`office`，上海，2025 面积 `30,000 m²` → 2026 面积 `32,000 m²`，展示跨年口径变化检测）；
  - `bldg-b` **B · 汇景商业综合体**（`commercial`，上海，`58,000 m²`，含受控总分表差额与未分项样本）；
  - `bldg-c` **C · 启明教育园区**（`school`，上海，`45,000 m²`，含学生数业务分母与周期季节曲线）。
- **表计、非能耗通道与时间覆盖**：
  - 57 块正式配置计量点（每栋 19 块：电 5 / 水 4 / 气 4 / 热 3 / 冷 3，含 15 套总表↔直接分表拓扑、42 条拓扑边），以及 12 条电气瞬时量非能耗排除通道（`voltage_a`/`current_a`/`active_power`/`power_factor`）。
  - 写入 `snapshot_coarse` **192,644 行**（`2025-01-01 ~ 2026-11-18` 全量日/月/年汇总 + 近 90 天逐时 + 去年同期逐时）与 `raw_item` **16,128 行**（近 7 天 15 分钟原始流水）。
- **9 类受控可审计异常样本（`ANOM-01`..`ANOM-09`）**：覆盖整日缺测、部分时段缺测（触发覆盖率门禁）、异常激增（`bldg-a` 2026-11-17 触发 F10 `usage_candidate` 与 F17 诊断线索）、异常骤降、满量程回卷（`sim-b-e-light` 2026-11-12 正确解算 `4,926.563 kWh` 无负值）、换表复位（`sim-a-e-light` 2026-11-14 正确解算 `3,664.536 kWh` 无负值）、零用量（节假日/停运）、受控总分表不平衡（`bldg-b` 差额率 `8.09%` 标为未分配/待核查）与未分项用量（`bldg-b` `784.698 kWh`）。

### 二、F01–F25 全部 25 项功能开放与后端引擎统一

- **总分表不重计（PRD CALC-02）与跨页绝对对账**：[state.go](../../internal/buildingenergy/liveapi/state.go) 在汇总建筑/分类/多维/趋势用量时自动排除 `meter_topology` 中的父级总表，仅累加有效叶子分表；F09 总分表平衡单独加载父总表与直接分表进行对账。实库对账：`bldg-a` 用电 `2026-11-17` 下 **F01 = F06 = F07 = F08 = 3,624.550 kWh**，毫瓦时级完全一致。
- **日级快照多月/年度聚合**：[p2_features.go](../../internal/buildingenergy/liveapi/p2_features.go) 新增 `computeDailySnapshotBuildingUsage` 直接基于 `snapshot_coarse(granularity='day')` 计算全年/跨月用量，使 F05/F14 2025 全年目标（`954,655.503 kWh`，`comparable`）与 F18–F20 基线期/报告期实算全部直出 PostgreSQL 授权结果。
- **新增 6 项 P2 业务能力（F15 / F18 / F19 / F20 / F23 / F24）**：
  - **F15 组织与区域考核**（`GET /assessment`）：直接复用 F07 实时 Go 计算结果，支持权重/公区分摊/豁免/复核与申诉状态，低覆盖对象按 PRD §6.3 阻断正式确认与评分；
  - **F18 改造前基线**（`GET /retrofit/baselines`）、**F19 潜力与收益测算**（`GET /retrofit/potential`，显式标明「仅模型测算估算，不冒充已发生节能量」）、**F20 改造后节能核验**（`GET /retrofit/verification`，按 PRD §7.2–§7.3 区分 `verified_simulated`、`pending_verification`、`estimated_only`，缺基线或气象因子禁止标记已核验）；
  - **F23 组织考核报表**（`GET /reports/assessment`）与 **F24 诊断与改造成效报告**（`GET /reports/retrofit`）：与 F21/F22/F25 一并包含 `数据属性` 列（`模拟数据 (sim-v1.0.0 / seed=20261118)`），全部 5 份报表预览与 CSV 导出 100% 逐格一致。
- **前端统一标识与五能源支持**：[BuildingEnergyNavigation.tsx](../../web/src/components/BuildingEnergyNavigation.tsx) 与 [App.tsx](../../web/src/App.tsx) 在 `ENERGY_LIVE` 模式下开放全部五父级 25 子项；[BuildingEnergyLivePage.tsx](../../web/src/pages/BuildingEnergyLivePage.tsx) 顶部常驻 **「模拟数据」**（F04 叠加 **「模拟实时」**）徽章与数据集溯源信息条（名称、版本/Seed、生成时间、建筑范围、数据区间），支持电/水/气/热/冷五能源切换，并修复 F09 `BalanceReport` 字段解析使父总表/分表能流表完整呈现。

| 本轮五能源模拟数据工程验收 | 结果与证据 |
| --- | --- |
| Go 编译、静态检查与关键离线套件 | `go vet ./...`、`go build ./...` 退出码 0；`internal/buildingenergy/...` + `cmd/server` + `internal/admin` + `internal/ingest` + `cmd/energy-simgen` 离线跑法 **433 通过 / 0 失败 / 64 外部 DB 环境跳过**；`internal/buildingenergy` 保持纯标准库。 [Go 测试](../../evidence/go-test.jsonl) · [Vet](../../evidence/go-vet.txt) · [Go 构建](../../evidence/go-build.txt) |
| 本地 PostgreSQL 17.12 五能源端到端对账 | `TestUnifiedFiveEnergySimulationEndToEnd` **通过（3.66s）**：3 建筑 × 5 能源 × 57 计量点 + 12 排除通道、`F01=F06=F07=F08=3,624.550 kWh`、F09 平衡 `0.79%` 与受控不平衡 `8.09%`、F10 `usage_candidate`、F17 线索、回卷/换表无负值、2025 年度目标 `954,655.503 kWh`、F15/F18–F20 及 **F21/F22/F23/F24/F25 全部 5 份报表预览↔CSV 逐格一致**。 [五能源实库 E2E](../../evidence/go-live-sim-e2e.txt) |
| SQL 静态解析 | `pglast v8.8`（`libpg_query PG 18.6`）全部通过：**3 份增量迁移 DDL（`0001`/`0002`/`0003`，共 37 条语句）+ 27 条 Go SQL 常量（16 个文件）**。 [SQL 解析日志](../../evidence/sql-parse.txt) |
| 前端编译与构建隔离 | `tsc --noEmit` 退出码 0；标准 `vite build` 与 `vite.energy-live.config.mjs` 构建退出码 0；标准包 `0` 能源泄漏，实时包 `0` 旧合成预览泄漏；`internal/ui/dist` 与根 `migrations/` 零修改。 [前端构建](../../evidence/web-build.txt) |
| 全部 25 个页面（F01–F25）Chromium 实库冒烟 | **29/29 通过**：未鉴权 `401`、F01 五能源 + 「模拟数据」溯源条、F04 「模拟实时」、配置中心（57 启用 / 12 排除）、F09 8.1% 不平衡、F10 异常候选、F15/F18–F20、F21–F25 及 CSV 真实下载、**1920/1440/768/390px 零横向溢出**、零 `pageerror`。 [UI 冒烟记录](../../evidence/live-ui-smoke.txt) · [F01 1440px](../../evidence/screenshots/live-f02-1440-light.png) · [F15 1440px](../../evidence/screenshots/live-f15-1440-light.png) · [F20 1440px](../../evidence/screenshots/live-f20-1440-light.png) · [F01 390px](../../evidence/screenshots/live-f02-390-light.png) |

## 上轮第二阶段真实业务开发（P0–P5 与导航职责对齐）

### P0：真实计量点严格识别与口径门禁

- **禁止全量 `number` 属性入账与默认归电**：在 [iot_bridge.go](../../internal/buildingenergy/pgadapter/iot_bridge.go) 移除 `inferEnergyType` 默认回落 `Electricity` 的逻辑，改为显式 `ClassifyChannelCandidate`：
  - 明确排除电压（`V`/`kV`/`voltage`/`ua..uc`）、电流（`A`/`mA`/`current`/`ia..ic`）、有功/无功/视在功率（`W`/`kW`/`kVar`/`kVA`）、功率因数（`power_factor`/`pf`）、频率/谐波（`Hz`/`freq`）及温湿度/压力/瞬时流量等非能耗监测通道（标记 `excluded_non_energy` 并禁止发布为正式计量点）。
  - 未知数值通道保留为空能源与 `unconfigured`（待配置），绝不默认归类为用电。
  - 明确区分 `cumulative`（累计量）、`interval`（区间量）、`instantaneous`（瞬时量，禁止发布为消费能耗点）。
- **读取完整 `metering_point_profile` 与双表源分派**：[state.go](../../internal/buildingenergy/liveapi/state.go) 完整加载 `source_table`、`measurement_kind`、`cadence_seconds`、`register_epoch`、`register_modulus`、`unit`、`multiplier`、`effective` 与多视角绑定；按 `source_table` 分别调用 `SnapshotCoarseMeteringSource`（`snapshot_coarse`）与 `RawMeteringSource`（`raw_item`），并统一挂载 `StaticRegisterEventResolver`（消费 `meter_register_event` 换表与回卷记录）。
- **未配置禁止出数**：所有正式能耗计算接口仅纳入在 `energy_domain.metering_point_profile` 中已显式配置且启用的计量点；未配置时返回 `configurationState="unconfigured"` 与空值，不自动推断入账。

### P1：完整计量点管理（API + 可操作前端配置中心）

- 在 [0002_energy_governance_and_business.sql](../../internal/buildingenergy/migrations/0002_energy_governance_and_business.sql) 为 `metering_point_head` 增量扩展 `enabled`、`status_reason`、`updated_at`（支持启用/停用治理）。
- 在 [management.go](../../internal/buildingenergy/liveapi/management.go) 与 [router.go](../../internal/buildingenergy/liveapi/router.go) 提供：`POST /management/metering-points`（发布不可变新代次，支持保留生效日前历史切片、自动按目标对象历史版本切分 `target_revision` 绑定区间）、`POST /management/metering-points/status`（启用/停用切换）、`GET /management/metering-points/history`（查看全部代次/口径/绑定/分类/事件历史）、`POST /management/events`（登记 `meter_swap`/`rollover`/`quality_suspect`/`quality_corrected`）。
- 在 [EnergyManagementDrawer.tsx](../../web/src/components/EnergyManagementDrawer.tsx) 提供六标签页可操作「建筑能耗配置中心」（1. 计量点配置 · 2. 能源分类与分摊 · 3. 建筑·空间·组织 · 4. 总分表拓扑 · 5. 换表与质量事件 · 6. 目标·规则·分母·定额）。

### P2：正式能源分类树与多分类权重分摊

- 彻底移除旧版基于名称关键词推断的 `deriveCategoryForMeter`。
- 严格读取 `energy_domain.energy_category`（分类树）与 `energy_domain.point_category_binding`（支持多分类 `weight_ppm` 分摊与历史有效版本），通过 `POST /management/categories` 与 `POST /management/category-bindings` 维护。
- 未配置正式分类的点位自动归入 M1-C `Unclassified`（未分项）。在本地适配样本库实测验证：`demo-e02` 按 70% 暖通空调 / 30% 动力系统分摊，`demo-e03` 未绑分类自动进入未分项（`522.654 kWh`），四项之和 `1,003.497 + 761.582 + 430.070 + 522.654 = 2,717.803 kWh` 与建筑总用电毫瓦时级一致。

### P3：建筑、空间、组织与总分表历史有效期管理

- 新增 `POST /management/spaces`、`POST /management/topology` 并完善 `POST /management/buildings`（含 `region`、`buildingType`、`grossAreaSqm`、`occupiedHeadcount`、`effectiveFrom`/`effectiveTo`）与 `POST /management/organizations`。
- 全部业务统计接口按**统计时段 `win.Range`** 解析对应生效版本（绝不统一取最新版本）：在本地开发库实测验证 `bldg-1` 于 `2026-11-05` 使用历史版本 `bldg-1-2025`（面积 `10,000 m²`），于 `2026-11-17` 自动切换至 `bldg-1-2026q4`（面积 `12,500 m²`），且点位绑定自动切片保持 P0-1 `target_revision` 严格一致。

### P4：补齐 9 项真实业务功能与导航职责对齐

- 在 [business_extra.go](../../internal/buildingenergy/liveapi/business_extra.go) 复用既有 `internal/buildingenergy` Go 引擎接入全部剩余 9 项正式业务接口（未配置目标/规则/分母/定额均诚实标明，零合成数据填补）：
  - **F05 年度目标摘要** & **F14 目标与分期分解**（`GET /targets`，复用 `PlanEnergyTarget`/`CompareEnergyTarget`）；
  - **F10 异常浪费识别**（`GET /anomalies`，复用 `EvaluateUsageAnomaly`）& **F17 节能机会分析**（`GET /opportunities`，复用 `BuildEnergyOpportunities`，仅输出 `pending_manual_review` 可核查线索）；
  - **F12 行业指标模板**（`GET /industry/templates`）& **F13 定额标准对标**（`GET /industry/benchmarks`，复用 `EvaluateIndustryMetric`/`EvaluateBenchmark`，明确区分 `demo` 演示阈值与 `external` 外部定额）；
  - **F21 综合与分类分项报表**（`GET /reports/consumption`）、**F22 指标与定额报表**（`GET /reports/metrics`）、**F25 行业专项与受限自定义报表**（`GET /reports/custom`，白名单拦截越权字段返回 `400`），三类报表预览与 `format=csv` 导出由同一份 Go 结果生成，实测逐格 100% 一致。
- **修正导航编号与独立页面职责**（保留五父级、25 子项左侧树导航）：
  - `F01 综合能耗概览`（`/a/energy/overview` -> `GET /overview`）：分能源 KPI 总览 + 逐时负荷概览 + 溯源；
  - `F02 能源分类构成`（`/a/energy/overview/composition` -> `GET /composition`）：多能源分类占比条与未分项审计；
  - `F03 分区分项概览`（`/a/energy/overview/objects` -> `GET /objects-overview`）：建筑总量与下属空间/组织分布；
  - `F04 实时与历史趋势`（`/a/energy/overview/trend` -> `GET /realtime-trend`）：近 7 日负荷与表计采集新鲜度监控；
  - `F06 分类分项统计`（`/a/energy/statistics` -> `GET /classification`）：单能源正式分项树明细与版本溯源；
  - `F07/F08/F09/F10/F11/F12/F13/F14/F16/F17/F21/F22/F25` 全部映射至独立视图（F15/F18–F20/F23–F24 按 PRD 保持未开放无链接）。

### P5：身份与部署边界

- 当前 HMAC + `MemoryRevocationStore` 仅作开发联调阶段认证，正式建筑能耗路由 `/admin/v1/energy-analysis/*` 继续保持默认拒绝（`401` + `Cache-Control: no-store`）；后续上线直接对接现有企业 OIDC/SSO JWKS 与集中撤销存储，不重复建设独立用户体系。

| 本轮第二阶段验收 | 结果与证据 |
| --- | --- |
| Go 编译、静态检查与关键离线套件 | `go vet ./...` 与 `go build ./...` 退出码 0；`internal/buildingenergy/...` + `cmd/server` + `internal/admin` + `internal/ingest` 离线跑法 **433 通过 / 0 失败 / 63 外部 DB 环境跳过**；`TestNoOutOfScopeArtifacts` 通过；`internal/buildingenergy` 保持纯标准库。 [Go 测试](../../evidence/go-test.jsonl) · [Vet](../../evidence/go-vet.txt) · [Go 构建](../../evidence/go-build.txt) |
| 本地 PostgreSQL 17.12（`iot_preview` 适配样本）P0–P4 实测 | `TestLiveLocalDevGovernanceEndToEnd`（增量迁移 `0001+0002`、P0 瞬时量拒绝、P1 启停与历史、P2 多权重分摊与未分项、P3 历史面积版本切换、P4 F05..F25 及三份报表 CSV 逐格一致）、`TestClassifyChannelCandidateP0Rules`（10/10）、`TestLiveReadOnlyIOTDatabaseEndToEnd` 与 `TestLiveReadOnlyBusinessAPIEndToEnd` **全部通过**。 [数据库实测日志](../../evidence/live-readonly-db-test.txt) |
| SQL 静态解析 | `pglast v8.8`（`libpg_query PG 18.6`）全部通过：**4 份 DDL（含 `0001` 与 `0002`）+ 76 条 Go SQL 语句**。 [SQL 解析日志](../../evidence/sql-parse.txt) |
| 前端编译与包体隔离 | `tsc --noEmit` 退出码 0；标准 `vite build` 与 `vite.energy-live.config.mjs` 构建退出码 0；标准包 `0` 泄漏，实时包 `0` 合成数据泄漏；`internal/ui/dist` 与根 `migrations/` 零修改。 [前端构建](../../evidence/web-build.txt) |
| 19 个实时页面 + 6 标签配置中心 Chromium 冒烟 | **24/24 通过**：未鉴权 `401`、F01–F25 全部 19 个开放页面实数渲染、配置中心 6 标签页、F11 跨历史日期自动切换版本、F25 实时 CSV 下载、1440px 与 390px 零横向溢出、零 `pageerror`。 [UI 冒烟记录](../../evidence/live-ui-smoke.txt) · [F01 1440px](../../evidence/screenshots/live-f02-1440-light.png) · [配置中心](../../evidence/screenshots/live-config-drawer-1440-light.png) · [F06 1440px](../../evidence/screenshots/live-f06-1440-light.png) · [F25 1440px](../../evidence/screenshots/live-f25-1440-light.png) · [F01 390px](../../evidence/screenshots/live-f02-390-light.png) |

## 上轮正式业务集成开发（第一～三阶段）

### 第一阶段：现有 PostgreSQL 只读盘点与真实数据适配

- **真实数据库只读盘点（`default_transaction_read_only=on`，零凭据输出）**：
  - 实例版本：本机既有 `iot-admin` PostgreSQL **17.12**（`127.0.0.1:55432`，数据库 `iot_preview`，官方迁移已执行 `0001_datasource.sql` .. `0025_energy_locations.sql`，`btree_gist 1.7` 可用未启用）。
  - 既有业务表与数据清单：`data_domain` 4 条（电/水/气/热）、`device_type` 4 条、`thing_model_property` 4 条（均为 `item_code='total_usage'`，单位 `kWh`/`m³`/`GJ`）、`terminal` 10 条（`demo-e01..e05`、`demo-w01..w02`、`demo-g01`、`demo-h01..h02`）、`energy_building` 2 栋（`演示 · A 座`、`演示 · B 座`）、`energy_area` 3 个区域、`energy_meter_location` 10 条、`exception_count` 4 组、`raw_item` 1 行、`snapshot_coarse` **12,580 行**（每块表 841 条小时快照，覆盖 `2026-10-14 13:00+08`..`2026-11-18 13:00+08`；401 条日快照）。
  - **事实边界说明**：当前本地 `iot_preview` 中的 10 块表及快照记录由本地演示初始化脚本生成，用于跑通真实 PostgreSQL SQL 协议与 Go 引擎端到端链路；接入 FNOS / 真实项目库时只需切换只读连接串，无需改动适配器代码。
- **复用既有 Go 计算引擎（未重写任何计算语义）**：
  - 新增 [snapshot_metering.go](../../internal/buildingenergy/pgadapter/snapshot_metering.go)（`SnapshotCoarseMeteringSource`）：在保留既有 [raw_metering.go](../../internal/buildingenergy/pgadapter/raw_metering.go)（`raw_item` 适配）基础上，直接适配现有库中有完整连续历史记录的 `snapshot_coarse(granularity='hour')` 累计表底快照，强制经过 `PointReadAuthorizer` 授权与 `as_of` 截断，并附带 `iot-snapshot-coarse` 行级溯源标识。
  - 新增 [iot_bridge.go](../../internal/buildingenergy/pgadapter/iot_bridge.go)（`LoadIOTMetadata` / `IOTReadOnlyCatalog`）：兼容有/无 `0025_energy_locations.sql` 两类库结构，将 `public.*` 终端、物模型、建筑、区域映射为标准 `buildingenergy.Catalog` 与 `MappingReader`，支持倍率配置与历史版本追踪。
- **真实单建筑/单电表端到端实测验证**：[live_db_test.go](../../internal/buildingenergy/pgadapter/live_db_test.go)（`TestLiveReadOnlyIOTDatabaseEndToEnd`）在只读连接下验证建筑 `演示 · A 座 (bldg-1)` 电表 `demo-e01 (演示 · A 座照明)` 于 `2026-11-17` 自然日：`1× = 761.582 kWh`，`2× = 1523.164 kWh`，有效时段 `24/24`，首条行级来源 `demo-e01:total_usage:hour:2026-11-16T15:00:00Z:rev0`。 [只读数据库实测日志](../../evidence/live-readonly-db-test.txt)

### 第二阶段：最小建筑档案、计量点管理与增量迁移

- **纯增量可演进数据库结构（绝不在 `cmd/server` 启动时自动执行）**：
  - 新增 [0001_energy_management.sql](../../internal/buildingenergy/migrations/0001_energy_management.sql) 与 [migrate.go](../../internal/buildingenergy/migrations/migrate.go)（显式调用 `migrations.Apply(ctx, pool)` 才执行，与官方根 `migrations/` 完全隔离，避免未备份自动建表影响 FNOS）。
  - 独立 `energy_domain` schema（lock-only 兼容，无须 `btree_gist` 扩展）覆盖：建筑档案版本（含建筑面积与常驻人数分母）、区域层级、组织归属、计量点代次/属性/绑定/分摊（保留真实 `target_revision`）、能源分类分项、总分表拓扑 `meter_topology` 与换表/复位/倍率异常事件 `meter_register_event`。
- **正式鉴权与管理接口（绝不回落 `AllowAll`）**：
  - 新增 [auth.go](../../internal/buildingenergy/liveapi/auth.go)：标准库 HS256 Bearer JWT 验签器与撤销存储，直接接入加固后的 `authguard.Guard`；当 `IOT_ENERGY_AUTH_SECRET/ISSUERS/AUDIENCE` 未配置时，[cmd/server/main.go](../../cmd/server/main.go) 中 `buildFormalEnergyHandler` 保持默认拒绝 `401`；合成预览签发方即使由真实密钥签名也一律 `403` 拒绝。
  - 新增 [service.go](../../internal/buildingenergy/liveapi/service.go) 与 [router.go](../../internal/buildingenergy/liveapi/router.go)：合并读取既有 `public.*` 表与可选 `energy_domain.*` 管理表，提供 `GET /admin/v1/energy-analysis/management/inventory` 与写管理接口（`POST /management/buildings`、`POST /management/organizations`、`POST /management/events`，只读主体返回 `403`）。

### 第三阶段：六个真实业务页面（F02 / F06 / F07 / F08 / F09 / F11）与严格隔离

- **后端实时计算接口（复用既有 Go 引擎）**：
  - `GET /admin/v1/energy-analysis/overview`（**F02 能耗总览**）：`演示 · A 座 (2026-11-17)` 返回 3 张分能源卡片（用电 `2,717.803 kWh`、用水 `31.359 m³`、供热 `8.362 GJ`，绝不跨能源硬加总）、24 小时负荷序列与 5 条 PostgreSQL 溯源记录。
  - `GET /admin/v1/energy-analysis/classification`（**F06 分类分项**）：用电 `2,717.803 kWh`（暖通空调 `52.7%`、照明插座 `28%`、动力系统 `19.2%`，口径 `m1-a-v1 / m1-c-v1`）。
  - `GET /admin/v1/energy-analysis/dimensions`（**F07 多维统计**）：建筑/区域双视角同口径排名（`演示 · A 座` / `演示 · B 座` 均 `同口径可比`）。
  - `GET /admin/v1/energy-analysis/trend`（**F08 趋势对比**）：本期 `2,717.803 kWh`，环比基期 `1,904.689 kWh`（`同口径可比`），同比诚实返回 `missing_data`（当前小时快照历史仅 35 天，不伪造同比）。
  - `GET /admin/v1/energy-analysis/balance`（**F09 总分表平衡**）：尚未在 `energy_domain.meter_topology` 配置物理总分表关系时诚实返回 `topologyConfigured=false`，零虚构差额。
  - `GET /admin/v1/energy-analysis/efficiency`（**F11 能耗强度**）：诚实展示真实分子 `2,717.803 kWh` 与 `status=missing_denominator`、`value=null`（因现有库尚未配置真实建筑面积/常驻人数，不伪造单耗）。
- **前端实时工作台与严格互斥隔离**：
  - 新增 [BuildingEnergyLivePage.tsx](../../web/src/pages/BuildingEnergyLivePage.tsx) 与 [vite.energy-live.config.mjs](../../web/vite.energy-live.config.mjs)，并在 [AppShell.tsx](../../web/src/components/AppShell.tsx) 与 [App.tsx](../../web/src/App.tsx) 接入 `ENERGY_LIVE` 模式（与 `ENERGY_PREVIEW` 互斥）：
    - 保留左侧五父级、25 子项导航树与移动端抽屉；
    - 顶部显式标识 `真实数据库 · live_database`，无有效 Bearer Token 时展示 `401` 默认拒绝门禁，绝不回落合成样本；
    - 集成第二阶段「建筑档案与计量点实时台账」抽屉；
    - 标准生产构建（`vite build`）经静态检查确认 `0` 泄漏（不含 `live_database`、`/admin/v1/energy-analysis` 或任何合成页面），`ENERGY_LIVE` 构建亦确认 `0` 合成数据泄漏。

| 本轮正式业务集成验收 | 结果与证据 |
| --- | --- |
| Go 全仓（离线 + 安全） | **763 项通过、0 失败、62 项外部数据库环境依赖跳过**（`internal/admin` 9、`internal/ingest` 51、`pgadapter`/`liveapi` 实时只读 DB 2 项在离线跑法下按约定跳过）；`TestNoOutOfScopeArtifacts` 通过；`internal/buildingenergy` 保持纯标准库零外部依赖；`go vet` 与 `go build` 退出码 0。 [Go 测试](../../evidence/go-test.jsonl) · [Vet](../../evidence/go-vet.txt) · [Go 构建](../../evidence/go-build.txt) |
| 真实 PostgreSQL 17.12 只读端到端 | `TestLiveReadOnlyIOTDatabaseEndToEnd`（`0.15s`）、`TestHMACBearerAuthAndFailClosedBoundary` 与 `TestLiveReadOnlyBusinessAPIEndToEnd`（`0.30s`）**3/3 通过**（`default_transaction_read_only=on`，零凭据输出）。 [只读实测日志](../../evidence/live-readonly-db-test.txt) |
| SQL 离线静态解析 | `pglast v8.8`（`libpg_query PG 18.6`）全部通过：**3 份 DDL（含新增 19 条 `0001_energy_management.sql`）+ 42 条 Go SQL 查询**。 [SQL 解析日志](../../evidence/sql-parse.txt) |
| 前端编译与包体隔离 | `tsc --noEmit` 退出码 0；标准 `vite build` 与 `vite.energy-live.config.mjs` 构建均退出码 0；标准包 `0` 泄漏，实时包 `0` 合成数据泄漏；`internal/ui/dist` 与官方 `migrations/` 零修改。 [前端构建](../../evidence/web-build.txt) |
| 实时数据库页面 Chromium 冒烟 | 10/10 通过：未鉴权 `401` 拦截、登录后 F02/F06/F07/F08/F09/F11 六页实数渲染、五父级左侧导航、第二阶段台账抽屉、1440px 与 390px 零横向溢出、零 `pageerror`。 [UI 冒烟记录](../../evidence/live-ui-smoke.txt) · [F02 1440px](../../evidence/screenshots/live-f02-1440-light.png) · [F06 1440px](../../evidence/screenshots/live-f06-1440-light.png) · [F02 390px](../../evidence/screenshots/live-f02-390-light.png) |

## 上轮正式数据库实测前代码修正（P0-1 / P0-2 / P1）

### P0-1：修复分摊对象权威目录版本 `target_revision`

- **领域模型**：在 [mapping.go](../../internal/buildingenergy/mapping.go) 为 `Allocation` 新增 `TargetRevision string` 字段（每条分摊对象独立携带真实 `platform_directory.object_revision.revision`，绝不使用 `BindingRevision.Meta.Version` 冒充）；同步调整 [balance.go](../../internal/buildingenergy/balance.go) 避免结构体全等比较受新字段干扰，不改变 F09 计算语义。
- **读取校验**：`ReadAuthorizedMapping` 保留 `ResolveScope` 返回的权威 `Catalog` 对象版本集合，在授权检查之后逐条核验每条活跃 `Allocation.TargetRevision` 非空且与当前窗口对应的权威目录 `object_revision.revision` 精确一致，目录变更后陈旧版本、空版本、错版本或跨项目对象均返回 `ErrCatalogScope`；`PublishMapping` 同步拒绝缺失 `TargetRevision`。
- **PG 读写还原**：
  - [catalog.go](../../internal/buildingenergy/pgadapter/catalog.go) 在 `DirectoryCatalog.Project/Object` 拒绝空 `revision`/`source_system`/`source_id`。
  - [mapping_store.go](../../internal/buildingenergy/pgadapter/mapping_store.go) 要求注入权威 `Catalog`：`CompareAndSwapMapping` 在开启事务前逐条核验 `alloc.TargetRevision` 非空、对象归属当前项目、版本与 `object_revision.revision` 一致且对象版本有效期覆盖绑定区间，并直接写入真实 `alloc.TargetRevision`；`selectAllocationsByGenSQL` 增加 `target_revision` 列并在 `ReadMapping` 完整还原（空值返回 `ErrMappingUnavailable`）。
  - [raw_metering.go](../../internal/buildingenergy/pgadapter/raw_metering.go) 在 `verifyResolvedOwnership` 同步校验 `TargetRevision` 非空。
- **验证**：[mapping_test.go](../../internal/buildingenergy/mapping_test.go) 与 [adapter_test.go](../../internal/buildingenergy/pgadapter/adapter_test.go) 覆盖多分摊对象各自独立目录版本写入/还原、目录变更历史回放与陈旧版本拦截、`BindingRevision.Meta.Version` 冒充拦截、空版本、跨项目对象与无目录 fail-closed。

### P0-2：修复身份凭据生命周期与 typed-nil 防护

- **强制签发/会话创建时间与最大寿命**：在 [guard.go](../../internal/buildingenergy/authguard/guard.go) 为 `Principal` 新增等价的 `SessionCreatedAt` 字段，为 `ContextIdentitySource` 新增有界 `MaxClockSkew`（默认 `0` 严格校验，上限 `HardMaxClockSkew = 2m`）：
  - `IssuedAt` 与 `SessionCreatedAt` 同时为零值时 fail-closed 返回 `ErrIdentityMissingIssuedAt`（HTTP `401`）；
  - 任一时间超前 `now + MaxClockSkew` 返回 `ErrIdentityFutureIssuedAt`；
  - 过期（`now >= ExpiresAt`）返回 `ErrIdentityExpired`；
  - 剩余有效期 `ExpiresAt - now` 或总生命周期 `ExpiresAt - IssuedAt` / `ExpiresAt - SessionCreatedAt` 任一超过 `MaxCredentialTTL` 均返回 `ErrIdentityTTLTooLong`（阻止长寿命会话靠短期换签绕过上限）；
  - `ScopedPolicy.checkRevocation` 同步改为强制校验非空 `Revocations`。
- **全鉴权/适配器路径 typed-nil 防 panic**：在 [contracts.go](../../internal/buildingenergy/contracts.go)、[mapping.go](../../internal/buildingenergy/mapping.go)、[guard.go](../../internal/buildingenergy/authguard/guard.go)、[auth.go](../../internal/admin/auth.go) 及 [sqltypes.go](../../internal/buildingenergy/pgadapter/sqltypes.go) / `catalog.go` / `mapping_store.go` / `raw_metering.go` 全面使用反射空值检测替换原始 `== nil` 判断，任何 typed-nil 指针或函数注入接口均 fail-closed 返回错误/`401` 而绝不 panic。
- **验证**：[guard_test.go](../../internal/buildingenergy/authguard/guard_test.go)（50 项通过）、[auth_test.go](../../internal/admin/auth_test.go)、[mapping_test.go](../../internal/buildingenergy/mapping_test.go) 与 [adapter_test.go](../../internal/buildingenergy/pgadapter/adapter_test.go) 覆盖零值、未来时间、过期、总寿命超限、长会话短令牌绕过与全部 typed-nil 注入场景。

### P1：部署安全边界（旧 `/admin/v1/*` 兼容与反代回环边界）

- **默认关闭风险明确化**：`IOT_LEGACY_ADMIN_LOOPBACK_ONLY` 为兼容旧 IoT 业务默认保持 `false`（不改变既有 `/admin/v1/*` 与 `WithAuth(nil)` 行为）；若在生产环境未开启该开关且将 `IOT_LISTEN` 绑定到非回环地址，旧 `/admin/v1/*` 将以 `AllowAll` 暴露网关明文凭据、原始表码直读与全表写删接口。
- **同机反向代理本机回环边界**：同机 Nginx/Envoy/Caddy 转发外部请求到 `127.0.0.1:8080` 时，Go 进程看到的 TCP `RemoteAddr` 仍为 `127.0.0.1`。为此，[auth.go](../../internal/admin/auth.go) 中 `WithLoopbackOnly` 与 `LoopbackOnlyPermission` 在校验直连回环 IP 的同时，额外拦截携带 `Forwarded`/`X-Forwarded-For`/`X-Real-IP` 转发头的请求（返回 `403 legacy_admin_proxy_rejected` + `Cache-Control: no-store`）；同时明确：**若同机反代剥离了所有转发头，TCP 层无法将其与本机直连区分**。
- **生产部署硬性要求（SSO / mTLS / ACL）**：
  1. 设备接入面（`POST /ingest/v1/messages`）与旧技术管理面（`/admin/v1/*`）按网段/端口分离；不需要外部反代访问旧管理面时保持 `IOT_LISTEN=127.0.0.1:8080` 并开启 `IOT_LEGACY_ADMIN_LOOPBACK_ONLY=true`。
  2. 任何通过同机或异机反向代理暴露 `/admin/v1/*` 的部署，**必须在网关/反代层强制实施企业 SSO（OIDC/OAuth2）+ mTLS（运维客户端证书或网关双向认证）+ 源 IP/网段与路由级 ACL**，对 `/admin/v1/gateways/*/credential*`、`/admin/v1/raw-*`、`/admin/v1/snapshot-query` 及所有 `POST/PUT/DELETE` 路由仅放行运维管理角色，禁止普通建筑能耗业务用户访问。
  3. 正式建筑能耗前缀 `/admin/v1/energy-analysis/` 始终独立执行 `authguard` fail-closed 边界。

### P1：计量时间边界（`RawMeteringSource` 半开区间与累计读数 `effective.To`）

- **半开区间与累计右端点口径审查（未改变既有 M1-A 计算口径）**：
  - **区间量（`Interval`）**：严格遵循半开区间 `[From, To)`。无论时间戳标记区间起点还是终点（`IntervalTimeIsEnd=true`），仅接受完整落于 `period`、`profile.Meta.Effective` 与 `binding.Effective` 三重交集内的 `[sample.At, sample.End)`；起点在 `effective.To` 的区间（`[effective.To, effective.To+cadence)`）严格归属下一代次并被剔除。
  - **累计量（`Cumulative`）**：遵循 M1-A 已确认的半开窗差分语义（`[period.From, period.To)` 的末段原子区间 `[period.To-cadence, period.To)` 需消费精确位于 `period.To` 的闭合表底读数）。当 `period.To == effective.To` 时，位于精确 `effective.To` 的读数作为相邻两代次 `[t0, t1)` 与 `[t1, t2)` 共享的交接边界读数保留，从而保证跨代次能量守恒（`usage([t0,t1)) + usage([t1,t2)) == R(t2) - R(t0)`）；任何早于 `period.From`/`effective.From` 或晚于 `period.To`/`effective.To`（含 `effective.To + 1µs`）的读数均被严格过滤；若 `effective.To` 恰发生换表，由 `RegisterEventResolver` 返回新代次 `epoch` 或冲突双读数在 M1-A 中判定为 `Suspect`（`EpochChange`/`ConflictingSample`），不产生负增量。
- **验证**：[adapter_test.go](../../internal/buildingenergy/pgadapter/adapter_test.go) 新增 `TestRawMeteringSourceHalfOpenAndCumulativeEffectiveToBoundaries` 覆盖相邻代次能量守恒、`effective.To + 1µs` 排除、子窗口越界过滤、边界换表 `EpochChange` 以及起点/终点两种区间量边界。

| 本轮全仓验收 | 结果与证据 |
| --- | --- |
| Go 全仓 | **762 项通过、0 失败、60 项外部环境依赖跳过**（`internal/admin` 9、`internal/ingest` 51）；本轮新增 21 项测试事件（`buildingenergy` 277、`authguard` 50、`pgadapter` 24、`admin` 57、`server` 3）；`TestNoOutOfScopeArtifacts` 通过；`internal/buildingenergy` 保持纯标准库零外部依赖；`go vet` 与 `go build` 退出码 0。 [Go 测试](../../evidence/go-test.jsonl) · [Vet](../../evidence/go-vet.txt) · [Go 构建](../../evidence/go-build.txt) |
| SQL 离线解析 | `pglast v8.8`（`libpg_query PG 18.6`）通过全部 23 项 SQL 静态解析（4 份候选 DDL 变体 + 19 条适配器 SQL 常量，含新增 `target_revision` 读取列）。 [SQL 解析日志](../../evidence/sql-parse.txt) |
| 前端与边界保护 | 本轮未修改前端、`internal/ui/dist`、正式 `migrations/`、FNOS 与原 `dev` 工作区；`tsc --noEmit` 退出码 0；未执行数据库写入，未提交/推送。 [前端构建](../../evidence/web-build.txt) |

**总准入结论维持：No-Go（M1B-01 至 M1B-04 仍待人工签认与一次性隔离 PostgreSQL 实测授权）**。

## 上轮确定性安全风险收口与隔离数据库准备

### 1. 旧 `/admin/v1/*` 网络暴露与 `AllowAll` 风险审计及不破坏旧业务的隔离措施

- **实查暴露面**：[服务入口](../../cmd/server/main.go) 默认监听 `IOT_LISTEN=127.0.0.1:8080`，但同进程单端口同时承载设备接入面（`POST /ingest/v1/messages`）与未鉴权旧管理面（`/admin/v1/*` 走 `admin.WithAuth(nil, ...)` -> `AllowAll{}`）。一旦为接收边缘网关 HTTP 上报将 `IOT_LISTEN` 改为 `0.0.0.0:8080` 或 `:8080`（或挂到无认证反代后），[管理路由](../../internal/admin/admin.go) 下三类高危能力将直接暴露：
  1. **凭证明文泄露与重置**：`GET /admin/v1/gateways/{id}/credential`（解密并返回 MQTT 密码）、`POST /admin/v1/gateways/{id}/credential/reset`（同步调用 Broker API）。
  2. **全表写入与删除**：11 个核心实体的 `POST`/`PUT`/`DELETE` 与批量接口（含 `gateway`、`terminal`、`thing_model_property`、`snapshot_binding`、`snapshot_settings`）。
  3. **跨项目原始表码直读旁路**：`GET /admin/v1/raw-items`、`GET /admin/v1/raw-message`、`GET /admin/v1/snapshot-query`、`GET /admin/v1/analytics/*` 可无鉴权读取任意 `terminal_id` 原始数据，直接旁路正式建筑能耗的租户/项目/对象/点位授权边界。
- **已落地的零破坏隔离护栏**：
  - 保留 `admin.WithAuth(nil, ...)` 与默认 `127.0.0.1:8080` 行为 100% 不变（零破坏旧业务与既有单测）。
  - 在 [管理鉴权](../../internal/admin/auth.go) 新增 `IsLoopbackAddr`、`LoopbackOnlyPermission` 与 `WithLoopbackOnly`（仅校验直接 TCP `RemoteAddr` 是否为回环 `127.0.0.0/8` / `::1`，显式忽略可伪造的 `X-Forwarded-For`/`X-Real-IP`，非回环来源返回 `403` + `Cache-Control: no-store`）。
  - 在 [服务入口](../../cmd/server/main.go) 新增可选环境变量 `IOT_LEGACY_ADMIN_LOOPBACK_ONLY=true`（默认关闭保持兼容；开启后即使 `IOT_LISTEN` 绑定非回环地址接收设备接入，旧 `/admin/v1/*` 仍严格仅放行本机回环来源）；未开启且监听非回环地址时，进程启动立即输出结构化 `WARN` 安全风险告警。
- **上线前部署层隔离建议（不破坏旧业务）**：① 保持 `IOT_LISTEN=127.0.0.1:8080` 或启用 `IOT_LEGACY_ADMIN_LOOPBACK_ONLY=true`，将边缘网关接入流量经独立网关/端口转发至 `POST /ingest/v1/messages`；② 反代层对 `/admin/v1/gateways/*/credential*`、`/admin/v1/raw-*`、`/admin/v1/snapshot-query` 及所有写方法单独加运维 SSO/mTLS 与网段 ACL，禁止普通建筑能耗用户访问旧技术接口；③ 正式建筑能耗前缀 `/admin/v1/energy-analysis/` 始终独立运行于 `authguard` 默认拒绝栈。

### 2. `ContextIdentitySource` 正式配置硬校验加固

- 在 [安全守卫](../../internal/buildingenergy/authguard/guard.go) 新增 `MaxCredentialTTL`（硬上限 `HardMaxCredentialTTL = 24h`）、`ContextIdentitySource.ValidateConfig()` 与 `Guard.ValidateFormalConfig()`，强制同时满足四项前提才允许启用正式业务接口，缺失任一项即 fail-closed 返回 `401 energy_auth_unconfigured` / `ErrIdentityConfigInvalid`：
  1. **可信签发方白名单**（`AllowedIssuers` 非空、无空白项、严禁包含任何合成预览签发方）。
  2. **预期受众**（`ExpectedAudience` 非空，且 `Principal.Audience` 必须精确匹配）。
  3. **有效凭据期限**（配置侧 `0 < MaxCredentialTTL <= 24h`；请求侧 `Principal.ExpiresAt` 不得为零值、不得已过期、剩余有效期与总签发寿命均不得超过 `MaxCredentialTTL`）。
  4. **必要撤销机制**（`ContextIdentitySource.Revocations` 与 `ScopedPolicy.Revocations` 均不得为 `nil`，撤销后端异常一律 fail-closed 拒绝）。
- [守卫单测](../../internal/buildingenergy/authguard/guard_test.go) 扩充至 39 项通过，覆盖全部缺失配置、合成白名单污染、缺失/超长有效期、过期、撤销与正常放行路径。

### 3. `RawMeteringSource` 强绑定经验证的项目、终端及点位历史归属

- 针对 `raw_item` 表（[RAW 迁移](../../migrations/0024_raw_ingest.sql)）仅含 `(terminal_id, item_code)`、不含 `tenant_id`/`project_id` 的底层事实，移除 [RAW 适配器](../../internal/buildingenergy/pgadapter/raw_metering.go) 中无鉴权保护的 `ProfileResolver` 直连路径，改为强制要求 `PointReadAuthorizer`（默认实现 `MappingPointAuthorizer`）：
  - 每次 `Load` 必须先通过 `buildingenergy.ReadAuthorizedMapping` 串行校验：可信主体认证 -> `ReadUsage` 授权（项目 + 对象 + 点位 + 时段）-> 权威目录 `Catalog` 覆盖校验 -> 不可变 M1-B 映射代次读取 -> 该时段所有活跃归属对象均落入调用方已授权范围。
  - `RawMeteringSource.Load` 自身再次执行 `verifyResolvedOwnership` 纵深校验：拒绝 `Authorizer == nil`、拒绝无绑定对象（未归属任何项目对象）、拒绝跨项目/跨对象/跨代次边界（如终端历史迁移前时段）、拒绝缺少通道证据；**所有拒绝均在查询 `raw_item` SQL 之前阻断（`dbQueries == 0`）**。
  - 行级纵深校验：SQL 显式回传 `terminal_id, item_code`，核验与请求点位一致，并再次校对 `received_at <= as_of` 与样本时间戳落于经验证的历史归属生效区间内。
- [适配器契约测试](../../internal/buildingenergy/pgadapter/adapter_test.go) 扩充至 15 项通过（仍属内存 SQL 替身契约测试，不冒充真实上游样本试点）。

### 4. 独立 PostgreSQL 测试环境只读勘查与最低成本隔离方案

- **宿主机只读实查结果（零安装、零建库、零写入）**：
  - `docker`、`podman`、`nerdctl`、`psql`、`postgres`、`pg_ctl`、`initdb`、`choco`、`scoop` 均**未安装**；
  - `wsl.exe` 仅为系统存根（`wsl --status` 退出码 `50`：`Windows Subsystem for Linux is not installed`）；
  - `winget.exe` 可用；无 PostgreSQL/Docker Windows 服务；本机 `5432–5435` / `55432` 端口均无监听；环境变量无 `PG*`/`IOT_PG_DSN`/`IOT_TEST_DSN` 配置；Go 模块缓存仅有 `pgx`/`pgpassfile`/`pgservicefile`，无嵌入式 PG 引擎。
  - **结论**：当前宿主机**尚无**立即可用的独立 PostgreSQL 实例；DB-01 至 DB-07 实测仍为 **0 项**。
- **最低成本且与生产完全隔离的实施方案对比（等待用户明确授权后方可任选其一）**：
  1. **方案 A（最低成本推荐，零虚拟化/免装 Docker）**：下载官方绿色免安装 Windows PostgreSQL 16/17 压缩包至临时目录（不注册 Windows 服务、不写入系统目录），使用 `initdb` 在 `%TEMP%\pg-m1b-isolated` 创建一次性数据目录，仅绑定 `127.0.0.1:55432`（非默认端口，绝不复用生产配置与连接串），创建无超级权限的测试角色（`energy_reader` / `energy_caller`）后执行 `m1-b-mapping.sql`、`platform-directory-candidate.sql` 与 [m1-b-isolation-cases.sql](review/m1-b-isolation-cases.sql)（DB-01..DB-07），测试完成采集日志后 `pg_ctl stop` 并彻底删除临时目录。
  2. **方案 B（DBA 托管隔离库，零本机安装）**：由 DBA 在与 FNOS/生产完全隔离的测试实例新建一次性库与非 owner 角色，提供脱敏 `PGSERVICE` 参数后在获批终端执行 DB-01..DB-07 并在验收后删库。
  3. **方案 C（本机安装 WSL2 + Docker Desktop / Rancher Desktop）**：使用 `postgres:16-alpine` `--tmpfs /var/lib/postgresql/data` 绑定 `127.0.0.1:55432` 运行一次性容器，但本机需先开启 WSL2 并重启，初始化成本高于方案 A。

| 本轮全仓回归 | 结果与证据 |
| --- | --- |
| Go 全仓 | **741 项通过、0 失败、60 项外部环境依赖跳过**（`internal/admin` 9、`internal/ingest` 51）；本轮新增 26 项安全与归属绑定测试事件；`TestNoOutOfScopeArtifacts` 通过；`internal/buildingenergy` 保持纯标准库零外部依赖；`go vet` 与 `go build` 退出码 0。 [Go 测试](../../evidence/go-test.jsonl) · [Vet](../../evidence/go-vet.txt) · [Go 构建](../../evidence/go-build.txt) |
| SQL 离线解析 | `pglast v8.8`（`libpg_query PG 18.6`）通过全部 23 项 SQL 静态解析（4 份候选 DDL 变体 + 19 条适配器 SQL 常量）。 [SQL 解析日志](../../evidence/sql-parse.txt) |
| 前端与基线保护 | 本轮未改动前端、`internal/ui/dist`、正式 `migrations/` 与原 `dev` 工作区；`tsc --noEmit` 退出码 0；复核既有标准 Vite 构建与 70 张 Playwright 回归截图。 [前端构建](../../evidence/web-build.txt) |

**总准入结论维持：No-Go（G1–G4 / M1B-01 至 M1B-04 均未签认）**。

## 上轮正式开发准入与真实集成专项（G1–G4）

### 第一阶段：仓库真实接口与数据来源盘点（基于代码实查）

| 盘点领域 | 现有代码路径 | 可复用部分 | 相对 M1-A / M1-B 的真实缺口与影响 |
| --- | --- | --- | --- |
| 设备/终端与点位模型 | [元信息存储](../../internal/meta/store.go) · [验证模型](../../internal/validate/model.go) | `terminal(terminal_id)` 与 `thing_model_property(item_code, unit)` 已提供天然 `terminal_id + item_code` 键、类型与标称单位。 | 缺少 `tenant_id`、`project_id`、`physical_channel_ref`、`measurement_kind`（累计/区间/瞬时）、`multiplier`、`register_modulus`、`register_epoch`、换表/回卷事件与采样周期；无法单独作为计量点主数据。 |
| RAW / 快照 / 历史读取 | [RAW 存储](../../internal/raw/store.go) · [快照写入](../../internal/snapshot/write.go) · [RAW API](../../internal/admin/raw.go) · [快照查询](../../internal/admin/snapshot_query.go) | `raw_item` 保留 `id`、`received_at`、`value_time`、`value_num`、`unit`、`quality`（位掩码 0/1/2/4）与 `status`（`written/dropped/ignored`），可作可重放只读原始读数基座。 | `snapshot_coarse` 按自然窗口原地覆写且固定 `Asia/Shanghai`，不支持按 `as_of` 回放与分表/分钟明细（分钟表已在历史迁移移除）；`raw_item` 允许 `MaxClockSkew=5m` 且 `value_time` 不强制对齐采样周期，直连 M1-A 时会触发 `ErrInvalidSample`，需上游确认时钟偏移与对齐策略。 |
| 项目/建筑/空间/组织目录 | [管理入口](../../internal/admin/admin.go) · [能耗统计](../../internal/admin/analytics.go) | 无现成权威目录表；`dev` 工作区另存在草稿 `migrations/0025_energy_locations.sql`（扁平位置层级，未合入本分支）。 | `migrations/0001..0024` 零租户/项目/建筑/空间/组织表；`data_domain` 仅用于 IoT 域分类，`gateway` 为采集网关而非建筑目录。不可由能耗模块私建孤立、不可迁移的影子主数据。 |
| 鉴权、会话与管理路由 | [服务入口](../../cmd/server/main.go) · [管理鉴权](../../internal/admin/auth.go) | 既有 `/admin/v1/*` 技术管理接口通过 `admin.WithAuth(nil, ...)` 保持向后兼容，网关接入鉴权独立于 `gateway` 表。 | 主服务未接入真实用户 IdP/OIDC/Session 认证；`WithAuth(nil)` 默认回退 `AllowAll{}`，无租户/项目/建筑/组织/点位/下载细粒度授权。 |
| PostgreSQL 迁移与角色 | [数据库连接](../../internal/store/db.go) · [基线迁移](../../migrations/0001_datasource.sql) · [扩展移除迁移](../../migrations/0008_conv_remove_interval_and_history.sql) · [RAW 迁移](../../migrations/0024_raw_ingest.sql) | `pgxpool` 与 `store.Migrate` 按文件名递增执行并自动包裹单文件事务。 | 单连接池共用 `IOT_PG_DSN` 执行 DDL 迁移与运行时读写，且会话硬编码 `timezone = Asia/Shanghai`；`0008` 已移除 `btree_gist`，不能假设目标库具备该扩展；`review/m1-b-mapping.sql` 含外层 `BEGIN/COMMIT`，若未来转入 `store.Migrate` 须剥离外层事务。 |

**最小平台共享目录方案（消除孤立主数据）**：新增平台评审稿 [共享平台目录候选 DDL](review/platform-directory-candidate.sql)，将权威目录归属至平台级 `platform_directory` schema（`project_revision`、`object_revision`，支持半开 `tstzrange` 生效区间、GiST 或锁互斥两套变体、以及与 `dev` 位置层级的字段兼容）。建筑能耗模块仅通过只读适配器 [目录适配器](../../internal/buildingenergy/pgadapter/catalog.go) 消费，仅申请 `USAGE + SELECT`，绝不持有写权限或自建不可迁移目录。

### 第二阶段：权限安全加固（默认拒绝，保留旧 IoT 接口兼容）

- **旧接口零破坏**：[管理鉴权](../../internal/admin/auth.go) 保留旧 `WithAuth(nil)` 行为供既有 `/admin/v1/*` 技术接口使用；新增 `DenyAll{}` 与 `WithFailClosedAuth`（参数为空或校验失败均返回 `401 unauthorized` 且强制 `Cache-Control: no-store`）。
- **正式建筑能耗安全边界**：新增独立子包 [安全守卫](../../internal/buildingenergy/authguard/guard.go)（保持根包 `internal/buildingenergy` 纯标准库零外部依赖）：
  - `ContextIdentitySource` 仅信任经服务端认证注入的 `Principal`，校验允许的 `Issuer`、受众、过期时间、撤销检查（检查器报错时 fail-closed），并显式拒绝合成预览签发方（`preview`/`f06-preview`/`industry-synthetic`/`f09-demo`/`f06-demo`/`f12-industry-demo`）。
  - `ScopedPolicy` 实现 `buildingenergy.AccessPolicy` 与 `buildingenergy.MappingChangePolicy`，按主体、动作（`ReadUsage`/`ExportUsage`/`DownloadReport`/`ManageBindings`）、租户、项目、对象、计量点与有效时间窗执行精确授权，拦截跨租户、跨项目、局部超范围请求、权限提升、导出后撤权二次下载拦截，以及映射变更缺少新旧双目标授权或审批单号。
  - `NewFormalRouter(guard)` 挂载于主服务 [服务入口](../../cmd/server/main.go) 的 `/admin/v1/energy-analysis/` 路由前缀；未注入可信身份源与策略时默认 fail-closed 返回 `401 energy_auth_unconfigured`，并忽略客户端伪造头（`X-User-ID`/`X-Project-ID`/`X-Tenant-ID`/`X-Forwarded-User`）。
- **单测验证**：[鉴权单测](../../internal/admin/auth_test.go)（2 项）、[守卫单测](../../internal/buildingenergy/authguard/guard_test.go)（25 项）、[服务路由单测](../../cmd/server/main_test.go)（2 项）全部通过。

### 第三阶段：PostgreSQL 集成准备与本地隔离环境审计

- **候选 DDL 与适配器 SQL 静态解析**：使用离线 `pglast v8.8`（`libpg_query PG 18.6`）完成语法验证，[SQL 离线解析日志](../../evidence/sql-parse.txt) 覆盖：
  - [M1-B 五表候选 DDL](review/m1-b-mapping.sql)（GiST 版 11 条语句、锁互斥剥离版 11 条语句，SHA-256 `38d15346c5cfb0d59f5bfdbf8753a767279fb223a07f2f0504c5c6e6f6d85152`）。
  - [平台共享目录候选 DDL](review/platform-directory-candidate.sql)（GiST 版 9 条语句、锁互斥剥离版 7 条语句，SHA-256 `6536816d50cc840678114a79c1b164d3bbc36393723c1b43a46e1a70da82cd01`）。
  - [PG 适配器](../../internal/buildingenergy/pgadapter/mapping_store.go) 与 [RAW 适配器](../../internal/buildingenergy/pgadapter/raw_metering.go)、[目录适配器](../../internal/buildingenergy/pgadapter/catalog.go) 内全部 18 条 SQL 语句模板（含 `READ COMMITTED` CAS 序列与 `LockOnlyCheck` 重叠/超分摊自检）。
- **实际运行环境与硬门禁状态**：
  - 当前宿主机**未安装** `docker`、`psql` 与 `wsl`，且**尚未获得用户对一次性数据库写入/建库的明确授权**。
  - 严格遵守准入边界：**未执行**任何建库、DDL 或数据写入，**未修改**主仓库正式 `migrations/`。
  - [DB-01 至 DB-07 隔离脚本](review/m1-b-isolation-cases.sql) 均保持**未执行（待授权 + 待提供一次性本地 PostgreSQL 隔离容器/实例）**。

### 第四阶段：真实表结构适配器与契约测试（非真实上游试点）

- 新增 [PG/RAW 真实表结构适配器包](../../internal/buildingenergy/pgadapter/sqltypes.go)：
  - `DirectoryCatalog`（[catalog.go](../../internal/buildingenergy/pgadapter/catalog.go)）：对接 `platform_directory.{project_revision,object_revision}`。
  - `MappingStore`（[mapping_store.go](../../internal/buildingenergy/pgadapter/mapping_store.go)）：对接 `energy_mapping` 五表，实现 `READ COMMITTED` `INSERT head ON CONFLICT DO NOTHING -> SELECT FOR UPDATE -> INSERT generation/profile/binding/allocation -> (可选 LockOnlyCheck) -> UPDATE head CAS`，校验 `uint64->bigint`、微秒时间戳、有限数值、`NULL` 满量程转 Go `0`。
  - `RawMeteringSource`（[raw_metering.go](../../internal/buildingenergy/pgadapter/raw_metering.go)）：直接按现有 `raw_item` 表字段读取读数，执行 `received_at <= as_of` 截止、组合溯源键 `id@received_at`、保守质量映射（`quality != 0` 或单位不一致一律标 `Suspect`）及可选换表/回卷事件接口。
- **契约测试结果**：[适配器契约测试](../../internal/buildingenergy/pgadapter/adapter_test.go)（5 组测试通过）使用内存 SQL 替身验证 Go/SQL 编码、CAS 冲突与回滚、锁互斥校验、`authguard + DirectoryCatalog + MappingStore + RawMeteringSource -> M1-A/M1-B/M1-C` 同窗对账，以及时钟偏移触发 `ErrInvalidSample`。
- **真实上游试点声明**：本轮**未取得已授权脱敏真实上游样本**；以上均为**适配器契约测试**，**真实上游试点验收 0 项，严禁视为正式上游验收**。

### 第五阶段：正式开发准入审查矩阵（G1–G4）

| 门禁 | 对应阻塞 | 已完成事项 | 实际验证证据 | 仍需人工确认与签认 | 风险与回滚条件 | 结论 |
| --- | --- | --- | --- | --- | --- | --- |
| **G1** | M1B-01 权威项目/建筑/空间/组织目录 | 盘点仓库零目录事实；提出平台级 `platform_directory` 共享方案与只读 `DirectoryCatalog` 适配器。 | `pglast v8.8` 语法解析通过；`TestDirectoryCatalogEnforcesProjectAndTimezoneContracts` 通过；`nongoal_test` 通过。 | 确认目录由外部系统对接还是由平台核心新增 `platform_directory`；明确与 `dev` `0025_energy_locations.sql` 合并策略及生效区间治理。 | 若能耗模块私建孤立目录将导致后期难以迁移与组织变迁历史失真。回滚：删除评审稿 `platform-directory-candidate.sql` 与未挂载的 `pgadapter`。 | **No-Go**（M1B-01 未签认） |
| **G2** | M1B-02 可信身份与细粒度授权 | 新增 `WithFailClosedAuth`、`authguard` 默认拒绝安全层与 `/admin/v1/energy-analysis/` 路由占位，旧 `/admin/v1/*` 零破坏。 | `internal/admin`（56 通过）、`authguard`（25 通过）、`cmd/server`（2 通过）；验证无身份/伪造头/合成主体/跨项目/局部越权/撤权/二次下载拒绝。 | 签认上游真实 IdP/OIDC/Session 鉴权器实现、签发方白名单、撤销查询源与项目/建筑/组织/点位/下载 RBAC 授权存储。 | 缺少真实上游 Authenticator 时正式路由处于持续 `401` 拒绝状态（安全但不可用）。回滚：移除 `cmd/server/main.go` 新前缀路由挂载及 `authguard`。 | **No-Go**（M1B-02 未签认） |
| **G3** | M1B-03 计量点/通道/倍率/换表与真实样本 | 盘点 `terminal`/`thing_model_property`/`raw_item` 缺口；实现 `RawMeteringSource` 与 `MappingStore` 适配器并跑通 M1-A/M1-B/M1-C 契约测试。 | `pgadapter` 5 组契约测试通过；明确暴露 `raw_item` 5 分钟时钟偏移与未对齐时间边界。 | 提供已授权脱敏真实终端与 `raw_item` 样本；签认物理通道/倍率/换表/回卷主数据来源、时钟偏移与时间戳对齐口径、物理总分表拓扑。 | 未经真实样本核对可能出现换表负跳变、时钟偏移丢数或总分表重计。回滚：移除未启用的 `pgadapter` 包。 | **No-Go**（M1B-03 未签认） |
| **G4** | M1B-04 PostgreSQL 结构/扩展/角色与隔离库实测 | 完成 5 表 DDL 与 `store.db.go` 迁移/时区/连接池审计；完成离线 `pglast` 解析与 `LockOnlyCheck` 备选实现。 | [SQL 离线解析日志](../../evidence/sql-parse.txt)；全仓 Go 715 通过 / 0 失败 / 60 环境跳过；正式 `migrations/` 零改动。 | **明确授权**一次性本地隔离库写入测试并选择环境（本机安装 Docker/PostgreSQL 或指定 DBA 隔离实例）；签认目标 PG 版本、`btree_gist` vs 锁互斥、读写/迁移角色分离与外层事务移除。 | 本机无 `docker`/`psql`/`wsl` 且未获 DB 写入授权，DB-01 至 DB-07 实测为 0；单连接池与固定时区尚待分离。回滚：未执行任何 DB 变更，无需库级回滚。 | **No-Go**（M1B-04 未签认） |

**总准入结论：No-Go（尚不满足正式数据集成开发与生产数据库实施条件）**。当前可安全保持隔离预览与适配器契约开发；M1B-01 至 M1B-04 均保持未签认。

| 本轮全仓回归 | 结果与证据 |
| --- | --- |
| Go 全仓 | 715 项通过、0 失败、60 项外部环境依赖跳过（`internal/admin` 9、`internal/ingest` 51）；本轮新增 33 项安全与适配器测试事件；`TestNoOutOfScopeArtifacts` 通过；`internal/buildingenergy` 保持纯标准库零外部依赖；Go Vet 与 Go Build 退出码 0。 [Go 测试](../../evidence/go-test.jsonl) · [Vet](../../evidence/go-vet.txt) · [Go 构建](../../evidence/go-build.txt) |
| SQL 离线解析 | `pglast v8.8`（`libpg_query PG 18.6`）通过 4 份 DDL 变体与 18 条适配器 SQL 静态解析。 [SQL 解析日志](../../evidence/sql-parse.txt) |
| 前端与既有预览 | 本轮未修改前端及 `internal/ui/dist`；`tsc --noEmit` 退出码 0；复核既有标准 Vite 构建与全部 Playwright 回归证据（保留五父级、25 子项导航、16 条旧技术路由与原 `dev` 工作区未修改）。 [前端构建](../../evidence/web-build.txt) |

## 上轮 F22 与 F25

F22 从已验证的 F11/F12/F13 Go 输出生成固定列报表。报表保留项目、对象、能源、自然日及 IANA 时区、分子、分母、指标值、质量、规则来源和版本。样本规则的适用性由 Go 判定。内部分期目标、外部定额标准和演示阈值分别列出。页面和 CSV 均显示「演示规则，非权威标准」。无规则、不适用、分母缺失或覆盖不足时保留原因，正式考核结果留空。

F25 支持医院、学校、酒店、办公楼模板和字段、排序白名单。快照根据单一授权项目、建筑、能源和样本自然日生成。范围不匹配时拒绝，范围内缺少模板时展示空状态。字段和排序输入仅用于投影现有 Go 结果。能力边界为只读合成预览和 CSV，自定义表达式、模板持久化、共享和定时执行未开放。

只读 `GET /admin/v1/analytics/building-energy/preview/industry-reports` 按合成项目、建筑、时间和能源范围独立检查读取和导出能力。Go 一次性生成字段定义、行、来源、版本和 CSV，沿用 F21 的 UTF-8 BOM 和 CRLF 编码。F25 的必选列保留统计边界、质量、缺失率、生成/截止时间、版本、报表编号、操作人和来源点位数。按指标值排序时先按单位分组。左侧树仍为五父级、25 子项，本轮启用两个既有槽位，合计 19 个可用入口。 [合成 API](../../cmd/energy-preview/industry_reports.go) · [报表投影](../../cmd/energy-preview/industry_report_projection.go) · [报表页面](../../web/src/pages/BuildingEnergyIndustryReportsPage.tsx)

本轮由 Codex 启动并在 F22/F25 核心投影、只读接口、初版页面及交接草稿完成后因额度中断；由 Antigravity 接手补完越权字段/无效规则/缺失分母/AT-04 70% 低覆盖与全缺测/F22 空结果测试（新增 49 个 Go 测试事件与浏览器拒绝断言）、桌面列宽密度优化与 PRD §8.2 固定报表快照页脚（生成时间、数据截止、统计时区、口径版本、来源点位、质量与缺失率、报表编号、操作人）、导航 `motion-reduce:transition-none` 修正、测试脚本重置提示匹配修复，以及全仓构建、Playwright 回归、四档视口视觉核验和最终 `energy-analysis-handoff.zip` 打包交付。

| 验证 | 结果与证据 |
| --- | --- |
| Go | 全仓测试和子测试 682 项通过、0 失败。60 项依赖外部环境的既有用例跳过，admin 9 项、ingest 51 项。本轮 F22/F25 合计 108 个通过事件（接手前 59 项 + 补完 49 项）。Go Vet 和 Go 构建通过。 [测试](../../evidence/go-test.jsonl) · [Vet](../../evidence/go-vet.txt) · [Go 构建](../../evidence/go-build.txt) |
| 合成快照 | 医院床日 3,072 kWh ÷ 128 床日 = 24 kWh/床日，演示阈值 25。收入指标为 128 kWh/万元，暂无演示规则。学校、酒店和办公楼分别核对 3.2 kWh/人、16 kWh/间夜、1.28 kWh/m² 与 16 kWh/人（办公楼 B 缺人数保留空白）。酒店完整计量为零时保留数值 0，缺分母、低覆盖或全缺测时指标留空。 [F22 样本](../../evidence/report-f22.csv) · [F25 样本](../../evidence/report-f25.csv) |
| 前端及回归 | TypeScript 与 Vite 标准构建通过，保持 `internal/ui/dist` 未修改。校验 16 条显式旧技术路由，标准产物无建筑能耗工作区及合成预览路径。F05/F09/F10/F11/F12/F13/F14/F17/F21 及导航回归全部通过，F21 与 F22/F25 实际下载均与原快照逐字节一致。 [前端构建](../../evidence/web-build.txt) · [导航](../../evidence/navigation-playwright.txt) · [F09](../../evidence/f09-playwright.txt) · [F10/F17](../../evidence/anomalies-playwright.txt) · [F11/F21](../../evidence/f11-f21-playwright.txt) · [F12/F13](../../evidence/industry-playwright.txt) · [F05/F14](../../evidence/targets-playwright.txt) |
| 性能 | 本地隔离预览（经 `127.0.0.1:5176` 代理）各 20 次串行热请求，F22 min 1.42 ms、中位数 1.68 ms、p95 2.06 ms、最大 2.22 ms；F25 min 1.39 ms、中位数 1.53 ms、p95 2.02 ms、最大 2.07 ms。Vite 标准主包 623.14 kB（gzip 182.62 kB），仍报告既有的 500 kB 阈值警告。 [性能记录](../../evidence/industry-reports-performance.txt) |

依据 `FRONTEND_VISUAL_BASELINE.md` 与已安装的 `design-taste-frontend` 规范核查表格列宽密度、数字 `tabular-nums` 对齐、报表页脚完整性、长名称/大数值及手机明细可读性。Playwright 使用 Chromium 检查四档视口的浅深主题、键盘筛选与焦点返回、列顺序和排序设置、越权字段/表达式/跨项目拒绝、有效零、低覆盖/缺测/缺失分母及无适用模板。复跑浏览器检查使用 `python -B scripts/test_energy_industry_reports.py`。 [复跑脚本](../../scripts/test_energy_industry_reports.py) · [浏览器日志](../../evidence/industry-reports-playwright.txt)

- [F22 桌面 1920px](../../evidence/screenshots/reports-f22-1920-light.png) · [F22 平板 768px 覆盖不足](../../evidence/screenshots/reports-f22-768-dark.png)
- [F25 桌面 1440px 长名称与大数值](../../evidence/screenshots/reports-f25-1440-light.png) · [F25 手机 390px 有效零](../../evidence/screenshots/reports-f25-390-dark.png) · [缺分母](../../evidence/screenshots/reports-f25-390-dark-missing.png) · [无适用模板](../../evidence/screenshots/reports-f25-390-dark-empty.png)

**Go** 合成预览和本地 CSV 验收。**No-Go** 正式报表、权威标准、真实数据接入及数据库实施。M1B-01 权威目录、M1B-02 可信授权、M1B-03 计量来源、M1B-04 DBA 继续未签认。隔离数据库和真实上游均未运行验证。

## 上轮 F12 与 F13

F12 按版本化模板选择适用建筑和能源，通过同一公式处理床日、已售间夜、收入、学生人数和办公楼等分母。与 F11 复用 Go 能耗分子、计量证据和质量口径。业务累计分母检查统计范围与同窗有效性，时点或稳定分母校验生效区间覆盖。分母缺失、无效、跨期或分子质量不足时保留原因和可查证据。覆盖完整的实测零仍输出零。 [纯计算](../../internal/buildingenergy/industry.go) · [合成 API](../../cmd/energy-preview/industry.go)

F13 的规则记录来源、编制者、编号和版本、适用地区及建筑类型、指标模板版本、单位、有效期、统计周期和业务条件。演示规则限于合成预览，合格时仅输出实测指标与演示阈值的数值关系。不适用、无标准、不可比较和数据不足分别注明原因，阈值和差额字段留空。现有 F14 内部用能目标单列入口，不参与指标阈值比较。权威外部标准与正式考核分列，仍待接入和核验。 [共用前端](../../web/src/pages/BuildingEnergyIndustryPage.tsx)

F12/F13 复用现有五父级、25 子项导航，共 17 个可用入口。两页共用只读 `GET /admin/v1/analytics/building-energy/preview/industry`，选择建筑、能源和样本情景。同组入口保留筛选，转入不支持行业专用样本的旧页面时提示恢复办公楼范围。行业专用医院、学校和酒店以独立合成项目 `f12-industry-demo` 划分计量边界，办公楼仍使用原 F11 合成样本。

| 合成样本 | 结果 |
| --- | --- |
| 办公楼 A 用电 | F11 与 F12 分子 1,536 kWh，结果 1.28 kWh/m² 和 16 kWh/人，分母、范围、计量来源对账一致。 |
| 医院床日、业务收入 | 3,072 kWh ÷ 128 床日 = 24 kWh/床日，演示阈值 25 kWh/床日。240,000 元折算为 24 万元，收入指标为 128 kWh/万元，暂无对应演示规则。 |
| 学校学生人数 | 768 kWh ÷ 240 人 = 3.2 kWh/人，超过样本阈值 3 kWh/人。 |
| 酒店已售间夜 | 1,152 kWh ÷ 72 间夜 = 16 kWh/间夜。缺少间夜分母时不可计算。完整计量为零时指标为 0。 |

| 验证 | 结果与证据 |
| --- | --- |
| Go 全仓 | 574 个通过事件、0 失败、60 个外部环境用例跳过。新增 47 个纯计算事件、24 个 API 事件，覆盖对象、单位、累计及快照分母有效期、完整计量、有效零、规则适用条件及历史版本快照。 [Go 日志](../../evidence/go-test.jsonl) |
| Playwright F12/F13 | 3 组检查覆盖指标与 F11 对账、分母缺失、有效零、无演示规则、不适用、不可比较、覆盖不足、来源抽屉和跨页筛选。1920/1440/768/390px 浅深主题与键盘焦点验收。 [复跑脚本](../../scripts/test_energy_industry.py) · [日志](../../evidence/industry-playwright.txt) |
| 回归 | F05–F11、F14、F17、F21 通过。上一交付包中的 42 个计算及预览 Go 文件逐字节一致，预览程序入口仅增加路由注册。 [导航](../../evidence/navigation-playwright.txt) · [F05/F14](../../evidence/targets-playwright.txt) · [F09](../../evidence/f09-playwright.txt) · [F10/F17](../../evidence/anomalies-playwright.txt) · [F11/F21](../../evidence/f11-f21-playwright.txt) |
| 静态检查与构建 | Go Vet、Go 构建、TypeScript 与 Vite 标准构建通过。标准产物保留 16 条显式技术路由，合成预览入口继续隔离。 [Vet](../../evidence/go-vet.txt) · [Go 构建](../../evidence/go-build.txt) · [前端构建](../../evidence/web-build.txt) |

使用 Taste Skill 核查信息层级、对齐和数字可读性。启动隔离预览后可运行 `python scripts/test_energy_industry.py`，设 `ENERGY_INDUSTRY_EVIDENCE_DIR` 保存视觉证据。

- [F12 桌面指标模板](../../evidence/screenshots/industry-f12-1920-light.png) · [F12 手机缺少分母](../../evidence/screenshots/industry-f12-390-dark-missing.png)
- [F13 桌面演示对标](../../evidence/screenshots/industry-f13-1440-light.png) · [F13 平板不适用](../../evidence/screenshots/industry-f13-768-dark-inapplicable.png) · [F13 手机有效零](../../evidence/screenshots/industry-f13-390-dark-zero.png)

**Go** 合成指标及演示对标预览。**No-Go** 正式标准接入、正式考核、真实数据接入与数据库实施。真实财务和经营分母、外部标准发布与核查流程均待签认。M1B-01 至 M1B-04 继续未签认。

## 上轮 F10 与 F17

F10 使用已有 Go 统计证据。F06 提供带覆盖率的分类计量结果，F08 提供自然周期与比较口径，F07 的区域排名同时供现有 F16 和 F17 使用。F09 使用独立合成项目，差额不拼入本轮统计。F10 和 F17 共用只读 `GET /admin/v1/analytics/building-energy/preview/anomalies`，选择建筑、能源和合成场景。页内可查当前及历史值、阈值版本、通道、计量版本、归属代次与覆盖质量。两页进入原导航中的 F10、F17，现有 15 个可用入口和 10 个未开放项。 [纯计算](../../internal/buildingenergy/anomalies.go) · [合成 API](../../cmd/energy-preview/anomalies.go) · [前端](../../web/src/pages/BuildingEnergyAnomaliesPage.tsx)

合成规则按项目、对象、能源、有效期与版本配置。当前样本采用完整同星期自然日，以至少三个合格历史样本的中位数为基线。相对偏差与绝对增量须同时达到该范围的阈值。办公楼 A 用电示例阈值为 60% 与 120 kWh，用水阈值为 40% 与 30 m³。比较检查项目时区、周期、计量范围、数据截点和口径版本。规则和阈值仅用于合成预览。

| 结果层级 | 显示及含义 |
| --- | --- |
| 异常候选 | 同窗条件、覆盖和历史基线满足规则的用量观察。办公楼 A 样本为 1,536 kWh 对历史中位数 384 kWh，增加 1,152 kWh（300%）。 |
| 计量数据异常 | 缺测、可疑或部分覆盖有独立计量质量状态。用量判定返回「证据不足」，比较率留空。 |
| 无异常候选或证据不足 | 有效实测零仍为零。完整、低于门槛的样本显示未发现候选。不可比、零基线或无规则给出证据不足。 |
| 待人工核查 | F17 根据合格用量候选或同口径高耗能观察生成线索，分别列出已有证据、局限与待确认项。办公楼 A 同日东区为 1,056 kWh，来源限于该区域已授权证据。无适用 F10 规则时，独立成立的 F16 观察仍可提供核查线索。 |
| 已确认异常 | 属于正式人工复核流程。本轮预览不生成确认状态、节能量、节能率、收益、回收期或设备操作。 |

| 验证 | 结果与证据 |
| --- | --- |
| Go 全仓测试 | 503 个通过事件、0 失败、60 个外部环境用例跳过。F10/F17 新增 40 个纯计算事件和 21 个 API 事件，覆盖日期与口径、同边界计量、双阈值、质量抑制、来源对账及独立区域观察。 [Go 日志](../../evidence/go-test.jsonl) |
| Playwright F10/F17 | 3 组检查通过。验证标准值和历史中位数、缺测、可疑、部分覆盖、有效零、无候选、不可比、零基线、无规则、有无人工核查线索、来源抽屉及跨页筛选。 [复跑脚本](../../scripts/test_energy_anomalies.py) · [日志](../../evidence/anomalies-playwright.txt) |
| 回归 | F05/F14 目标分解、F06–F08 导航与深链接、F09 样本比较、F11 指标和 F21 导出均通过。F09 六个源码与测试文件和上一交付包逐字节一致。 [导航](../../evidence/navigation-playwright.txt) · [目标](../../evidence/targets-playwright.txt) · [F09](../../evidence/f09-playwright.txt) · [F11/F21](../../evidence/f11-f21-playwright.txt) |
| 静态检查和构建 | 全仓 Go Vet、Go 构建、TypeScript 和 Vite 标准构建通过。标准产物保留 16 条显式技术路由，合成预览入口继续隔离。 [Vet](../../evidence/go-vet.txt) · [Go 构建](../../evidence/go-build.txt) · [前端构建](../../evidence/web-build.txt) |

使用 Taste Skill 检查信息层级、状态文字和数值可读性。Playwright 验证 1920、1440、768、390px 的浅深主题、键盘与焦点恢复。启动隔离预览后运行 `python scripts/test_energy_anomalies.py`。设 `ENERGY_ANOMALY_EVIDENCE_DIR` 可输出截图。

- [F10 桌面异常候选](../../evidence/screenshots/anomalies-f10-1920-light.png) · [F10 手机深色计量可疑](../../evidence/screenshots/anomalies-f10-390-dark-suspect.png)
- [F17 桌面核查线索](../../evidence/screenshots/anomalies-f17-1440-light.png) · [F17 平板深色](../../evidence/screenshots/anomalies-f17-768-dark.png)

**Go** 继续合成预览。**No-Go** 正式诊断、真实数据接入及数据库实施。生产规则发布、真实节假日与工况、人工确认流程以及可量化节能核验尚未开放，M1B-01 至 M1B-04 仍待签认。

## 上轮 F05 与 F14

F05 挂在「能耗总览」，F14 挂在「能效指标与考核」。沿用现有五父级、25 子项结构，现有 13 个可用入口。两个目标页面保留兼容的建筑、能源和年度，复用筛选抽屉、主题和计量范围表达。总览链接进入当前建筑与能源的目标摘要。 [目标页面](../../web/src/pages/BuildingEnergyTargetsPage.tsx) · [现有导航配置](../../web/src/components/BuildingEnergyNavigation.tsx)

目标契约绑定项目、对象、能源、标准单位、项目时区、自然年度、版本及生效区间。线性与月度权重分解生成 12 个自然月目标，按千分之一单位以最大余数法精确分配，余数并列时按月份顺序分配。拒绝同范围重叠版本。选定版本仅覆盖年度一部分时，只对完整落在有效区间内的月展示目标。目标版本选择时点、版本来源及生效区间与计量截点分别记录。比较实际用量由现有 Go 分类统计提供，使用对应的完整自然年或自然月。只提供有依据的「用量 / 目标」比值，覆盖不全、统计期未结束、无有效目标或目标为零时给出状态。 [纯计算](../../internal/buildingenergy/targets.go) · [样本 API](../../cmd/energy-preview/targets.go)

| 合成样本校验 | 预期 |
| --- | --- |
| 办公楼 A 2025 年用电 | 完整年 221,520 / 240,000 kWh = 92.3%，线性月目标各 20,000 kWh。 |
| 办公楼 A 2026 年用电 | 年度目标 360,000 kWh。九月 25,200 / 32,400 kWh ≈ 77.78%。十月有 72 个缺测和 72 个可疑表小时，可用量 26,880 kWh，覆盖率约 93.5%，不显示比值。年度截至 11 月 1 日尚未闭合。 |
| 办公楼 A 2026 年用水 | 年度目标 90,000 m³，线性月目标各 7,500 m³。 |
| 办公楼 B | 当前没有有效目标，计量证据与配置状态分别显示。 |

| 验证 | 结果与复跑材料 |
| --- | --- |
| Go 全仓测试 | 442 个通过事件、0 失败、60 个外部环境依赖用例跳过。新增 45 个纯计算事件及 13 个预览 API 事件，覆盖分期守恒、生效边界、夏令时与闰年、缺测、部分覆盖、范围不符和零目标。 [Go 日志](../../evidence/go-test.jsonl) |
| Playwright 新功能 | 3/3 检查组通过。验证 F05 年度合计、F14 月度分解、无目标、溯源、路由与年筛选，以及 1920、1440、768、390px 的浅深主题和键盘焦点。 [目标测试](../../scripts/test_energy_targets.py) · [日志](../../evidence/targets-playwright.txt) |
| 既有功能 | F06–F08 筛选与锚点回归、F09 现场样本交互与计量明细、F11/F21 计算与导出均通过。F09 六个源文件和测试文件与上一轮包逐字节一致。 [导航回归](../../evidence/navigation-playwright.txt) · [F09](../../evidence/f09-playwright.txt) · [F11/F21](../../evidence/f11-f21-playwright.txt) |
| 静态检查与构建 | 全仓 Go Vet 与 Go 构建、TypeScript 与 Vite 标准构建通过。标准产物保留 16 条显式技术路由，预览页面和 API 仍在隔离配置。 [Vet](../../evidence/go-vet.txt) · [Go 构建](../../evidence/go-build.txt) · [前端构建](../../evidence/web-build.txt) |

隔离预览已启动时运行 `python scripts/test_energy_targets.py`，用 `ENERGY_TARGET_EVIDENCE_DIR` 指定截图输出目录。视觉检查应用 Taste Skill 的信息层级、对齐和数值可读性，沿用已有主题设计。

- [F05 桌面年度摘要](../../evidence/screenshots/targets-f05-1920-light.png)
- [F14 桌面月度分解](../../evidence/screenshots/targets-f14-1440-light.png) · [F14 平板深色](../../evidence/screenshots/targets-f14-768-dark.png)
- [F14 手机用水](../../evidence/screenshots/targets-f14-390-dark-linear.png) · [F14 手机无配置](../../evidence/screenshots/targets-f14-390-dark-unconfigured.png)

**Go** 合成目标预览。**No-Go** 正式发布、真实数据接入与数据库实施。当前为只读合成配置样本，目标录入、审批和持久化留待正式契约及权限签认，M1B-01 至 M1B-04 保持未签认。

## 上轮左侧导航与 F09

- 桌面固定侧栏和手机 Radix 抽屉共用五父级、25 子项定义。11 个可用入口、14 个不可交互的未开放条目。父级用按钮和 `aria-expanded` 表达展开状态，子项用链接和 `aria-current` 表达当前路由。内容区保留一个页面标题和统计上下文。折叠组不影响其他父级，移动端选择后关闭并聚焦导航按钮。 [树形导航](../../web/src/components/BuildingEnergyNavigation.tsx) · [AppShell](../../web/src/components/AppShell.tsx)
- 保留 F06 旧路径、F07/F08 地址及原查询口径。旧 `#category-tree` 会定位到当前分类明细，即使数据异步到达；常用兼容条件随树形切换和返回按钮传递。`全部建筑`、不兼容的时间范围和样本场景回到目的页默认值并给出可见提示。F02/F03/F04 增加独立书签，使用现有总览的组成、对象及历史数据。 [路由](../../web/src/App.tsx) · [内容容器](../../web/src/pages/BuildingEnergyWorkspace.tsx)
- F09 拓扑为独立合成项目 `f09-demo`，版本 `f09-topology-v1`、依据 `synthetic-single-feed-east-west-common-v1`，项目时区 `Asia/Shanghai`，有效期为 2026-10-01 00:00 至 2026-11-01 00:00，结束点不计入。每栋楼、每种能源独立定义一只进线总表，供给东区分表、西区分表及未装分表的公共支路。样本声明三支路互斥且无旁路、跨接、储能或本地补给。拓扑先于表码生成，计量通道与区域分别建模。 [合成服务](../../cmd/energy-preview/balance.go)
- 计算限定在一棵已确认版本的总表及直接分表，检查同项目、对象、能源、标准单位、有效期、目录归属、通道、映射代次及分表物理区域互斥。M1-A 生成累计量区间证据，M1-B 验证范围和归属，M1-C 校验区间来源与质量。两个层级分别汇总，差额使用总表减分表合计。差额保留符号，成因待核查。覆盖不全时只保留可用的单表数据，时段不一致的点位不投影不匹配的用量和质量。页面使用该 Go 结果渲染柱、表及来源。F09 合成计量与 F06–F08 预览样本口径不同。 [纯计算](../../internal/buildingenergy/balance.go) · [预览页](../../web/src/pages/BuildingEnergyBalancePage.tsx)

| F09 实测样本 | Go 与页面结果 |
| --- | --- |
| AT-02，办公楼 A 自然日用电 | 总表 100 kWh，东区 70，西区 20，分表合计 90，待核查差额 +10 kWh，差额率 10%，三表各 24/24。 |
| 负差额 | 总表 100，分表 85 + 20 = 105 kWh。差额 −5 kWh，提示需核对范围、倍率和读数。 |
| 缺测、可疑、换表 | 可用期分别为 22/24、22/24、23/24，缺少完整分表合计时差额为空。 |
| 时段不一致 | 东区观测窗 23 小时，查询窗 24 小时。东区用量、覆盖率、分表合计及差额为空，来源仍可核对时段与版本。 |
| 办公楼 B 用水末 7 天 | 总表 560、分表合计 504、差额 +56 m³，单位单列。 |

### 本轮验收

| 检查 | 结果及证据 |
| --- | --- |
| Go 全仓 | 384 通过、0 失败、60 环境依赖用例跳过。F09 新增 50 个测试事件，含 7 主测试和 43 子用例。 [日志](../../evidence/go-test.jsonl) |
| 静态检查与构建 | Go Vet、全仓 Go 构建、TypeScript 和 Vite 标准构建通过。16 条显式旧技术路由保留，产物不包含建筑能耗预览路由或 API。 [Vet](../../evidence/go-vet.txt) · [Go 构建](../../evidence/go-build.txt) · [前端](../../evidence/web-build.txt) |
| Playwright 导航 | 7/7 检查组通过，包含 11 个有效路径、5 个父级、25 个子项、F06 锚点实际滚动、F07/F08 深链接及返回、旧终端导航、1920/1440/768/390px 浅深主题。检查 Chromium 可访问角色、状态、键盘操作和焦点，无单独屏幕阅读器测试。 [复跑脚本](../../scripts/test_energy_navigation.py) · [日志](../../evidence/navigation-playwright.txt) |
| Playwright 样本 | F09 Go 数据、图形、表格和计量来源逐项核对，覆盖负差额、缺测、可疑、换表和时窗不一致。F11/F21 下载和计量回归通过。 [F09 日志](../../evidence/f09-playwright.txt) · [F09 复跑](../../evidence/f09-playwright.py) · [F11/F21 日志](../../evidence/f11-f21-playwright.txt) |

Playwright 导航复跑在隔离服务已启动时执行 `python scripts/test_energy_navigation.py`，可设 `ENERGY_NAV_EVIDENCE_DIR` 保存截图。F09 脚本通过 `BALANCE_QA_ARTIFACTS` 指定截图目录。采用 Product Design 截图审查、Taste Skill 后台排版、Superpowers 工程验证和独立代码审查；页面使用现有 AppShell、主题和组件。

| 视觉证据 | 截图 |
| --- | --- |
| 树形导航桌面 | [1920 浅色](../../evidence/screenshots/nav-tree-1920-light.png) · [1440 深色](../../evidence/screenshots/nav-tree-1440-dark.png) |
| 导航抽屉 | [768 深色](../../evidence/screenshots/nav-tree-768-dark.png) · [390 浅色](../../evidence/screenshots/nav-tree-390-light.png) |
| F09 业务结果 | [1440 标准样本](../../evidence/screenshots/f09-balance-standard-1440.jpg) · [390 时窗不一致](../../evidence/screenshots/f09-balance-mismatch-dark-390.jpg) · [390 用水](../../evidence/screenshots/f09-balance-water-dark-390.jpg) |

**Go** 隔离预览继续验收。**No-Go** 真实数据接入、数据库实施、跨多层拓扑汇总及真实差额归因。M1B-01 权威目录、M1B-02 可信授权、M1B-03 计量来源、M1B-04 DBA 仍未关闭。真实拓扑、上游接口、隔离数据库本轮各验证 0 项。原 `dev` 工作区和其两份规范保持调研前指纹。清单、补丁及证据统一收入 ZIP。

## 上轮五业务栏目

- 独立预览侧栏为一个「建筑能耗监测分析」入口。五个业务栏目在共用工作区内切换，每页保留一个屏幕阅读器标题。统计分析保留分类分项、多维统计、趋势与对比三个子页签，以及 F06 旧路径、F07/F08 深链接。跨页保留可表达的建筑、能源、统计周期，不兼容的时间粒度或范围回到目标页可见默认值。技术配置和路由独立保留。 [共享工作区](../../web/src/pages/BuildingEnergyWorkspace.tsx) · [路由](../../web/src/App.tsx)
- 总览分别显示电和水，复用 F06 分类和计量来源、F07 三种对象视角与 F08 同窗口自然日历史。组合结果匹配时间、单位、计量及分类、目录和拓扑版本。7/14/30 天按同窗 F06 展示历史。年度目标显示未配置。旧总览的粒度参数仍指向单表时段明细。 [总览](../../web/src/pages/BuildingEnergyPreviewPage.tsx)
- 能效 `GET /admin/v1/analytics/building-energy/preview/efficiency` 用授权后的 F06 同窗用量作为分子。合成面积和日均人数均有有效期和来源版本。分母缺失、无效、跨期或用量覆盖率不足时，保留原因和可用证据。 [Go 实现](../../cmd/energy-preview/efficiency.go) · [能效页](../../web/src/pages/BuildingEnergyEfficiencyPage.tsx)
- 诊断沿用 F07 的区域对比图和来源明细。只有至少两个合格的可比对象且最高用量为正值时才展示高用量观察，保留并列。部分覆盖、未排名和原因仍可查。改造评估列出缺少的基线、工况、成本或核验证据。 [诊断页](../../web/src/pages/BuildingEnergyDiagnosisPage.tsx)
- 报表 `GET /admin/v1/analytics/building-energy/preview/reports` 支持分类分项与逐日汇总。Go 生成同一快照的 JSON 和 UTF-8 BOM CSV，两种格式携带期间、单位、口径、质量和生成信息。读与导出分别校验合成主体及同一项目、建筑、点位、窗口。下载使用该快照的 CSV 字节。 [Go 实现](../../cmd/energy-preview/reports.go) · [报表页](../../web/src/pages/BuildingEnergyReportsPage.tsx)

### F01–F25 交付状态

| 功能 | 状态 | 范围与缺口 |
| --- | --- | --- |
| F01 综合能耗概览 | 部分完成，上游阻塞 | 电水分列。综合折标待有效系数和折算范围。 |
| F02 能源分类构成 | 合成样本，测试通过 | 用电与用水独立，分类占比使用 Go 结果。 |
| F03 分区分项概览 | 合成样本，测试通过 | 建筑、区域、组织切换，保留分类树路径。 |
| F04 实时及历史趋势 | 部分完成，上游阻塞 | 历史及同窗自然日对比已验，实时计量待接入。 |
| F05 年度目标摘要 | 合成样本 | 可查版本化目标和可比的年度用量，未配置及不完整覆盖分别显示。 |
| F06 分类分项统计 | 合成样本，回归通过 | 树、构成、趋势、质量、来源。 |
| F07 多维统计 | 合成样本，回归通过 | 建筑、区域、组织，保留图表优先和键盘下钻。 |
| F08 趋势、同环比、排名 | 合成样本，回归通过 | 逐时/日/月及同口径排名。完整日切换传递统计窗。 |
| F09 能流与总分表平衡 | 合成样本，测试通过 | 独立合成拓扑已证明分表互斥，可查总表与直接分表、差额、数据质量和证据。真实拓扑仍待签认。 |
| F10 异常浪费识别 | 合成样本 | 版本化基线上升候选、计量质量异常及证据不足，待正式规则与人工确认。 |
| F11 通用能效强度 | 合成样本，测试通过 | 单位面积、人均与计量/分母证据，正式分母档案待签认。 |
| F12 行业指标模板 | 合成样本 | 版本化分母与按行业应用的指标计算，真实业务分母待接入。 |
| F13 定额标准对标 | 合成样本 | 明示适用性和演示阈值比较，真实外部标准与正式考核待签认。 |
| F14 目标与分期分解 | 合成样本 | 线性与权重分解、12 期核对、同窗计量证据。正式配置和审批留待签认。 |
| F15 组织/区域考核 | 尚未实现 | 待授权、分摊和正式考核口径。 |
| F16 高耗能对象识别 | 部分完成，合成样本测试通过 | 区域同口径总耗观察及证据。强度、相似偏差和长期基载排序待实施。 |
| F17 节能机会分析 | 合成样本 | 按合格用量候选或高耗能观察提供待核查线索，正式诊断与核验仍待业务证据。 |
| F18 改造前基线 | 上游阻塞 | 待历史工况、天气和业务活动量。 |
| F19 潜力与收益测算 | 上游阻塞 | 待基线、方案与成本。 |
| F20 改造后节能核验 | 上游阻塞 | 待改造后计量与核验证据。 |
| F21 综合与分类分项报表 | 合成样本，测试通过 | 按能源分列的逐日与分类报表，预览与 CSV 一致。综合折标受 F01 限制。 |
| F22 指标与定额报表 | 合成样本 | 固定字段快照与 CSV，独立显示演示规则、内部目标及外部标准状态。权威标准与正式指标档案待接入。 |
| F23 组织考核报表 | 尚未实现 | 待正式考核。 |
| F24 诊断/改造成效报告 | 尚未实现 | 待评估及核验。 |
| F25 行业专项/自定义报表 | 合成样本 | 四类行业、字段及排序白名单、同快照 CSV。模板持久化、共享及定时任务未开放。 |

### 验证与证据

| 项目 | 结果 |
| --- | --- |
| 全仓 Go `go test -json ./... -count=1` | 334 通过、0 失败、60 环境依赖用例跳过，admin 9 条、ingest 51 条。新增 F11/F21 6 主测试及 14 子用例。 |
| Go 静态检查和构建 | `go vet ./...`、`go build ./...` 通过。 |
| 前端 | `tsc --noEmit`、Vite 标准构建通过。产物保留 16 条技术路径，预览端点仅限隔离配置。既有大包告警仍在。 |
| F01–F08 | 办公楼 A 的自然日用电 1,536 kWh、用水 384 m³。F06/F07 同窗对象合计对账 8/8 相符。旧 F06 用水链接保留参数与锚点并显示 5,376 m³。F08 环比 +33.3%、同比 +60%，缺测场景隐藏对应比较率。 |
| F11 | 办公楼 A 1,536 kWh ÷ 1,200 m² = 1.28 kWh/m²，÷ 96 人 = 16 kWh/人。办公楼 B 无人数记录，7 天场景覆盖不完整，均保留独立状态。 |
| F16 | 同日两区域 1,056 与 480 kWh，保留排名证据与来源。末 7 日低覆盖不生成观察。 |
| F21 | 两种真实浏览器下载与 API CSV 内容逐字节相同，解析后与合计和表格值一致。7 天用水为 960 m³，缺测及可疑用量空白，有效零保存。测试专用样本覆盖空结果及 503 重试。 |

按 1920、1440、768、390px 检查五页、选中状态和横向溢出。检查浅深主题、键盘切换、抽屉 Escape 及焦点恢复。390px 中 F07 首个业务结果仍为比较图。旧终端直接路径及技术导航可用，技术数据读写未接入隔离服务。验收采用 Product Design 视觉审查、Taste Skill 排版与信息密度规则、Playwright 浏览器检查、Superpowers 测试与独立代码审查。Caveman 用于精简输出。GitHub 只读提交工具参数绑定失败，改用本地 Git 审查基线和差异。 [浏览器矩阵](../../evidence/browser-qa.json) · [下载和能效验证](../../evidence/f11-f21-playwright.txt) · [Go 测试](../../evidence/go-test.jsonl) · [构建](../../evidence/web-build.txt)

| 页与场景 | 截图 |
| --- | --- |
| 总览 1920px 与手机部分覆盖 | [桌面](../../evidence/screenshots/five-overview-1920.jpg) · [手机](../../evidence/screenshots/five-overview-quality-390.jpg) |
| 统计 F07 深色手机 | [对比图与页签](../../evidence/screenshots/five-f07-dark-390.jpg) |
| 能效常规与缺分母 | [桌面](../../evidence/screenshots/five-efficiency-1440.jpg) · [手机深色](../../evidence/screenshots/five-efficiency-missing-dark-390.jpg) |
| 诊断常规与部分覆盖 | [桌面](../../evidence/screenshots/five-diagnosis-1440.jpg) · [平板深色](../../evidence/screenshots/five-diagnosis-quality-dark-768.jpg) |
| 报表分类与逐日质量 | [桌面](../../evidence/screenshots/five-report-1920.jpg) · [平板](../../evidence/screenshots/five-report-quality-768.jpg) · [手机](../../evidence/screenshots/five-report-quality-390.jpg) |

复跑下载验证时启动隔离预览，使用已安装 Chromium 的 Python Playwright，设置 `FIVE_REPORT_QA_ARTIFACTS` 和 `FIVE_REPORT_QA_SCREENS` 为可写输出目录，再运行包内脚本。 [Playwright 脚本](../../evidence/f11-f21-playwright.py)

**Go** 继续验收合成数据工作台。**No-Go** 正式数据接入、M1-B 数据库实施和正式指标、诊断、报表发布。M1B-01 权威目录、M1B-02 可信授权、M1B-03 计量来源、M1B-04 DBA 均待签认。隔离数据库和真实上游本轮各验证 0 项。原 `dev` 工作区差异指纹保持 `061f7ae20f62fff9af992b2103b9f479b95b907a`。修改文件与补丁见 ZIP 的清单。

## 上轮 F07 UI/UX 设计专项

Product Design 审查读取了 F06 分类树、F07 对象排名和 F08 趋势及各自截图。F07 桌面 1440px 区域视角下，六个等权重的筛选项分散注意力，表格缺少共同尺度，首条排名位于 y≈445px。390×844px 质量样本下，筛选区高 218px，首条排名位于 y≈573px。F06/F08 的来源抽屉、数值刻度和质量语义可复用。 [桌面实测](../../evidence/screenshots/ux-baseline-f07-desktop.jpg) · [手机实测](../../evidence/screenshots/ux-baseline-f07-mobile.jpg) · [F06 已验收页](../../evidence/screenshots/nav-02-statistics-light-1440.jpg) · [F08 已验收页](../../evidence/screenshots/f08-01-daily-light-1440.jpg)

两轮初稿收到三项反馈：左侧模块名在内容区重复、字体层级不合理、横向排行占据主展示。用户明确选择手机首屏先看对象差异。选定方向使用纵向柱图，侧栏负责模块定位，F07 工作区由二级页签、当前范围、筛选和结果组成。F07 的 H1 保留在无障碍树中。合成样本使用同一日的办公楼 B 东区 2,112、办公楼 A 东区 1,056、办公楼 B 西区 960 和办公楼 A 西区 480 kWh。 [桌面设计稿](../../evidence/screenshots/ux-f07-target-1440.png) · [手机设计稿](../../evidence/screenshots/ux-f07-target-390.png)

- 字体沿用项目 Inter/Noto Sans SC 与系统回退。柱顶量值 390px 为 14px、桌面为 16px，轴线和辅助信息用较小的中性色文字。图表与明细使用现有表面、边线、正色和警告 token。CSS 绘制基于零起点的同尺度数值柱，精确数值、排名和可比状态来自 API。
- 六个筛选收进 Radix 抽屉。工作区保留当前建筑范围、统计维度、能源、日期和筛选入口。草稿仅在「应用筛选」后写入 URL，取消保留当前结果，浏览器返回可恢复筛选。精确表格通过原生展开组件访问，柱形和对象名可直接打开计量来源，支持键盘和 Escape 焦点恢复。
- 部分覆盖对象在图下说明原因，并在明细保留用量和覆盖率；缺测留空，有效零保留 0 和基线标记。空列表、无可比对象、请求失败和重试有独立反馈。空列表、503、全零并列通过测试专用本地代理建模；部分覆盖与所有对象不具备可比条件使用原合成服务。 [F07 样板](../../web/src/pages/BuildingEnergyDimensionsPage.tsx) · [统计页签](../../web/src/pages/BuildingEnergyStatisticsLayout.tsx)

| 验收视口 | 样板实测 | 证据 |
| --- | --- | --- |
| 1440px 浅色 | 同一范围、数据和主题与设计稿并排核对。柱图是首个主要区块，0–2,400 kWh 刻度可读。首轮柱高偏短，增高绘图区并调大柱顶数值后复核。单位集中在图表标签，柱顶显示用量。 | [设计对照](../../evidence/screenshots/ux-f07-compare-1440.jpg) · [实拍](../../evidence/screenshots/ux-f07-built-1440.jpg) |
| 768px 深色 | 页签和条件保持紧凑，图轴、用量和对象名无截断，图表和展开入口保留同一信息顺序。 | [深色实拍](../../evidence/screenshots/ux-f07-built-dark-768.jpg) |
| 390px 浅色 | 实拍首个值约在 y=258px。四个对象名称按建筑和区域分行，无横向溢出。可用键盘展开精确表格，质量异常对象保留用量 1,496 kWh 与 98.6% 覆盖。 | [设计对照](../../evidence/screenshots/ux-f07-compare-390.jpg) · [实拍](../../evidence/screenshots/ux-f07-built-390.jpg) · [部分覆盖](../../evidence/screenshots/ux-f07-built-partial-390.jpg) |
| 390px 异常状态 | 空列表和 503 不绘制残留图表；筛选、重试可访问。两条有效零保留并列第一与明细标签。 | [空数据](../../evidence/screenshots/ux-f07-built-empty-390.jpg) · [请求失败](../../evidence/screenshots/ux-f07-built-error-390.jpg) |

验收使用浏览器无障碍树、键盘和同场景截图。独立视觉审查确认图表层级与可读性通过，触屏下的柱形点击提示较轻，设有明确的明细与计量来源入口。未使用独立屏幕阅读器做完整认证。F06 旧链接、锚点及 1,536 kWh 结果已在浏览器复核，F08 趋势维持同一例的 +33.3% 环比与 +60% 同比。共享截面的现有 API 各为 1,536 kWh。本轮提交样板相关 UI 与统一交付，沿用原有测试用例。

## 上轮 F07 多维统计与 F08 对象排名

「能耗统计分析」二级导航增加「多维统计」，展示当前视角的用量、覆盖率、状态、名次和来源抽屉。可按建筑、区域、组织及建筑范围筛选，能源支持用电和用水。自然小时、自然日、自然月沿用 F08 时间计划，日/月支持同期截至。样本末 7、14、30 天与 F06 共享同一窗口。F08 标准样本的「查看对象排名」带入建筑、能源和周期，打开建筑内区域排名。 [多维统计预览](http://127.0.0.1:5176/ui/a/energy/statistics/dimensions) · [统计分析视图](../../web/src/pages/BuildingEnergyStatisticsLayout.tsx)

- 样本 API `GET /admin/v1/analytics/building-energy/preview/dimensions` 先由 M1-B 校验项目、对象、点位和窗口，再由 M1-A 计算计量区间，按 M1-B 各视角的有效期归属交给 M1-C 汇总。每次查询只计算一个视角，跨视角用量会重述同一计量，不能相加。重复点位、重复通道、重叠有效期及重复分配沿用现有约束；样本的两个建筑来自明确定义的独立网络，联合选择使用合成拓扑 `combined-topology-v1`。
- 同一个授权视角、能源、窗口、单位和计算口径内，按 Go 计算用量降序排名。精确并列共享名次，后续名次跳号。只有计量归属在统计期内稳定且数据覆盖完整的对象参与。有效零保留数值；缺测、低覆盖、可疑数据和归属口径变化保留状态与证据，排名为空。范围外对象不出现在结果中，也不返回范围外用量或未分配的项目总量。
- 每行下钻展示该对象范围内的分配量、加权覆盖率、可见截止时点、目录/拓扑版本以及当前通道的计量/归属版本。前端按 API 顺序呈现，不重算用量与名次。来源输出不包含原始读数、原始记录标识和分摊权重。 [纯计算](../../internal/buildingenergy/dimensions.go) · [样本 API](../../cmd/energy-preview/dimensions.go) · [样本范围与授权](../../cmd/energy-preview/dimensions_fixture.go) · [多维统计页](../../web/src/pages/BuildingEnergyDimensionsPage.tsx)

| 合成样本 | 计算及验证结果 |
| --- | --- |
| 10 月 15 日用电，两栋建筑 | 建筑 1,536 + 3,072 kWh；区域 1,056 + 480 + 2,112 + 960 kWh；组织 3,024 + 1,584 kWh。三种视角分别对账为 4,608 kWh。 |
| 办公楼 A 用电，F06/F07/F08 | 三个 API 均为 1,536 kWh，72/72 个有效表小时；起止、可见截止、单位、时区、目录/拓扑、计量和分类版本一致。F06 增加单日对账样本，原筛选与旧链接保留。 |
| 办公楼 A 用水，F06/F07 | 末 14 天两页均为 2,688 m³，浏览器实测传递建筑、能源和窗口。 |
| 办公楼 A 用电，F07/F08 | 逐月同期截至两页均为 14,208 kWh，起止、可见截止和覆盖一致。完整月缺测和可疑各 72 表小时，两栋建筑均留空名次。 |
| 建筑质量异常场景 | 办公楼 A 显示可用量 1,496 kWh、覆盖率 98.6%（71/72）及一条可疑记录，无名次。办公楼 B 覆盖完整，显示 3,072 kWh 及第 1 名。 |

| 视口 | 浏览器验收 | 截图 |
| --- | --- | --- |
| 1440px 浅色 | 两个一级菜单、二级选中状态、建筑/区域筛选、四行区域排名。 | [区域排名](../../evidence/screenshots/f07-01-space-ranking-light-1440.jpg) |
| 768px 深色 | 组织统计、范围联动及主题切换；页面无横向溢出。 | [组织统计](../../evidence/screenshots/f07-02-organization-dark-768.jpg) |
| 390px 浅色 | 未排名文本完整，用户可用键盘打开来源及 Escape 恢复焦点。F06 旧链接保留锚点，F08 排名入口和返回路径带入相同条件。 | [质量与排名](../../evidence/screenshots/f07-03-quality-light-390.jpg) · [来源追溯](../../evidence/screenshots/f07-04-source-trace-390.jpg) |

当前按样本的同层对象进行排名。多级对象汇卷、归属配置写入及正式报表尚未实现。正式数据接入前须由上游确认每个对象的完整计量边界。当前覆盖率只表示选定点位的有效观测情况。真实大规模对象批量计算和性能未验证。 [纯计算测试](../../internal/buildingenergy/dimensions_test.go) · [三页 API 对账测试](../../cmd/energy-preview/dimensions_test.go)

## 上轮 F08 趋势与对比

F06 的标准路径及旧路径兼容跳转继续保留筛选和锚点。F08 仅在开发预览显示，样本以 2026 年 10 月 15 日、`Asia/Shanghai` 为固定锚点。可选择两栋合成建筑、用电/用水、逐时/逐日/逐月、部分周期及质量场景。 [F08 预览](http://127.0.0.1:5176/ui/a/energy/statistics/trend)

- Go 复用 M1-A 项目自然时间分桶及标准计量，使用 M1-B 对相应区间的对象和点位授权，复用 M1-C 相同对象、能源和聚合口径的结果。历史桶按顺序连接，区间前闭后开，逐时/逐日/逐月保留 24/14/12 个桶。部分周期只截断本期的最后一个桶。环比使用相邻自然周期，同比使用上年同一自然周期。日、月同期截至对齐当地日与小时，跨月短日、闰日和夏令时导致对齐变化时保留实际区间，标出调整。
- 比较固定三期统一的可见截止时点。自然周期实际时长不等或基期边界调整时，保留三期用量与覆盖率，差值和变化率为空。等时还要求相同项目、对象、能源、单位、分类/拓扑版本和来源、点位及计量规则、归属版本。当前无可用读数且无可疑读数时返回缺测，只有可疑读数或覆盖不完整时返回质量不足。口径不一致不显示差值或变化率。基期为有效零时保留差值，变化率为空。
- API 在本地样本服务注册 `GET /admin/v1/analytics/building-energy/preview/trend`，对全部五个筛选参数执行白名单验证。每期返回实际起止、经过的小时数、用量和单位、加权覆盖与质量、计量/分类规则及目录/拓扑版本。React 使用 Go 给出的统计和业务状态，下钻在现有 Radix 抽屉中呈现证据。 [纯计算](../../internal/buildingenergy/trend.go) · [样本 API](../../cmd/energy-preview/trend.go) · [趋势页](../../web/src/pages/BuildingEnergyTrendPage.tsx)

| 办公楼 A 合成用电 | 手算本期 / 环比基期 / 同比基期 | 样本实际 |
| --- | --- | --- |
| 逐时完整 | 64 / 48 / 40 kWh | 相符，环比 +33.3%，同比 +60% |
| 逐日完整 | 1,536 / 1,152 / 960 kWh | 相符，环比 +384 kWh（+33.3%），同比 +576 kWh（+60%） |
| 逐日同期截至 | 768 / 576 / 480 kWh，各 12 小时 | 相符，差值与变化率按同比、环比各自基期计算 |
| 逐月同期截至 | 14,208 / 12,432 / 8,880 kWh，各 348 小时 | 相符，环比 +1,776 kWh（+14.3%），同比 +5,328 kWh（+60%） |
| 逐月完整及质量样本 | 本期 26,880 kWh；本期/环比基期为 744/720 小时；本期缺测、可疑各 72 表小时 | 相符，环比周期不等，同比质量不足，两侧变化率为空 |
| 逐日零基期及口径变化 | 环比基期有效零时差值 1,536 kWh，口径变化时差值为空 | 相符，两种状态的变化率均为空 |

纯计算测试覆盖闰日对齐、跨年逐时、跨月日与月桶、重复的夏令时当地小时、23/25 小时自然日、23.5 小时的半小时夏令时自然日及未完成周期。遇到半小时时移且窗口端点无法落在本地整点时，逐时计划按 M1-A 的窗口规则拒绝。数据完全失去归属与已归属但全部缺测有独立测试。 [纯计算用例](../../internal/buildingenergy/trend_test.go) · [API 验收用例](../../cmd/energy-preview/trend_test.go)

| 浏览器视口 | 本轮验收 | 截图 |
| --- | --- | --- |
| 1440px 浅色 | 两个一级入口、二级选中状态，逐日同环比、逐时及键盘展开明细，带尾斜杠的深链接。 | [逐日](../../evidence/screenshots/f08-01-daily-light-1440.jpg) |
| 768px 深色 | 逐月完整状态、逐月同期截至、末桶虚线区分、依据抽屉和 Escape 焦点回归。 | [逐月同期](../../evidence/screenshots/f08-02-month-to-date-dark-768.jpg) |
| 390px 浅色 | 零基期、缺测、可疑和口径变化，页面无横向溢出。F06 旧链接、筛选、锚点、来源抽屉、切换视图和浏览器返回通过。 | [零基期](../../evidence/screenshots/f08-03-zero-baseline-light-390.jpg) · [对比依据](../../evidence/screenshots/f08-04-comparison-trace-390.jpg) |

## 上轮建筑能耗导航验收

隔离预览首页进入「能耗总览」。导航第二项「能耗统计分析」直接进入已实现的 F06 分类分项，旧 `/a/energy/classification` 链接保留查询参数与锚点后跳转至 `/a/energy/statistics`。能效指标与考核、节能诊断与改造评估、能耗报表保留在规划中，待功能可用后加入导航。 [预览首页](http://127.0.0.1:5176/ui/) · [能耗统计分析](http://127.0.0.1:5176/ui/a/energy/statistics)

共用 `AppShell`、React Router 和现有主题。建筑能耗路由使用 `navSections(meta, 'building-energy')`，技术管理路由继续从独立配置获得「业务」「快照」「分析」「观测」导航及原有默认首页。两种配置按路由选择，建筑能耗菜单仅在 DEV 隔离预览启用。建筑能耗范围内的未知路径使用该模块原有壳层显示未找到。标准构建保留全部 16 条旧显式路由。导航可见性只控制界面显示，后端身份认证和数据授权独立执行。 [AppShell](../../web/src/components/AppShell.tsx) · [路由](../../web/src/App.tsx)

| 视口与流程 | 上轮检查 | 截图 |
| --- | --- | --- |
| 1440px 桌面 | 根路径跳到总览，点击统计分析显示 F06。两页各仅一个 `aria-current="page"`。浏览器返回恢复总览的用水、按日筛选。 | [修改前](../../evidence/screenshots/nav-00-legacy-mixed-1440.jpg) · [总览](../../evidence/screenshots/nav-01-overview-light-1440.jpg) · [F06](../../evidence/screenshots/nav-02-statistics-light-1440.jpg) |
| 768px 平板 | 浅深色切换，键盘打开菜单后聚焦当前项。Escape 收起并将焦点归还给打开按钮。 | [深色菜单](../../evidence/screenshots/nav-03-tablet-dark-768.jpg) |
| 390px 手机 | 点按打开、关闭及从 F06 返回总览。选中项正确，导航时收起并归还焦点。主页面无横向溢出。 | [浅色菜单](../../evidence/screenshots/nav-04-mobile-light-390.jpg) |

统计分析深链接实测保留办公楼 B、用水、14 天与锚点，显示 5,376 m³，来源抽屉仍限定于当前建筑。直接访问旧终端路径可见原技术配置及有效选中项。隔离预览只提供样本 API，旧技术模块的数据读写不在本轮浏览器验收范围。浏览器检查覆盖上述交互，未进行完整无障碍认证。

| 验证状态 | 本轮证据 |
| --- | --- |
| 已由纯计算与隔离预览验证 | M1-C 分类和区间质量、M1-B 合成授权与范围、F08 趋势与同比环比，以及 F07 分组、F08 对象排名和 F06/F07/F08 对账。 |
| 已由隔离数据库验证 | 本轮 0 项，M1-B 隔离库授权待取得。 |
| 已由真实上游验证 | 本轮 0 项，尚未取得可验收的目录、权限、计量样本或接口。 |
| 仍待签认 | M1B-01 权威目录、M1B-02 可信授权、M1B-03 计量来源及 M1B-04 DBA 均未关闭。M1-C 权威分类目录、分类分摊口径及物理拓扑需由相应所有者确认。 |

## M1-C 分类分项纯计算

`CalculateClassifiedUsage` 接收同一项目、窗口、时间粒度及 as_of 的 M1-A 区间结果，以及对应 M1-B 映射、分类目录和已选物理边界的版本快照。当前每个入选点位提供一份完整区间序列和覆盖全窗的计量口径。口径或快照跨窗切换由调用方拆分。对象范围支持单一明确对象或已授权的项目边界。对象归属或分类权重落在原子区间内部变更时提示边界冲突。 [分类计算](../../internal/buildingenergy/classification.go)

- 分类树阻止环、重名 ID、无父项和跨能源分支。点位只分配到叶子，按百万分率计量，余量进入未分项。同一能源总量与分类树根合计和未分项余额对账。权重按区间生效，只有目录显式批准比例口径才相乘部分对象和部分分类权重。合成样本得到 `100 = 75 + 25 kWh`，水量独立为 `3 m³`。
- 拓扑按物理边界选择总表或互不重叠的分表，校验点位唯一、映射代次和通道一致，拒绝重复输入、通道别名和同边界总分表混选。不同通道及不同分组间的真实物理重叠由拓扑接口提供权威证明。
- 能源、单位、覆盖率与质量分别保留。有效零有数值，缺测或可疑片段仅计入相应覆盖项。分项追溯含点位、有效修订、范围内用量、单位和质量。追溯数据供内部使用，后续接口须按授权投影，避免对象权限可推导范围外用量。 [内存测试](../../internal/buildingenergy/classification_test.go)

`ClassificationCatalog` 与 `CategoryTopologySource` 是待签认输入接口。前者需提供项目分类 ID、父级、能源、有效期与分摊口径。后者须由设备接入和计量负责人提供经签认的单线图或管网图及分支对照，证明同一项目、建筑、能源与时间窗内各选定物理边界互斥。证据需固定每组的总/分表角色、点位、通道、映射代次、有效期及来源修订。不同通道 ID 只能排查标识重复。拓扑、点位或代次变化后须重新取得覆盖相应窗口的证明。

## F06 分类分项隔离预览

[本地预览](http://127.0.0.1:5176/ui/a/energy/statistics) 提供两栋合成办公楼、用电和用水、样本末 7/14/30 天。页面展示总量、已分类、未分项、构成和每日趋势、可展开分类树、覆盖率及有效零/缺测/可疑值。服务仅挂载于 `cmd/energy-preview`，地址为回环网口 `127.0.0.1:18089`。Vite 隔离配置通过 `127.0.0.1:5176` 代理，标准构建已检查排除 F06 的路由及 API 路径。 [服务](../../cmd/energy-preview/classification.go) · [样本与授权](../../cmd/energy-preview/classification_fixture.go) · [页面](../../web/src/pages/BuildingEnergyClassificationPage.tsx)

- `manifest-v1-isolated-circuits` 显式定义合成样本的三个独立回路，适用范围为对应建筑、能源及 2026 年 10 月。预览核对来源、窗口和完整点位选择，计算核心继续校验映射代次和通道。样本证据仅能说明预览中的互斥前提。
- 预览通过 `ResolveScope` 检查本地样本主体、读权限、项目、建筑、点位集合及窗口，随后读取计量输入。同项目其他建筑、夹带对象、外部点位、越界窗口及非法参数都有拒绝测试。只读抽屉投影当前范围内的计量通道、M1-A/M1-C 计算版本、分类目录、拓扑来源、映射代次、截止时点、质量与分项用量。原始读数、原始记录标识、其他对象用量和分配权重留在内部。
- 部分对象权重乘分类分配权重默认拒绝，即使多个分类的比例之和是 100%。启用比例口径后，测试以 100 kWh × 50% 对象份额 × 两个 50% 分类份额得到 25 + 25 kWh，区间追溯保留 50 kWh 和有效零，余额无预期区间。
- 日趋势复用 M1-A 的自然日分桶及同一数据截止，再调用 M1-C。构成、树与来源下钻均使用 Go 输出的数量，前端只负责展示与图表比例尺。

| 合成样本或校验项 | 手算预期 | 实际 |
| --- | --- | --- |
| 办公楼 A 末 7 天用电。3 个回路分别为 10、4、2 kWh／时。前 4 天用量系数为 2、3、4、1。其后依次为有效零、缺测、可疑。 | 总量 3,840 kWh；已分类 2,880；未分项 960 | 相符，按日求和与总量一致 |
| 照明全分配，通风一半分配，备用回路全部未分项 | 分类叶节点 2,400 和 480 kWh；未分项 960 kWh | 树、构成、逐通道追溯相符 |
| 3 台表 × 7 天 × 24 小时 | 有效 360；缺测 72；可疑 72；预期 504 | 覆盖率 71.4%，质量加总 504 |
| 办公楼 B 末 14 天用水；办公楼 B 末 30 天用水 | 5,376 m³；13,056 m³ | 浏览器与 Go 样本相符 |

### 上轮 F06 计算与界面验收

使用实际 Product Design 审查流程和 Codex 内置浏览器，复用 AppShell、Tailwind 主题 token、Lucide 与 Radix Drawer。桌面保留左右图表，手机堆叠显示。各视口检查树表、筛选和图表，页面横向无溢出。键盘可操作筛选、主题、树和趋势明细。Tab 限定在打开的来源抽屉内，Escape 关闭后焦点回到触发按钮。无障碍趋势明细提供日期、合计、已分类、未分项和质量。旧预览的 HTTP 冒烟检查返回 14 个用水日区间，浏览器实测旧能耗监测页按日切换与新旧预览导航。业务、快照、观测的真实接口和浏览器全量回归未覆盖。

| 视口 | 验证样本 | 截图 |
| --- | --- | --- |
| 1440px 浅色 | 办公楼 A 用电 7 天，构成、质量、完整树表 | [桌面](../../evidence/screenshots/f06-01-desktop-light-1440.jpg) |
| 768px 深色 | 办公楼 B 用水 14 天，单位与筛选联动 | [平板](../../evidence/screenshots/f06-02-tablet-dark-768.jpg) |
| 390px 浅色 | 办公楼 B 用水 30 天，图表堆叠和树表 | [手机](../../evidence/screenshots/f06-03-mobile-light-390.jpg) |
| 390px 深色 | 当前建筑来源、计量/分类规则、分项用量与质量 | [来源抽屉](../../evidence/screenshots/f06-04-source-drawer-390.jpg) |

### 需求基线

《建筑能耗监测分析模块 PRD V1.0》，编制日期 2026-11-18，原审计源文件 `建筑能耗监测分析模块_PRD_V1.0.md` 的 SHA-256 为 `96525ba930e75e876aff7a8cd3fd834dbe9e854e2ede16619b5014d78e010df4`。本轮打包读取到同名文件的 SHA-256 为 `5bd2758871770161a81b5e28f1107e15984b8f1577d80d4c3ab4dad8b9a6c643`，记录在归档清单。本轮 UI 验收沿用既有 F06/F07/F08 口径，PRD 内容差异待单独核对。 [现状审计](current-state.md) · [功能差距](feature-gap-matrix.md)

## A. 上游交接与签认

以下样本为合成评审数据。使用半开区间 `[from,to)`，示例日期为 2026 年，时间为 UTC。示例中的权威来源、授权结果和计量口径需分别由对应负责人用脱敏材料签认。

### 目录接口和最小样本

`Catalog.Project(tenant, project, at)` 返回稳定键、IANA 时区、版本、有效期和来源。 `Catalog.Object(tenant, project, kind, id, at)` 返回相同版本元数据及父对象、所属建筑。接口支持按生效时间返回当时版本，当前状态与历史失效可区分。空间父子和建筑归属在同租户、同项目内验证。组织父级使用组织树。请求跨目录版本时，按已签认版本界点拆分后查询。完整范围的审计需保存可重放目录截面与每个目标的有效版本，不以当前名称推导历史。

| 租户 / 项目 | 类型与 ID | 父对象 / 建筑 | 版本与有效期 | 时区或来源引用 |
| --- | --- | --- | --- | --- |
| t1 / p1 | project p1 | — | p1-v1，从 11-01 起 | Asia/Shanghai，directory-fixture/p1 |
| t1 / p1 | building b1 | — / b1 | b1-v1，从 11-01 起 | directory-fixture/b1 |
| t1 / p1 | space s1 | building b1 / b1 | s1-v1，从 11-01 起 | directory-fixture/s1 |
| t1 / p1 | organization o0、o2 | — | 各自 v1，从 11-01 起 | directory-fixture/o0、o2 |
| t1 / p1 | organization o1 | o0 → o2 | o1-v1 到 11-15，o1-v2 从 11-15 起 | 两条来源修订，稳定 ID 仍为 o1 |
| t2 / p2 | project p2、building b1、space s1 | s1 属于 p2 的 b1 | 各自 v1，从 11-01 起 | Asia/Shanghai，独立项目记录 |

### 可信主体与权限矩阵

`IdentitySource` 从服务器验证过的身份上下文取得 `issuer + sub`。接入方应签认签发方、受众、过期与撤权策略。 `AccessPolicy` 以动作、租户项目、对象、点位和业务时间段逐项判断。发布另检查审批引用、旧代次、变更前后所有目标和有效期间。缺少身份、授权适配或完整范围时返回拒绝。仅有对象级权限的汇总不得透露其他对象、完整分配或可推导的残差。历史读取使用记录的配置代次和目录截面，下载重新检查权限。

| 合成主体与范围 | read_usage | manage_bindings | export_usage | download_report |
| --- | --- | --- | --- | --- |
| reader1，t1/p1，s1 与 e1/power_total | 允许所列范围 | 拒绝 | 拒绝 | 拒绝 |
| publisher1，t1/p1，审批覆盖旧与新目标、点位和时间 | 仅有独立读授权时允许 | 审批且当前代次匹配时允许 | 拒绝 | 拒绝 |
| exporter1，t1/p1，所列对象与时间 | 允许所列范围 | 拒绝 | 允许所列范围 | 下载时仍有对应授权才允许 |
| auditor1，t1/p1 的旧版本只读 | 仅可读授予的历史范围 | 拒绝 | 拒绝 | 拒绝 |
| reader2，t2/p2，s1 与 e1/power_total | 仅本项目，即使对象 ID 重名也隔离 | 拒绝 | 拒绝 | 拒绝 |
| 未认证、过期、已撤权或夹带任一未授权对象的请求 | 拒绝 | 拒绝 | 拒绝 | 拒绝 |

### 量测来源和最小样本

输入键为 `tenant_id + project_id + terminal_id + item_code`。 `MeteringSource.Load(point, range, as_of)` 返回已审核 profile、物理通道引用和可见样本。profile 包含类型、能源、原单位、倍率、模数、采样周期、表码代次、来源和有效期。样本包含事件开始、区间结束（区间量）、首次可见时间、数值、代次、回卷标记、质量、源行引用及修订关系。RAW 的 `value_time`、`received_at` 是候选事件/可见字段，`(id, received_at)` 是候选行引用。由接入方确认字段语义和保留期限。 `as_of` 固定查询可见上限，重新出具历史结果需固定原始截面和修订选择规则。

| 点位 / 通道 | 口径和生效时间 | 合成原始值 | 期望 |
| --- | --- | --- | --- |
| t1/p1/e1/power_total，c-old | 累计电、kWh、倍率 2、周期 900s、epoch-A，至 11-01 00:30 | 00:00 为 1000，00:15 为 1005，00:30 旧表终值为 1008 | 前两段分别 10、6 kWh。终值只经同代次来源证明用于旧 profile 的右端 |
| t1/p1/e1/power_total，c-new | 累计电、kWh、倍率 1、900s、epoch-B，从 00:30 起 | 00:30 新表初值 0，00:45 为 4 | 新段 4 kWh。旧终值缺失时旧段待核查，新段仍可计算 |
| t1/p1/w1/water_delta，c-water | 区间水量、m³、源已加倍率，配置 1，900s | 事件区间 00:15–00:30，4 m³，00:41 才首次可见，RAW 引用含分区键 | 截面 00:40 缺测，00:42 为 4 m³。重放同一截面结果一致 |
| t1/p1/e2/power_total，c-wrap | 累计电、kWh、倍率 1、模数 10000，900s | 9998 → 3，有经确认的一次回卷标记 | 5 kWh。标记缺失时下降待核查 |
| t1/p1/e1/power_now，c-now | 瞬时功率，kW | 00:15 为 12 | 保留原口径与证据，用量入口拒绝 |

### 联合评审决策记录

联合评审人员在对应编号记录签认人、日期（含时区）、结论（同意、附条件同意或退回）、证据版本或 SHA-256 以及未满足的条件。附条件项在实证齐全前继续阻塞。四项均未收到实际签认人及证据，表中的角色是责任建议。

| 编号及状态 | 负责角色与待确认决定 | 脱敏样本必备字段 | 关闭条件及签认证据 |
| --- | --- | --- | --- |
| M1B-01 目录，未关闭 | 平台目录负责人主签，建筑与组织负责人会签。决定唯一权威目录、ID 生命周期、对象归属、IANA 时区和历史截面。确认旧草案的建筑、区域对照及生效日。 | tenant、project、object_kind、object_id、parent、building_id、timezone、revision、effective_from/to、source_ref 及两个项目可重名的对象。至少一条组织历史变更和一条旧草案对照。 | 版本化 API 或导出字段说明及脱敏样本摘要。复现相邻有效区间的查询、跨项目隔离、历史读回和已知旧 ID 转换。目录负责人签字确认追溯期限和缺失记录处理。 |
| M1B-02 授权，未关闭 | 平台身份与授权负责人主签，能耗审批负责人会签。确认可信签发方、权限粒度、撤权时延、受控发布者和下载二次授权。 | issuer、脱敏 sub、audience、tenant、project、action、objects、points、effective_range、expected_generation、approval_ref、授权或撤权时刻，以及预期 allow/deny。 | 提交上游身份/授权版本、独立角色矩阵、审批样本。用两个项目和真实脱敏角色核对无授权、夹带对象、旧新目标、撤权后查询和下载结果。 |
| M1B-03 计量，未关闭 | 设备接入与数据平台负责人主签，能耗负责人确认量测解释。确认点位与物理通道对应、倍率施加方、表码代次、事件/能见时间及原始截面保存。 | tenant、project、terminal_id、item_code、channel_ref、measurement_kind、unit、multiplier、register_epoch、modulus、cadence、effective_range、value_time、received_at、raw_ref、quality、revision。历史样本覆盖旧/新表同端点、回卷和晚到。 | 有权威出处的脱敏点表与原始记录，端点及倍率手算对账，固定 as_of 后可重放。接入方签认时间定义、历史保留、拓扑来源及无法重放的区间。 |
| M1B-04 DBA，未关闭 | DBA 与能耗负责人会签。确认 PostgreSQL 实际版本、GiST 或锁变体、迁移/运行角色拆分、批准连接的隔离库及回滚策略。 | server_version_num、major/patch、扩展可用和安装版本及 schema、默认 GiST opclass、实际登录角色/继承、schema owner、隔离服务名与预期服务端地址/端口/库名。审批证据中保留候选文件哈希。 | 签认 DDL 变体和哈希、实际角色正/负授权记录、隔离计划单独授权，以及 DB-01 至 DB-07 的日志与异常证据。DBA 审批和隔离授权分别记载。 |

实际签认留档统一使用这些字段，缺项填待确认。

```text
决定编号 | 签认人及角色 | 签认时间（带时区） | 结论 | 证据编号/版本及 SHA-256 | 未满足条件
```

现有管理入口默认放行，GitHub 查询未发现可用来确认上述决定的公开 issue 或 PR。Go 内存测试和本次合成样本验证消费方逻辑。 [现有授权入口](../../cmd/server/main.go#L127) · [RAW 字段](../../migrations/0024_raw_ingest.sql#L23) · [开放事项原记录](m1-b-open-issues.md)

## B. 候选 DDL 与发布设计

候选 SQL 位于评审目录。五表以租户、项目、终端、属性为完整点位键。head 指向当前配置代次。generation 保存前代、可信主体、审批时间和来源，以及单独的记录时间。profile 和 binding 保存业务有效期与来源修订，allocation 使用完整父键、目标修订和整数百万分率。先在适配器验证 Go `uint64` 可进入正 `bigint`，时间边界可精确表达为 PostgreSQL 微秒，数值换算可进入有限 `float64`。未配置累计模数以 SQL NULL 表示，读取后转为 Go 0。瞬时量字段按可空元数据处理。目录修订必须覆盖绑定有效期，否则在采样语义允许的边界拆分，审批时固定版本。版本与提交先后通过代次和事务可见性确定。 [候选 SQL](review/m1-b-mapping.sql) · [历史设计评审](m1-b-design-review.md)

| 方案 | 重叠防护 | 共同约束及取舍 |
| --- | --- | --- |
| 排斥约束，首选 | 同点同代次的 profile 和同视角 binding 由 GiST 拒绝重叠，允许相邻端点。 | 依赖可用的 `btree_gist` 等值操作类。索引和并发代价在目标库测量。跨行权重、外部目录及审批仍由受控发布事务校验。 |
| 事务锁，备选 | 移除候选 SQL 中两条命名的 EXCLUDE 约束。按点位锁 head，在该事务完整扫描候选 profile 和同视角 binding 的重叠及权重总和。 | 要求全部写入经过同一强制发布边界，任何旁路都破坏保证。采用稳定点位顺序取得批量锁，并验证竞争、回滚、SQLSTATE 与重试。 |

两种方案均使用受控写者，应用读者和批量回填账户只有受授予的读或受控调用权限。隔绝对 head、generation、profile、binding 和 allocation 的普通直接 DML。发布在 `READ COMMITTED` 事务内先按完整主键 `INSERT ... ON CONFLICT DO NOTHING` 初始化 head，再以独立语句 `SELECT ... FOR UPDATE`。空 head 等价于期望 0。检查期望代次、同一授权证据及有效期后插入完整新代次，基于实际入库行复核单位、目录和分配总额。以完整点位键和 `current_generation IS NOT DISTINCT FROM expected_db_value` 更新 head，影响行数必须为 1。失败回滚全部候选及审计，新代次只在提交后可见。正式迁移评审时由仓库迁移运行器拥有事务，移除候选 SQL 的外层 `BEGIN/COMMIT`，保证 DDL 和迁移版本标记原子提交。 [PostgreSQL 排斥约束](https://www.postgresql.org/docs/current/rangetypes.html#RANGETYPES-CONSTRAINT) · [行锁](https://www.postgresql.org/docs/current/explicit-locking.html#LOCKING-ROWS) · [迁移运行器](../../internal/store/db.go#L51)

历史修正和回填追加配置代次。首轮回填按脱敏对照表和经确认的有效日起算，留存来源行与幂等导入键。历史重放保留旧代次和当时有效归属。归属改变时收集新旧对象、允许汇总的祖先、未分配和受影响时间桶。profile 修改波及所有相关视角。晚到累计端点重算相邻段及其小时、日、月；采样段内部缺有效端点时标记待核查。计算结果的空值、真实 0 与覆盖率保持区分。

重算从**实际可见结果的 manifest** 到期望输入计算累计变更。若可见 G1、G2 改桶 A、G3 改桶 B，G3 需要处理 A 和 B。锁定点位配置代次向量、目录及拓扑修订、时区、计算规则版本、RAW 截面、范围和质量。按这些输入及输出目标标识生成幂等任务键，用于重试与持久恢复。建议在正式设计中采用同事务 outbox，或用已提交代次与任务键对账补发。候选结果完整校验后，以原结果指针和当前预期输入令牌做独立 CAS。落后任务不能覆盖更新的结果期望。保留旧结果代次供既有报表重放。持久任务与结果存储作为五表之外的设计依赖审定。

### 兼容性与实际角色复核

DBA 评审状态为待签认。本次候选 SQL 的 SHA-256 为 `38d15346c5cfb0d59f5bfdbf8753a767279fb223a07f2f0504c5c6e6f6d85152`。签认对象包含选择的变体和相应文件哈希，不以语法检查结果替代数据库评审。

- 对照 PostgreSQL 14 和当前版本文档，候选使用 `tstzrange`、复合外键、GiST 排斥约束和行锁。当前仓库只说明使用 PostgreSQL，目标主/次版本与补丁需 DBA 确认。离线解析结果仅覆盖语法。GiST 依赖目标库安装的 `btree_gist` 和适用于 `text`、`bigint` 的默认操作类。历史迁移 `0003` 安装扩展，`0008` 移除，因此迁移历史无法证明目标库有此扩展。授权后记录已安装版本、schema、默认操作类及实际迁移角色。单独批准扩展安装，回滚保留既存扩展和其他依赖。 [PG 14 btree_gist](https://www.postgresql.org/docs/14/btree-gist.html) · [PostgreSQL 默认操作类选择](https://github.com/postgres/postgres/blob/REL_18_STABLE/src/backend/commands/indexcmds.c) · [扩展安装迁移](../../migrations/0003_exception.sql#L3) · [扩展移除迁移](../../migrations/0008_conv_remove_interval_and_history.sql#L24)
- 候选 DDL 撤销 PUBLIC 权限。实际可写边界取决于所有权、继承和显式授权。建议专用 NOLOGIN owner、批准的单一迁移执行者、非 owner 查询角色、受控发布调用角色。调用者无直接写表、schema CREATE、owner 成员或可 SET ROLE 路径。读口执行真实项目权限。若采用 SECURITY DEFINER，固定可信 search_path、将 pg_temp 放在最后、明确限定对象名，并在建函数事务显式撤销 PUBLIC EXECUTE。当前启动逻辑以**同一连接池**执行迁移和运行期业务，应用能耗 schema 前必须明确所有权及运行角色隔离。实际 GRANT 和受控写者实现待批。 [数据库权限](https://www.postgresql.org/docs/current/ddl-priv.html) · [SECURITY DEFINER 安全配置](https://www.postgresql.org/docs/current/sql-createfunction.html#SQL-CREATEFUNCTION-SECURITY) · [启动执行路径](../../cmd/server/main.go#L48)
- 历史 profile、binding、allocation 在发布后由写者边界保护。物理外键保证引用同点位有效代次，受控发布另保证指针单调、不会清空、旧代次不可改及无断链。迁移运行器按文件开启事务并写版本标记，表 `schema_migrations` 自身可在失败后保留。目标测试需要在执行 DDL 后、写入版本标记前注入失败，确认候选 schema 和版本标记同回滚。使用获批经序列化的单一迁移执行者。 [迁移事务](../../internal/store/db.go#L42)
- 五表的 `view_kind` 仅覆盖建筑、空间和组织，`energy_type` 表示计量介质/量测口径。F06 分类分项后续使用经签认的分类字典、版本和有效期树，并验证分类内权重与汇总去重。F09 总分表拓扑后续使用独立的物理点位边、有效期和单位/采样边界，验证环路、同窗多父、总分同口径及重复计量。未经拓扑确认的差额需标明覆盖范围和质量，权限不完整时不暴露可推导残差。 [功能计划](p0-implementation-plan.md) · [现有计算约束](../../internal/buildingenergy/mapping.go)

### 隔离库验收设计和可重复命令

**授权状态：待授权。** 下列命令由 DBA 在获批的可丢弃数据库上执行。实际角色、主版本、后端地址和端口均使用签认值。命令依赖本机 `psql` 和获批的 libpq service 配置，连接串、口令及真实数据保存在 DBA 管理域。在 psql 中执行前置脚本，核对实际地址和角色，样本数据库以人工签认的服务身份为准。脚本只写合成数据，结构测试末尾回滚，并发成功代次留在一次性库以便审计。 [可运行的参数化隔离用例](review/m1-b-isolation-cases.sql) · [可评审 DDL](review/m1-b-mapping.sql)

```powershell
# Set M1B_REVIEW_* from the approved DBA record before using these commands.
$env:PGSERVICE = $env:M1B_REVIEW_SERVICE
$env:PGCONNECT_TIMEOUT = '5'
$Base = @('-X', '-w', '--dbname', $env:M1B_REVIEW_DB, '-v', 'ON_ERROR_STOP=1',
  '-v', "review_db=$env:M1B_REVIEW_DB", '-v', "review_major=$env:M1B_REVIEW_MAJOR",
  '-v', "review_address=$env:M1B_REVIEW_ADDRESS", '-v', "review_port=$env:M1B_REVIEW_PORT",
  '-v', "variant=$env:M1B_REVIEW_VARIANT", '-v', "reader_role=$env:M1B_REVIEW_READER",
  '-v', "caller_role=$env:M1B_REVIEW_CALLER", '-v', "case_id=$env:M1B_REVIEW_CASE")
$Cases = 'docs/energy-analysis/review/m1-b-isolation-cases.sql'
$Ddl = 'docs/energy-analysis/review/m1-b-mapping.sql'

# Read-only preflight. For each run use a shared 12–32 character hex case ID.
& psql @Base -v phase=preflight -v expected=0 -f $Cases

# Only after approval of this GiST candidate and provisioning of btree_gist.
# A lock-only test uses a separately reviewed variant file in its own new DB.
if ($env:M1B_REVIEW_VARIANT -ne 'gist') { throw 'Use the DBA-approved lock variant file' }
& psql @Base -v phase=preflight -v expected=0 -f $Cases -f $Ddl
if ($LASTEXITCODE -ne 0) { throw 'Candidate loading failed' }
& psql @Base -v phase=fixtures -v expected=0 -f $Cases
if ($LASTEXITCODE -ne 0) { throw 'Schema fixtures failed' }
```

同一场景使用相同 `M1B_REVIEW_CASE`，在三个独立 psql 连接中记录并发证据。先运行 A，等 `M1B_CAS_READY` 后保持提示未确认，再运行 B。观察者记录 B 被 A 阻塞后，才在 A 按 Enter。A 提交时 B 应报告 `M1B_CAS_CONFLICT`。使用新场景 ID 从空 head 复测，将 A 改为 `phase=cas_rollback`，预期 B 报告 `M1B_CAS_WIN`，失败候选不残留。对已有代次用 `expected=1` 重复两种场景，历史应保留且每次只增长一代。

```powershell
# Shell A. Use cas_rollback for the rollback scenario.
$env:PGAPPNAME = 'm1b-review-A'
& psql @Base -v phase=cas_hold -v expected=0 -f $Cases

# Shell B. Start after A reports READY.
$env:PGAPPNAME = 'm1b-review-B'
& psql @Base -v phase=cas -v expected=0 -f $Cases
```

观察者使用同一获批 service 和数据库检查 `pg_stat_activity` 中 A/B 的 `pid`、`application_name`、`wait_event_type`、`wait_event` 与 `pg_blocking_pids(pid)`。证据附阻塞前后两次采样、双方退出码和最终配置代次。权限 ACL 清单由 preflight 输出；脚本里的合成发布步骤属于数据库原语验证，真实受控发布者、迁移事务和重算仍按下表验证。

```powershell
# Observer. Confirm B lists A's PID before releasing A.
& psql @Base -c "SELECT pid, application_name, wait_event_type, wait_event, pg_blocking_pids(pid) FROM pg_stat_activity WHERE datname=current_database() AND application_name IN ('m1b-review-A','m1b-review-B')"

# In the approved service for each non-owner test login, run preflight again.
# A direct zero-row write must fail with SQLSTATE 42501.
& psql @Base -v phase=preflight -v expected=0 -v VERBOSITY=verbose -f $Cases `
  -c "BEGIN; DELETE FROM energy_mapping.energy_mapping_generation WHERE false; ROLLBACK"
```

A 的 `\prompt` 需要可交互终端。若 B 先达到锁等待超时，使用新的场景编号重测。真实发布接口建立后，再按授权矩阵补充读者拒绝与发布调用者成功记录。 [psql 脚本命令](https://www.postgresql.org/docs/current/app-psql.html)

| 编号与动作 | 验收证据 |
| --- | --- |
| DB-01 版本、扩展、结构 | preflight 保存服务端版本、schema、权限和操作类。扩展缺失的 GiST 试验应失败并回滚候选 schema，启用后在新库恢复。fixtures 证明跨项目/租户代次引用拒绝、相邻区间、GiST 重叠、单项权重和去重。锁变体的多行权重及重叠由未来受控发布者单独拒绝。 |
| DB-02 实际角色 | 实际非 owner 读者和发布调用者验证直接表 DML、ALTER、TRUNCATE、CREATE 等被拒绝。读者不能调用发布接口，发布调用者可凭批准调用。验证继承、PUBLIC 权限和跨项目可见性。 |
| DB-03 并发 CAS | 对空 head 和旧代次分别记录 A 提交、A 回滚四组。B 确实等待。提交时一方冲突，回滚时 B 胜出。观察者确认旧状态在提交前仍可见。归档双方日志、最终 head 与历史代次。 |
| DB-04 原子回滚 | fixtures 注入事务内异常验证 head 和候选同回滚。未来使用 `store.Migrate` 与合成文件系统注入标记写入失败，确认候选 schema 和版本行不存在，移除故障后重跑只写一条标记。覆盖移除外层事务和进程中断。 |
| DB-05 历史与回填 | 使用同一目录、计量截面重放旧代次。更正回填两次幂等，旧报表固定。验证微秒边界、无上界、无穷值拒绝和大小数转换。 |
| DB-06 重算发布 | 用 G1/G2/G3、晚到数据和失败重试验证累计回算。较旧结果不得覆盖当前预期，切换前后中断可恢复，旧报表可重放。 |
| DB-07 上游真实证据 | A 节的脱敏身份、项目目录、点表和读数，经角色负责人签认后，与上表运行证据交叉核对。 |

记录每组实际命令（清除任何连接凭证）、文件 SHA-256、版本与角色、运行时间、成功/失败代码、预期与实际行数。回滚验证完成后按 DBA 隔离环境清理流程处理测试库。

## C. 打包、验证与下一轮决策

脚本对比基线、HEAD、暂存区、工作区及未跟踪路径。清单列出每个 Git 变更及选择结果。任何范围外路径先打印，未分类就终止；只有明确无关的路径可用 `--exclude 路径=理由` 留痕。相关文件须纳入规则。ZIP 使用相对路径。文档引用的基线源码作为只读证据，截图和历史差异放入 `evidence/`。源 PRD 仅登记版本与 SHA-256。打包校验临时目录内的补丁应用和文件重建、链接目标、CRC、清单及每个成员的 SHA-256，并筛查凭证形态；仅收入经确认的合成预览截图。归档自身 SHA-256 由脚本输出。 [打包脚本](../../scripts/package_energy_handoff.py) · [现有 UI/UX 与截图](m1-b-ui-ux-spec.md) · [历史差异证据](../../evidence/m1b-go.diff)

```powershell
python scripts/package_energy_handoff.py --output <目录>/energy-analysis-handoff.zip --base <起始提交> --prd <PRD原文件> --evidence-dir <证据目录> --screenshot <样本截图>
```

重复传入 `--screenshot` 收录所有被文档引用的截图。证据目录包含 Go、前端构建、SQL 静态解析日志及历史 `m1b-go.diff`。同一路径的 ZIP 在验证成功后才替换。

| F07 样板轮次回归 | 通过 | 失败 | 跳过 |
| --- | ---: | ---: | ---: |
| 全仓 Go 测试事件，含子用例 | 314 | 0 | 60 |
| 能耗计算与隔离预览，含 M1-B、M1-C、F07/F08 | 149 | 0 | 0 |
| M1-B，5 个主测试和 11 个子用例 | 16 | 0 | 0 |
| M1-C，6 个主测试和 19 个子用例 | 25 | 0 | 0 |
| F06 预览，5 个主测试和 10 个子用例 | 15 | 0 | 0 |
| F08 趋势与同比环比，8 个主测试和 40 个子用例 | 48 | 0 | 0 |
| F07/F08 排名纯计算及跨 API 对账，9 个主测试和 18 个子用例 | 27 | 0 | 0 |

本轮执行 `go test -json ./... -count=1`，测试进程清除 PostgreSQL、MQTT 和容量变量。既有 admin 9 条、ingest 51 条环境依赖用例跳过。Go 静态检查与构建、TypeScript 类型检查、Vite 临时目录构建、标准产物 16 条技术路由与预览路径隔离检查通过。Vite 保留主包体积警告。归档保留 M1-B 历史 SQL 离线语法证据。 [Go 日志](../../evidence/go-test.jsonl) · [前端日志](../../evidence/web-build.txt) · [M1-B 历史 SQL 解析日志](../../evidence/sql-parse.txt)

本轮使用 Product Design 审查、视觉探索和设计 QA、ImageGen、Codex 浏览器及独立设计审查。逐一比对上轮 ZIP 中的 41 个 Go/React 源码成员，差异仅为 F07 页面和共享布局的 F07 分支。原 `dev` 的工作区差异指纹保持一致。源文件和 Git 变更收录状态见包内 `MANIFEST.json`。 [GitHub 基线](https://github.com/Xoone0/iot-admin/tree/2563cd367bcd333d85a650f1be441f3f9589dcaa) · [历史验证记录](m1-b-verification.md)

F07 样板验收完成后，业务开发已恢复为本文件顶部的五栏目工作台实施。M1B-01 至 M1B-04、权威分类目录、分摊口径及物理拓扑仍待签认，隔离库授权仍待取得。
