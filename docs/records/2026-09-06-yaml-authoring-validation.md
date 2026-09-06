# 组件 YAML、目录隔离与检查流程验证

范围：本地代码、文档、临时数据库及隔离 Ansible 作业。保留工作区原有未提交修改；未提交、部署或操作真实集群。

## 实现结果

- 组件和场景验收移除 facts 开关及固定探测表；旧值只作为只读迁入待办保留。普通保存保留旧逻辑，显式确认的源码保存原子清除旧配置。未迁入定义无法产生新执行或发布证据。
- 工作区统一由平台分配固定层级的根目录，Release ID 保证名称归一化后的独立性；组件 Role 并列打包，拒绝重叠目录、越界和符号链接。
- 部署维持前检查、主体、后检查；回滚默认主体、后检查，支持可选前检查和专用后检查。默认后检查锁定安装、升级或配置来源动作的前置检查和来源参数。
- 运行准备区分平台检查与“执行时检查”，资源合同通过不代表现场残留探测通过。
- 新作业合同为 `clusterforge-native-job-v3`，历史原生 v2 和 Role v1 冻结作业继续按原合同校验。

## 本地回归

- `go test ./...`：通过。
- `go vet ./...`：通过。
- `go test -race ./internal/service ./internal/store ./internal/api ./internal/ansible`：通过。
- `npm test --prefix web`：21 个文件、210 项测试通过。
- `npm run build --prefix web`：通过。
- `git diff --check`：通过。
- 使用临时安装的 Ansible Core 2.18.6 / Python 3.11.15，运行 `RoleJobRealAnsible`、`RoleJobAcceptance`、`ScenarioBusinessAcceptanceRealAnsible`、`YAMLTwoStepRollbackRealAnsible`、`NativeJobRealAnsible`、`NativeRollbackResumeAfterProviderRemoval`：全部通过。覆盖显式 facts 采集及阶段隔离、顺序执行、失败/不可达/超时/取消停止、两步回滚、失败检查保留恢复记录，以及续跑不重复已成功主体。

## 浏览器验证

通过 `TestYAMLBrowserFixture` 导出独立临时数据库，使用协议样例生成历史安装记录；浏览器未提交任何 Run。临时环境归属调整为样例环境 Owner，以验证回滚预览。

- 组件编辑：旧开关和探测表消失；普通源码保存保留迁入待办；补齐前置检查 YAML 和 facts YAML 后，确认保存成功清除旧配置。
- 回滚编辑与预览：默认不选前检查，后检查显示实际来源；安装基线预览显示两步，并列出 `install pre` 的具体 YAML 路径。
- 场景验收：只读展示旧配置，在自身 YAML 中补齐采集和工具检查，确认保存后待办消失、内容保留。
- 执行准备：组件 YAML 显示“执行时检查”，列出来源路径；资源合同说明明确指出现场检查在执行时进行。

截图保存在 `output/playwright/yaml-*.png`。测试日志为 `/tmp/owner-yaml-final-*.log`。
