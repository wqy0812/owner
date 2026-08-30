# ClusterForge 模块化与简化实施方案

> 方案日期：2026-08-27
> 适用范围：ClusterForge V1 测试环境
> 当前状态：2026-08-30 复核时，本文提出的模块化、模型简化、Delivery 与 Readiness 主体设计已经进入当前代码。本文保留原始决策依据和实施顺序，不再作为未完成事项清单。
> 权威边界：当前行为以代码、数据库合同和 [正式文档中心](docs/README.md) 为准；真实环境结果只以带日期的验收记录为准。
> 核心目标：保留交付安全所需的复杂度，删除没有业务价值的模型和实现复杂度，用模块化单体承载内部交付平台。

## 1. 结论

ClusterForge 不应拆成微服务，也不应建设通用工作流引擎或动态插件系统。当前最合适的形态仍是：

**模块化单体主平台 + 独立 FSS + 外部 Ansible/Registry 执行能力。**

本文编写时，领域边界基本正确，但代码层的职责尚未收口：`Platform` 聚合过多能力，原 Run 实现同时承担规划、快照、调度、执行、交付介质、证据记录和回滚绑定；组件分类、Release 类型、执行策略和环境并发字段也提供了当前场景并不需要的自由度。当前代码已经按本文方向拆分模块并收口模型，现状说明见 [项目结构说明](docs/project-structure.md)。

本方案不削弱以下业务不变量：

- Component、Scenario、Environment 三类 Owner 的职责分离；
- Component Release、Scenario Revision、Environment Revision 的不可变历史；
- 精确 Release 依赖、静态 DAG 和静态参数映射；
- 组件安装验证与回滚验证双证据；
- 场景候选交接和联合发布事务；
- Run 快照、`planDigest`、环境审批、同环境 FIFO、审计和回滚来源。

本方案删除或收口以下非必要复杂度：

- 不建设条件、循环、动态并行、表达式数据流等工作流 DSL；
- 不建设运行时动态插件市场，仅保留代码内静态适配器；
- 不让分类字段参与调度、依赖推断或发布规则；
- 不同时保存可由证据推导出的多份“已验证/已就绪”状态；
- 不让核心 Run 逻辑直接理解 FSS HTTP、Docker CLI 和 Registry 搬运细节；
- 不让来源地址变化导致内容身份和 Release 生命周期失效。

## 2. 使用场景与设计原则

### 2.1 使用场景

平台服务于少量内部团队和若干受控环境，核心任务不是“任意自动化”，而是：

1. Component Owner 将能够独立版本、测试、复用或回滚的能力制作成组件；
2. Scenario Owner 用静态 DAG 组合精确的组件 Release；
3. Environment Owner 管理执行环境并批准风险操作或介质平移；
4. 平台锁定计划、环境和内容身份，调用 Ansible 等执行器完成交付；
5. 平台保留证据、安装基线、审批和回滚来源。

Playbook 内的普通技术步骤不拆成组件；只有具备独立生命周期价值时才建立组件。

### 2.2 简化原则

- **核心模型表达业务事实**：Release 表达版本，Revision 表达快照，Run 表达一次执行，Evidence 表达执行证据。
- **来源不等于内容**：文件名与 SHA-256、镜像 Digest 表达内容身份；来源地址只是可变定位信息。
- **计划与执行分离**：Planner 只生成不可变计划，Executor 只执行已锁定计划。
- **强一致只用于核心写入**：发布、审批、Run 状态、证据、删除在事务内完成；工作台、Readiness、SSE 均为派生视图。
- **适配器只隔离工具差异**：接口由当前业务需要定义，不为未来未知工具预造扩展框架。

## 3. 目标模块划分

| 模块 | 高内聚职责 | 不再承担的职责 | 主要代码落点 |
| --- | --- | --- | --- |
| Catalog | Component、Release、Action、Dependency、内容身份、候选意图 | 场景联合发布、Run 调度、FSS/Docker 细节 | `internal/service/components.go`、`playbooks.go`、`artifacts.go`、`images.go` 及 `internal/store/components*` |
| Scenario | Scenario、Revision、静态 DAG、参数映射校验 | 直接修改 Component Release 状态 | `internal/service/scenario*`、`internal/store/scenarios*` |
| ReleaseCoordinator | 候选闭包检查、Readiness 复核、场景与组件联合发布事务 | 组件日常编辑、Run 执行 | `internal/service/release_coordinator.go` |
| Environment | Environment Revision、Inventory、变量、CredentialRef、健康检查 | Run Worker、组件发布 | `internal/service/environment*` |
| Execution | Plan、Run 快照、审批、FIFO 调度、执行、取消、重试、证据记录 | 组件编辑、HTTP/Registry 工具细节 | `plan_builder.go`、`run_creator.go`、`run_scheduler.go`、`run_executor.go`、`lifecycle_recorder.go`、`rollback_planner.go`、`approval_service.go` |
| Delivery | 介质/镜像目标解析、来源探测、直用/平移要求 | Run 生命周期、组件发布状态 | `internal/service/delivery_binding.go`、`delivery_planner.go`、`delivery_adapters.go` |
| Adapters | Ansible、FSS、镜像构建、Registry 查询/平移 | 业务审批与发布规则 | `internal/ansible`、`internal/fss` 和 `internal/service` 中的静态 Adapter 实现 |
| Read Model | 我的工作、Readiness、阻断原因、通知视图 | 写入业务状态 | `internal/service/workbench.go` 及查询服务 |

依赖方向固定为：

```text
API -> 应用服务 -> 领域规则/端口 -> Store 或静态 Adapter
                    ^
            ReleaseCoordinator

Read Model -> Store 只读查询
```

禁止以下反向依赖：Store 决定角色权限、Adapter 修改 Release 状态、Scenario 直接发布 Component、React 自行推断发布资格。

## 4. 具体修改方案

### 4.1 A 类：纯后台模块化，不改变前台用户逻辑

#### A1. 拆分 `Platform` 聚合服务

修改点：

- 保留 `Platform` 作为装配门面，逐步将业务委托给 `CatalogService`、`ScenarioService`、`EnvironmentService`、`ExecutionService`、`ReleaseCoordinator`。
- API 路径、请求 DTO 和响应 DTO 在本阶段保持不变。
- 删除业务代码通过 `Platform.Store()` 直接取得数据库对象的路径；API 只调用应用服务。
- 为各服务定义最小 Store 接口，测试使用窄接口替身，不再依赖完整 `*store.Store`。

验收：所有现有 API、前端测试和运行流程不变；服务间没有跨模块直接写表。

#### A2. 拆分 Run 内部职责

将 `internal/service/runs.go` 按职责拆成：

| 新文件/对象 | 职责 |
| --- | --- |
| `plan_builder.go` / `PlanBuilder` | 参数解析、依赖顺序、动作选择、内容交付要求、计划摘要 |
| `run_creator.go` | 校验 `expectedPlanDigest`，原子创建 Run/Step/Approval/Snapshot |
| `run_scheduler.go` | 每环境 FIFO、Worker 恢复、Watchdog、抢占 |
| `run_executor.go` | 读取锁定快照，调用 Delivery 和 ActionRunner，执行 Step |
| `lifecycle_recorder.go` | 成功证据、安装清单、失效原因、Run 完成状态 |
| `rollback_planner.go` | 安装基线校验、逆序计划、回滚来源绑定 |
| `approval_service.go` | 单次/批量审批、交付逐项决策 |

原则：Planner 不写数据库；Executor 不重新解释当前 Component/Scenario 定义；Recorder 不重新规划。

#### A3. 场景联合发布收口

- `ScenarioService` 只维护 Revision、DAG 和测试状态。
- `ReleaseCoordinator` 在一个事务内锁定当前 Scenario Revision，重新校验 DAG、候选 Release、依赖闭包和双证据，再原子发布 Scenario Revision 与候选 Component Release。
- Component 的单独发布也复用同一个 Release Readiness 规则，避免两套门槛漂移。

#### A4. 静态适配器

只定义当前用得上的三个接口：

```go
type ActionRunner interface { Run(context.Context, ActionRequest) (ActionResult, error) }
type ArtifactDelivery interface { Probe(context.Context, ArtifactLocation, ArtifactIdentity) error; Transfer(context.Context, ArtifactTransfer) error }
type ImageDelivery interface { Probe(context.Context, ImageLocation, ImageDigest) error; Transfer(context.Context, ImageTransfer) error }
```

- Ansible 实现 `ActionRunner`。
- HTTP/HTTPS/FSS 实现介质探测；目标 FSS 自主从来源 URL 获取内容并校验 SHA-256。
- OCI Registry 实现镜像探测和平移。
- 适配器在启动时静态装配；不增加插件发现、动态加载、版本协商或插件市场。

### 4.2 B 类：数据模型简化，会影响 API 和前台字段

#### B1. Component 分类简化

保留：

- `layer`：仅用于架构分层展示和检索，不参与调度；
- `tags: []string`：少量自由标签，仅用于检索和展示。

删除：

- `category`；
- `component_kind`；
- `requiredness`。

数据库：`components` 删除上述三列，增加 `tags_json TEXT NOT NULL DEFAULT '[]'`。

API：Component DTO 删除 `category/kind/requiredness`，增加 `tags`。

前台：

- 组件创建/编辑页只保留“层级”和“标签”；
- 列表筛选去掉类别、形态和必要性筛选；
- 场景画布可继续显示 layer，不依据标签自动生成边或执行顺序。

#### B2. Release 类型简化

- 删除 `ComponentRelease.type` 和数据库 `release_type`；
- Release 是否包含多个 Action、介质或镜像由实际内容表达，不再用 `atomic/bundle` 重复描述；
- 创建、复制、导入模板和详情页同步删除该字段。

#### B3. Scenario 执行策略简化

- 删除 `ScenarioRevision.executionPolicy` 和 `execution_policy_json`；
- 静态 DAG 固定采用“依赖满足后执行，任一步骤失败即停止”；
- 不提供通用条件、循环、自动重试或动态并行策略；
- 前台删除“执行策略 JSON”，场景模板只包含 `nodes` 和 `edges`。

若未来出现明确且重复的策略需求，先增加一个具名字段和固定语义，不恢复任意 JSON 策略袋。

#### B4. Environment 并发字段简化

- 删除 `EnvironmentRevision.maxConcurrent` 和数据库 `max_concurrent`；
- V1 继续固定“每环境一个活跃 Run，FIFO 串行”；
- 前台和导入/导出模板删除该字段。

#### B5. Release Readiness 单一来源

保留强状态：`draft/released/deprecated` 和 Component Owner 的 `candidate` 交接意图。

删除重复状态：数据库 `verified` 布尔值不再作为独立事实。后端根据以下内容实时计算 `readiness`：

- install、verify、rollback 合同是否完整；
- 当前内容身份和 Playbook 摘要下是否有安装+验证证据；
- 当前内容身份和 Playbook 摘要下是否有回滚+验证证据；
- 精确依赖及参数映射是否有效；
- 候选依赖闭包是否满足。

统一响应：

```json
{
  "status": "ready|blocked|risky",
  "blockers": [{"code": "rollback_evidence_missing", "message": "缺少回滚后验证证据", "actionUrl": "..."}],
  "installEvidenceRunId": "...",
  "rollbackEvidenceRunId": "..."
}
```

前台：候选开关仍保留；组件详情和“我的工作”展示同一套阻断原因与入口，不再分别解释 `verified`、证据和候选状态。

### 4.3 C 类：介质/镜像交付逻辑，会改变 Run 与审批页面

#### C1. 内容身份与来源地址分离

介质内容身份：

```text
artifact identity = releaseId + alias + filename + sha256
```

镜像内容身份：

```text
image identity = releaseId + logicalName + OCI digest
```

`sizeBytes` 只作为展示/校验元数据，不参与 Release 规格摘要。`sourceUrl/sourceRef` 是可变来源，也不参与 Release 规格摘要。

数据库建议：

```text
component_release_artifacts
  id, release_id, alias, filename, sha256, size_bytes,
  source_url, source_updated_by, source_updated_at,
  created_by, created_at

component_release_images
  id, release_id, logical_name, digest,
  source_ref, source_updated_by, source_updated_at,
  created_by, created_at
```

约束：

- Draft 修改文件名、SHA-256 或镜像 Digest：内容身份变化，现有证据失效，Readiness 重新计算；
- 任何 Release 状态下，仅更新同一内容身份的 `sourceUrl/sourceRef`：不改变 Release 状态、不清除 candidate、不使证据失效；
- 后端必须禁止通过“更新来源”接口同时改变文件名、SHA-256 或 Digest；
- Released/Deprecated Release 允许其 Component Owner 修复来源地址，并写审计。

#### C2. 录入方式

介质支持：

1. 用户通过平台上传入口，把文件流式写入所选 FSS；平台不落本地文件，只登记返回的文件名、SHA-256 和来源 URL；
2. 用户登记已有 HTTP、HTTPS 或 FSS 地址；平台只做可读性和身份探测，不下载并保存内容。

镜像支持：

1. 平台通过 Dockerfile 构建并推送，构建成功后登记 OCI Digest 和来源 Ref；
2. 用户登记已有 OCI Registry 镜像 Ref，平台解析并锁定 Digest。

平台数据库始终只保存内容身份、来源定位和审计元数据，不保存介质二进制或镜像层。

#### C3. Run 目标解析

对每个介质或镜像逐项执行以下固定状态机：

```text
读取 Environment Revision 中的 FILE_STATION / IMAGE_REGISTRY
    |
    +-- 目标中存在相同 SHA/Digest --> 使用目标，正常执行
    |
    +-- 目标中不存在 -------------> 探测 Release 登记来源
                                         |
                                         +-- 来源不可读 --> Run 创建/预检失败
                                         |                 通知 Component Owner 更新来源
                                         |
                                         +-- 来源可读 ----> 生成 DeliveryRequirement
                                                             Environment Owner 选择：
                                                             1. 直接使用来源
                                                             2. 平移到环境目标
```

执行前再次检查目标：若审批等待期间相同内容已经存在目标中，直接复用目标，不重复平移。

Environment Owner 选择“直接使用来源”时，Run 将来源地址注入该项对应变量；选择“平移”时，由目标 FSS/Registry Adapter 主动从来源拉取或执行 Registry 到 Registry copy，平台主服务不代理介质字节。

来源不可读的错误必须包含：组件、Release、介质别名或镜像名、当前来源、Component Owner 和更新入口。来源修复后重新预览/创建 Run，不自动复活旧失败 Run。

#### C4. 一页完成风险审批与逐项交付决策

Run 计划新增：

```json
{
  "deliveryRequirements": [
    {
      "id": "artifact:release-id:media",
      "kind": "artifact",
      "name": "media",
      "identity": "sha256:...",
      "source": "https://.../media.tar.gz",
      "target": "http://file-station/.../media.tar.gz",
      "sourceReadable": true,
      "targetPresent": false
    }
  ]
}
```

审批接口增加逐项决定：

```json
{
  "reason": "维护窗口内执行",
  "deliveryDecisions": [
    {"requirementId": "artifact:release-id:media", "mode": "direct"},
    {"requirementId": "image:release-id:main", "mode": "transfer"}
  ]
}
```

规则：

- 所有交付项必须逐项选择，不允许前端静默默认；
- 交付决策和风险批准一次提交、一个事务写入 Run 锁定快照；
- 拒绝审批不要求逐项选择；
- 含交付选择的 Run 暂不支持通用批量批准，避免不同 Run 的来源/目标被错误套用；批量拒绝仍可保留；
- Run 详情展示每项最终使用的地址、决策人、决策和实际平移结果。

#### C5. API 修改清单

| 接口 | 修改 |
| --- | --- |
| `POST /component-releases/{id}/artifacts/upload` | 保留上传入口，返回内容身份与 `sourceUrl` |
| `POST /component-releases/{id}/artifacts/register` | 输入 `alias/filename/sourceUrl/sha256` |
| `PATCH /component-releases/{id}/artifacts/{alias}/source` | 仅更新同一内容的来源，任何 Release 状态可用 |
| `POST /component-releases/{id}/images/register` | 登记 OCI Ref 并锁定 Digest |
| `PATCH /component-releases/{id}/images/{name}/source` | 仅更新相同 Digest 的来源 Ref |
| `POST .../test-plan`、场景预览、回滚预览 | 返回 `deliveryRequirements` |
| `POST /approvals/{id}/approve` | 接收 `deliveryDecisions` |
| Run 查询 | 返回锁定决策和执行结果，不重新读取当前来源覆盖历史 |

### 4.4 前台页面修改汇总

| 页面 | 用户可见修改 | 类型 |
| --- | --- | --- |
| 组件列表/编辑 | category/kind/requiredness 改为少量 tags；保留 layer | 简化 |
| Release 编辑 | 删除 atomic/bundle；Readiness 展示统一阻断原因 | 简化 |
| 介质管理 | 区分“内容身份”和“当前来源”；Released 后仍可更新来源 | 新行为 |
| 镜像管理 | 同时支持 Dockerfile 构建和已有 OCI Ref 登记；相同 Digest 可修复来源 | 新行为 |
| 场景画布 | 删除执行策略 JSON；保留静态 DAG、精确 Release 和参数映射 | 简化 |
| 环境管理 | 删除 maxConcurrent；继续维护 `FILE_STATION`/`IMAGE_REGISTRY` | 简化 |
| Run 预览 | 显示哪些内容命中环境目标、哪些需要选择 | 新行为 |
| 审批页 | 风险原因与介质/镜像逐项“直用/平移”在一页完成 | 新行为 |
| Run 详情 | 显示锁定来源、目标、决策和传输结果 | 可追溯增强 |
| 我的工作 | 使用统一 Readiness/阻断原因，引导正确 Owner 处理 | 派生视图优化 |

## 5. 原实施批次（历史顺序）

本节保留 2026-08-27 制定的实施与验收顺序，用于解释变更为什么按这些边界拆分；其中的命令式表述不代表当前仍未完成。当前能力和限制应查阅正式设计、操作手册及带日期的验收记录。

### 第 0 批：冻结合同与补充测试

- 确认本文第 7 节决策门；
- 固化当前核心流程的 API/领域回归测试；
- 将已有未提交的探索性改动与正式实施批次隔离，避免混合提交；
- V1 测试环境采用清库重建，不写旧字段兼容、双写、回填或迁移窗口。

交付物：确认后的 DTO、数据库草图、验收用例，不改变运行行为。

### 第 1 批：后台职责拆分

- 完成 A1-A3；
- 保持 API、数据库和前端完全不变；
- 每个提交只移动一种职责，并证明行为等价。

验收：Go 全量测试、前端全量测试和构建通过；关键 API 快照无变化。

### 第 2 批：内容身份与来源分离

- 新建介质/镜像内容模型；
- 来源更新接口和审计；
- Release 规格摘要排除来源地址；
- 先完成“录入、查看、修复来源”，暂不改变 Run 决策。

验收：Released Release 更新同 SHA/Digest 来源后状态、candidate 和历史证据不变；改变内容身份必须走新 Draft 或 Draft 编辑并使旧证据失效。

### 第 3 批：Delivery Planner 与审批支线

- 完成目标优先、来源回退、可读性探测、DeliveryRequirement；
- 审批页逐项直用/平移；
- FSS/Registry 目标端主动获取；
- Run 快照锁定决策。

验收：覆盖目标已存在、来源可读直用、来源可读平移、来源不可读、审批期间目标出现、传输校验失败六类路径。

### 第 4 批：模型字段简化

- 一次性清库重建新 Schema；
- 删除 category/kind/requiredness/release type/executionPolicy/maxConcurrent；
- 更新 API、Seed、导入导出模板、前端和文档；
- 不保留旧字段兼容解析。

验收：未知字段由严格 DTO 拒绝；新 Seed 能完成组件测试、候选、场景联合发布、正式运行和回滚。

### 第 5 批：Readiness 与读模型统一

- 以证据和当前规格摘要派生 Readiness；
- 移除 `verified` 重复状态；
- 统一组件详情、场景候选选择器和“我的工作”的阻断解释。

验收：同一 Release 在各页面得到相同结论和入口；SSE 丢失或服务重启不影响事实状态。

### 第 6 批：真实环境验收与文档收口

- 在隔离测试环境执行介质直用/平移、镜像直用/平移；
- 执行安装验证、回滚验证、联合发布、审批、取消、安全重试；
- 更新后端设计、项目结构、操作手册、Demo 模板和部署说明。

## 6. 验收标准

### 6.1 架构验收

- `runs.go` 不再同时包含 Planner、Worker、Executor、Delivery 和 Evidence 全部职责；
- Scenario 模块不能直接更新 Component Release 表；
- 核心 Service 不出现 FSS URL 拼接、Docker/Registry 命令细节；
- Store 事务维护数据不变量，但不决定 Owner 权限；
- API 和 React 不重复实现 Release 发布资格规则。

### 6.2 业务验收

- 静态 DAG、精确依赖、参数映射、双证据和联合发布全部保留；
- 介质来源地址变化不影响 Release 状态和证据；
- 内容身份变化一定使旧证据失效；
- Run 优先使用环境目标中已有的相同内容；
- 来源可读但目标缺失时，Environment Owner 可逐项选择直用或平移；
- 来源不可读时明确通知正确的 Component Owner；
- 平台主服务不保存介质二进制，也不代理跨站平移数据流；
- Run 历史始终可解释当时使用了哪个内容、地址和审批决定。

### 6.3 回归验收

以下现有功能不得因简化而删除：

- 我的工作；
- Release 复制；
- 组件/场景/环境批量导入导出；
- 环境平移；
- Run Input Preset；
- 安全重试；
- 安全删除；
- 组件测试预览/执行；
- 候选共享与场景联合发布；
- 场景测试和正式 Run；
- 环境整集群回滚；
- Run 查询、审批、通知和审计。

## 7. 历史决策门

以下问题记录实施前用于收敛用户行为的决策门。它们不再是阻塞当前代码的待确认项；当前语义以正式设计文档和服务测试为准。后续若要改变这些策略，应作为新需求重新评审，而不是直接修改本历史方案。

### 决策 1：环境未配置目标地址时如何处理

建议：如果 `FILE_STATION` 或 `IMAGE_REGISTRY` 未配置，但来源可读，则允许直接使用来源，并在预览中提示“环境未定义本地目标，不能平移”；不额外阻断 Run。

当时待确认：部分离线环境是否必须强制配置本地目标并禁止直连来源？若需要，应增加一个明确的环境策略 `deliveryMode=local_only`，而不是隐含推断。

### 决策 2：上传介质默认写入哪个 FSS

建议：继续由 Component Owner 在上传页选择一个已配置 `FILE_STATION` 的环境作为初始来源，上传完成后登记来源 URL；避免建设全局仓库管理模块。

当时待确认：是否存在独立于环境的“公共来源 FSS”？只有答案为是，才增加平台级默认来源配置。

### 决策 3：标签是否限制词表

建议：V1 使用最多 8 个、每个不超过 32 字符的小写标签，不建设标签治理后台；搜索和展示即可。

当时待确认：是否有必须用于报表或权限的固定标签？如果没有，保持自由标签。

### 决策 4：是否允许所有环境直接使用外部来源

建议：默认由 Environment Owner 在每次审批中决定，不增加永久白名单和复杂策略。

当时待确认：是否存在明确的安全域要求，规定某些环境只能使用本地 FSS/Registry？若存在，再增加简单的环境级 `local_only` 固定策略。

## 8. 不实施的设计

本轮明确不实施：

- 微服务拆分、服务网格、分布式事务；
- Kafka/持久消息总线替代当前 SQLite + Worker；
- 通用 BPMN/工作流 DSL；
- 动态插件加载、插件市场、插件版本协商；
- 标签驱动自动编排、分类驱动依赖推断；
- 运行时表达式、组件输出变量和动态数据流；
- 为 V1 测试数据建设旧合同兼容层、双写和迁移框架；
- 用单一分数掩盖 Readiness 的具体阻断原因。

这些能力只有在出现明确、重复且无法由现有简单模型解决的使用场景时再单独立项。
