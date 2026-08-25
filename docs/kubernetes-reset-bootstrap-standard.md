# Kubernetes reset 与 bootstrap Playbook 标准

适用于 Kubernetes 1.17.x、Ubuntu 18.04 和 Docker 20.10.x 的细粒度组件。目标是让前台托管 Playbook 自行收敛；SSH 只处理平台无法执行的环境残留，并必须记入验收记录。

## 1. reset 拆分

- 控制面与工作节点使用不同场景节点和 Host Group。
- 控制面 Play 使用 `serial: 1`，避免多台 API Server 同时退出导致 kubeadm 注销竞态。
- 工作节点可使用 `serial: "100%"`；仍由环境级 FIFO 保证同一环境只有一个 Run 执行。
- `kubeadm reset -f` 的 API 注销失败不能直接视为本地 reset 失败；后续清理和端口后置条件必须 fail-closed。

```yaml
- name: Reset control plane safely
  hosts: k8smaster
  serial: 1
  any_errors_fatal: true
  become: true
  tasks:
    - name: Stop kubelet before unmounting state
      ansible.builtin.systemd:
        name: kubelet
        state: stopped
      failed_when: false

    - name: Discover kubelet mounts deepest first
      ansible.builtin.shell: |
        set -o pipefail
        awk '$2 ~ "^/var/lib/kubelet" {print $2}' /proc/self/mounts | awk '{ print length, $0 }' | sort -rn | cut -d' ' -f2-
      args:
        executable: /bin/bash
      register: kubelet_mounts
      changed_when: false

    - name: Unmount kubelet residual mounts
      ansible.builtin.command: "umount -l {{ item }}"
      loop: "{{ kubelet_mounts.stdout_lines }}"
      when: kubelet_mounts.stdout_lines | length > 0
      failed_when: false

    - name: Run kubeadm reset
      ansible.builtin.command: kubeadm reset -f
      register: kubeadm_reset
      failed_when: false

    - name: Remove local Kubernetes and CNI state
      ansible.builtin.file:
        path: "{{ item }}"
        state: absent
      loop:
        - /etc/kubernetes
        - /var/lib/etcd
        - /var/lib/kubelet
        - /etc/cni/net.d

    - name: Remove CNI links
      ansible.builtin.command: "ip link delete {{ item }}"
      loop: [cni0, flannel.1]
      failed_when: false
      changed_when: false

    - name: Verify reset ports are closed
      ansible.builtin.wait_for:
        port: "{{ item }}"
        state: stopped
        timeout: 15
      loop: [6443, 2379, 2380, 10250]
```

## 2. bootstrap join 材料

- 不依赖 `kubeadm token create --print-join-command`；Kubernetes 1.17.5 在 API 可用时仍可能长期等待。
- 从已存在的 `bootstrap-token-*` Secret 读取 token id/secret，并确认 `usage-bootstrap-authentication=true`、`usage-bootstrap-signing=true`。
- 从 `/etc/kubernetes/pki/ca.crt` 计算 `sha256` 公钥哈希，显式拼装标准 join 命令。
- 控制面 join 前确认 API `/healthz`、etcd health 和 `cluster-info` JWS 均存在；工作节点 join 复用同一份锁定材料。
- token、证书 key 等敏感值只能通过 CredentialRef 或运行时内存传递，不写入 Release 参数、日志或审计 metadata。

## 3. 验收与恢复

- reset 后检查四个端口、残留挂载、CNI 配置和接口；不能只看 `kubeadm reset` 返回码。
- join 后检查 Node Ready、kubelet active、控制面静态 Pod、Flannel、kube-proxy 和 CoreDNS。
- 每个 reset 前生成按主机隔离的恢复目录，至少保存 Kubernetes/etcd/kubelet/CNI 状态、iptables、地址和服务状态。
- 如必须 SSH 恢复，只处理明确的残留主机；修复后必须回到同一前台 Playbook 合同重跑并留下 Run 证据。
