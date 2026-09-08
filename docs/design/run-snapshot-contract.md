# Run 快照合同与离线迁移

状态：实现于当前工作区；实际停服、业务数据迁移和部署需另行安排。实施覆盖创建、审批、调度、执行、续跑、证据读取、清理、离线转换和受保护切换。

## 合同边界

`internal/domain/run_snapshot.go` 定义唯一运行时合同 `clusterforge-run-v1`。`domain.Run.Snapshot` 保存冻结业务输入，`domain.Run.DeliveryResults` 保存本次交付观察。Run ID、Kind、环境 Revision、关联 Release/Revision、续跑链仍由 Run 关系字段提供。

| 分组 | 数据及用途 |
| --- | --- |
| contract | 精确合同标识；不接受场景数字版本 |
| subject | 组件/场景定义摘要、组件测试证据类别、场景关联 |
| plan | Runtime、Steps、ParentSteps、TreeDigest |
| inputs | 参数来源与脱敏 CredentialRefs；用户参数值保持开放 JSON |
| delivery | 交付要求、审批决定、计划中的镜像及介质传输 |
| scenarioExecution | 三种执行模式、来源 Revision、目标节点、安装基线、验收作业和恢复回执 |
| submission | 幂等标识、请求摘要和提交绑定的预览摘要 |
| retry | 恢复状态摘要及续跑捕获的场景基线 |
| recovery | 组件安装基线、环境恢复摘要、重置目标及边界摘要 |

`DecodeRunSnapshot` 拒绝未知控制字段、类型错误、未知合同、尾随 JSON、必需成员缺失或 null；整数不接受字符串或小数。控制字段的存在检查独立于 Go 零值，`0`、`false` 和 `[]` 不等同于缺失。参数使用 JSON number 解码，避免大整数经过 float64 舍入。`ValidateRunSnapshot` 按 Run Kind 检查执行语义、步骤身份、场景模式、必需基线/提交摘要及续跑上下文。环境回滚即使关联场景，也不取得场景安装或升级语义。

domain 只依赖标准库，统一拥有计划步骤、运行时、参数来源、目标节点、交付数据和执行回执。`RunExecutionPlan` 是准备计划及原 Native Job metadata 的平面投影。`role_jobs.go` 显式将其转换为独立的 Ansible Job v4 类型，执行时直接传递 domain 计划，禁止反向解析 Job metadata 恢复业务计划。

存储合同版本与业务摘要算法分别管理：Release/Revision、预览、内容树和 Native Job 的摘要继续使用原投影。新的分组不参与原业务摘要。已存在的作业包、归档文件、归档来源摘要保持原字节；迁移报告记录源/目标快照摘要对应关系。

## 生命周期与读取

创建入口组织强类型快照；Store 在写事务前进行纯校验，并在原有事务内重查关联实体、适配、来源、安装基线、幂等提交和续跑状态。审批完成时，快照、交付结果、审批决定和队列状态同事务提交。数据库 `runs_snapshot_frozen` 只允许待审批 Run 在批准并入队时更新执行快照；入队后运行状态、步骤、日志及交付结果独立更新。

续跑显式复制来源主题、冻结业务输入及场景执行信息，重建剩余计划、恢复上下文和本次提交身份。保留 ParentSteps、备份、审批选择与来源证据链。本次交付结果从 pending 初始化；执行器重新检查锁定来源或目标后写入本次观察，不继承旧 Run 的完成时间或失败描述。

启动协调和领取 Run 均使用共享解码/校验。坏记录沿失效路径保留为 failed；领取事务不会留下无人执行的 running 记录。当前环境、权限、源文件和恢复状态检查仍在原事务及执行边界，纯校验不访问外部状态。

完整 Run、列表 `RunSummary`、证据/工作台 `RunReadModel`、清理 `RunCleanupIdentity` 分离。`retained_run_history` 只提供历史身份、排序和可见性，不能返回可执行 Run。证据及工作台先限制候选 Run ID，再显式从真实 `runs` 读取最小步骤投影；需要完整业务输入时读取完整 Run。工作台 SQL 选择最小字段，不把全部快照读入 Go 后裁剪；生成列和索引继续服务证据查询。查询 JSON 路径集中在 `internal/store/run_snapshot_sql.go`，DDL 对应路径在合同投影回归中验证。

执行快照或交付结果损坏时，完整 Run 读取仍拒绝解码；详情接口返回带 `snapshotError` 的诊断投影，只包含历史身份、实际步骤、审批和故障信息。日志诊断及下载使用同一只读事务中的非执行投影，继续执行原有可见性检查。前端提示快照不可用并关闭审批、取消、归档和续跑入口。失败记录清理独立解码主题摘要及步骤、父步骤的 Release 身份，不依赖执行控制字段或交付结果；身份损坏、冲突或存在受保护引用时仍拒绝清理。

数据库合同为 `clusterforge-v1-20260908-typed-run-snapshot`：

- `execution_snapshot_json` 替代 `input_snapshot_json`，`delivery_results_json` 独立存储结果。
- `run_snapshot_references(run_id, referenced_run_id)` 通过 typed reference traversal 提取并在创建/审批同事务维护；反向索引和外键保护真实平台引用。
- 引用遍历覆盖步骤及父步骤 Backup/Previous、安装恢复基线、场景基线、续跑变更 Run、恢复回执；不扫描用户参数或参数来源值中的 `runId`。
- 清理记录保留关系身份、主题摘要和 Release 锁定摘要，不再构造 `{"cleaned":true}` 执行快照。
- 启动只接受精确的新数据库合同，不含旧格式兼容读取。旧合同解码器只在离线迁移包及测试夹具中引用。

## 离线转换与切换

源库必须精确为 `clusterforge-v1-20260907-no-resource-contract`。先通过正常流程完成或取消所有待审批、排队和执行中 Run，再停止平台、备份定时器和所有后台写入。工具检查活动业务、归档任务、文件事务、数据库完整性、外键及源文件/WAL 指纹；进程停写是操作前提，不能用“当前没有 Run”替代停服。

使用新版本 backup CLI，在源库所在主机执行。以下仅是操作模板：

```sh
# 预先创建空的迁移输出目录，源库及归档均保持只读使用。
clusterforge-backup database migrate-run-snapshot \
  --db /var/lib/clusterforge/platform.db \
  --target /var/lib/clusterforge/run-migrations/change-001/target.db \
  --archive-dir /var/lib/clusterforge/run-archives \
  --report /var/lib/clusterforge/run-migrations/change-001/preflight.json \
  --dry-run

# 将预检返回的 sourceDigest 原样填入。
clusterforge-backup database migrate-run-snapshot \
  --db /var/lib/clusterforge/platform.db \
  --target /var/lib/clusterforge/run-migrations/change-001/target.db \
  --archive-dir /var/lib/clusterforge/run-archives \
  --report /var/lib/clusterforge/run-migrations/change-001/ready.json \
  --expected-source-digest '<preflight sourceDigest>'

clusterforge-backup database verify-run-migration \
  --db /var/lib/clusterforge/platform.db \
  --target /var/lib/clusterforge/run-migrations/change-001/target.db \
  --report /var/lib/clusterforge/run-migrations/change-001/ready.json
```

预检也完整构建并核验临时目标，成功后仅输出 checked 报告。正式转换必须绑定源摘要，分别发布新的 `target.db`、`target.db.source.db` 和 ready 报告；任何已有输出路径均拒绝覆盖。报告包含合同、工具版本、源主文件/WAL 指纹、一致性备份及目标 SHA256、逐表数量和摘要、逐 Run 转换摘要与引用、归档清单和哈希。独立目标导入保留业务 ID、时间戳、状态、rowid、AUTOINCREMENT 高水位、关系和不变列；触发器只在目标事务中暂时卸下并恢复，导入后检查外键与数据摘要。源库始终以只读方式打开。未知字段、错误类型、活动任务、缺失表、悬空关系或制品哈希不符都会失败，不丢弃记录或猜测修复。失败的临时产物不会获得 ready 报告。

目标数据库在写入业务数据前以 `0600` 创建，发布时保留该权限；不依赖调用者的 umask。生成 ready 报告前及独立验证时均检查目标为私有普通文件，权限扩大到组或其他用户后拒绝切换。

受保护的远端切换入口：

```sh
./scripts/deploy-test-88-55.sh \
  --verified-run-migration /var/lib/clusterforge/run-migrations/change-001
```

目录位于目标主机，内含上一步的 `target.db`、ready 报告及报告指向的源备份；归档仍位于报告记录的原目录。此选项必须与数据库重建、空库初始化、活动 Run 覆盖选项互斥。平台及备份服务必须已停用写入；脚本在预检及数据库切换前两次验证源绑定、目标合同/哈希、备份和归档，保存原程序/数据库/配置及迁移清单后成套切换。默认部署仍拒绝旧合同，不自动迁移，也不调用业务库重建。

新程序启动前的失败恢复原程序、数据库和服务状态。**启动新程序即视为可能开放写入**；此后 readiness、制品校验失败或中断只报告故障，禁止自动覆盖新库。应保留现场并人工评估恢复路径。原始备份、源备份和迁移报告独立留存；不要在新库已有写入后直接恢复旧库。

## 验收与实施范围

回归覆盖合同类型/模式、未知字段和零值、JSON 往返、摘要投影、审批竞争/原子性、冻结保护、交付、重启/领取失效、续跑和恢复基线；证据投影、清理身份、可见性、真实引用与参数伪引用；离线转换的终态记录、续跑链、归档、作业包、数量/顺序/哈希、坏数据拒绝、输出冲突、中断和源未变验证；切换前回退及开放写入后禁止自动回退。

发布前运行全量 Go、关键 Store/调度 race、部署脚本、文档、现有前端覆盖率及 live API 流程，并在固定 Ubuntu 18.04 / Ansible 2.8.8 容器执行原生作业测试。测试库演练不能替代待迁移实际库的预检。当前实施不执行实际停服、迁移或部署。
