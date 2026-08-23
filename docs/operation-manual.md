# ClusterForge 平台操作手册（分角色）

> 文档基线：2026-08-23 当前工作区代码
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

生产式本地构建：

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
| 环境 | 管理 Inventory、Facts、Parameters 和 CredentialRefs |
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
- 环境 Facts 和 Parameters。

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
2. 运行 Go、前端测试、生产构建和差异检查；缺少 Ansible 等依赖时必须写成“未验证”，不能写成“通过”。
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
- 分开报告代码测试、生产构建、Ansible 门禁和真实环境验收，不能相互替代。

### 2.5 场景节点、组件和环境主机的数量关系

- “六节点集群”表示目标环境 Inventory 中有六台主机，不表示场景画布必须有六个节点。
- 场景节点是逻辑执行单元；一个 bundle 或交付阶段可以通过 Playbook 操作多个软件组件和多台主机。
- 历史的“Kubernetes 1.17.5 六节点集群搭建”Released Revision 使用“主机预检 → 节点准备 → 集群引导 → 集群验收”四个聚合节点，其中“集群引导”是 bundle。
- 当前代码中的细粒度核心模板有 21 个节点，分别表达控制面和工作节点上的 Docker、Flannel、kubelet、kube-proxy 等动作；部署环境使用 `NEWPLATFORM_SEED_PROFILE=identities` 时不会把该模板写入已有数据库。
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
2. 单击“创建新版本”。
3. 填写版本号和发布说明。
4. 如有不兼容变更，勾选“包含不兼容变更”。
5. 创建 Draft。

如果组件已有版本，新 Draft 会复制当前最新可见版本的类型、依赖和动作；如果是首个版本，需要选择 `atomic` 或 `bundle`，然后继续配置。

### 3.4 配置 Draft Release

在发布历史中找到 Draft，单击“配置”。配置项包括：

- 版本和 Release 类型。
- 发布说明和 Breaking 标记。
- 环境约束 JSON。
- 结构化参数表：名称、说明、类型、必填、默认值、可见性、环境路径和约束。可见性必须显式选择 `internal` 或 `public`。
- 依赖下拉框：只能选择已发布上游 Release，并映射其公开参数。
- Ansible 动作 JSON。

示例环境约束：

```json
{
  "architecture": ["amd64", "arm64"],
  "os": "Kylin V10",
  "network": "IPv4"
}
```

示例公开参数：`kubeInstallRoot`，类型 string，可见性 public，默认值 `/approot1/paas/kube`。下游 kube-proxy 通过映射导入为 `kubeRoot`。首版不支持 Playbook 执行后动态产生的参数输出。

示例动作：

```json
[
  {
    "name": "install",
    "type": "install",
    "playbook": "example/install.yml",
    "tags": ["install"],
    "hostGroup": "worker_nodes",
    "allowedParameters": ["install_root", "mode"],
    "timeoutSeconds": 1800,
    "riskLevel": "medium",
    "destructive": false
  },
  {
    "name": "verify",
    "type": "verify",
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
- 依赖必须锁定已发布的上游 Release。
- 参数映射只能选择上游公开参数，且类型必须一致。
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

### 3.6 发起组件测试

1. 在发布历史中单击目标 Release 的“测试”。
2. 选择安装验证或回滚验证。回滚验证会展示 Draft rollback 的不可变 from/to 合同；可以选择一个同组件的 Released/Deprecated Release 追加其 Verify，也可以选择“仅执行 Draft rollback”。Verify 目标不会覆盖 rollback 合同。
3. 选择共享环境并填写动作声明允许的运行参数。
4. 如果该 Release 或所选 Verify 目标声明了参数映射，必须填写 `dependencyFixtures`。界面可用上游公开默认值预填，但提交时不会由 API 静默推断。
5. 单击“预览执行计划”，确认每一步所属版本、Playbook、目标主机组和审批要求。
6. 只有预览成功后才能确认提交；任何环境、策略、目标版本或输入变化都会使旧计划失效，需要重新预览。
7. 进入“运行”查看状态、步骤、解析参数来源和日志。

平台优先测试 Upgrade；没有 Upgrade 时测试 Install。如果定义了 Verify，会自动追加 Verify。测试成功后，版本显示为已验证；如果测试期间 Draft 又被修改，旧测试不会把新内容标记为已验证。Fixture 测试只证明组件能够消费参数，实际组件间传递必须通过场景完整测试。

计划预览执行与正式提交相同的权限、参数、CredentialRef、备份、Playbook 摘要和 Inventory 校验，但不会创建 Run 或 Approval。确认提交时后端重新规划并核对 `planDigest`；若 Draft、环境 Revision、输入或可执行内容已变化，会返回 Conflict，必须刷新计划。

环境 Owner 也可以发起任意可见组件的测试，用于基础设施侧验证。

### 3.7 发布组件版本

1. 找到 Draft，单击“发布”。
2. 查看影响预览：下游组件 Owner、场景 Owner、受影响场景和依赖路径。
3. 检查 Breaking 和验证状态。
4. 单击“确认发布并通知”。

发布前检查清单：

- Release ID 依赖是否准确。
- 环境约束是否与支持范围一致。
- 必填参数是否可解析。
- Playbook、tags、limit 和 timeout 是否正确。
- 危险动作是否显式标注。
- Upgrade/Rollback 起止版本是否正确。
- 发布说明是否足够让下游判断影响。

未验证版本也允许发布，但下游会看到风险。发布后平台只发送通知，不会自动升级场景中的锁定版本。

### 3.8 废弃组件版本

1. 找到 Released 版本。
2. 单击“废弃”。
3. 确认操作。

废弃不会删除 Release，历史 Run 仍可追溯。当前平台不会在废弃前强制阻止已有场景引用，因此操作前应先检查影响通知和关联场景。

### 3.9 处理组件影响通知

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

1. 在左侧组件库选择已发布组件，单击加入画布。
2. 拖动画布节点，使用连接线表达先后顺序。
3. 选择节点，在右侧配置：
   - 显示名称。
   - 生命周期动作。
   - 主机组。
   - 依赖参数来源：唯一上游自动绑定，多个可达节点时必须选择。
   - 节点参数 JSON。已被上游映射的参数不能再填。
   - 环境参数绑定 JSON。
   - 允许的运行时输入。
4. 根据需要编辑“执行策略 JSON”。
5. 单击“保存草稿”。

节点会锁定加入时的精确 Release，不会自动跟随组件最新版本。

环境参数绑定支持点路径。例如 `cluster_id` 可以绑定到
`operation.work_cluster_id`。解析时完整顶层键优先，因此原有扁平键仍兼容；
路径不存在或中间节点不是对象时，该参数保持未绑定并在必填校验阶段失败。

当前界面虽然显示并允许选择 Bootstrap / Management Cluster“阶段”，但后端不会持久化这个字段；重新读取时会根据主机组名称推导显示。阶段不决定执行顺序，请用 DAG 连线表达顺序。执行策略 JSON 当前也只保存、不驱动并发或失败处理，不要把它当作已经生效的运行策略。

参数配置示例：

```json
{
  "values": {
    "runtime": "containerd"
  },
  "bindings": {
    "cluster_version": "clusterVersion"
  },
  "runInputs": ["install_root"]
}
```

含义：`runtime` 使用节点固定值；`cluster_version` 从环境参数 `clusterVersion` 读取；`install_root` 允许发起运行时填写。

### 4.3 校验场景

单击“校验”。常见失败原因：

| 提示类型 | 处理方式 |
| --- | --- |
| 图为空或存在环 | 增加节点，调整连线形成 DAG |
| Release 未发布 | 选择 Released Release |
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

只有当前 Revision 状态为 Test Passed 时才显示“发布”：

1. 确认完整测试结果。
2. 再次检查锁定的组件版本、主机组和参数。
3. 单击“发布”。

后端会重新校验 DAG。发布成功后 Revision 不可编辑。

### 4.6 运行已发布场景

1. 选择 Released Revision。
2. 单击“环境运行”。
3. 选择目标环境并填写声明的运行参数。
4. 提交后到“运行”跟踪。

只有 Released Revision 可以走“正式运行”入口。环境 Owner 也能发起可见的已发布场景。

### 4.7 创建新 Revision

当当前 Revision 已发布或已废弃时：

1. 单击“新 Revision”。
2. 系统复制现有图和执行策略为新 Draft。
3. 更新组件节点、依赖顺序或参数。
4. 重新保存、校验、完整测试和发布。

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

### 5.2 配置 Inventory

1. 选择自己拥有的环境。
2. 打开“Inventory”页签。
3. 单击“添加主机”。
4. 填写主机名、地址、主机组、SSH 用户和端口。
5. 单击“保存新 Revision”。

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
3. 保存新 Revision。

常用键：

```json
{
  "architecture": "amd64",
  "os": "Kylin V10",
  "network": "IPv4"
}
```

平台会用这些事实匹配组件环境约束。键支持部分别名，例如 architecture/arch、os/operatingSystem/distribution、network/ipFamily。

### 5.4 配置普通环境参数

1. 打开“环境参数”。
2. 填写不敏感 JSON。
3. 保存新 Revision。

环境参数的优先级高于参数合同默认值和场景节点值。只有发起时显式允许的 Run Input 能进一步覆盖。

不要填写 `password`、`secret`、`token`、`privateKey`、`encryptionKey`、`credential` 等敏感键；后端会拒绝保存。

### 5.5 配置非敏感环境变量

1. 打开“环境变量”。
2. 单击“添加变量”，填写大写变量名和字符串值。
3. 保存新 Revision。

变量会直接成为每个组件作业的同名 Ansible extra-vars。例如配置：

```text
IMAGE_REGISTRY=192.168.88.54:5000
```

Playbook 可以直接使用 `{{ IMAGE_REGISTRY }}`。变量名只允许大写字母、数字和
下划线，且不能以数字开头；不得使用 password、secret、token、private key、
credential 等敏感名称，也不能与组件参数或 CredentialRef 重名。`IMAGE_REGISTRY`
必须是不带 `http://`、`https://`、tag 或 digest 的 Registry 前缀。

### 5.6 配置凭据引用

1. 打开“凭据引用”。
2. 单击“添加引用”。
3. 填写变量名称、类型和引用。
4. 保存新 Revision。

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

不要把密钥值写入 `.env.example`、seed、环境 Parameters 或 Run Input。

### 5.7 审批危险 Run

1. 进入“运行”。
2. 选择状态为“等待审批”的 Run。
3. 查看发起人、环境、场景/组件、危险原因和锁定信息。
4. 完成线下检查。
5. 单击“批准执行”或“拒绝”。

批准前至少确认：

- Inventory 是预期目标，而不是模板或生产误选环境。
- Playbook 与 Release/Revision 版本正确。
- 介质版本、校验和、仓库地址和网络已核验。
- 凭据引用在后端主机上可解析。
- destructive/recovery/clean/rcv 的实际影响已理解。
- 目标环境已有备份、变更窗口和回退方案。
- 当前环境没有绕过平台运行的外部变更任务。

拒绝后 Run 终止为 Rejected；场景 Draft 测试会回到 Draft。当前界面的拒绝动作没有填写理由的输入框，API 支持 reason 字段，但页面会提交空理由。

### 5.8 查看环境运行和审计

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

“实时日志”显示 stdout、stderr 和平台 system 日志。平台会按已解析的 secret 执行脱敏，并限制保留大小。

如果看不到最新日志：

- 检查左下角 SSE 是否在线。
- 切换到其他 Run 再切回，或刷新页面。
- 以 REST/数据库状态为准；SSE 事件可能丢失。

### 6.4 取消 Run

- Queued 或 Awaiting Approval：取消后立即进入 Cancelled。
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
确认 BKECluster，再按原作业顺序执行 common 和 nodes。

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
旧 wrapper 的 `management_cluster_id` 不属于本平台 Release 参数合同。

### 8.2 Kubernetes 1.17.5 SUSE Template

用途：展示最小逻辑组件编排。核心和 Extended 两条场景均保持 Draft；Host Preflight 只读，其余写主机或集群状态的动作均为 destructive。同一 Release 可重复加入，并通过节点 `hostGroup` 分别部署控制节点和工作节点。

正式测试前必须：

- 替换 Inventory 占位地址并核对主机组。
- 核对 Docker 等前置条件。
- 为每个外部压缩包/二进制填写 SHA256，为每个容器镜像填写 digest；未知值不得伪造。
- 设置并复核加密密钥环境变量。
- 检查网络、仓库和介质服务地址。
- 明确备份、变更窗口和回退流程。

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

查看失败 Step 的 Summary 和日志，区分 Syntax Check、List Hosts 还是 Execute 阶段。修复组件 Draft、环境 Revision 或目标主机后重新提交；当前平台不支持从失败步骤继续。

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
