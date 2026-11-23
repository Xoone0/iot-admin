# M1-B 设计评审

基于 [M0 契约](m0-contract.md)、[M1-A 验收](m1-a-verification.md)和 [P0 计划](p0-implementation-plan.md)。本轮以消费方契约、内存测试和文档推进设计。候选结构留在文档，正式写入及数据库校验在后续评审。

## 数据所有权

| 数据 | 权威来源 | M1-B 的职责 |
| --- | --- | --- |
| 租户、项目、时区、稳定建筑/空间/组织 ID | 平台目录和建筑、组织负责人，来源待确认 | 保存经过验证的键、来源版本及依赖关系。空间树和组织树独立。 |
| `terminal.id + item_code`、物理仪表/通道、代次与原始读数 | 设备接入、物模型、RAW/快照及相应负责人 | 读取共享数据，核对设备物理标识和历史。该终端属性属于某项目须有权威依据。 |
| 计量口径和统计归属 | 能耗负责人审核，平台与设备方提供依据 | 按点位保留口径和各视角的历时版本。每个视角有独立的分配集合。 |
| 身份、能否访问项目/对象/点位 | 平台鉴权 | 在读目录和映射前验证操作权限，发布由服务端重新校验旧、新目标及受影响范围。当前管理端默认放行，真实适配待定。 |
| 总分表拓扑 | 设备/专业计量负责人提供物理边界与关系 | 后续用独立拓扑验证。当前代码只检查聚合输入角色，避免父表和其覆盖子表同时入账。 |

本地原 `dev` 有建筑/区域草案。草案只可用于评审数据兼容，尚无组织、项目有效期和权威所有者证明，不作为正式目录。 [设备档案](../../migrations/0001_datasource.sql#L16) · [入口](../../cmd/server/main.go#L127)

## 候选 PostgreSQL 结构

采用**点位配置代次 + 业务有效期**。每个代次保存一份不可变配置，按读侧给定代次重放。目录和源记录仍用稳定键引用。完整代次复制便于事务与审计，真实数据量较大时需评估行数和版本复用。候选对象和字段如下。

| 对象 | 键及字段 |
| --- | --- |
| `energy_mapping_head` | 主键 `(tenant_id, project_id, terminal_id, item_code)`，包含 `current_generation`。首版未发布时为空。 |
| `energy_mapping_generation` | 同点位键 + `generation` 为主键，包含 `previous_generation`、`published_by_issuer/sub`、`published_at`、`reason`、`approval_ref`。旧、新代次充当审计引用，可附上变更明细。 |
| `energy_point_profile` | 点位键 + `generation, revision` 为主键。包含 `effective tstzrange`、`source_system/id`、`physical_meter/channel_ref`、`register_epoch`、能源、量测类型、源单位、倍率、模数、采样周期、核验依据。量测为瞬时仍可归档，计算入口拒绝参与用量。 |
| `energy_point_binding` | 点位键 + `generation, view_kind, view_key, revision` 为主键。包含 `effective tstzrange`、`source_system/id`、目录版本证据。内置建筑/空间/组织采用固定 view_key，分类、自定义维度及统计边界保留类型设计。 |
| `energy_point_binding_allocation` | 以父集合完整复合键加目标 ID 为主键，包含 `weight_ppm`。目标来自对应权威目录，同一集合每个原子目标只出现一次。未分配为 `1000000 - sum(weight_ppm)`，无需造虚假目录记录。 |

以下为约束片段，仅用于评审。所有跨表引用都含租户、项目和点位键，同库目录才考虑真实外键。目录不可在事务里跨服务锁定时，发布时核对已审批目录版本或带签名的验证结果。

```sql
-- effective 是 timestamptz 范围，下界必须存在，空上界表示开放。
CHECK (NOT isempty(effective) AND NOT lower_inf(effective)
       AND lower_inc(effective) AND NOT upper_inc(effective)
       AND isfinite(lower(effective))
       AND (upper_inf(effective) OR isfinite(upper(effective))));
-- 口径表在同一配置代次内互斥
EXCLUDE USING gist (tenant_id WITH =, project_id WITH =, terminal_id WITH =,
                    item_code WITH =, generation WITH =, effective WITH &&);
-- 归属集合在同一点位、同一视角和同一配置代次内互斥
EXCLUDE USING gist (tenant_id WITH =, project_id WITH =, terminal_id WITH =,
                    item_code WITH =, generation WITH =, view_kind WITH =,
                    view_key WITH =, effective WITH &&);
-- 权重整数，100% = 1,000,000
CHECK (weight_ppm BETWEEN 1 AND 1000000);
```

GiST 等值索引方案依赖 `btree_gist` 是否获准且在目标 PostgreSQL 可用。如果扩展未获批准，所有发布路径在同一事务锁定该点位的版本头，再检查范围。多行权重总额、目录中同层不可重叠的原子目标、旧/新审批证据无法由单行 CHECK 保证，须在同一发布事务验证。数据库头更新采用期望代次 CAS，变化后返回冲突，禁止后写静默覆盖。 [PostgreSQL 范围类型与排斥约束](https://www.postgresql.org/docs/current/rangetypes.html#RANGETYPES-CONSTRAINT) · [btree_gist 文档](https://www.postgresql.org/docs/current/btree-gist.html)

## 修改后的重算

1. 对比上次和本次已审核代次，按点位和视角计算有效时间差集。归属变更只影响对应视角的旧目标、新目标、其允许汇总的祖先节点与未分配项。口径变更才重算用量端点，并影响所有依赖视角。
2. 以 M1-A 的原子采样段为单位查口径和归属。生效时刻有实际端点时可用半开边界分段。变更发生在采样段中间时，整个该段待核查，禁止按时间比例猜测。重算扩至受影响小时、自然日和自然月。
3. 使用点位、视角/对象、桶区间、M1-A 规则版本、配置代次和读数 `as_of` 作为幂等键的组成。后台任务先写新结果代次，完成后原子发布。保留旧代次供旧报表复核。累积晚到端点只影响相邻两段及上层桶。
4. 结果同时保存用量、原单位/归一单位、质量、`Valid/Expected/Missing/Suspect` 和版本证据。部分值可帮助排查，正式考核受质量门槛约束。权重分配不是采样覆盖率，二者独立。没有有效区间时数值为空。

## 兼容、评审和回退

- 正式 DDL 构建前需核对上游稳定键、权限、归属所有者、可回填样本、版本保留策略和截止定义。先用隔离副本评估索引/锁和扫描范围，再对固定样本做影子统计。老接口和旧快照保持访问契约。
- 首次导入为新增代次。对来源和历史不明的项记录未配置及来源待核对，避免倒填归属。留存旧行与授权范围，闭环后再决定旧稿转换。
- 回退优先停止新写入和新查询入口，保留原始表和历史配置，使用经授权的旧可见代次，或审批追加修正代次。撤回或清理 schema 单独评审。主服务启动会执行嵌入迁移，待审 SQL 不放进迁移目录。

## 本轮评审顺序

先确认完整配置代次和分视角权重契约，再对内存输入验证重叠、跨版本、权限、父子防双计和并发冲突。UI 审查复用当前后台作为视觉基线，正式数据接口与数据库集成以 [开放事项](m1-b-open-issues.md)为门槛。
