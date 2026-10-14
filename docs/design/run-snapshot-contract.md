# Run 快照与直接执行合同

当前版本用于全新数据库，不提供旧审批数据迁移。

## 合同边界

`internal/domain/run_snapshot.go` 定义唯一运行时合同 `clusterforge-run-v2`。`domain.Run.Snapshot` 保存冻结业务输入，`domain.Run.DeliveryResults` 保存本次交付观察。Run ID、Kind、环境 Revision、关联 Release/Revision、续跑链仍由 Run 关系字段提供。

| 分组 | 数据及用途 |
| --- | --- |
| contract | 精确合同标识；不接受场景数字版本 |
| subject | 组件/场景定义摘要、组件测试证据类别、场景关联 |
| plan | Runtime、Steps、ParentSteps、TreeDigest |
| inputs | 参数来源与脱敏 CredentialRefs；用户参数值保持开放 JSON |
| delivery | 交付要求、自动交付策略、计划中的镜像及介质传输 |
| scenarioExecution | 三种执行模式、来源 Revision、目标节点、安装基线、验收作业和恢复回执 |
| submission | 幂等标识、请求摘要和提交绑定的预览摘要 |
| retry | 恢复状态摘要及续跑捕获的场景基线 |
| recovery | 组件安装基线、环境恢复摘要、重置目标及边界摘要 |

`DecodeRunSnapshot` 拒绝未知控制字段、类型错误、未知合同、尾随 JSON、必需成员缺失或 null；整数不接受字符串或小数。控制字段的存在检查独立于 Go 零值，`0`、`false` 和 `[]` 不等同于缺失。参数使用 JSON number 解码，避免大整数经过 float64 舍入。`ValidateRunSnapshot` 按 Run Kind 检查执行语义、步骤身份、场景模式、必需基线/提交摘要及续跑上下文。环境回滚即使关联场景，也不取得场景安装或升级语义。

domain 只依赖标准库，统一拥有计划步骤、运行时、参数来源、目标节点、交付数据和执行回执。`RunExecutionPlan` 是准备计划及原 Native Job metadata 的平面投影。`role_jobs.go` 显式将其转换为独立的 Ansible Job v4 类型，执行时直接传递 domain 计划，禁止反向解析 Job metadata 恢复业务计划。

新数据库合同、Run 快照和归档分别使用当前精确版本。Native Job v4 执行结构保持不变，计划元数据改用 highRisk 和自动交付策略，不接受旧审批快照。

## 生命周期与读取

创建入口完成技术校验和自动交付规划，Store 在事务内重查关联实体、适配、来源、安装基线、幂等提交及续跑状态。Run 直接以 queued 写入，执行快照自创建起不可修改；状态、步骤、日志及交付结果独立更新。

续跑显式复制来源主题、冻结业务输入及场景执行信息，重建剩余计划、恢复上下文和本次提交身份。保留 ParentSteps、备份、自动交付策略与来源证据链。本次交付结果从 pending 初始化；执行器重新检查锁定来源或目标后写入本次观察，不继承旧 Run 的完成时间或失败描述。

启动协调和领取 Run 均使用共享解码/校验。坏记录沿失效路径保留为 failed；领取事务不会留下无人执行的 running 记录。当前环境、权限、源文件和恢复状态检查仍在原事务及执行边界，纯校验不访问外部状态。

完整 Run、列表 `RunSummary`、证据/工作台 `RunReadModel`、清理 `RunCleanupIdentity` 分离。`retained_run_history` 只提供历史身份、排序和可见性，不能返回可执行 Run。证据及工作台先限制候选 Run ID，再显式从真实 `runs` 读取最小步骤投影；需要完整业务输入时读取完整 Run。工作台 SQL 选择最小字段，不把全部快照读入 Go 后裁剪；生成列和索引继续服务证据查询。查询 JSON 路径集中在 `internal/store/run_snapshot_sql.go`，DDL 对应路径在合同投影回归中验证。

执行快照或交付结果损坏时，完整 Run 读取仍拒绝解码；详情接口返回带 `snapshotError` 的诊断投影，只包含历史身份、实际步骤和故障信息。日志诊断及下载使用同一只读事务中的非执行投影，继续执行原有可见性检查。前端提示快照不可用并关闭取消、归档和续跑入口。失败记录清理独立解码主题摘要及步骤、父步骤的 Release 身份，不依赖执行控制字段或交付结果；身份损坏、冲突或存在受保护引用时仍拒绝清理。

数据库合同为 `clusterforge-v1-20261014-owner-direct-execution`：

- `execution_snapshot_json` 替代 `input_snapshot_json`，`delivery_results_json` 独立存储结果。
- `run_snapshot_references(run_id, referenced_run_id)` 通过 typed reference traversal 提取并在创建同事务维护；反向索引和外键保护真实平台引用。
- 引用遍历覆盖步骤及父步骤 Backup/Previous、安装恢复基线、场景基线、续跑变更 Run、恢复回执；不扫描用户参数或参数来源值中的 `runId`。
- 清理记录保留关系身份、主题摘要和 Release 锁定摘要，不再构造 `{"cleaned":true}` 执行快照。
- 启动只接受精确的新数据库合同，不含旧格式兼容读取。旧版离线迁移命令已移除。

## 初始化与验证

只初始化空库或打开当前精确合同的数据库；旧库启动失败，不自动升级或清空。数据库操作工具与部署脚本使用相同合同标识。归档格式为 `clusterforge-run-archive-v2`，包含 manifest、run、run_steps 和 logs，不包含审批文件。

回归覆盖创建即排队、完整交付策略、快照冻结、FIFO、取消、续跑、坏记录隔离、资源权限、候选失效及发布证据。实际数据库删除与远程部署不属于此代码变更。
