# Playbook 参考入口

当前原生组件参考见 [kubernetes-1.17.5](kubernetes-1.17.5/README.md)：依据 2026-09-08 从 88.55 测试平台读取的 15 个已发布组件与一个已发布场景整理，支持固定 Ansible 2.8.8 / Python 3.6.9 的本地参考门禁。

| 目录 | 定位 |
| --- | --- |
| [kubernetes-1.17.5](kubernetes-1.17.5/README.md) | 当前 Ubuntu 18.04 六节点原生 Role、参数合同和场景编排参考 |
| [k8s-1.17.5-cluster](k8s-1.17.5-cluster/SOURCE.md) | 历史 SUSE 来源快照，保留源代码及旧测试夹具 |
| [k8s-1.17.5-kubeadm](k8s-1.17.5-kubeadm/README.md) | 历史 kubeadm 参考与归属测试 |
| [openfuyao](openfuyao/SOURCE.md) | 历史 OpenFuyao/BKE 脱敏来源快照 |

这些目录不会自动注册到平台目录。组件 Owner 需要通过当前 Draft 合同与工作区流程录入，再重新测试和发布。当前参考的来源证据、可用范围、复用步骤及本地门禁见其 README；历史测试保留在 `test-historical-reference-playbooks`。
