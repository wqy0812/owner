import type { ReactNode } from 'react';
import {
  ArrowRight,
  BookOpenText,
  Boxes,
  CheckCircle2,
  CloudCog,
  FileText,
  KeyRound,
  Network,
  PlayCircle,
  ShieldCheck,
  TestTube2,
  type LucideIcon,
} from 'lucide-react';
import { Link, useParams } from 'react-router-dom';
import { PageHeader } from '../components/Primitives';
import { useApp } from '../context/AppContext';
import { ROLE_LABELS, type Role } from '../types/domain';

interface OwnerManual {
  title: string;
  summary: string;
  icon: LucideIcon;
  tone: 'indigo' | 'cyan' | 'amber';
  primaryPath: string;
  primaryLabel: string;
  markdown: string;
  checkpoints: string[];
}

const OWNER_MANUALS: Record<Role, OwnerManual> = {
  component_owner: {
    title: '组件 Owner 操作手册',
    summary: '把一个可复用能力整理成可测试、可追踪、不可变的 Component Release。',
    icon: Boxes,
    tone: 'indigo',
    primaryPath: '/components',
    primaryLabel: '进入组件中心',
    checkpoints: ['依赖锁定到精确 Release', '敏感信息只声明 CredentialRef', '发布前复核影响预览'],
    markdown: `## 组件 Owner 操作路径

> 目标：维护自己名下的组件，并把经过验证的能力发布为不可变 Release。已发布版本不能原地修改。

### 1. 创建组件与 Draft

1. 在 **组件** 页面新建组件，填写名称、标识、说明和 L1-L6 分类。
2. 创建新版本；已有版本时会复制当前合同，新组件则从空 Draft 开始。
3. 只有组件 Owner 能修改自己拥有的组件和 Draft。

### 2. 配置版本合同

- 设置环境约束、参数、依赖和 Ansible 动作。
- 依赖必须锁定已发布的上游 Release；下游只能映射上游的公开参数。
- 密码、Token、私钥等不能写进参数或动作 JSON，只能声明所需的 CredentialRef。
- 分层用于目录展示；真正的执行先后由依赖关系和场景 DAG 决定。

### 3. 在共享环境测试

1. 选择 Draft，点击 **测试** 并选择共享环境。
2. 补齐运行输入和依赖 Fixture，提交后到 **运行** 页面查看步骤与脱敏日志。
3. Draft 再次保存后，旧测试证据会失效，需要重新测试。

### 4. 发布与维护

1. 打开发布影响预览，检查下游组件、场景和依赖路径。
2. 确认版本、Breaking 标记、动作风险和发布说明后发布。
3. 组件允许以未验证状态发布，但下游会看到风险；平台不会自动升级场景锁定的版本。
4. 后续变更请创建新 Draft；不再使用的 Released Release 可废弃但不会删除历史。`,
  },
  scenario_owner: {
    title: '场景 Owner 操作手册',
    summary: '把精确组件版本编排成 DAG，通过完整环境测试后发布不可变 Scenario Revision。',
    icon: Network,
    tone: 'cyan',
    primaryPath: '/scenarios',
    primaryLabel: '进入场景中心',
    checkpoints: ['节点锁定精确 Release', 'DAG 连线表达执行顺序', 'Test Passed 后才能发布'],
    markdown: `## 场景 Owner 操作路径

> 目标：把已经发布的组件版本编排成可复现的交付场景，并对完整链路测试结果负责。

### 1. 创建并编排 Draft

1. 在 **场景** 页面新建场景，系统会创建初始 Draft Revision。
2. 从组件库加入已发布 Release；每个节点锁定加入时的精确版本。
3. 用 DAG 连线表达硬依赖和实际顺序，再配置动作、主机组、节点参数、环境绑定和运行输入。

### 2. 保存并校验

- 保存 Draft 后执行校验，处理空图、环、缺失依赖、顺序错误和必填参数未解析等问题。
- 上游映射得到的参数不能再被节点值、环境绑定或运行输入覆盖。
- 当前 Demo 会保存 Execution Policy，但调度仍按 DAG 串行、首个失败即停止，不要把它当成并发策略。

### 3. 完整环境测试

1. 选择共享环境，填写已声明的运行输入并提交完整测试。
2. 全部步骤成功后 Revision 进入 **Test Passed**；失败、取消或拒绝会回到 Draft。
3. 危险步骤会停在等待审批，由目标环境 Owner 决定批准或拒绝。

### 4. 发布与运行

1. 只有 Test Passed 的当前 Revision 可以发布，发布时后端会再次校验 DAG。
2. Released Revision 不可编辑；修改时克隆一个新 Revision 并重新测试。
3. 已发布场景可选择目标环境运行，提交后统一在 **运行** 页面跟踪。`,
  },
  environment_owner: {
    title: '环境 Owner 操作手册',
    summary: '维护可执行的环境快照，管理 Inventory、非敏感参数、凭据引用和危险运行审批。',
    icon: CloudCog,
    tone: 'amber',
    primaryPath: '/environments',
    primaryLabel: '进入环境中心',
    checkpoints: ['Inventory 主机与主机组完整', 'Secret 不进入普通参数', '危险运行审批前核对目标与动作'],
    markdown: `## 环境 Owner 操作路径

> 目标：提供可信的目标环境快照，并对凭据引用、环境容量和危险操作审批负责。

### 1. 创建与维护环境

1. 在 **环境** 页面新建环境，填写架构、操作系统、网络栈和说明。
2. 在 Inventory 中维护主机地址、SSH 用户、端口和主机组。
3. 分别维护环境事实、非敏感参数与 CredentialRef；每次保存都会创建新的 Environment Revision。

### 2. 管理参数与凭据

- 环境事实用于组件兼容性预检，键名和值应与 Release 的环境约束一致。
- 普通环境参数可能覆盖场景节点值，变更前要检查受影响的场景。
- 数据库只保存 CredentialRef，不保存真实密码；不要把 Secret 粘贴到 Facts、Parameters 或运行输入。

### 3. 支持测试与正式运行

1. 环境 Owner 可以用可见组件发起测试，也可以运行已发布场景。
2. Run 提交时会锁定环境、组件、场景、参数和 Playbook 摘要，后续环境修改不会改变已排队 Run。
3. 同一环境按 FIFO 串行执行；到 **运行** 页面查看队列、步骤与脱敏日志。

### 4. 审批危险操作

1. destructive、recovery、clean、destroy、uninstall 等动作会进入等待审批。
2. 核对目标环境、主机组、锁定版本、动作和运行参数后再批准或拒绝。
3. 批准只表示允许平台执行，不替代真实环境变更评审；回滚也必须由组件或场景中明确声明的动作发起。`,
  },
};

const OVERVIEW_MARKDOWN = `## 从可复用能力到可追踪运行

ClusterForge 把交付过程拆成组件 Release、场景 Revision、环境 Revision 和 Run 四类记录。前三类负责定义“执行什么、按什么顺序、在哪里执行”，Run 负责锁定快照并留下状态、审批和日志。

### 协作边界

- **组件 Owner** 发布能力合同和动作，不决定某次集群交付的完整顺序。
- **场景 Owner** 锁定精确 Release，并通过 DAG 连线编排完整交付流程。
- **环境 Owner** 维护目标主机、参数和凭据引用，并审批危险操作。
- 已提交 Run 使用提交时快照，不会跟随后续 Release、Scenario 或 Environment 修改。

### 发布与执行规则

1. Released Release 与 Released Scenario Revision 都不可原地修改，演进必须创建新版本。
2. 场景必须通过当前 Revision 的完整测试后才能发布；组件可以未验证发布，但会明确显示风险。
3. 同一环境的 Run 按 FIFO 串行；危险动作先等待环境 Owner 审批。
4. 所有角色都从 **运行** 页面查看最终步骤、解析参数来源和脱敏日志。`;

const WORKFLOW_STEPS = [
  { icon: Boxes, title: '1. 组件 Release', owner: '组件 Owner', text: '定义参数、依赖、环境约束与 Ansible 动作。', tone: 'indigo' },
  { icon: Network, title: '2. 场景 Revision', owner: '场景 Owner', text: '锁定精确版本，用 DAG 编排并完成环境测试。', tone: 'cyan' },
  { icon: CloudCog, title: '3. 环境 Revision', owner: '环境 Owner', text: '提供 Inventory、Facts、Parameters 与 CredentialRef。', tone: 'amber' },
  { icon: PlayCircle, title: '4. Run', owner: '协作交付', text: '锁定快照，经过审批后串行执行并沉淀日志。', tone: 'rose' },
] as const;

const CENTER_CARDS = [
  { to: '/components', icon: Boxes, title: '组件发布', text: '创建 Draft、配置合同、共享环境测试、影响预览与发布。', tone: 'indigo' },
  { to: '/scenarios', icon: Network, title: '场景发布', text: '选择精确 Release、编排 DAG、完整测试并发布 Revision。', tone: 'cyan' },
  { to: '/environments', icon: CloudCog, title: '环境管理', text: '维护主机、事实、普通参数、CredentialRef 与 Revision。', tone: 'amber' },
] as const;

function InlineMarkdown({ text }: { text: string }) {
  const parts = text.split(/(\*\*[^*]+\*\*|`[^`]+`)/g).filter(Boolean);
  return <>{parts.map((part, index) => {
    if (part.startsWith('**') && part.endsWith('**')) return <strong key={index}>{part.slice(2, -2)}</strong>;
    if (part.startsWith('`') && part.endsWith('`')) return <code key={index}>{part.slice(1, -1)}</code>;
    return <span key={index}>{part}</span>;
  })}</>;
}

function isBlockStart(line: string) {
  return /^(#{2,3})\s|^>\s|^-\s|^\d+\.\s/.test(line);
}

function MarkdownPreview({ source }: { source: string }) {
  const lines = source.trim().split('\n');
  const blocks: ReactNode[] = [];
  let index = 0;

  while (index < lines.length) {
    const line = lines[index].trim();
    if (!line) { index += 1; continue; }
    const heading = /^(#{2,3})\s+(.+)$/.exec(line);
    if (heading) {
      const content = <InlineMarkdown text={heading[2]} />;
      blocks.push(heading[1].length === 2 ? <h2 key={index}>{content}</h2> : <h3 key={index}>{content}</h3>);
      index += 1;
      continue;
    }
    if (line.startsWith('> ')) {
      blocks.push(<blockquote key={index}><InlineMarkdown text={line.slice(2)} /></blockquote>);
      index += 1;
      continue;
    }
    if (/^-\s/.test(line)) {
      const items: string[] = [];
      while (index < lines.length && /^-\s/.test(lines[index].trim())) {
        items.push(lines[index].trim().replace(/^-\s+/, ''));
        index += 1;
      }
      blocks.push(<ul key={`ul-${index}`}>{items.map((item) => <li key={item}><InlineMarkdown text={item} /></li>)}</ul>);
      continue;
    }
    if (/^\d+\.\s/.test(line)) {
      const items: string[] = [];
      while (index < lines.length && /^\d+\.\s/.test(lines[index].trim())) {
        items.push(lines[index].trim().replace(/^\d+\.\s+/, ''));
        index += 1;
      }
      blocks.push(<ol key={`ol-${index}`}>{items.map((item) => <li key={item}><InlineMarkdown text={item} /></li>)}</ol>);
      continue;
    }

    const paragraph = [line];
    index += 1;
    while (index < lines.length && lines[index].trim() && !isBlockStart(lines[index].trim())) {
      paragraph.push(lines[index].trim());
      index += 1;
    }
    blocks.push(<p key={`p-${index}`}><InlineMarkdown text={paragraph.join(' ')} /></p>);
  }

  return <div className="markdown-preview">{blocks}</div>;
}

function WorkflowOverview() {
  return (
    <div className="manual-flow" aria-label="平台交付工作流">
      {WORKFLOW_STEPS.map(({ icon: Icon, title, owner, text, tone }) => (
        <article key={title} className={`manual-flow__step manual-flow__step--${tone}`}>
          <span><Icon size={19} /></span>
          <div><small>{owner}</small><strong>{title}</strong><p>{text}</p></div>
        </article>
      ))}
    </div>
  );
}

export function OperationManualPage() {
  const { user } = useApp();
  const { section } = useParams<{ section: string }>();
  const roleView = section === 'role';
  const manual = OWNER_MANUALS[user.role];
  const OwnerIcon = manual.icon;

  return (
    <div className="page page--manual">
      <PageHeader
        eyebrow="Role-based playbook"
        title="操作说明书"
        description={`当前身份：${ROLE_LABELS[user.role]}。先了解平台协作流程，再按角色完成发布、管理与审批。`}
        actions={<Link className="button button--primary" to={roleView ? '/manual' : '/manual/role'}>{roleView ? <BookOpenText size={16} /> : <OwnerIcon size={16} />}{roleView ? '查看工作流总览' : '查看我的操作手册'}</Link>}
      />

      <div className="manual-shell">
        <aside className="manual-toc panel" aria-label="说明书目录">
          <div className="manual-toc__heading"><BookOpenText size={17} /><div><strong>阅读目录</strong><small>随演示身份自动切换</small></div></div>
          <nav>
            <Link to="/manual" className={roleView ? '' : 'active'}><FileText size={16} /><span><strong>工作流总览</strong><small>组件、场景、环境与运行</small></span></Link>
            <Link to="/manual/role" className={roleView ? 'active' : ''}><OwnerIcon size={16} /><span><strong>{ROLE_LABELS[user.role]}手册</strong><small>{user.name}</small></span></Link>
          </nav>
          <div className="manual-toc__note"><ShieldCheck size={16} /><p>页面按当前身份展示操作边界；实际写操作仍由后端 RBAC 校验。</p></div>
        </aside>

        <main className="manual-content">
          <article className="panel manual-preview-panel">
            <header className="manual-preview-panel__header">
              <span className={`manual-title-icon manual-title-icon--${roleView ? manual.tone : 'indigo'}`}>{roleView ? <OwnerIcon size={21} /> : <FileText size={21} />}</span>
              <div>
                <div className="eyebrow">Markdown preview</div>
                <h2>{roleView ? manual.title : '平台工作流程总览'}</h2>
                <p>{roleView ? manual.summary : '用四类不可变记录串起组件发布、场景发布、环境管理与一次真实执行。'}</p>
              </div>
            </header>

            <div className="manual-preview-panel__body">
              {!roleView && <WorkflowOverview />}
              <MarkdownPreview source={roleView ? manual.markdown : OVERVIEW_MARKDOWN} />

              {roleView ? (
                <>
                  <section className="manual-checklist" aria-label="角色检查清单">
                    <header><CheckCircle2 size={17} /><div><h3>离开页面前检查</h3><p>三项都确认后，再进入下一阶段。</p></div></header>
                    <div>{manual.checkpoints.map((item) => <span key={item}><CheckCircle2 size={15} /> {item}</span>)}</div>
                  </section>
                  <div className="manual-actions">
                    <Link className="button button--primary" to={manual.primaryPath}>{manual.primaryLabel} <ArrowRight size={15} /></Link>
                    <Link className="button button--quiet" to="/runs"><PlayCircle size={15} /> 查看运行中心</Link>
                  </div>
                </>
              ) : (
                <>
                  <section className="manual-card-grid" aria-label="核心工作中心">
                    {CENTER_CARDS.map(({ to, icon: Icon, title, text, tone }) => (
                      <Link key={to} to={to} className={`manual-center-card manual-center-card--${tone}`}>
                        <span><Icon size={19} /></span><div><h3>{title}</h3><p>{text}</p><small>进入页面 <ArrowRight size={13} /></small></div>
                      </Link>
                    ))}
                  </section>
                  <div className="manual-security-callout"><KeyRound size={19} /><div><strong>Secret 只走 CredentialRef</strong><p>参数、Facts、节点值和运行输入都不是密钥存储位置；危险动作还需要目标环境 Owner 审批。</p></div><TestTube2 size={19} /></div>
                </>
              )}
            </div>
          </article>
        </main>
      </div>
    </div>
  );
}
