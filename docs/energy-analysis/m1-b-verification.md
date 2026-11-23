# M1-B Skill-First 盘点与验证记录

本轮在独立 worktree `feature/energy-analysis-prd-v1` 开展。以用户消息确定实施范围。附件用于读写契约、设计审查方式和验收口径。基线为 `2563cd367bcd333d85a650f1be441f3f9589dcaa`。

## 实际能力与用途

| 能力 | 可用性 | 本轮调用与结果 / 未用原因 |
| --- | --- | --- |
| GitHub MCP | 可用，目标仓库读取成功 | `github_fetch` 核实远端仅有 `dev`，提交与指定基线相同。开放 Issue 和 PR 均为空。工作区未推送内容以本地 Git 为准。 [GitHub 基线](https://github.com/Xoone0/iot-admin/tree/2563cd367bcd333d85a650f1be441f3f9589dcaa) |
| 本地 Git 和 GitHub CLI | 可用 | Git 核对 worktree、HEAD、原 dev 状态及已跟踪差异摘要，检查可审阅变更。 `gh --version` 确认为 2.103.3，仓库查询使用 MCP，未用 CLI 代发或审批。 |
| Superpowers 规划、TDD、完成前验证 | 本地 Skill 文件可读 | 先形成设计与契约，再观察缺失实现导致的测试失败，写小范围纯 Go 代码并验证。额外人工核对配置有效期、物化边界、错代次/越权以及发布事务职责。代码审查 Skill 文件存在，本轮未分派或发起远端审查。 |
| Product Design Audit / User Context | 可用，本地 Skill 实际执行 | 预检未找到已保存产品上下文，使用仓库和本机隔离预览。现场保存并检查 4 张截图。结论限定在可访问的样本流程，五页为待实现规范。 |
| Computer Use 与应用内浏览器 | 可用 | 通过其浏览器 locator（Playwright 接口）核对筛选、样本表格、浅深色、键盘焦点与宽度。截图在本机保存，未上传。 |
| Figma MCP | 已发现工具，文件/账户资源未查 | 本轮无现成目标稿或编辑请求，规格以已有组件和 token 为准，未调用 Figma。 |
| Vite、TS、npm、本地 Go | 可用 | Node 24.22.1、npm 11.19.1。执行原 `npm run build`、Go 测试/构建/静态检查，无新增依赖。 |
| 项目独立 Playwright、axe-core、Storybook | 仓库 manifest 与锁文件未发现 | 使用应用内浏览器完成现有样本验证。没有运行 axe 或 Storybook，也未安装新工具。 |
| 部署连接器 | 与本轮无关 | 只读现场画面来自现有本机隔离预览。本轮不使用 Sites、FNOS 或外部部署。 |

保留并复用 `AppShell`、分析路由、页面语义 token、筛选、对象选择、表格、状态组件、分页与 Radix 交互基础。详细映射及四档宽度结果见 [UI/UX 规范](m1-b-ui-ux-spec.md)。

## 工程质量

| 检查 | 命令或方式 | 实测 |
| --- | --- | --- |
| M1-B 契约 | `go test -v ./internal/buildingenergy -run '^TestM1B' -count=1` | 5 个入口测试、11 个子用例通过，0 失败、0 跳过。 |
| 计算与隔离预览回归 | 全仓 JSON 日志提取 `buildingenergy` 与 `energy-preview`；另以 `-count=20 -shuffle=on` 重跑两包 | 34 条通过记录（含子用例），0 失败、0 跳过。乱序重复通过。 |
| 全仓 | `go test -json ./... -count=1` | 199 条通过、0 失败、60 跳过（计测试/子测试事件）。跳过为原有环境依赖，admin 9、ingest 51，本轮未接入 PostgreSQL/MQTT 或容量环境。 |
| Go 静态检查与构建 | `go vet ./...`、`go build ./...`、计算包依赖检查 | 通过。计算包无 HTTP、SQL 或 pgx 依赖。 |
| 前端 | `npm run build -- --outDir <临时目录>` | TypeScript 与 Vite 构建通过。现有入口包约 623 kB 的 chunk 提示仍在，仓库内嵌产物保持原样。 |
| 浏览器 | 本机隔离预览，1440、1280、768、390px | 用电/用水与时/日联动、单位、真实零和缺测、部分覆盖、主题、焦点与移动布局已查。正式权限和下载未在 UI 接入；权限的验证范围是 Go 契约。 |

并发测试使用线程安全内存存储替身，两个发布请求基于同一旧代次，结果为一个成功、一个冲突、旧代次仍可回读。真实 PostgreSQL 的执行计划、锁、约束和事务恢复尚待隔离库验收。

## 修改文件和差异

| 文件 | 本轮变更 |
| --- | --- |
| [mapping.go](../../internal/buildingenergy/mapping.go) | 有效期、不同视角的百万分率集合、全范围读授权、可信发布与 CAS 端口、同物理边界输入防双计。 |
| [mapping_test.go](../../internal/buildingenergy/mapping_test.go) | 权重、历史切换、跨项目/部分对象拒绝、父子混选、并发代次冲突与审计。 |
| [usage.go](../../internal/buildingenergy/usage.go) | 提取原计量口径检查和单位换算，供映射验证复用。原 M1-A 算法和预览响应沿用。 |
| [m1-b-design-review.md](m1-b-design-review.md)、[m1-b-contracts.md](m1-b-contracts.md) | 候选 PostgreSQL、兼容与回退、重算、读写契约。 |
| [m1-b-ui-ux-spec.md](m1-b-ui-ux-spec.md)、[m1-b-open-issues.md](m1-b-open-issues.md) | 界面审查与规范、风险和数据库实施门槛。 |
| [m1-b-verification.md](m1-b-verification.md) | 能力盘点、证据及验证结果。 |

供本轮评审的 [Go 统一差异](../../evidence/m1b-go.diff) 以已有 M1-A 工作区文件为比较基线。既有前端未提交改动保持原状。本轮未启动主服务、执行数据库迁移、连接真实数据、改动生产或 FNOS。原 dev 的状态和差异摘要前后一致，分支保持本地。

## 结论

**代码和文档具备设计评审条件。数据库实施暂缓。** 需由平台、接入、数据库和能耗负责人确认权威目录、授权范围、物理通道与计量历史、候选 DDL 及隔离库迁移方案。 [开放事项](m1-b-open-issues.md)
