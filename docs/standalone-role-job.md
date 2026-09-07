# 原生 Ansible 作业包

新作业使用 `clusterforge-native-job-v3` 契约，直接通过 `ansible-playbook` 运行，不需要 ClusterForge 服务或 Go 启动器。旧 `clusterforge-role-job-v1` 和 `clusterforge-native-job-v2` 下载包保留原内容、摘要、入口和版本要求。

## 执行方式

作业包含 `site.yml`、只用于预检的 `syntax.yml`、精确 Release 的 Role、`inventory.ini`、控制与回调插件、完整计划及文件摘要。运行目录必须可信且不能对其他用户开放写入。进入解压目录后执行：

```sh
export ANSIBLE_CONFIG="$PWD/ansible.cfg"
export PYTHONDONTWRITEBYTECODE=1
ansible-playbook -i inventory.ini site.yml -e @/private/run-inputs.json
```

包外的私有输入文件示例：

```json
{
  "cf_job": {
    "operation": "install",
    "results": "/private/cluster-results"
  },
  "cf_credentials": {}
}
```

`operation` 支持 `install`、`resume`、`rollback-preview`、`rollback`。续跑和回滚沿用同一个结果目录。回滚必须填写当前预览返回的 `cf_job.expectedPlanDigest`；可选的 `cf_job.nodes` 使用 `componentId/nodeId`，只能选逆序计划的依赖安全前缀。

结果目录在包外，权限为 0700；首次创建时自动设置。目录锁阻止同时执行，`receipt.json` 原子保存并同步到磁盘，`events.jsonl` 记录脱敏的步骤、主机和任务结果。保留原包、结果目录和远端原始备份。没有确认完成的阶段视为中断，不能根据日志中的最后一行推断成功。

## 恢复边界

前检查失败时重做前检查；主体失败或中断时，只有锁定计划中具备安全重试资格的动作才能从前检查重跑。主体已确认成功、后检查失败时只重做后检查。续跑会先复查已完成上游组件。会修改资源的场景验收不能自动重试；应先检查其保留的现场。

不得改写 Role、Inventory、参数或运行时后沿用断点。不得通过 `--limit`、`--tags`、`--skip-tags`、`--start-at-task`、`--step` 或 `--check` 替代恢复计划。外部变量只接受 `cf_job`、`cf_credentials` 和连接认证、Python 解释器设置。组件仍通过 `cf.inputs`、`cf.context`、`cf.credentials` 取值。

执行器停止后，已经派发的远端命令仍可能完成；停止不会撤销已有修改。回滚根据实际开始执行的动作和原始备份编排，后检查通过后才确认恢复成功。

## 运行环境与介质

Ansible 和控制端 Python 必须与清单中的精确版本一致，包含 Ansible 2.8.8 / Python 3.6.9 支持。目标机连接及其解释器由锁定 Inventory 和连接设置确定。插件使用 Python 3.6 兼容语法。

介质及镜像使用锁定的来源、目标和摘要。执行器在组件变更前准备并校验；镜像平移需要控制端 Docker。凭据、私钥和镜像仓库认证由操作者在包外提供。独立运行不会连接平台获取凭据，也不会回写平台审批、测试或安装记录。

## 完整交付与历史包

运行页面的“下载本次作业包”保留本次实际执行快照；续跑的快照可能只包含剩余步骤。“下载已验证完整作业”对应 `GET /api/v1/runs/{id}/job-bundle?verified=true`，要求场景成功并具有完整业务验收证据。

正式交付沿续跑链核对环境、版本、源码、参数及各阶段状态，返回根 Run 的完整流程，清单 `verification` 记录证据 Run 链和原始包摘要。缺少该字段的包不能视为已验证的完整集群交付。场景预览导出的调试包仍使用既有 `job-plan`、`job-bundle` 接口。

## 独立介质准备与包依赖

CLI 通过 `internal/jobcli/media.go` 读取 JobPlan.Metadata 的介质字段，并调用 `internal/delivery.Prepare`。
缺失的计划目标按显式决定转移，转移后再次核验身份；已有正确内容直接复用。摘要不一致和目标缺失保持不同错误。
每次底层 Probe 有三分钟上限并遵守父 context 取消；执行和回执仍由 CLI 管理。
包含 `source_verify` 的升级作业仍在来源验证完成后才准备介质。

平台与 CLI 共用介质类型及传输实现，平台另行记录准备进度、审批和逐 Run 交付结果。
CLI 不导入 service、store、api 或 SQLite。完整计划使用 `ansible.PlanDigest` 的既有 JSON + SHA-256 算法；
介质投影不得覆盖原 Metadata，也不得用于计算完整计划摘要。历史作业包和回执不因内部代码拆分而重写。
