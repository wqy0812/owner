# MCP 接入

ClusterForge 在同一个 Go 服务中提供 `/mcp`，AI 可调用现有平台业务能力。默认关闭；配置后提供 138 个业务工具和 5 个文件工具。完整清单见 [工具覆盖清单](mcp-tools.md)。实现使用官方 Go SDK v1.8.0 的无状态 Streamable HTTP，工具发现按每页 50 项分页。客户端应遍历 `nextCursor`。

## 开启与连接

从仓库根目录生成一个客户端的凭据文件；`user-id` 必须是数据库中存在的用户。示例绑定组件 Owner，其他身份应分别生成凭据。

```sh
go run ./cmd/mcp-token -out ./data/mcp-component-agent -client-id component-agent -user-id component-alice -ttl 2160h
```

输出目录必须不存在，目录权限为 0700，`config.json` 与 `token` 权限为 0600。命令只输出文件路径，不打印令牌；令牌由 32 个随机字节生成。`config.json` 只含令牌摘要，不含明文。将 `token` 文件内容（去掉结尾换行）配置到 AI 客户端的私密凭据字段，不要将它放进对话、URL、代码或日志。

启动平台前配置：

```sh
export CLUSTERFORGE_MCP_CONFIG=/absolute/path/to/data/mcp-component-agent/config.json
export CLUSTERFORGE_MCP_TRANSFER_DIR=/absolute/path/to/data/mcp-transfers
```

| 客户端配置 | 值 |
| --- | --- |
| Transport | Streamable HTTP |
| URL | `https://platform.example.com/mcp`，本地可用 `http://127.0.0.1:8080/mcp` |
| Authorization | `Bearer <token 文件中的值>` |
| 初始化后首次工具调用 | `clusterforge_me`，核对绑定身份 |

第一版支持可配置 Bearer Header 的客户端，不提供 OAuth 授权登录或 stdio 入口。`GET` / `DELETE /mcp` 返回 405；不使用 MCP 会话 ID，也不开放旧版独立 SSE 端点。

## 配置、轮换与身份

配置示例中的摘要是占位符，必须替换为生成工具输出的值：

```json
{
  "credentials": [
    {
      "clientId": "component-agent",
      "userId": "component-alice",
      "tokenSha256": "<64 位小写 SHA-256 摘要>",
      "expiresAt": "2027-01-01T00:00:00Z"
    }
  ],
  "allowedOrigins": [],
  "maxFileBytes": 2147483648,
  "maxTotalBytes": 8589934592
}
```

每个 `clientId` 和令牌摘要必须唯一，过期时间必填。多个客户端的条目由部署者合并到一个配置文件。配置文件最多 64 KiB，每次请求重新读取；用同目录临时文件加原子替换更新配置。删除凭据、更新摘要或设置已到期时间后，后续请求立即失效，无须重启执行器。正在进行的操作不会因为撤销令牌而自动取消；取消 Run 应使用平台取消操作。轮换后的令牌不能读取旧令牌的临时文件。

配置文件不存在、JSON 无效或字段不合法时，MCP 返回 503；启动时配置错误或绑定用户不存在会阻止服务启动。运行中用户不可用或令牌无效返回 401。空凭据列表表示拒绝所有访问。修改配额对后续请求生效，已有文件仍计入总额度。

MCP 不接受网页 Cookie，也没有身份切换工具。调用身份由令牌绑定；工具中的资源 ID 不改变身份。所有角色、Owner、生命周期、计划和工作区摘要检查仍由原业务处理入口执行。当前网页是无密码演示身份模式，远程发布应只开放 `/mcp`，不要把身份切换入口作为 MCP 登录方式。

## 操作合同

业务工具名称采用 `clusterforge_` 加稳定操作名。输入最多包含四部分：

| 字段 | 用途 |
| --- | --- |
| `path` | 路由中的 `id`、`revisionId`、`alias` 等标识 |
| `query` | 具体工具声明的查询字段，值使用字符串，例如 `{"page":"1","pageSize":"20"}` |
| `body` | JSON 请求 DTO；上传时是 multipart 文本字段，JSON 内容需编码成字符串 |
| `files` | multipart 文件字段，例如 `{"file":{"transferId":"..."}}` |

字段名称、枚举、可空类型和所需摘要见工具 `inputSchema`，业务规则以当前 REST 合同为准。未声明的外层字段、请求头和任意 URL 均不接受。工具注解是客户端提示，不能替代服务端权限或用户授权。

普通结果返回 `{"status":200,"response":<原 REST JSON>}`；原有 `data`、`items`、分页字段及错误码保留。业务错误设置 MCP `isError=true`，协议错误由 SDK 处理。读取与预览工具不提交 Run；调用审计作为操作记录另行保存。

典型场景执行流程：

1. 使用列表、详情、工作区工具获取实际资源和当前摘要。
2. 调用 `clusterforge_preview_scenario_execution`，传入 `path.id` 和 `body.environmentId`、`executionMode`、`testOnly`。
3. 检查 `ready`、计划内容与风险；提交时使用同一组参数、计划返回的 `planDigest` 作为 `expectedPlanDigest`，并提供 `idempotencyKey`。
4. 根据目的调用 `clusterforge_start_scenario_lifecycle_test` 或 `clusterforge_start_scenario_lifecycle_run`。保存返回的 Run ID。
5. 调用 `clusterforge_get_run` 与 `clusterforge_run_activity`；活动查询使用 `afterId` 和 `limit` 增量读取，建议间隔 2–5 秒，终态后停止。

组件测试、Draft 创建、续跑和环境回滚分别使用对应预览工具。环境回滚还须提供 `confirmEnvironmentName`。MCP 不自动生成确认值、跳过发布证据或重试变更。网络失败后先查询状态；已知幂等请求继续使用原 `idempotencyKey`，不要生成新键重新提交。

## 文件上传与大结果

所有文件经过 MCP 传输，不要求服务端读取客户端路径。

1. `clusterforge_transfer_create`：提交 `filename`、`sizeBytes`、小写 `sha256`，可选 `mediaType`，取得 `transferId`。
2. `clusterforge_transfer_write`：按顺序提交字节 `offset` 与 `contentBase64`；单块解码后最多 1 MiB。返回 `nextOffset`。重复已写入区间只有字节完全相同时成功。
3. `clusterforge_transfer_finish`：验证总长度和 SHA-256，文件变为不可修改。空文件也必须完成校验。
4. 业务上传工具通过 `files` 引用完成的文件，并提供原接口的字段及摘要。例如工作区上传使用 `files.file`，安装包使用 `files.artifact`，Dockerfile 使用 `files.dockerfile`。
5. 用完后调用 `clusterforge_transfer_delete` 释放暂存；这不会删除已经保存的平台资产。

二进制下载以及超过 256 KiB 的 JSON 结果返回 `file`，包含 `transferId`、`filename`、`mediaType`、`sizeBytes`、`sha256` 和 `expiresAt`。用 `clusterforge_transfer_read` 分块读取（默认 1 MiB），按 `nextOffset` 继续直到 `eof=true`；客户端组装后校验 SHA-256。结果不会静默截断。

默认单文件最多 2 GiB、总预留配额 8 GiB、最多 1024 个临时文件、最多 4 个并发 HTTP 请求。业务调用在执行前按单文件上限预留结果空间，容量不足返回 503 且不调用业务入口。配额覆盖上传和输出文件；各业务接口原有的 1 MiB Playbook、10 MiB 工作区文件等限制继续生效。

文件从创建起一小时过期，每分钟清理。文件绑定客户端、用户及令牌摘要，跨凭据不可读取或引用。重启会清理上次进程遗留的本模块暂存文件；临时文件不属于业务资产或备份。暂存目录必须是独立 0700 目录，只允许单个服务进程占用；不要将现有业务目录设为暂存目录。

若业务已经调用而结果写入失败，返回 `result_unavailable`，应先检查平台状态再决定后续操作。调用成功不代表长任务已经完成。

## 远程入口、审计与验证

MCP 校验显式 `Origin`：默认列表为空，只接受没有 Origin 的原生客户端。浏览器来源必须在 `allowedOrigins` 中逐个填写完整 origin，例如 `https://agent.example.com`，不支持通配符；不提供跨域网页登录。SDK 同时启用本地 Host 保护。

远程通过 HTTPS 反向代理只转发 `/mcp`，保留 Authorization、Content-Type、Accept、MCP-Protocol-Version 等请求头。示例见 [Nginx 片段](../deploy/platform/mcp-nginx.conf.example)。MCP HTTP 请求上限为 3 MiB；较大文件须分块。代理日志不要记录 Authorization 或请求体。

现有业务审计保持原样；新增 `mcp.tool_called` 记录绑定用户、客户端、工具名、服务器生成的请求标识、状态和耗时，不记录令牌、完整参数或文件内容。

开发验证：

```sh
go run ./cmd/mcp-catalog
go test ./internal/mcpserver ./internal/api ./cmd/mcp-token ./cmd/server
go test -race ./internal/mcpserver ./internal/mcpcatalog ./internal/api -run 'TestMCP|TestTransfers|TestInvoke|TestGenerate|TestConfig' -count=1
make check-docs
git diff --check
```

工具覆盖测试会重新分析 REST 路由与请求 DTO，并与生成代码及清单比较；接口新增或参数改变后必须重新生成。第一版不改变数据库合同，没有网页令牌管理。代码测试、真实 Ansible 验证、部署和目标环境验收应分别记录。
