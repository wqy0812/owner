# 组件资源范围整体移除与验证

用户确认首版无旧数据，整体移除组件资源范围声明及相关门禁，不提供迁移或兼容。实现基于已有未提交工作区；本次未提交、推送、部署或重建现有数据库。

## 最终行为

- 动作编辑和合同评审不再显示资源管理范围；组件 YAML 自行负责宿主机文件操作、归属和检查。
- API、模板导入、持久化、Release 摘要、执行计划与原生交付包均移除资源声明字段；新请求携带删除字段会被严格接口拒绝。
- 组件、场景、执行准备及执行入口不再校验路径声明、共享来源或与已有安装路径的重叠，不再生成“资源合同通过”检查项。
- 保留环境版本漂移检查、Owner 执行顺序、精确依赖、工作区隔离、目标主机限制、审批和回滚来源匹配。回滚来源节点函数从删除模块迁入回滚模块。
- 数据库合同为 `clusterforge-v1-20260907-no-resource-contract`，原生作业合同为 `clusterforge-native-job-v4`；Catalog、部署检查和当前文档同步。

## 自动验证

| 检查 | 结果 |
| --- | --- |
| `go test ./...` | 全部通过；包含 API 录入、导入、评审发布、场景顺序、回滚、Catalog 往返和数据库合同检查 |
| `pnpm --dir web test` | 26 个文件、271 项测试通过 |
| `pnpm --dir web build` | 类型检查和生产构建通过；最终专用样式清理后再次构建通过 |
| `make check-docs test-deploy-script test-fixture-boundary` | 全部通过 |
| `make test-role-job ANSIBLE_PLAYBOOK=/tmp/owner-yaml-ansible/bin/ansible-playbook` | 20 个顶层测试通过，包含 43 个子用例；ansible-core 2.18.6、Python 3.11.15 |
| `git diff --check` | 通过 |

新增回归覆盖两个独立组件按序写入同一临时文件：第二个组件的前置 YAML 验证第一个组件的内容，两个组件分别验证自己的写入结果；平台 Runner 和独立原生作业都通过。另覆盖排队后环境版本变化仍被拒绝、冻结来源复核、同组件不同节点的回滚来源参数隔离，以及删除字段的输入拒绝。

## 浏览器验证

使用 Playwright、隔离临时 SQLite 和独立本地 API/Vite 服务，视口为 1280 × 900。

- 实际载入并保存动作：PUT 返回 200，请求包含动作和 YAML 当前字段，不包含 `resourceContract`。
- 无资源声明的 Draft 成功提交合同审核；平台 Owner 的完整评审预览显示动作及源码，没有资源声明区块。
- 执行准备成功生成三阶段计划，返回 5 项检查：执行器、连通性、Ansible 运行基础、两个组件 YAML 执行时检查；没有资源合同类别，确认提交按钮可用。浏览器未提交 Run。
- 编辑弹窗可见宽度和内容宽度均为 978px；页面可见宽度和内容宽度均为 1280px。评审和执行准备无页面横向溢出。
- 已人工检查截图：`output/playwright/no-resource-action-1280.png`、`output/playwright/no-resource-review-1280.png`、`output/playwright/no-resource-preparation-1280.png`。

以上为本地隔离验证；不代表测试服务器部署或真实集群验收。
