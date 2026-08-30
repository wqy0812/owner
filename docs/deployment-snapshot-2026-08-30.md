# 测试环境节点快照（2026-08-30）

> 版本与环境：本文记录项目首个版本（V1）的测试环境，不是生产环境清单或生产验收证据。统一规则见 [首版与环境策略](version-policy.md)。
>
> 证据基线：2026-08-30 09:07（Asia/Shanghai）。通过 ClusterForge 前台读回 Environment Revision 和连通性结果，并通过 SSH、Docker Registry API、文件 SHA-256 和 Docker 拉取交叉核对 Registry 迁移。
>
> 历史边界：2026-08-20/24 的原始清理基线保留在 [2026-08-20 节点快照](deployment-snapshot-2026-08-20.md)，本文不回写当时证据。
>
> 检查器边界：09:06/09:07 的前台 SSH 结果来自当时已部署的 Ansible Ping 检查器。当前工作区已改为严格 `known_hosts`、显式 CredentialRef 和远端 `true` 的 Go SSH 检查器，但本次代码审核不包含部署或真实环境重检；两类结果不能互相替代。

## 基础服务节点

| IP | 主机名 | 用途 | 当前入口 |
| --- | --- | --- | --- |
| `192.168.88.116` | `image` | 主 Docker Registry | `192.168.88.116:5000` |
| `192.168.88.54` | `deploy` | 旧 Docker Registry，仅作迁移回退源 | `192.168.88.54:5000` |
| `192.168.88.55` | `ansible` | ClusterForge 平台、Ansible 执行和镜像构建节点 | `http://192.168.88.55:8080/` |
| `192.168.88.57` | `fss` | File Station 组件介质服务器 | `192.168.88.57:8080` |

两套环境的当前变量均为：

```text
IMAGE_REGISTRY=192.168.88.116:5000
FILE_STATION=192.168.88.57:8080
```

`.55` 和测试集群 2 的六台 Docker 节点在 `insecure-registries` 中同时保留 `.54:5000` 和 `.116:5000`。这是内部 HTTP Registry 的必需运行时配置；不能仅修改页面变量却不验证 Docker 实际 push/pull。

## Registry 迁移证据

新 Registry 容器为 `clusterforge-registry`，数据绑定到 `/srv/clusterforge-registry`，重启策略为 `always`。Docker 服务重启后容器自动恢复，`GET /v2/` 从外部返回 HTTP 200。

| 镜像 | Manifest digest |
| --- | --- |
| `components/coredns:1.6.5` | `sha256:608ac7ccba5ce41c6941fca13bc67059c1eef927fd968b554b790e21cc92543c` |
| `components/coredns-dnscheck:1.36` | `sha256:5b0745afdfec8efe7225bbe20cd87dc9139381e676400707ea979ec5406de3a8` |
| `components/coredns-standalone:1.6.5` | `sha256:2dd447634852401ea08fe9e9c408756af45462a72de9fe0ae7caea99f96205c2` |
| `components/k8s1175-bootstrap:v1.17.5-lab.1` | `sha256:138d0adf7aca170e03dc1a237d4caa488192965696a72c2b396dcba506193384` |

新旧 Registry 的文件树校验和均为 `c11c8d50bb8c906d18a1617c51d66a308393d412093cf3aecd45c4e34dcfba9c`。`.55` 已完整拉取 `192.168.88.116:5000/components/coredns-dnscheck:1.36`，读回的 RepoDigest 与上表一致，且实际下载了 `2275512` 字节的镜像层。

## Kubernetes 测试集群 1

- Environment ID：`environment-39570277a72a9747f0e1f289`
- 当前 Revision：r3，`environment-revision-a7163582d88aba120e4d9cef`
- 调度状态：空闲
- Facts：`architecture=amd64`、`operatingSystem=Ubuntu`、`operatingSystemVersion=18.04 / 24.04`、`ipFamily=IPv4`

| 主机 | IP | Inventory 分组 |
| --- | --- | --- |
| `master-1` | `192.168.88.78` | `k8s_cert_controller,k8setcd,k8smaster,k8s_F5` |
| `master-2` | `192.168.88.80` | `k8setcd,k8smaster` |
| `master-3` | `192.168.88.117` | `k8setcd,k8smaster` |
| `node-1` | `192.168.88.75` | `k8snode` |
| `node-2` | `192.168.88.81` | `k8snode` |
| `node-3` | `192.168.88.82` | `k8snode` |

2026-08-30 09:07 前台检查：

- TCP `8/8` 通过：6 台 Inventory 主机、`.116:5000` Registry 和 `.57:8080` File Station 均可达。
- 旧版 SSH / Ansible `0/6`：六台都返回 `ansible_ping_failed`。三台重装 master 已完成新主机指纹登记和 `.55` 平台公钥授权，严格免密 SSH 已独立通过；当时阻断是 `.55` 的 Ansible 2.8.8 不支持 Ubuntu 24.04 master 上的 Python 3.12。三台 worker 是已有 Ansible Ping 故障，本次未扩大修复范围。
- 该环境六台主机当前都未安装 Docker；后续交付必须在安装运行时时注入 `.116:5000` 内部 Registry 配置。

TCP 通过不能解释为该环境已具备 Ansible 或 Kubernetes 交付条件。

## Kubernetes 测试集群 2

- Environment ID：`environment-932f756b4bf4ecc3dcabecf5`
- 当前 Revision：r5，`environment-revision-a814305ba59dbdeaef44cf8a`
- 调度状态：空闲
- Facts：`architecture=amd64`、`deploymentMode=standard`、`dockerVersion=20.10.21`、`hardwareProfile=general`、`ipFamily=IPv4`、`isolationRuntime=runc`、`operatingSystem=Ubuntu`、`operatingSystemVersion=18.04`

| 主机 | IP | Inventory 分组 |
| --- | --- | --- |
| `master-4` | `192.168.88.59` | `k8s_cert_controller,k8setcd,k8smaster,k8s_F5,k8s_cluster,primary_control_plane` |
| `master-5` | `192.168.88.56` | `k8setcd,k8smaster,k8s_cluster,secondary_control_plane` |
| `master-6` | `192.168.88.58` | `k8setcd,k8smaster,k8s_cluster,secondary_control_plane` |
| `node-4` | `192.168.88.67` | `k8snode,k8s_cluster,worker_nodes` |
| `node-5` | `192.168.88.68` | `k8snode,k8s_cluster,worker_nodes` |
| `node-6` | `192.168.88.69` | `k8snode,k8s_cluster,worker_nodes` |

2026-08-30 09:06 前台检查：TCP `8/8` 通过，旧版 SSH / Ansible `6/6` 通过。六台 Docker 服务在配置新 Registry 并重启后均为 `active`，无运行容器。

## 回退与保留边界

- 旧 Registry `.54:5000` 仍运行，数据和新端在迁移点一致；不要同时往两个端点发布新镜像。
- 迁移前数据备份：`.54:/srv/clusterforge-registry-backups/registry-pre-migration-20260830T0905+0800.tar.gz`。
- 备份 SHA-256：`5d3708dbb52a47008644188af84252ef22070da6299fa489a4ad34bb497ff117`。
- `.55` 和测试集群 2 六台节点的 Docker 配置备份：`/etc/docker/daemon.json.pre-registry-migration-20260830T0909+0800`。
- `.55` 的原 SSH 主机指纹备份：`/root/.ssh/known_hosts.pre-master-reinstall-20260830T0907+0800`。
- 回退 Environment 变量时必须通过前台基于历史 Revision 创建新 Revision，不覆盖 r3/r5 历史。

## 前台维护路径

1. 使用“王维 · 基础设施”进入“环境”。
2. 在 `Inventory` 中维护主机 IP、分组、SSH 用户和端口。
3. 在“环境变量”中维护 `IMAGE_REGISTRY` 和 `FILE_STATION`，只填 `host:port`。
4. 每次保存必须核对差异、填写原因并创建新 Environment Revision。
5. 保存后单击“立即检查”，分别读回 TCP 与当前版本 SSH 结果；任一类失败都不能声称环境已可交付。部署原生 Go SSH 检查器后，应先通过前台为当前 Revision 显式配置 `ssh_private_key` 或 `ssh_password` CredentialRef，再生成新的检查证据。
