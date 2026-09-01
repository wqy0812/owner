# 真实环境 API 场景自动化

本目录只保存与具体业务目录、机房和主机无关的自动化逻辑。套件通过 Go 调用 ClusterForge、File Station 和 Registry API，不依赖浏览器，并按以下顺序执行：

1. 完整场景安装/验证、整环境回滚和外部声明的主机级后置条件。
2. 可控失败、失败 Run 安全重试、运行中取消与组件回滚。
3. Artifact/Image 摘要拒绝、直接来源、平移、环境目标复用与组件回滚。

## 安全边界

- `go test ./...` 默认只编译本目录，真实测试会跳过。
- 只允许访问外部配置中精确声明并由 `CLUSTERFORGE_REAL_E2E_CONFIRM` 二次确认的环境。
- 完整执行前要求环境无活动 Run、镜像构建和安装记录，并在平台主机创建环境专用锁。
- 预检要求外部配置声明的两个 Release 具有明确安装依赖，保证回滚顺序符合配置合同。
- 整环境回滚首次失败时只允许一次有审计记录的幂等重试；最终仍失败则整套测试失败，报告同时保留首次失败 Run。
- 第 3 组只会清理两个专用目标：固定 FSS 文件和固定 Registry manifest；发现内容摘要漂移时拒绝删除。
- 异常退出时只取消本次执行已记录 ID 的活动 Run。只有环境锁仍由本次 token 持有、干净预检已通过、且回滚预览中的全部安装来源 Run 都由本次测试创建时，才允许整环境回滚；否则保留现场并报告人工处理。不会扫描后取消其他 Run，也不会部署平台或 File Station。
- 执行报告写入 `output/real-scenario-e2e/<UTC 时间>/summary.json`。

## 只读预检

预检验证外部配置声明的环境、测试夹具、干净生命周期、File Station `/api/v1/fetch` 能力以及 Registry 定向删除能力。它不获取锁、不清理目标、不创建 Run，也不回滚环境。

真实环境数据不进入代码库。操作者必须在仓库外准备 JSON 配置，并通过绝对路径传入：

- 平台与身份：`baseUrl`、`environmentId`、`environmentName`、三个 Owner ID、平台与 FSS SSH 目标；
- 环境合同：File Station/Registry 端点、主机数和必需主机组；
- 场景合同：Revision ID、定义摘要、节点/边/步骤计数、回滚顺序 Release ID、主机组与声明式主机后置条件；
- 可控失败与交付夹具：Component/Release ID、版本、计划摘要、动作限制、超时，以及 Artifact/Image 的地址、路径和摘要。

配置文件必须位于仓库外，使用严格 JSON 字段校验且不允许未知字段。URL 不得包含凭据、查询参数或 fragment；Artifact 删除目标必须严格位于配置声明的专用根目录下。配置不得包含密码、Token、私钥或任意远程脚本；主机后置条件只能声明缺失路径、停用服务、缺失网卡和缺失容器标签。

```sh
CLUSTERFORGE_REAL_E2E_CONFIG='/absolute/path/to/real-scenario.json' \
CLUSTERFORGE_REAL_E2E_CONFIRM='<external environmentName>' \
make test-e2e-real-scenarios-preflight
```

若提示 File Station 缺少 `/api/v1/fetch`，先由操作者单独准备兼容服务；本套件不会隐式部署。

## 完整执行

确认测试环境当前可独占，且允许重置第 3 组专用 Artifact/Image 目标后运行：

```sh
CLUSTERFORGE_REAL_E2E_CONFIG='/absolute/path/to/real-scenario.json' \
CLUSTERFORGE_REAL_E2E_CONFIRM='<external environmentName>' \
CLUSTERFORGE_REAL_E2E_RESET_DELIVERY=1 \
make test-e2e-real-scenarios
```

可选参数：

- `CLUSTERFORGE_REAL_E2E_RUN_TIMEOUT`：单个 Run 的等待上限，默认 `90m`。
- `CLUSTERFORGE_REAL_E2E_POLL_INTERVAL`：轮询间隔，默认 `2s`。
- `CLUSTERFORGE_REAL_E2E_OUTPUT_DIR`：报告输出目录。
