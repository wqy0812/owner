# ClusterForge 平台操作手册（分角色）

> 版本与环境：本文属于项目首个版本（V1）；所有操作目标均为测试环境，不是生产环境。V1 不提供通用历史兼容，仅允许代码显式列出的精确前序 V1 合同执行经过测试的加法迁移；未知合同失败关闭。统一规则见 [首版与环境策略](version-policy.md)。

> 文档基线：2026-08-24 当前工作区代码
> 适用对象：组件 Owner、场景 Owner、环境 Owner 及演示平台管理员
> 重要提示：这是本地 Demo。身份可无密码切换，不应直接作为生产权限系统使用。

## 1. 平台入口与准备

### 1.1 启动开发环境

首次使用：

```bash
cp .env.example .env
make bootstrap
make dev
```

访问地址：

- 前端：`http://127.0.0.1:5173`
- 后端：`http://127.0.0.1:8080`

嵌入式本地构建：

```bash
make build
./bin/newplatform
```

构建后的前端由 Go 服务直接提供，默认访问 `http://127.0.0.1:8080`。

### 1.2 演示身份

右上角身份选择器可切换四个内置身份：

| 显示身份 | 角色 | 主要职责 |
| --- | --- | --- |
| 林晓 · Runtime | 组件 Owner A | Runtime、主机基础等组件 |
| 周工 · Kubernetes | 组件 Owner B | Kubernetes 相关组件 |
| 陈晨 · 集群交付 | 场景 Owner | 场景 DAG、完整测试和发布 |
| 王维 · 基础设施 | 环境 Owner | 环境、Inventory、凭据引用和危险作业审批 |

身份切换仅用于演示。平台没有登录密码，也没有管理员越权身份。

### 1.3 页面导航

| 页面 | 用途 |
| --- | --- |
| 概览 | 汇总可见资产、进行中的运行和未读通知 |
| 组件 | 维护组件和 Release，发起组件测试 |
| 场景 | 编排 DAG，校验、测试、发布和运行场景 |
| 环境 | 管理 Inventory、Facts、Variables、CredentialRefs、历史 Revision 和连通性检查 |
| 运行 | 查看队列、审批、步骤和实时脱敏日志 |
| 通知 | 查看上游组件发布影响并标记已读 |

左下角“实时通道在线”表示 SSE 已连接。SSE 只负责提示刷新，实际状态以页面重新读取的后端数据为准。

前台会在启动、每 60 秒及页面重新获得焦点时读取不缓存的构建版本。检测到服务端已部署新前台后，页面会阻断旧界面的继续操作并提示“刷新使用新版本”；刷新完成前不要继续确认测试计划、审批或其他写操作。刚部署版本守卫时，部署前已经打开的更老页面本身不包含检测代码，需要手工刷新一次；此后的部署可以自动提示。

## 2. 通用操作规则

### 2.1 资产不可变规则

- 已发布组件 Release 不可编辑；修改时创建新 Draft。
- 已发布场景 Revision 不可编辑；修改时创建新 Revision。
- 每次保存环境配置都会创建新 Environment Revision。
- 已提交 Run 始终使用提交时锁定的 Revision，不会跟随后续编辑变化。

### 2.2 敏感信息规则

以下位置只能填写非敏感数据：

- 组件参数合同和环境约束。
- 场景节点参数、参数绑定、运行时输入。
- 环境 Facts 和 Variables。

密码、token、私钥、加密密钥等必须通过环境的“凭据引用”配置。不要把实际 secret 粘贴到普通 JSON 编辑框、组件发布说明或运行参数中。

### 2.3 危险操作规则

出现以下任一情况时，Run 会停在“等待审批”：

- 动作被标记为 destructive。
- 风险等级为 destructive。
- 动作名称或 Playbook 包含 recovery、clean、destroy、uninstall、rcv 等标记。

只有目标环境的 Owner 可以批准。批准意味着允许平台实际调用 Ansible，并不表示作业已经通过真实环境安全评审。

### 2.4 部署与版本切换交接（分角色）

#### 平台部署人员

1. 部署前确认目标地址、当前分支和工作区，并确认没有 `running`、`queued` 或 `awaiting_approval` 的活动 Run。
2. 运行 Go、前端测试、测试环境嵌入式构建和差异检查；缺少 Ansible 等依赖时必须写成“未验证”，不能写成“通过”。
3. 切换服务前备份二进制、数据库和环境配置；失败时三者一起恢复。
4. 部署后核对服务状态、HTTP、二进制 SHA-256、HTML 构建版本及 `Cache-Control: no-store` 的 `version.json`。
5. 用已经加载版本守卫的旧页面验证升级提示：旧应用区域应为 `inert`，刷新后页面版本应与服务端一致。
6. 默认验收只读取页面和调用计划预览；没有额外授权时，不发布 Release、不提交破坏性 Run、不修改 Inventory。

首次上线版本守卫时，部署前已经打开的页面没有检测代码，需要通知所有角色手工刷新一次。从该版本开始，后续部署会自动阻断旧页面。

#### 组件 Owner

- 计划内部署前保存 Draft；出现版本提示后刷新并重新打开组件和 Release。
- 复核 rollback 的不可变 from/to、verify 目标及环境；旧 `planDigest` 作废，必须重新预览。
- 不修改 Released Release、Environment Revision 或 Inventory 来绕过计划校验。

#### 场景 Owner

- 计划内部署前保存 DAG Draft；未保存的画布状态不会跨刷新保留。
- 刷新后重新确认 Revision、节点锁定 Release、连线、主机组和运行输入，再执行校验或测试。
- 已创建 Run 使用锁定快照，不要因为前台升级重复提交同一场景。

#### 环境 Owner

- 版本提示出现后先刷新，再处理审批、拒绝、取消或环境编辑。
- 审批前重新核对 Run 状态、Environment Revision、主机组、步骤所属版本和破坏性动作。
- 不篡改 Inventory、安装备份记录或 Revision 来消除计划错误；应修复真实合同或重新建立合格基线。

#### 交接证据

- 记录部署前后构建版本、二进制 SHA-256、备份目录、服务状态和 HTTP 状态。
- 对比 Run、Approval、审计事件与 Environment Revision 数量，证明只读验收没有产生业务写入。
- 分开报告代码测试、嵌入式构建、Ansible 门禁和测试环境验收，不能相互替代。

### 2.5 场景节点、组件和环境主机的数量关系

- “六节点集群”表示目标环境 Inventory 中有六台主机，不表示场景画布必须有六个节点。
- 场景节点是逻辑执行单元；一个 bundle 或交付阶段可以通过 Playbook 操作多个软件组件和多台主机。
- 首版支持聚合节点和细粒度节点；当前细粒度核心模板有 21 个节点，分别表达控制面和工作节点上的 Docker、Flannel、kubelet、kube-proxy 等动作。
- `NEWPLATFORM_SEED_PROFILE=identities` 只向空的首版数据库写入身份，不写入场景模板。
- Released Scenario Revision 不可原地扩容或替换节点；需要克隆/新建 Revision，加入精确 Release，重新校验、真实环境测试并发布。
- 判断真实执行内容时应查看节点的 Release、Action、Host Group 与最终 Run Steps，不能只看场景节点总数。

## 3. 组件 Owner 操作手册

### 3.1 新建组件

1. 切换到“组件 Owner A”或“组件 Owner B”。
2. 进入“组件”。
3. 单击“新建组件”。
4. 填写组件名称、标识和说明，并选择 L1-L6 层级、能力类别、组件形态和必选性。
5. 单击“创建组件”。

标识只能使用小写字母、数字和单连字符，例如 `containerd` 或 `kube-apiserver`，并且全局唯一。能力类别会随层级过滤，其中网络类别可用于 L3 kube-proxy 或 L4 CNI。分层只影响目录展示和编排提示，不会自动建立依赖或决定执行顺序。

### 3.2 编辑组件元数据

1. 在左侧选择自己拥有的组件。
2. 单击“编辑组件”。
3. 修改名称、标识、说明或分类元数据。
4. 保存。

该操作不会改变任何已发布 Release，也不会改变历史 Run。

### 3.3 创建组件版本

1. 选择自己拥有的组件。
2. 单击“创建 Draft 编辑合同”。
3. 填写版本号、发布说明、Release 类型和 Breaking 标记。
4. 单击“创建 Draft”。

如果组件已有版本，新 Draft 会复制当前最新可见版本的类型、依赖和动作；如果是首个版本，需要选择 `atomic` 或 `bundle`，然后继续配置。

需要批量录入细粒度组件时，可在组件页使用“导入细粒度模板”，粘贴 JSON 后单击“校验并导入”。平台最多处理 50 个条目，并在首次写入前校验全部组件字段、参数类型、公开参数映射、依赖 DAG、Action 和 Playbook。每个 `release.actions[].playbook` 必须填写 `playbooks[].filename` 中唯一存在的文件名；允许多个 Action 复用同一文件，但拒绝缺失、重复和未引用文件。新组件模板不接受无法预先解析 ID 的显式 Upgrade 或目标版本 Rollback；应使用幂等 Install，或创建后在前台绑定既有 Release。导入器先创建无 Action 的安全 Draft，再保存文件并把后端返回的 `managed/...` 路径绑定回 Action。所有写入都走正常 API 并保留审计，但不会自动验证、加入候选集或发布；中途失败会逐项列出已创建 Draft、已保存文件、已完整绑定和未完成组件，且不会把未绑定项报告为成功。

### 3.4 配置 Draft Release

在发布历史中找到 Draft，使用“配置合同”和“Playbook”（或顶部“编辑依赖和参数”“编辑版本与 Playbook”）维护下列内容：

- 版本和 Release 类型。
- 发布说明和 Breaking 标记。
- 结构化环境约束：架构、操作系统、操作系统版本、Docker 版本和网络栈。
- 结构化参数表：名称、说明、类型、必填、默认值、可见性、枚举和最小长度。可见性必须显式选择 `internal` 或 `public`。
- 依赖下拉框和参数映射：编辑期可锁定同一组件 Owner 的私有 Draft；跨 Owner 只能选择已发布或已验证且共享的候选 Release。直接发布链最终必须全部锁定已发布上游；相互依赖的新 Draft 应走候选集与场景原子发布。
- 结构化生命周期动作及在线 Playbook 编辑器：动作类型、路径、tags、主机组、参数、凭据引用、超时、风险、幂等及版本转换端点分别填写。

示例环境约束：

```json
{
  "architecture": ["amd64", "arm64"],
  "operatingSystem": "Ubuntu",
  "operatingSystemVersion": "18.04",
  "dockerVersion": "20.10.21",
  "ipFamily": "IPv4"
}
```

示例公开参数：`kubeInstallRoot`，类型 string，可见性 public，默认值 `/approot1/paas/kube`。下游 kube-proxy 通过映射导入为 `kubeRoot`。首版不支持 Playbook 执行后动态产生的参数输出。

示例动作：

```json
[
  {
    "name": "install",
    "kind": "install",
    "playbook": "example/install.yml",
    "tags": ["install"],
    "hostGroup": "worker_nodes",
    "allowedParameters": ["install_root", "mode"],
    "timeoutSeconds": 1800,
    "riskLevel": "medium",
    "destructive": false,
    "idempotent": true
  },
  {
    "name": "verify",
    "kind": "verify",
    "playbook": "example/verify.yml",
    "hostGroup": "worker_nodes",
    "allowedParameters": ["install_root"],
    "timeoutSeconds": 300,
    "riskLevel": "low",
    "destructive": false
  }
]
```

注意事项：

- Playbook 必须是允许 Ansible 根目录下的相对路径，不能包含 `..`。
- 直接发布的依赖必须锁定已发布上游 Release；候选 Draft 依赖由同一场景候选集整体校验。
- 参数映射只能选择上游公开参数，且类型必须一致。
- 安装 Playbook 可安全重复执行并能收敛到目标版本时，勾选“幂等安装，同时作为升级作业”；场景即可选择 Upgrade 并复用该动作，不必重复录入。显式 Upgrade 动作仍优先。
- Upgrade 和 Rollback 还必须配置正确的 `fromReleaseId` 和 `toReleaseId`，且映射合同必须与对端 Release 一致。
- 保存 Draft 会使之前的组件测试证据失效，需要重新测试。

### 3.5 构建并发布镜像

1. 确认目标环境的 Environment Owner 已在“环境变量”中配置 `IMAGE_REGISTRY`。
2. 在 Draft 发布行单击“构建镜像”。
3. 选择目标环境；界面会显示该 Environment Revision 的仓库地址。
4. 选择不超过 1 MiB、UTF-8 且包含 `FROM` 的 Dockerfile，填写小写 tag。
5. 单击“上传并构建”，查看 build、push 日志和最终 RepoDigest。

平台把目标固定为 `IMAGE_REGISTRY/components/<组件 slug>:<tag>`，并锁定提交时的
Environment Revision。环境 Owner 后续修改仓库地址不会改变已有构建。构建上下文
只包含 Dockerfile，依赖本地文件的 `COPY`/`ADD` 会失败；Dockerfile 在平台构建机
执行，只应上传可信内容。

### 3.6 录入组件介质

1. 确认介质环境已配置 `FILE_STATION=host:port`。
2. 在 Draft 发布行单击“组件介质”，填写小写下划线别名。
3. 选择“上传文件”或“登记已有路径”，并输入 SHA-256 文本或上传 `.sha256` 文件。
4. 平台调用 file-station 重新计算指纹；Release 只保存站点、固定相对路径和 SHA-256。

上传路径固定为 `components/<组件 slug>/<版本>/<文件名>`。运行时会注入
`<alias>_path`、`<alias>_url`、`<alias>_sha256`。发布后介质不可修改；Draft
移除操作只解除引用，不删除 file-station 上的物理文件。

### 3.7 发起组件测试

1. 在发布历史中单击目标 Release 的“环境验证”。
2. 选择安装验证或回滚验证。回滚验证会展示 Draft rollback 的不可变 from/to 合同；可以选择一个同组件的 Released/Deprecated Release 追加其 Verify，也可以选择“仅执行 Draft rollback”。Verify 目标不会覆盖 rollback 合同。
3. 选择共享环境并填写动作声明允许的运行参数。
4. 如果该 Release 或所选 Verify 目标声明了参数映射，必须填写 `dependencyFixtures`。界面可用上游公开默认值预填，但提交时不会由 API 静默推断。
5. 单击“预览执行计划”，确认每一步所属版本、Playbook、目标主机组和审批要求。
6. 只有预览成功后才能确认提交；任何环境、策略、目标版本或输入变化都会使旧计划失效，需要重新预览。
7. 进入“运行”查看状态、步骤、解析参数来源和日志。

安装验证的主动作按 Upgrade → Install → Configure → Preflight → Inspect 的顺序选择第一个已定义动作；如果定义了 Verify，会自动追加 Verify。测试成功后，版本显示为已验证；如果测试期间 Draft 又被修改，旧测试不会把新内容标记为已验证。Fixture 测试只证明组件能够消费参数，实际组件间传递必须通过场景完整测试。

计划预览执行与正式提交相同的权限、参数、CredentialRef、备份、Playbook 摘要和 Inventory 校验，但不会创建 Run 或 Approval。确认提交时后端重新规划并核对 `planDigest`；若 Draft、环境 Revision、输入或可执行内容已变化，会返回 Conflict，必须刷新计划。

环境 Owner 也可以发起任意可见组件的测试，用于基础设施侧验证。

### 3.8 发布组件版本

1. 找到 Draft，单击“发布”。
2. 查看影响预览：下游组件 Owner、场景 Owner、受影响场景和依赖路径。
3. 检查 Breaking、生命周期完整性、安装验证和回退证据。
4. 单击“确认发布并通知”。

发布前检查清单：

- Release ID 依赖是否准确。
- 环境约束是否与支持范围一致。
- 必填参数是否可解析。
- Playbook、tags、limit 和 timeout 是否正确。
- 危险动作是否显式标注。
- Upgrade/Rollback 起止版本是否正确。
- 发布说明是否足够让下游判断影响。

前台和 `POST /component-releases/{id}/publish` 使用同一门禁：Release 必须定义 install、verify、rollback，具备当前规格摘要下成功的 install + verify 安装验证和 rollback + verify 回退证据；规格摘要过期等同缺少证据。直接发布还要求所有上游已经 Released。

“仅执行 Draft 回退”用于验证清理动作本身，不包含回退目标的 verify，因此不会满足候选共享或发布门禁。要形成交付证据，回退测试必须选择同组件的 Released/Deprecated 目标 Release 并成功执行其 verify。

需要与一组相互依赖的 Draft 一起交付时，不要逐个直接发布：每个组件 Owner 在证据完整后单击“加入候选集”。该显式交接会让场景 Owner 看见 Draft；场景经完整测试后使用“预览候选集并发布”，把 Scenario Revision 与全部候选 Release 原子发布。组件 Owner 可在交接失效前使用“撤回候选”；任何后续合同或 Playbook 修改也会自动撤回候选状态并使验证证据失效。

### 3.9 废弃组件版本

1. 找到 Released 版本。
2. 单击“废弃”。
3. 确认操作。

废弃不会删除 Release，历史 Run 仍可追溯。当前平台不会在废弃前强制阻止已有场景引用，因此操作前应先检查影响通知和关联场景。

### 3.10 处理组件影响通知

1. 进入“通知”。
2. 查看版本变化、Breaking 标记和影响路径。
3. 评估自己组件锁定的上游版本是否需要更新。
4. 如需更新，创建新的 Draft Release，不要修改 Released Release。
5. 完成处理后标记已读。

## 4. 场景 Owner 操作手册

### 4.1 新建场景

1. 切换到“场景 Owner”。
2. 进入“场景”。
3. 单击“新建场景”。
4. 填写名称、标识和说明。
5. 创建场景。

系统会同时创建可编辑的 Revision 1 Draft。

### 4.2 编排 DAG

1. 在左侧组件库选择已发布组件或组件 Owner 已共享的候选 Draft，单击加入画布。
2. 拖动画布节点，使用连接线表达先后顺序。
3. 选择节点，在右侧配置：
   - 显示名称。
   - 生命周期动作。
   - 主机组。
   - 依赖参数来源：唯一上游自动绑定，多个可达节点时必须选择。
   - 节点参数 JSON。已被上游映射的参数不能再填。
   - 允许的运行时输入。
4. 根据需要编辑“执行策略 JSON”。
5. 单击“保存草稿”。

大图可使用“导入模板”一次载入 `nodes`、`edges` 和 `executionPolicy`，或使用“复制模板”导出当前图。边的 V1 合同只包含 `id/source/target`，不保存展示标签。导入会先校验唯一节点/边 ID、有限坐标、端点、无环 DAG、当前可见 Release、组件归属、可执行动作和执行策略；全部通过后才替换浏览器中的未保存草稿，失败时保留原图。复制后重新导入会保留规范化后的节点、边和执行策略。细粒度组件也可在组件中心通过“导入细粒度模板”一次创建最多 50 个组件，系统先创建组件，再按 Release 依赖拓扑创建 Draft 和独立托管 Playbook。

节点会锁定加入时的精确 Release，不会自动跟随组件最新版本。

当前界面和后端都没有场景“阶段”字段。层级标签只用于识别组件分类；主机组决定执行目标，DAG 连线决定顺序。执行策略 JSON 当前只保存、不驱动并发或失败处理，不要把它当作已经生效的运行策略。

Kubernetes reset 与 join 的可复用 Playbook 约束见 [`docs/kubernetes-reset-bootstrap-standard.md`](kubernetes-reset-bootstrap-standard.md)：控制面 reset 使用 `serial: 1`，工作节点可并行；必须处理 kubelet 残留挂载、CNI 和端口后置条件；bootstrap join 优先复用健康的 bootstrap token Secret 和 CA hash。

参数配置示例：

```json
{
  "values": {
    "runtime": "containerd"
  },
  "runInputs": ["install_root"]
}
```

含义：`runtime` 使用节点固定值；`install_root` 允许发起运行时填写。环境不再提供普通参数覆盖。

### 4.3 校验场景

单击“校验”。常见失败原因：

| 提示类型 | 处理方式 |
| --- | --- |
| 图为空或存在环 | 增加节点，调整连线形成 DAG |
| Release 未发布 | 选择 Released Release，或让组件 Owner 完成验证后加入候选集 |
| 硬依赖边未声明合同 | 在目标 Release 的直接依赖中锁定该上游 Release；场景连线和 Release 合同必须同时成立 |
| 动作不存在 | 选择该 Release 实际定义的动作 |
| 缺少依赖节点 | 将锁定的上游 Release 加入画布 |
| 依赖顺序错误 | 增加从上游到下游的可达路径 |
| 必填参数未绑定 | 填默认值、节点值、环境绑定、声明运行输入，或确认上游映射会导入 |
| 需要选择依赖来源 | 在重复上游节点中明确选择 sourceNodeId |
| 映射参数被覆盖 | 删除节点值、绑定或 Run Input 中的映射目标 |

只有当前场景 Owner 可以校验自己的 Draft；后端会再次校验，不依赖画布前端判断。

### 4.4 完整测试 Draft

1. 完成保存和校验。
2. 单击“环境测试”。
3. 选择共享环境。
4. 填写场景声明的运行参数。
5. 单击“开始完整测试”。
6. 在“运行”查看拓扑步骤、自动 Verify 和日志。

测试提交后 Revision 进入 Testing：

- 全部步骤成功：进入 Test Passed。
- 失败、取消、拒绝或服务中断：回到 Draft。
- 危险步骤：先等待环境 Owner 审批。

测试使用当前 Environment Revision。测试完成后环境发生变化，不会自动使场景 Test Passed 失效；发布前仍应人工确认测试环境是否代表目标环境。

### 4.5 发布场景

只有当前 Revision 状态为 Test Passed 时才显示“预览候选集并发布”：

1. 确认完整测试结果。
2. 再次检查锁定的组件版本、主机组和参数。
3. 单击“预览候选集并发布”，核对所有候选组件版本。
4. 确认原子发布。

后端会重新校验 DAG、Release 合同和候选状态，并在同一 SQLite 事务中发布场景 Revision 与全部候选 Release。任一候选被撤回、修改或失去验证状态时整体失败，不会留下部分发布。发布成功后 Revision 和 Release 均不可编辑。

### 4.6 运行已发布场景

1. 选择 Released Revision。
2. 单击“环境运行”。
3. 选择目标环境并填写声明的运行参数。
4. 提交后到“运行”跟踪。

只有 Released Revision 可以走“正式运行”入口。环境 Owner 也能发起可见的已发布场景。

### 4.7 创建新 Revision

当当前 Revision 已发布或已废弃时：

1. 单击“新 Revision”。
2. 确认将创建的 Revision 编号以及它会立即成为当前草稿。
3. 系统复制现有图和执行策略为新 Draft；同一场景不能重复创建活动 Draft。
4. 更新组件节点、依赖顺序或参数。
5. 重新保存、校验、完整测试和发布。

如果误建或不再需要当前 Draft，单击“放弃草稿”并确认。系统保留该 Revision 为“已放弃”历史记录，同时恢复最近的 Released/Deprecated Revision。可以通过 Revision 选择器查看历史版本；历史 Draft 不能编辑或测试，Released Revision 仍可正式运行。

### 4.8 废弃场景 Revision

1. 选择 Released Revision。
2. 单击“废弃”。

废弃后不能再以普通用户视角作为已发布场景使用，但记录会保留。需要继续演进时，从保留的 Revision 创建新 Draft。

## 5. 环境 Owner 操作手册

### 5.1 新建环境

1. 切换到“环境 Owner”。
2. 进入“环境”。
3. 单击“新建环境”。
4. 填写名称、架构、操作系统、网络栈和说明。
5. 创建环境。

系统会创建空 Inventory 的 Revision 1。没有主机的环境不能执行 Run。

Inventory、环境事实、环境变量和凭据引用分区采用同一保存流程：编辑后先单击“保存新 Revision”，查看差异预览，填写非空变更原因，再单击“确认创建 Revision”。当前页存在未保存修改时不能切换环境或恢复历史版本；可以用“放弃本页更改”恢复当前分区。

环境处于 Queued、Awaiting Approval 或 Running 时仍可保存新 Revision，但活动 Run 继续使用提交时锁定的旧 Revision。页面会提示这一边界；不要把新 Revision 误认为已经改变正在执行的目标。

### 5.2 配置 Inventory

1. 选择自己拥有的环境。
2. 打开“Inventory”页签。
3. 单击“添加主机”。
4. 填写主机名、地址、主机组、SSH 用户和端口。
5. 单击“保存新 Revision”，核对差异并填写变更原因后确认。

要求：

- 主机名和地址不能为空。
- 每台主机至少属于一个组。
- 场景或动作引用的 Host Group 必须实际存在。
- `localhost`、`127.0.0.1`、`::1` 会使用 Ansible local connection。
- 其他主机使用 `ansible_host`、`ansible_user` 和 `ansible_port`。

正式使用前应核对环境是否为脱敏模板。内置 OpenFuyao 和 Kubernetes 模板包含占位地址，不能直接当作真实 Inventory。

### 5.3 配置环境事实

1. 打开“环境事实”。
2. 编辑 JSON。
3. 保存新 Revision，核对差异并填写变更原因后确认。

常用键：

```json
{
  "architecture": "amd64",
  "operatingSystem": "Ubuntu",
  "operatingSystemVersion": "18.04",
  "dockerVersion": "20.10.21",
  "ipFamily": "IPv4"
}
```

平台会用这些事实匹配组件环境约束。首版合同只接受 `architecture`、`operatingSystem`、`operatingSystemVersion`、`dockerVersion`、`ipFamily` 等页面写出的规范键；旧键或值不会被转换。

### 5.4 配置非敏感环境变量

1. 打开“环境变量”。
2. 单击“添加变量”，填写大写变量名和字符串值。
3. 保存新 Revision，核对差异并填写变更原因后确认。

变量会直接成为每个组件作业的同名 Ansible extra-vars。例如配置：

```text
IMAGE_REGISTRY=192.168.88.54:5000
FILE_STATION=192.168.88.57:8080
```

Playbook 可以直接使用 `{{ IMAGE_REGISTRY }}`。变量名只允许大写字母、数字和
下划线，且不能以数字开头；不得使用 password、secret、token、private key、
credential 等敏感名称，也不能与组件参数或 CredentialRef 重名。`IMAGE_REGISTRY`
必须是不带 `http://`、`https://`、tag 或 digest 的 Registry 前缀；`FILE_STATION`
同样填写不带协议和路径的 `host:port`。

### 5.5 配置凭据引用

1. 打开“凭据引用”。
2. 单击“添加引用”。
3. 填写变量名称、类型和引用。
4. 保存新 Revision，核对差异并填写变更原因后确认。

两种类型：

| 类型 | 引用填写内容 | 运行时行为 |
| --- | --- | --- |
| `envVarRef` | 大写环境变量名，如 `REGISTRY_TOKEN` | 后端执行时读取该环境变量的值 |
| `sshKeyPath` | 绝对路径，如 `/secure/keys/demo_id_rsa` | 后端执行时校验文件存在并传入同名变量 |

`envVarRef` 只能由大写字母、数字和下划线组成。保存引用时不会读取实际值；Run 真正执行时才解析，因此“保存成功”不代表凭据已可用。

Kubernetes 1.17.5 示例需要：

```bash
export NEWPLATFORM_K8S1175_ENCRYPTION_KEY='<32 字节密钥的 base64 值>'
```

不要把密钥值写入 `.env.example`、Seed、环境 Variables 或 Run Input。

### 5.6 检查环境连通性

环境 Owner 可以单击“立即检查”，对当前 Revision 执行只读 TCP 探测：

- Inventory 主机检查配置的 SSH 端口，未填写时使用 22。
- `IMAGE_REGISTRY` 和 `FILE_STATION` 存在时检查各自 `host:port`。
- 检查结果记录来源 Revision、端点、延迟、时间和 `healthy` / `degraded` 状态，并追加审计事件。

该检查不会登录 SSH、调用 Registry API、下载介质或执行 Ansible。`healthy` 只证明 TCP 端口在本次探测时可连接，不能证明凭据有效、镜像可推送、介质校验和正确或组件能够安装。仓库和文件站若未显式包含端口，会被报告为配置异常。

### 5.7 查看和恢复历史 Revision

“Revision 历史”列出每次保存的编号、创建者、变更原因和时间。历史项只读，不会覆盖或删除。

恢复流程：

1. 找到目标历史 Revision，单击“基于此恢复”。
2. 核对目标编号、主机数、环境变量数和 CredentialRef 数。
3. 填写非空恢复原因并确认。
4. 平台复制目标快照，创建一个编号递增的新 Revision，并把它设为当前版本。

恢复不会修改旧 Revision，也不会改变已排队、等待审批或正在运行的 Run。恢复后应重新执行环境连通性检查；旧检查若来自其他 Revision，页面会标记为过期。

### 5.8 一键回滚整个集群至干净状态

环境 Owner 可以在环境详情单击“一键回滚至干净状态”。该入口不会直接执行命令，而是先生成只读计划。计划必须同时满足：

- 环境调度为空闲，并且前台没有未保存的环境配置。
- 每个安装组件都能追溯到一个已结束且仍保留锁定计划的场景或组件 Run；允许环境存在多层来源。
- 每个安装记录都有与环境、组件、Release、来源 Run 完全匹配的 `backup_ref` 和备份元数据。
- 来源安装 Playbook 的当前 SHA-256 与捕获基线一致。
- 每个 Release 都声明无 from/to 版本端点的独立 `rollback`；版本回退不能冒充“恢复干净状态”。

平台先按来源 Run 的完成时间倒序排列安装层，再读取每个来源的锁定节点计划，过滤安装类步骤并反转原执行顺序。这样后安装的细粒度场景会先恢复到更早场景的状态，随后更早场景再恢复到其安装前状态。控制面和工作节点复用同一组件时仍保留各自的主机组，但只在该组件最后一个节点回滚成功后删除安装记录和远端备份。

操作流程：

1. 查看来源 Run、组件数、回滚节点数、Environment Revision、逆序步骤和备份引用。
2. 完整输入目标环境名称。
3. 单击“创建回滚 Run（待审批）”。提交时后端重新规划并校验 `planDigest`，计划漂移会拒绝创建。
4. 进入“运行”再次复核风险，批准后才进入环境 FIFO 队列。
5. Run 全部成功后，当前安装清单应为空；失败时已完成组件会被清除，尚未完成组件保留原基线，可修复后重新预览剩余计划。

整环境回滚从提交成功到 Run 终态会独占该环境；期间新组件测试、场景运行或第二个整环境回滚都会被数据库拒绝。执行开始前平台还会重新读取完整安装集合，并比对锁定的 Release、备份引用、来源 Run 和 Playbook 指纹；审批等待期间新增、删除或替换任何一项都会失败关闭，必须重新预览。该能力只执行 Release 已声明的 rollback Playbook，不会绕过平台直接 SSH，也不会隐式执行额外的 `kubeadm reset`。

### 5.9 审批危险 Run

1. 进入“运行”。
2. 选择状态为“等待审批”的 Run。
3. 查看发起人、环境、场景/组件、危险原因和锁定信息。
4. 完成线下检查。
5. 单击“批准执行”或“拒绝”。

同一维护窗口有多条待审批 Run 时，可在运行中心右上角使用“批量审批”。弹窗会列出每条 Run 的环境和风险，必须填写审批理由；前端与 `POST /approvals/batch` 都会拒绝空白理由，后端保存裁剪后的统一理由并原子消费整批审批。任一记录过期或不属于当前 Environment Owner 时整批失败，批准后仍按每个环境的 FIFO 队列串行执行。

批准前至少确认：

- Inventory 是预期目标，而不是模板或生产误选环境。
- Playbook 与 Release/Revision 版本正确。
- 介质版本、校验和、仓库地址和网络已核验。
- 凭据引用在后端主机上可解析。
- destructive/recovery/clean/rcv 的实际影响已理解。
- 目标环境已有备份、变更窗口和回退方案。
- 当前环境没有绕过平台运行的外部变更任务。

拒绝后 Run 终止为 Rejected；场景 Draft 测试会回到 Draft。单条审批仍可直接操作，批量批准会记录统一理由和批次大小。

### 5.10 查看环境运行和审计

环境详情显示最近四个相关 Run。完整记录请进入“运行”。

后端提供 `/api/v1/audit-events`，环境 Owner 可读取最近 500 条审计事件；当前前端没有独立审计页面。该接口适合开发和核查使用，不应视为生产级合规审计系统。

## 6. 运行中心操作

### 6.1 查看队列

运行列表可按“全部 / 进行中 / 已结束”过滤。Queued Run 会显示环境内队列位置。同一环境只有一个 Running Run，不同环境可并行。

### 6.2 查看执行步骤

Run 详情展示实际生成的步骤。每个步骤内部还会依次执行：

1. Syntax Check
2. List Hosts
3. Execute

场景节点动作后可能自动追加 Verify，因此界面步骤数可能多于 DAG 节点数。

### 6.3 查看日志

活动 Run 显示“实时日志”，结束后的 Run 显示“历史日志”。二者都包含 stdout、stderr 和平台 system 日志；平台会按已解析的 secret 执行脱敏，并限制保留大小。

工具栏可用“搜索运行日志”按主机、任务或错误关键字过滤，并用“日志流筛选”只看 stdout、stderr 或 system。“复制结果”只复制当前筛选后的可见行；“下载完整日志”始终下载该 Run 的全部脱敏日志，不受当前搜索和流筛选影响。

Failed 或 Interrupted Run 会在详情顶部汇总失败步骤和最近一条可识别的 Ansible 诊断。可以复制诊断或定位到失败步骤，但该摘要只是日志提取结果，最终仍应核对步骤 Summary、完整保留日志和目标主机状态。

如果看不到最新日志：

- 检查左下角 SSE 是否在线。
- 切换到其他 Run 再切回，或刷新页面。
- 以 REST/数据库状态为准；SSE 事件可能丢失。

### 6.4 取消 Run

- Queued：前台“取消”后立即进入 Cancelled。
- Awaiting Approval：API 支持发起人或环境 Owner 取消，但当前前台不显示取消按钮；环境 Owner 可在页面拒绝该 Run。
- Running：平台取消上下文并终止 Ansible 进程组。
- Terminal 状态：不能再次取消。

发起人可取消自己的 Run；环境 Owner 可取消自己环境上的 Run。取消只终止后续执行，不会自动撤销目标主机上已经完成的任务。

### 6.5 状态解释

| 状态 | 含义 |
| --- | --- |
| Awaiting Approval | 包含危险动作，等待环境 Owner 决策 |
| Queued | 已进入环境 FIFO 队列 |
| Running | Ansible 正在执行 |
| Succeeded | 所有步骤成功 |
| Failed | 规划校验、摘要校验或 Ansible 阶段失败 |
| Cancelled | 用户取消或运行中进程被终止 |
| Rejected | 环境 Owner 拒绝危险 Run |
| Interrupted | 服务重启时原 Run 仍在运行 |

## 7. 通知中心操作

组件 Release 发布时，平台沿反向依赖图计算直接和传递影响，并通知：

- 受影响的下游组件 Owner。
- 引用了上游或下游受影响组件的场景 Owner。

通知会显示版本变化、Breaking 标记、影响路径和关联场景。可单条标记已读或“全部已读”；当前 Demo 不支持恢复未读。

## 8. 内置示例使用建议

### 8.1 OpenFuyao Preflight Template

用途：承载三个相互独立的 OpenFuyao 场景：管理集群构建、业务集群控制面
构建、业务节点纳管。节点纳管不会重建控制面，而是先用 master verify 只读
确认 BKECluster，再直接执行 nodes install，不重复执行 common 或 addon。

模板包含四个 TEST-NET 主机组：`bootstrap_host`、
`management_cluster_k8smaster`、`work_cluster_k8smaster`、
`work_cluster_k8snode`。管理与业务场景已分别固定
`cluster_role=manager|work`、策略和目标主机组，界面不提供危险 Run Input
覆盖。普通参数按 `operation`、`network`、`versions`、`artifact_sources`、
`certificates`、`addon_params` 分组，通过点路径 Bindings 映射到 Ansible
实际变量名。

环境 Owner 必须声明动作所需的以下 CredentialRef；缺少任一项时运行会在排队
前失败：`ansible_ssh_pass`、`ENV_DOCKER_SECRET_USERNAME`、
`ENV_DOCKER_SECRET_PASSWORD`、`ENV_CHART_PULL_USERNAME`、
`ENV_CHART_PULL_PASSWORD`。Release 和运行快照只保存必需凭据名称，不保存值。

该环境使用脱敏 TEST-NET 地址，Playbook 依赖内部介质、仓库和合格主机。
包含 `rcv` 的 build 动作会被视为 destructive。不要在没有替换 Inventory、
普通参数、CredentialRef 并完成安全评审时批准。callback URL/token、task ID 和
外部 wrapper 的 `management_cluster_id` 不属于本平台 Release 参数合同。

### 8.2 Kubernetes 1.17.5 SUSE Template

用途：展示最小逻辑组件编排。核心和 Extended 两条场景均保持 Draft；Host Preflight 只读，其余写主机或集群状态的动作均为 destructive。同一 Release 可重复加入，并通过节点 `hostGroup` 分别部署控制节点和工作节点。

正式测试前必须：

- 替换 Inventory 占位地址并核对主机组。
- 核对 Docker 等前置条件。
- 为每个外部压缩包/二进制填写 SHA256，为每个容器镜像填写 digest；未知值不得伪造。
- 设置并复核加密密钥环境变量。
- 检查网络、仓库和介质服务地址。
- 明确备份、变更窗口和回退流程。

当前 Kubernetes 1.17.5 Action 尚未把 `K8S_ENCRYPTION_KEY` 声明为强制 `requiredCredentials`。因此删除环境中的同名 CredentialRef 不会被通用 Planner 在排队前阻断；在合同补齐前，环境 Owner 必须在审批时人工确认 CredentialRef 和 `NEWPLATFORM_K8S1175_ENCRYPTION_KEY` 都已配置，并依赖 Playbook 预检失败关闭。

安装 Run 的运行详情会显示备份基线目录、来源 Run、捕获时间和 Playbook 哈希。
回滚只能使用环境当前安装组件记录中的这条引用；看到“backup metadata does not
match”时应重新执行安装验证并捕获新基线，不要手工指向旧版本目录或复制旧
`.captured`。测试回滚成功后应确认远端测试备份已按
`clusterforge_backup_cleanup_on_success` 清理。

核心场景只含 PKI、Docker、etcd、Kubernetes 核心进程、Flannel 和 CoreDNS；Extended 场景再加入日志、监控、HAProxy、AMC、GlusterFS、pprof 和 RBAC 附加能力。恢复、清理、卸载、Housekeeping 不属于任何安装 Action。该快照尚未在真实 Kubernetes 1.17.5 SUSE 三控制节点加工作节点环境完成安装和收敛验收。

## 9. 常见问题排查

### 9.1 页面提示无法连接 API

- 确认 `make dev` 中 Go 服务仍在运行。
- 检查 `http://127.0.0.1:8080` 是否监听。
- 开发前端应从 `http://127.0.0.1:5173` 访问，以使用 Vite `/api` 代理。

### 9.2 没有操作按钮

- 检查右上角当前身份。
- 资源必须属于当前 Owner；同角色的另一个 Owner 也不能修改。
- Released 资产不可编辑，需要创建新 Draft/Revision。

### 9.3 场景无法测试

- 确认是当前 Draft Revision，不是旧 Revision 或 Released Revision。
- 先保存 DAG，并解决所有校验问题。
- 确认环境事实满足所有组件约束。
- 确认 Inventory 包含每个节点要求的主机组。
- 运行参数只能填写场景明确声明的键。

### 9.4 Run 一直等待审批

切换到目标环境的 Environment Owner，在运行详情批准或拒绝。其他环境 Owner、组件 Owner和场景 Owner都不能审批。

### 9.5 Run 执行前失败

常见原因：

- CredentialRef 对应环境变量未设置或 SSH 文件不存在。
- Playbook 不在允许目录中。
- 排队后 Playbook 或目录树发生变化，摘要不匹配。
- Inventory 为空或缺少 Host Group。
- 回滚找不到当前安装记录的 `backup_ref`，或备份元数据与环境、Release、安装
  Run、当前 Playbook 哈希不一致。
- Ansible 命令未安装或配置错误。

### 9.6 Run 执行中失败

先查看顶部失败摘要并定位失败 Step，再核对 Summary 和历史日志，区分 Syntax Check、List Hosts 还是 Execute 阶段。修复组件 Draft、环境 Revision 或目标主机后重新提交；当前平台不支持从失败步骤继续。

### 9.7 服务重启后的状态

- 原 Running Run 会变为 Interrupted，不会自动重跑。
- 原 Queued Run 会重新进入调度。
- 被中断的场景测试 Revision 会回到 Draft。

## 10. 数据重置与验证

重新幂等写入示例数据：

```bash
make seed
```

重置 Demo 数据并重新 seed：

```bash
make reset-demo
```

`reset-demo` 会删除当前 Demo 数据，执行前应确认数据库中没有需要保留的手工配置或运行记录。

项目验证命令：

```bash
make test
make test-e2e
make build
```

`make test` 包含真实 localhost Ansible 生命周期，需要本机安装 Ansible。测试和构建通过只证明本地代码门禁通过，不代表任何真实集群安装验收完成。
