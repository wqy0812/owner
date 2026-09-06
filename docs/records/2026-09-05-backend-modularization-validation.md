# 2026-09-05 后端模块化优化本地验证

本记录对应已批准的优化 1–4：Platform 收缩、删除业务转发层、Execution 职责拆分、Runner 显式依赖。基于当日已有未提交的工作区实施；保留此前的 Role Job、场景生命周期及其他改动。本次未提交、推送、部署或操作真实集群。

## 实施结果

- Platform 的生产方法由改造前工作区的 254 个收缩至 22 个，仅保留模块访问、配置和生命周期。业务模块与 Readiness 不持有 `*Platform`。
- 删除 `application_services.go`、`application_commands.go`、`execution_components.go`，业务直接归属 Catalog、Scenario、Environment、Execution、Identity、ReadModel、ReleaseCoordinator 等对象；保留有实际响应装饰或模块协调职责的入口。
- 模块通过实际使用的 Store 接口工作，组合根注入同一个数据库。Scenario 的活动 Run 查询改用具名 Store 方法，原事务内并发检查继续保留。
- ExecutionService 负责入口编排，PlanBuilder/RollbackPlanner 负责读取与规划，RunCreator 负责快照/审批创建，Scheduler 负责 FIFO，Executor 负责执行，Recorder 负责阶段/回执/基线/终态，ApprovalService 负责审批及取消。
- `RunnerDependencies` 显式提供 WorkspaceInspector、RuntimeInspector 和 JobBackend；缺少依赖在装配时失败，装配本身不执行外部探测。生产执行统一走作业包构建、校验、保存、执行路径，测试协议适配移至 `internal/testutil`。
- Catalog、Scenario、Execution、发布与 Readiness 继续共享同一工作区状态及锁；Readiness 保留响应内复用，发布和执行仍复核当前合同。

## 本地检查

Go 检查使用可写的临时 `GOCACHE`；包含本机 HTTP/SSH 监听器的测试在允许创建本机监听器的环境运行。

| 检查 | 结果 |
| --- | --- |
| `go test ./... -count=1` | 全部通过 |
| `go vet ./...` | 通过 |
| `go test -race ./internal/service ./internal/store ./internal/api ./internal/ansible ./internal/jobcli ./internal/seed -count=1` | 六个包全部通过 |
| `pnpm --dir web test` | 17 个文件、192 项测试通过 |
| `make check-docs` | 通过；校验 133 个 API 方法/路径、10 个页面路由及文档链接 |
| `make test-fixture-boundary` | 通过 |
| `scripts/test-deploy-test-88-55.sh` | 部署策略脚本测试通过；没有执行部署 |
| `git diff --check` | 通过 |

新增测试直接构造模块并使用对应依赖，覆盖 Owner/角色边界、初始 Revision、环境归档门禁、动作计划锁定、创建失败不调度、破坏性 Run 等待审批、作业包保存失败禁止执行，以及阶段/回执/安装基线写入失败中止后续记录。架构检查覆盖 Platform 反向依赖、具体 Store 泄漏、装配环、规划器写依赖和运行时 Runner 能力探测；现有 Scheduler 测试改为独立构造。

## 真实本地 Ansible 门禁

使用已有的本地 Ansible 2.19.12 / Python 3.12.14，运行：

```sh
ANSIBLE_LOCAL_TEMP=/private/tmp/clusterforge-module-refactor/ansible-local \
GOCACHE=/private/tmp/codex-review-go-cache \
make test-role-job ANSIBLE_PLAYBOOK=/private/tmp/clusterforge-singlejob-work/venv/bin/ansible-playbook
```

该目标构建本地 `clusterforge-job`，仅执行临时工作区和合成本机 Inventory。以下八项测试全部通过，未跳过：

- `TestRoleJobAcceptance`：串行主机组、同版本多节点、变量隔离、前检/主动作/handler/后检失败边界。
- `TestRoleJobRealAnsible`。
- `TestRoleJobRealAnsibleFactsAreGatheredAfterPhaseReset`。
- `TestScenarioRoleJobContinuationRealAnsible`。
- `TestScenarioAcceptanceCredentialIsolationRealAnsible`。
- `TestScenarioBusinessAcceptanceRealAnsible`（含清理失败分支）。
- `TestStandaloneRealControllerLoss`。
- `TestStandaloneRealRunResumeRollback`。

## 核对范围

与开始改造时保存的工作区快照核对：生产 API 仅改为通过 Execution/Audit 模块调用，服务端入口适配新的构造签名；本次未改动 `internal/ansible`、`internal/jobcli`、自动化流程或前端实现。HTTP 请求/响应、数据库 schema、锁定计划 JSON 字段与摘要算法保持原合同。最后的规划器独立测试与简化后的 Draft/Retry 路径另经定向竞态测试通过。

本次只完成本地代码重构和回归；真实主机交付、远端部署和数据库转换不属于这次验证。
