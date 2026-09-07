# 组件前端与构建交付职责拆分落地方案

> 状态：2026-09-07 已实施，验证记录见 [实施验收](component-ui-and-delivery-modularization-validation-2026-09-07.md)。
> 基线：2026-09-06 当前工作区，包含未提交修改；不代表测试环境部署状态。
> 范围：架构复核后的第 2 项“组件页面拆分与前端类型归位”、第 3 项“Docker 构建执行提取与独立 CLI 介质依赖收敛”。

## 1. 目标与实施边界

本轮完成两个结果：组件页面按实际工作流程维护；镜像构建与介质交付拥有可脱离平台业务服务使用的实现。

保留当前用户可见行为：页面布局、路由和深链、权限、发布与审批门禁、构建记录、准备检查、独立作业命令及失败恢复语义。HTTP API、数据库结构和已保存 Run/作业包合同保持原样。本轮不需要数据库重建或迁移。

执行快照整体强类型化是另一个事项，本方案不以它为前置条件。介质提取只共享已有介质字段的类型，不重新设计完整执行合同。后端全量业务拆包、通用工作流、通用执行引擎、全站状态管理替换及公共 API 客户端整体重写均不进入本轮。

本方案最初只交付规划文档与索引；用户随后授权实施，现已按以下批次集中完成并进入验收。

## 2. 当前代码及迁移关注点

| 当前入口 | 已核实职责 | 迁移关注点 |
| --- | --- | --- |
| [ComponentsPage.tsx](../web/src/pages/ComponentsPage.tsx) | 目录选择、Release 深链、合同、动作、文件、镜像、介质、验证和多个弹窗 | 编辑状态、未保存保护、身份切换和异步请求归属不能被拆散 |
| [API client](../web/src/api/client.ts) | 公共请求、响应校验和多个业务接口 | 从 UI 文件导入的是类型；归位类型即可消除该依赖，无需重写请求系统 |
| [image_builds.go](../internal/service/image_builds.go) | 构建权限与记录，也直接管理 Docker 进程、临时目录和输出 | 业务状态继续由 Catalog 管理，进程细节移入构建实现 |
| 原 `delivery_adapters.go`（现 [delivery](../internal/delivery/)） | HTTP/FSS、Docker 镜像探测与复制 | 引用了同包的镜像身份解析，须一并明确归属 |
| [media_observation.go](../internal/service/media_observation.go) | 适配器 Probe 的公共入口、请求内去重、超时和准备进度 | 不能仅移动适配器文件，否则丢失报告、缓存隔离或超时语义 |
| 原 `standalone_media.go`（现 [CLI 介质](../internal/jobcli/media.go)） | 独立介质准备及 JobPlan 摘要 | 依赖完整 lockedPlan，导致 CLI 导入 service |
| [jobcli/run.go](../internal/jobcli/run.go) | run/resume/rollback-preview/rollback、结果锁与回执 | 介质准备时机、摘要和已有回执必须保持一致 |

## 3. 第 2 项：组件前端拆分

### 3.1 建议目录与责任

以下为目标路径，实施时按完整业务职责落文件；小型展示组件可与所属流程放在同一文件。

```text
web/src/
  pages/ComponentsPage.tsx
  features/components/
    catalog/ComponentCatalog.tsx
    catalog/ReleaseDetail.tsx
    contract/ReleaseContractEditor.tsx
    contract/PlaybookActionEditor.tsx
    contract/PlaybookWorkspaceEditor.tsx
    media/ImageBuildModal.tsx
    media/ArtifactModal.tsx
    verification/ReleaseVerification.tsx
    releases/ReleaseDialogs.tsx
    releases/ReleaseLifecycleActions.tsx
    import/ComponentTemplateImportModal.tsx
    import/parseComponentTemplate.ts
    useComponentSelection.ts
    model.ts
  features/scenarios/model.ts
  types/runRetention.ts
  types/componentUsage.ts
```

| 目标 | 归属内容 | 对外协作 |
| --- | --- | --- |
| ComponentsPage | 页面入口、公共查询、选中身份、布局组合、跨流程刷新 | 给功能组件传递选中对象、操作意图和完成事件 |
| ComponentCatalog / ReleaseDetail | 分类与发布线目录、只读合同摘要、证据和按钮展示 | 以稳定 Component/Release ID 发出选择或操作事件 |
| ReleaseContractEditor | 参数与依赖的本地草稿、联合校验和保存 | 接收目标 Release、参考组件；报告 dirty、保存完成或取消 |
| PlaybookActionEditor | 动作合同、前后检引用、YAML 保存及文件摘要 | 保留动作与内容的现有原子保存调用 |
| PlaybookWorkspaceEditor | 文件列表、当前文件与未保存内容、文件/树摘要 | 接收 releaseId 和刷新标识；文件切换保护由它负责 |
| ImageBuildModal | 环境选择、构建/登记、列表、日志、轮询 | 完成后触发已有组件刷新，不持有目录选择状态 |
| ArtifactModal | 介质登记、来源更新和当前身份展示 | 沿用现有 API 与身份校验 |
| ReleaseVerification | TestReleaseModal、Run 证据弹窗及证据展示 | 继续复用 ExecutionPreparationPanel、JobPlanPreview |
| ReleaseDialogs | 组件新增/编辑、版本新增/编辑/查看的完整表单 | 完成时返回已有对象或稳定 ID，由页面负责定位 |
| ReleaseLifecycleActions | 发布预览、废弃影响与阻断说明、对应确认状态 | 保留原门禁、请求顺序和错误说明 |
| model | 当前页面使用的纯类型与纯展示计算 | 不发请求，不依赖 React、页面或组件 |

已有 ParameterEditors、ResourceContractEditor、BranchScope、Modal 等共享组件继续复用，不复制到新目录，也不借本轮扩展为通用表单引擎。

### 3.2 状态与刷新规则

1. URL 中的 selected、release、action、focus 仍由页面与 useComponentSelection 协调；深链只处理一次，处理后的清理方式保持原样。
2. 筛选、分类折叠属于目录；合同内容、YAML、文件、Dockerfile 等草稿属于对应编辑器。跨编辑器传递完成事件，不传递页面全部 setter。
3. 合同依赖和参数共享一个草稿保存边界。上游映射与新增引用参数继续联合保存，不能拆成先保存依赖再保存参数。
4. 页面保留当前合同离开保护的协调职责，编辑器报告是否 dirty；文件编辑器保留文件切换和远端文件变化保护。不能因一次 SSE 刷新重置草稿。
5. 用户或 Release 改变时沿用当前状态清理与请求失效规则。请求结果必须匹配原目标；关闭弹窗、切换目标后终止轮询和无效回调。
6. 镜像构建活动状态继续轮询；已完成构建仍能首次读取完整日志。复用当前 useApiData、signalRefresh 和 SSE 刷新目标，不引入第二套全局缓存。
7. 只有依赖编辑等确需全量合同的流程才读取全量组件，保留当前摘要、详情、编辑数据按需读取方式。拆分不能增加父子组件对同一数据的重复请求。

useComponentSelection 只收拢路由及选中身份的协调，不变成包含所有页面状态的 useComponentsPage。

### 3.3 类型与工具归位

- 将 RetentionPolicy、ArchiveInfo、CleanupItem、ArchiveHealth 从 RunRetentionPanel 移至 `types/runRetention.ts`；ComponentUsage 从 ComponentUsagePanel 移至 `types/componentUsage.ts`。API 与组件直接导入新位置，同批更新测试，不在 UI 文件留下长期转发导出。
- 将 `pages/scenarioLifecycle.ts` 的纯函数移入 `features/scenarios/model.ts`，同步修改验收、执行准备、场景页面和测试导入。
- 将 `pages/componentTemplateImport.ts` 与组件导入流程放在一起，同步修改模板测试。
- `api/client.ts` 保留当前请求、响应校验及 api 对象；此次只更新类型来源。公共 `types/domain.ts` 不按行数机械拆分。
- PlaybookWorkspaceEditor 的测试直接导入所属功能模块，页面只导出页面入口。

### 3.4 前端验收

| 路径 | 必须证明的行为 |
| --- | --- |
| 目录/版本/身份切换 | 选中 Release、权限按钮、默认定位、筛选结果与基线一致 |
| 深链 | validate、publish、contract、lifecycle、focus=parameters 能正确打开或定位，且不会重复触发 |
| 合同编辑 | 参数与映射联合保存；取消离开保留修改；刷新不覆盖草稿；非法合同继续被阻断 |
| 动作/工作区 | 新文件使用返回的 SHA 保存；远端变更按原门禁处理；二进制不能进入文本编辑 |
| 镜像/介质 | 提交的环境与内容身份准确；构建记录和日志可读；关闭后停止轮询 |
| 验证与发布 | 继续绑定预览摘要；就绪状态、审批和服务端错误说明原样生效 |
| 请求行为 | 首次加载、编辑按需读取及构建轮询请求数无无故增加 |

优先迁移现有 App、PlaybookWorkspaceEditor、client、YamlAuthoring、ExecutionPreparationPanel、templateImports 测试；仅对拆分引入的状态归属风险补充行为测试。浏览器使用隔离数据做桌面实际操作，至少覆盖 1440×900 和 1280×800；记录重构前后同一数据的页面与请求，不新增移动端适配任务。

完成标准：页面不再定义上述编辑、镜像、介质和验证流程的完整实现；模块的状态和保存职责可独立理解；API/types 不导入 UI 类型，功能模块不导入 pages。行数只作为观察值，不作为拆分指标。

## 4. 第 3 项：Docker 构建与独立介质交付

### 4.1 Docker 构建边界

新增 `internal/imagebuild`，包含 Request、Result、LogEvent 和 Docker 实现。由 service 声明消费端的窄接口，形式为：

```go
type ImageBuildBackend interface {
    Build(context.Context, imagebuild.Request, func(imagebuild.LogEvent)) (imagebuild.Result, error)
}
```

Request 只含已经由业务层确定的目标镜像引用和 Dockerfile 字节；Result 返回解析后的不可变摘要及完整引用；日志事件含来源流和文本。临时根目录、Docker 可执行文件由构造配置确定。具体声明在实施时以编译与现有合同为准。

| 留在 CatalogService | 交给 imagebuild.Docker |
| --- | --- |
| Owner、Draft、环境有效性与输入校验 | 创建隔离临时目录及写入 Dockerfile |
| 从锁定环境生成目标 Registry 和镜像名 | build → push → inspect 的进程顺序 |
| 创建构建记录、queued/running/终态转换 | stdout/stderr 读取与进程错误返回 |
| 将构建日志写入数据库并发布现有事件 | 传播 context 取消、释放临时资源 |
| 成功时事务保存 ComponentImage 与构建结果 | 校验 Docker 返回的镜像身份并返回结果 |
| 审计、失败脱敏、重启 interrupted 恢复 | 不访问用户、Release、Run、Store 或 EventHub |

平台组合根静态注入默认实现。保留 ConfigureImageBuilder 的既有配置含义：当前它同时配置构建用 Docker 和镜像交付用 Docker，两者仍接收同一指定 binary。Catalog 不再保存临时目录和 Docker 命令配置。

B1 同时建立 `delivery/image_identity.go`，迁入构建与交付共同需要的纯镜像身份解析；B2 再扩展该包的传输能力。这样构建实现不需要反向调用 service 的解析函数，也无需临时复制解析规则。

不另设 ImageBuildService 转发现有业务入口，不新增镜像任务队列、构建插件注册中心或审批流程。

### 4.2 提取共享交付能力

建议目标：

```text
internal/imagebuild/
  docker.go
  types.go
internal/delivery/
  types.go
  plan.go
  http_artifact.go
  docker_image.go
  image_identity.go
  prepare.go
internal/service/
  image_builds.go          # 业务记录与构建接口调用
  media_observation.go     # 平台准备进度、缓存和错误展示
  delivery_execution.go    # Run 交付结果、镜像/介质镜像记录
internal/jobcli/
  run.go
  media.go                # 已有 JobPlan.Metadata 的介质投影读取
internal/ansible/job.go    # JobPlan 摘要的唯一归属
```

`delivery` 持有位置、内容身份、Probe/Transfer 接口及现有 HTTP/FSS、Docker 实现。移动镜像引用/摘要解析的纯函数，供登记、构建与交付复用。它不导入 service、store、api，也不认识 PreparationCheck、权限或安装基线。

service 使用有实际职责的探测包装器：负责准备进度、同一准备请求内的去重、脱敏以及观测值回传。缓存键继续包含来源、身份和访问上下文，不能只按 URL 或镜像名缓存。平台默认装配与显式适配器注入都要核对，避免绕过观测或重复包装。

每次底层探测保留当前三分钟上限及父 context 取消约束，确保 CLI 脱离平台包装后也不会失去该限制。Transfer 的取消与错误分类沿用现有行为。超时属于执行约束，PreparationCheck 的状态与文案属于平台业务。

以下语义必须逐项迁移：目标缺失与摘要不一致区分；目标已存在可复用；转移后核验身份；ObservedSize/ObservedDigest 正确返回；显式计划转移可显示“将提供”，不能被改为“探测通过”。

平台 DeliveryService 保留逐 Run 的持久化、事件、审批决定和变更记录。CLI 保留自身回执和执行阶段控制。两者共享传输实现，业务记录分别归属原调用方，不为了复用整段流程把 Store/Recorder 塞进 delivery。

### 4.3 CLI 去除平台服务依赖

1. 将介质需求、决定及传输项的可移植结构移至 `delivery/plan.go`，保留字段名、JSON tag、空值和省略规则。平台 lockedPlan 引用这些类型；不复制出一套 CLI 专用的传输项合同。
2. CLI 的 media.go 从已有 Metadata 读取介质投影。保留当前元数据基本合法性检查；只解释介质相关字段，其他执行字段仍留在原 JobPlan。投影不得回写完整 Metadata 或参与完整 JobPlan 的重新序列化。
3. 将独立介质准备流程移至 delivery，依赖显式传入的 artifact/image 适配器。CLI 在启动装配处创建默认实现；平台继续使用自己的记录流程。
4. 将 JobPlan 的公共摘要入口归入 ansible，构建/验证与 CLI 共用同一现有 JSON + SHA-256 算法。删除 service.DigestStandalonePlan，避免 CLI 为摘要引用 service。
5. CLI 改用新入口后删除 service.PrepareStandaloneJobMedia 及仅供它使用的文件内容；不保留长期转发层。

CLI 中 source_verify 完成后才准备升级介质的时机保持原样；run/resume/rollback-preview/rollback、结果锁、回执、备份来源和 expected-plan-digest 继续按原合同工作。

本轮重建 CLI 会改变新导出包中的可执行文件字节，因此不能要求新旧整包文件摘要完全相同。应比较相同业务输入的计划摘要与源内容身份；已保存包、回执和历史 Run 不重写。

### 4.4 后端验收

| 层次 | 验收内容 |
| --- | --- |
| 构建适配器 | 假 Docker 验证命令顺序、输出读取、失败中止、取消、临时目录回收和摘要解析 |
| 构建业务 | 记录失败不启动构建；执行失败不登记成功镜像；成功沿用原事务和审计；服务取消仍记录 interrupted |
| API | 复用 TestDraftDockerfileBuildPublishesForcedRegistryReference，保持 HTTP 202、环境 Revision、强制 Registry、日志及 Owner/Draft 门禁 |
| 共享介质 | 迁移已有 delivery_adapters 测试；覆盖缺失、身份不匹配、已存在复用、转移后验证和取消 |
| 平台观测 | 复用 media_observation 测试，核对访问上下文隔离、单次请求去重、进度和“将提供”状态 |
| CLI 介质 | 覆盖无介质、目标复用、转移失败、元数据非法、多个需求及升级源验证前不复制介质 |
| 摘要与文件合同 | 固定有效样本验证计划摘要一致；移动介质类型前后 JSON 一致，含 nil/空切片、可选字段、镜像和制品组合 |
| 独立运行 | 原 run/resume/rollback、结果锁、回执篡改、恢复来源及控制器退出测试继续通过 |
| 依赖边界 | `go list -deps ./cmd/clusterforge-job` 不含 internal/service、internal/store、internal/api 和 SQLite 驱动；新适配器不反向依赖业务服务 |

不因本轮拆分把不同摘要用途、平台审批流程和独立 CLI 回执模型合成一个通用框架。

## 5. 实施批次与顺序

| 批次 | 交付内容 | 依赖 | 工作量/风险判断 |
| --- | --- | --- | --- |
| F1 | 前端类型、纯工具归位，更新直接导入与测试 | 无 | 小；主要风险是漏改消费者 |
| F2 | 合同、动作、文件、镜像、介质、验证和表单迁入功能模块 | F1 | 中；重点保护本地草稿与请求生命周期 |
| F3 | 目录/详情、Release 操作状态和 URL 选择职责收敛，页面只做组合 | F2 | 中；重点保护深链和未保存保护 |
| B1 | Docker 构建适配器、共享镜像身份解析与静态装配，业务记录继续留在 Catalog | 无；建议接 F3 完成 | 小至中；涉及子进程、日志与取消 |
| B2 | 扩展共享 delivery 类型/实现与平台观测包装 | B1 的共享镜像身份基础 | 中；调用面覆盖准备、登记、执行和测试替身 |
| B3 | CLI 介质准备及 JobPlan 摘要归位，删除 service 依赖 | B2 | 中；重点保护计划、包和回执合同 |

推荐执行顺序：F1 → F2 → F3 → B1 → B2 → B3 → 统一回归。两条工作线技术上独立；本方案没有启动并行代理或创建新任务。

开始实现时先核对当前未提交修改和涉及文件的最新内容，保存相关页面、API 响应及有效计划样本作为行为基线。不得以 HEAD 覆盖当前工作区，或把其他改动归入本次交付。每批保持可编译并跑受影响的关键测试，完整套件集中执行一次。

## 6. 统一回归与交付门槛

实施完成后的验证，不代表本规划阶段已经执行：

1. `pnpm --dir web test`、`pnpm --dir web build`；隔离本地 API + 桌面浏览器按第 3.4 节验收。
2. `go test ./... -count=1`、`go vet ./...`；对 service、api、delivery、imagebuild、jobcli 执行受影响包的 race 测试。
3. 构建 server、backup 和 clusterforge-job，核对所有构造函数、配置入口、测试替身和 CLI 依赖图。
4. 使用本地 Ansible 和隔离目标执行 `make test-role-job ANSIBLE_PLAYBOOK=<已验证的本地路径>`。共享介质和 CLI 的真实本地执行必须覆盖；环境缺失时说明未完成项，不能以模拟测试替代该门禁。
5. 检查前端导入方向及 Go 包边界；校验 JSON/摘要基线；`make check-docs` 和 `git diff --check`。

完成后更新 project-structure、backend-design、development-guide 和 standalone-role-job 的实际职责说明；按日期新增验证记录。规划稿在实施完成前不能改写为现状说明。

最终交付应能说明：哪些职责移到了哪里、哪些旧入口被删除、哪些行为通过了验证，以及是否存在未完成验证。提交、推送和部署分别记录；后续请求已授权本方案实施；提交、推送和部署仍未执行。
