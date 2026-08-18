# 组件公开参数与上游引用设计 TODO

## 目标

为 Component Release 建立明确的参数合同：

- 每个参数必须显式声明为内部参数或公开参数。
- 下游 Release 声明依赖时，只能引用上游 Release 的公开参数。
- 场景负责把依赖引用绑定到具体上游节点。
- Planner 使用上游节点本次运行的最终解析值，并锁入 Run 快照。
- 本次按首个版本实现，不兼容旧数据库、旧 `parameterSchema` 或旧 API 数据。

## 首版数据合同

废弃 `parameterSchema`，直接使用结构化字段：

```json
{
  "parameters": [
    {
      "name": "kubeInstallRoot",
      "description": "kubelet 安装根目录",
      "type": "string",
      "required": true,
      "defaultValue": "/approot1/paas/kube",
      "visibility": "public",
      "environmentPath": "kubernetes.installRoot",
      "enum": [],
      "minLength": 1
    }
  ]
}
```

参数规则：

- `name`、`description`、`type`、`visibility` 必填。
- `type` 仅允许 `string`、`boolean`、`integer`、`number`、`object`、`array`。
- `visibility` 仅允许 `internal` 或 `public`，不存在默认值。
- `defaultValue`、`environmentPath`、`enum`、`minLength` 可选。
- 敏感参数禁止进入普通参数合同，继续使用 `CredentialRef`。

依赖增加参数映射：

```json
{
  "id": "dependency-kube-proxy-kubelet",
  "upstreamComponentId": "component-kubelet",
  "upstreamReleaseId": "release-kubelet-1.17.5",
  "purpose": "复用 kubelet 安装目录",
  "parameterMappings": [
    {
      "upstreamParameter": "kubeInstallRoot",
      "targetParameter": "kubeRoot"
    }
  ]
}
```

场景节点增加具体来源选择：

```json
{
  "dependencySources": {
    "dependency-kube-proxy-kubelet": "node-kubelet-worker"
  }
}
```

组件独立测试增加依赖参数 Fixture：

```json
{
  "dependencyFixtures": {
    "kubeRoot": "/approot1/paas/kube"
  }
}
```

首版不接受旧字段别名、双读、自动转换或兼容默认值。

## TODO

### 1. 直接切换基础模型和数据库

- [x] 将 Domain 中的 `ParameterSchema` 替换为 `[]ParameterDefinition`。
- [x] 为依赖增加 `[]ParameterMapping`，为场景节点增加 `DependencySources`。
- [x] 直接修改 `001_init.sql`：以 `parameters_json` 替代 `parameter_schema_json`，在依赖表加入非空 `parameter_mappings_json`。
- [x] 不新增迁移文件，不回填或读取旧列。
- [x] 更新 Store 的新增、更新、查询和 Release 克隆逻辑。
- [x] 将参数和映射纳入 Release 规格摘要。
- [x] 更新所有 Kubernetes、OpenFuyao 和 API 测试种子为新结构。
- [x] 使用全新 SQLite 数据库验收；已有 `data/newplatform.db`、WAL 和 SHM 文件按废弃数据处理并重新初始化。

### 2. 发布合同校验

- [x] 校验参数名称唯一、类型合法、说明和可见性明确。
- [x] 校验默认值及 enum 与参数类型一致。
- [x] 校验 Action 引用的参数必须存在于当前 Release。
- [x] 校验每个映射的上游参数存在且为 `public`。
- [x] 校验目标参数存在、每个目标最多映射一次，且上下游类型完全一致。
- [x] 禁止公开或映射敏感参数。
- [x] 依赖仍必须锁定 Released 上游 Release，并沿用循环依赖校验。
- [x] Released Release 保持不可修改；参数合同变化必须克隆新 Draft。
- [x] Upgrade/Rollback 涉及参数映射时，要求起止 Release 的映射合同一致，否则拒绝发布。

### 3. 场景来源选择和校验

- [x] 根据依赖锁定的上游 Release 查找位于下游之前的可达节点。
- [x] 只有一个可达节点时自动选用，无需保存冗余选择。
- [x] 存在多个可达节点时，要求在 `dependencySources` 中明确选择。
- [x] 校验所选节点存在、Release 一致且在 DAG 中可达。
- [x] 映射目标参数不得同时出现在节点值、环境绑定或 Run Input 中。
- [x] 无上游节点、来源不唯一、选择错误或顺序错误时，在场景验证阶段返回带节点 ID 的明确错误。
- [x] Verify 节点继续遵守只观察既有组件的规则；只有实际需要参数导入时才要求来源节点。

### 4. Planner 参数解析

- [x] 按 DAG 拓扑顺序解析节点，并保存 `resolvedParametersByNode`。
- [x] 每个上游节点先按“默认值 → 节点值/环境绑定 → Environment Parameters → Run Input”得到最终值。
- [x] 下游完成自身参数解析后，再应用依赖映射；导入值为最终值，不允许本地覆盖。
- [x] 同名 Environment Parameter 对映射目标不生效，避免破坏组件间一致性。
- [x] 支持 A → B → C 的连续公开参数传递。
- [x] 上游公开参数无最终值且下游目标为必填时，在创建 Run 前失败。
- [x] 使用下游参数名把最终值传给 Ansible，不泄漏额外上游变量。
- [x] 自动追加的 Verify 步骤复用同一组已解析参数。
- [x] Run 快照记录参数值及 `sourceNodeId`、`upstreamParameter`、`targetParameter` 来源信息。

### 5. 组件独立测试

- [x] 有参数映射的组件测试要求通过 `dependencyFixtures` 提供映射目标测试值。
- [x] Fixture 键只能是当前 Release 的映射目标，并执行相同类型校验。
- [x] UI 可用上游公开参数默认值预填，但 API 不静默推断。
- [x] Run 快照将来源标记为 `dependency_fixture`。
- [x] 文档明确：Fixture 测试只证明组件能够消费参数，实际组件间传递必须通过场景完整测试。

### 6. 组件发布界面

- [x] 将参数 Schema JSON 编辑框替换为结构化参数表格。
- [x] 支持参数新增、删除、排序，以及名称、说明、类型、必填、默认值、可见性、环境路径和约束编辑。
- [x] 根据类型提供对应默认值编辑器；对象和数组使用局部 JSON 编辑器。
- [x] 依赖编辑器通过组件和 Released Release 下拉框选择上游。
- [x] 参数映射的来源下拉框只展示该上游 Release 的公开参数。
- [x] 目标下拉框只展示当前 Release 尚未映射的参数。
- [x] 在保存前显示类型不匹配、敏感参数和重复映射错误。
- [x] Release 详情页分区展示内部参数、公开参数及被下游引用的映射。

### 7. 场景和运行界面

- [x] 场景节点检查器增加“依赖参数来源”区域。
- [x] 唯一来源显示为自动绑定；多来源显示必选下拉框。
- [x] 来源候选项显示节点名称、Release 版本和主机组。
- [x] 参数已由上游映射时，禁用对应节点值、环境绑定和 Run Input 配置。
- [x] Run 详情展示下游参数值及其上游来源，但不展示 CredentialRef 实际值。

### 8. 示例和文档

- [x] 在首版种子中声明一个真实的 kubelet 公开安装目录参数。
- [x] 为一个下游组件声明本地目标参数和参数映射。
- [x] 在包含 control/worker 重复 kubelet 节点的场景中示范具体来源选择。
- [x] 确保示例 Ansible 入口实际消费下游参数，并保留 `/approot1/paas/kube` 作为首版默认路径。
- [x] 更新后端设计和操作手册，说明发布合同、来源选择、解析顺序、Fixture 测试和敏感参数边界。
- [x] 明确首版不支持 Playbook 执行后动态产生的参数输出。

## 测试与验收

- [x] Store 测试覆盖新基础表结构、参数和映射读写、Release 克隆。
- [x] 删除旧数据库迁移兼容测试，改为全新数据库建库和重复启动测试。
- [x] Service 测试覆盖参数定义、公开限制、类型匹配、敏感信息和发布校验。
- [x] Scenario 测试覆盖唯一来源、多来源选择、错误 Release、不可达来源和覆盖冲突。
- [x] Planner 测试证明上游环境覆盖后的最终值传入下游，并进入不可变快照。
- [x] 测试多级传递、缺失可选值、缺失必填值、Fixture 和 Upgrade/Rollback 限制。
- [x] API 测试覆盖唯一 JSON 合同，不保留旧字段请求。
- [x] 前端测试覆盖结构化参数编辑、公开参数过滤和场景来源选择。
- [x] 执行 `go test ./...`、`pnpm --dir web test`、`make test`、`make build`。
- [x] 本地门禁只证明模型、API、UI 和 Ansible 静态合同通过；真实 Kubernetes 部署与路径生效情况单独验收。

## 完成标准

- 组件 Owner 能在发布前明确每个参数的用途、类型和可见性。
- 下游依赖只能选择上游公开参数，不能手写任意参数名。
- 重复上游节点不会产生隐式或不确定取值。
- 下游获得的是上游节点本次运行的最终解析值。
- 参数合同、映射、来源和值全部进入锁定快照并可审计。
- 代码中不存在旧 `parameterSchema`、旧数据库列、兼容读取或迁移回填逻辑。

## 非目标与边界

- 首版只传递运行前可解析的配置值，不提供 Playbook 动态输出协议。
- 参数引用只在同一个场景 Run 内生效，不读取历史运行结果。
- `CredentialRef` 不属于普通参数，也不能作为公开参数被下游选择。
- Fixture 测试不替代场景完整测试。
- 文档完成不代表上述 TODO 已实施；各项只有在对应代码、测试及验收门禁通过后才能勾选。
