# 组件 Role 编写规范

适用于 `clusterforge-v1-20260905-scenario-lifecycle` 数据库契约（包含 Role 单作业与场景生命周期）。新库只初始化身份和平台目录；旧数据库和旧 Playbook 不做自动转换。参考快照不会作为可执行 Release 自动装入。

## 动作与入口

Release 必须定义 install 和 rollback。configure、upgrade、uninstall 为可选执行动作。同一种执行动作只能有一个。部署、配置、升级、卸载通过 `preCheckActionId`、`postCheckActionId` 绑定本 Release 的 check 动作；回滚前检查可选，回滚后检查默认复用实际被撤销动作的前置检查，也可以显式绑定专用检查。检查可以复用，不能继续绑定检查。Draft 允许尚未绑定，执行、评审和发布要求完整。

平台生成入口：

```text
Role 工作区/
  tasks/install.yml
  tasks/rollback.yml
  tasks/checks/<actionId>.yml
  tasks/helpers/...
  templates/...
  files/...
  handlers/main.yml
```

入口内容是非空 YAML 任务列表。组件 Owner 在线编辑或上传文件；平台管理主机范围、提权和失败策略，facts 由 YAML 自行采集。动作源码读写、删除按 Action ID 定位。删除引用中的检查会被拒绝。保存使用文件 SHA 与工作区树 SHA 校验并发修改；合同、绑定或任意工作区文件变化会使既有测试证据失效。

## 参数和检查

参数通过 `cf.inputs` 读取，执行信息通过 `cf.context` 读取，凭据通过 `cf.credentials` 读取。例如：

```yaml
- name: Validate version input
  ansible.builtin.assert:
    that: cf.inputs.version is defined
```

`cf.context` 包含 environmentId、componentId、releaseId、nodeId、actionId、parentActionId、phase、stepId、backupRef。前后检查使用执行动作的锁定参数和目标范围；默认回滚后检查保留来源动作的参数并使用当前回滚恢复上下文。检查应自行读取状态，不能依赖安装留下的 register、set_fact 或 handler。执行器在各阶段重置局部变量和 facts。组件之间只能通过显式参数合同映射传值。

检查不得承担安装、修复、清理或触发 handler。检查中的 command/shell 探测必须声明 `changed_when: false` 并明确有效返回值；该声明不会把命令变成只读，Owner 仍须审查实际命令。最终条件不满足必须失败。禁止 ignore_errors、ignore_unreachable、rescue 吞错、异步脱离任务、跨 Role 引用和 meta.dependencies。辅助任务使用固定的本 Role 相对路径。

## 原始恢复基线

执行上下文注入 `clusterforge_backup_ref`、`clusterforge_backup_marker`、`clusterforge_backup_metadata` 等恢复参数。安装主体在改动前捕获原始状态。存在捕获标记时不得覆盖基线；重试沿用原始引用。回滚主体恢复该基线，回滚后检查独立确认恢复结果。保留恢复材料至确认完成，不在主体完成时提前删除。

可安全重试声明只适用于已验证部分执行后可以重新执行的动作。执行器还会校验当前合同与运行时是否已有该动作的成功测试证据。后检查失败时平台只重跑后检查；已绑定的前检查不能因幂等声明而跳过；未绑定前检查的回滚可按两步计划安全重试。

可从 [原生 Role 示例包](../examples/components/role-file-example.json) 开始。它只修改 `/tmp/clusterforge-example-demo` 示例文件，并恢复原始内容或原始不存在状态，导入后仍是 Draft。


## Ansible 2.8.8 兼容

平台兼容 Ansible 2.8.8 和对应的 Python 3.6.9。组件入口仍是 Role 任务列表，参数入口和生命周期合同保持不变。平台不会自动改写组件源码，也不会使新版专属模块或参数在旧版中生效。

面向 2.8.8 的组件应使用已经在该运行时验证过的模块名称和参数。平台生成的控制任务使用短模块名及私有动态 Role；编写组件时不要依赖 `import_role public:false` 的新版行为。脚本需兼容实际目标 Python。语法预检不能替代模块参数、远端环境和实际恢复行为的验证。

普通任务失败能否重跑由动作的安全重试声明及精确合同、运行时测试证据共同决定。成功的主体不会为了重做后检查而重复执行。修改源码后必须重新测试，不能直接使用旧版本断点。

## facts、运行条件与残留检查

平台不自动采集 facts，也不根据组件固定表单生成探测。平台继续检查执行器、目标 Python、连通性和交付介质。组件在前置检查 YAML 中自行检查必要条件及残留；需要主机事实时显式调用 `setup`。后续动作需要 facts 时也须自行采集，不能依赖前一阶段的 facts。场景验收将工具检查写在自身 YAML 中。

```yaml
- name: Collect facts used by this action
  setup:
- name: Inspect required tool
  command: sh -c "command -v sh"
  changed_when: false
- name: Inspect residual marker
  stat:
    path: /tmp/example-component-marker
  register: cf_local_marker
- name: Require a clean initial state
  assert:
    that: not cf_local_marker.stat.exists
```

组件工作区由平台按组件、版本线和完整 Release ID 分配，组件依赖不会改变目录层级。不同组件的根目录不得相同或互为父子；打包后的 Role 并列存放。辅助任务、模板和本地文件使用当前 Role 内的相对路径，禁止通过父目录、符号链接或跨 Role 引用复用其他组件文件。`copy`、`template`、`unarchive` 使用 YAML 映射参数声明 `src`，本地文件查询使用固定相对路径；目标主机上的源文件需显式声明 `remote_src: true`。

新写入接口不再接受 `gatherFacts`、`resourceContract.checks` 或验收 `runtimeChecks`。旧定义仍可读取，草稿编辑器会列出只读迁入待办。Owner 将逻辑写入对应 YAML 后勾选确认，源码保存请求携带 `confirmYamlMigration: true`，源码及旧配置清除共同提交。普通保存保留旧配置；未完成迁入的定义不能产生新执行或发布证据。已发布版本通过新 Draft 修订，历史 Run、摘要和冻结作业不改写。

## 共享目录与公开路径

组件自己的目录由其参数与生命周期管理。只约定共享字符串时使用现有受治理环境变量；需要创建、权限管理和验收时，由主机基础准备组件管理目录并公开路径，下游通过精确版本依赖映射引用。下游在自己的目标主机上检查可用性，不能因获得字符串就认定共享存储已挂载。

可导入的[主机基础目录与消费组件样板](../examples/components/README.md)包含公开路径、独立检查、原始基线捕获和逆序恢复。样板只使用临时目录，不会迁入或接管现有组件目录。

资源共享声明的读取范围必须完全落在提供方授权范围内：目录树读取不能由子目录或文件声明满足，也不能包含提供方排除的路径。多个提供实例可按目标主机合并覆盖，但消费者每台主机都必须有来源。平台规划与独立作业运行时使用相同规则；执行前的路径不存在探测也会拒绝悬空符号链接。
