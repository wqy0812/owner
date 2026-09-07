import { Archive, Beaker, Boxes, Container, FileCode2, GitBranch, History, PencilLine, Plus, Rocket, Shield, Trash2, Undo2, UserRound } from 'lucide-react';
import { BranchScope } from '../../../components/BranchScope';
import { ComponentUsagePanel } from '../../../components/ComponentUsagePanel';
import { ComponentMappingOverview, DependencyContractList, mappingCount, ParameterContractList } from '../../../components/ParameterEditors';
import { EmptyState, formatTime, StatusPill } from '../../../components/Primitives';
import { StatusExplanationPanel } from '../../../components/StatusExplanationPanel';
import { useApp } from '../../../context/AppContext';
import {
  componentLayer
} from '../../../types/componentClassification';
import type { Component, ComponentRelease } from '../../../types/domain';
import { lifecycleSummary, publicCount, releaseEvidence, releaseReadyForPublish, type ContractSection } from '../model';
import { EvidenceLink } from '../verification/ReleaseVerification';
import { CompatibilityBadge, DraftReadiness, EnvironmentConstraints } from './ReleasePresentation';

import type { ReactNode } from 'react';
export type ReleaseAction = 'edit' | 'image' | 'artifact' | 'test' | 'evidence' | 'inspect' | 'review' | 'publish' | 'deprecate' | 'candidate' | 'review-submit' | 'restore' | 'delete';
export function ReleaseDetail({ selected, contractRelease, mine, canTest, busy, showDefaultHint, contractEditor, selectContractRelease, onReleaseAction, onEditContract, onEditComponent, onNewVersion, onNewBranch }: {
  selected: Component; contractRelease?: ComponentRelease; mine: boolean; canTest: boolean; busy: boolean;
  showDefaultHint: boolean; contractEditor?: ReactNode;
  selectContractRelease: (id: string) => void;
  onReleaseAction: (action: ReleaseAction, release: ComponentRelease) => void;
  onEditContract: (section: ContractSection, release?: ComponentRelease) => void;
  onEditComponent: () => void; onNewVersion: () => void; onNewBranch: () => void;
}) {
  const { user } = useApp();
  const releases = selected.releases?.length ? selected.releases : selected.latestRelease ? [selected.latestRelease] : [];
  const releaseGroups = selected.releaseLines?.length ? selected.releaseLines : releases.length ? [{ id: 'default', name: '默认发布线', releases }] : [];
  const contractComponents = [selected];
  const activeDraft = contractRelease?.state === 'draft' ? contractRelease : undefined;
  const releaseWorkItem = selected.readContext?.workItems.find(item => item.subject.type === 'component_release' && item.subject.id === contractRelease?.id);
  return <>
    <article className="panel component-hero">
      <div className="component-hero__title">
        <span className="component-logo"><Boxes size={26} /></span>
        <div><div className="eyebrow">{componentLayer(selected.layer).code} · {selected.slug ?? 'component'}</div><h2>{selected.name}</h2><p>{selected.description ?? '暂无组件说明'}</p><div className="classification-badges"><span>{componentLayer(selected.layer).label}</span>{selected.tags.map((tag) => <span key={tag}>{tag}</span>)}</div></div>
      </div>
      <div className="component-hero__meta">
        <span><UserRound size={15} /> {selected.ownerName ?? selected.ownerId}</span>
        <span><GitBranch size={15} /> {selected.releaseCount ?? releases.length} 个版本</span>
        {contractRelease && <><strong>{contractRelease.lineName} · {contractRelease.version}</strong><StatusPill status={contractRelease.state} /></>}
      </div>
      <BranchScope scope={contractRelease?.environmentConstraints} />
      {mine && <div className="row-actions"><button className="button button--quiet" onClick={() => onEditComponent()}><PencilLine size={16} /> 编辑组件</button><button className="button button--secondary" disabled={!selected.releaseLines?.some(line => line.id === contractRelease?.lineId && line.evolutionEligible)} title={selected.releaseLines?.find(line => line.id === contractRelease?.lineId)?.evolutionBlockedReason ?? '请先发布当前分支首版'} onClick={() => onNewVersion()}><Plus size={16} /> 新增版本</button><button className="button button--quiet" onClick={() => onNewBranch()}><GitBranch size={16} /> 新增分支</button>{activeDraft ? <button className="button button--quiet" onClick={() => onReleaseAction('edit', activeDraft)}><FileCode2 size={16} /> 编辑版本与 Playbook</button> : null}</div>}
    </article>

    <StatusExplanationPanel item={releaseWorkItem} />
    {(mine || user.role === "platform_admin") && <ComponentUsagePanel key={selected.id} component={selected} />}

    {mine && activeDraft ? <DraftReadiness key={`readiness:${activeDraft.id}`}
      release={activeDraft}
      evidence={selected.readContext?.evidence[activeDraft.id]}
      onContract={() => onEditContract('parameters', activeDraft)}
      onReview={() => onReleaseAction('review', activeDraft)}
      onLifecycle={() => onReleaseAction('edit', activeDraft)}
      onImage={() => onReleaseAction('image', activeDraft)}
      onArtifact={() => onReleaseAction('artifact', activeDraft)}
      onValidate={() => { selectContractRelease(activeDraft.id); onReleaseAction('test', activeDraft); }}
      onPublish={() => void onReleaseAction('publish', activeDraft)}
    /> : null}

    <article className="panel">
      <header className="panel__header"><div><span className="panel__icon"><Rocket size={18} /></span><div><h2>发布历史</h2><p>点击版本可切换下方依赖和参数合同；已发布版本不可修改</p></div></div></header>
      {releases.length ? <div className="release-table">
        <div className="release-table__head"><span>版本</span><span>适配标签</span><span>验证</span><span>依赖 / 动作</span><span>创建 / 发布时间</span><span /></div>
        {releaseGroups.flatMap((line) => [<div key={`line:${line.id}`} className="release-line-header"><GitBranch size={15} /><strong>{line.name}</strong><span>{line.releases.length} 个版本</span></div>, ...line.releases.map((release) => {
          const active = contractRelease?.id === release.id;
          const lifecycle = lifecycleSummary(release.actions);
          const evidence = releaseEvidence(selected.readContext?.evidence[release.id], 'install');
          const publishReady = releaseReadyForPublish(release);
          return <div key={release.id} className={`release-row${active ? ' release-row--active' : ''}`} onClick={() => selectContractRelease(release.id)}>
            <button type="button" className="release-row__version" aria-pressed={active} aria-label={`查看 ${release.version} 的依赖和参数合同`} onClick={() => selectContractRelease(release.id)}>
              <span className="release-state-line"><strong>{release.version}</strong><StatusPill status={release.state} />{release.candidate && <StatusPill status="candidate">候选集</StatusPill>}<StatusPill status={release.review.status}>{release.review.status === 'approved' ? '合同已审核' : release.review.status === 'pending' ? '合同审核中' : release.review.status === 'rejected' ? '合同已驳回' : '合同待提交'}</StatusPill><CompatibilityBadge release={release} /></span>
              <small>{release.releaseNotes ?? '未填写发布说明'}</small>
            </button>
            <EnvironmentConstraints constraints={release.environmentConstraints} />
            <div className="release-verification"><StatusPill status={release.readiness.status} /><EvidenceLink run={evidence} /></div>
            <div className="release-facts"><span>{release.dependencies?.length ?? 0} 项依赖</span><span>{mappingCount(release)} 个参数映射</span><span>{publicCount(release)} 个公开参数</span><span className={`lifecycle-completeness${lifecycle.completed === lifecycle.total ? ' lifecycle-completeness--complete' : ''}`}>动作 {lifecycle.completed}/{lifecycle.total}：{lifecycle.labels}</span></div>
            <div>{release.releasedAt ? formatTime(release.releasedAt) : `创建 ${formatTime(release.createdAt)}`}</div>
            <div className="row-actions" onClick={(event) => event.stopPropagation()}>
              {canTest && <button className="icon-text" onClick={() => { selectContractRelease(release.id); onReleaseAction('test', release); }}><Beaker size={15} /> 环境验证</button>}
              {mine && <button className="icon-text" onClick={() => onReleaseAction('evidence', release)}><History size={15} /> Run 证据</button>}
              <button className="icon-text" onClick={() => { selectContractRelease(release.id); onReleaseAction('inspect', release); }}>查看详情</button>
              {release.review.status === 'rejected' && <button className="icon-text" onClick={() => onReleaseAction('review', release)}><Shield size={15} /> 审批意见</button>}
              {mine && release.state === 'draft' && <button className="icon-text" onClick={() => onReleaseAction('edit', release)}><FileCode2 size={15} /> Playbook</button>}
              {mine && <button className="icon-text" onClick={() => onReleaseAction('image', release)}><Container size={15} /> 构建镜像</button>}
              {mine && <button className="icon-text" onClick={() => onReleaseAction('artifact', release)}><Archive size={15} /> 组件介质</button>}
              {mine && release.state === 'draft' && release.review.status !== 'pending' && release.review.status !== 'approved' && <button className="icon-text" disabled={busy} onClick={() => void onReleaseAction('review-submit', release)}><Shield size={15} /> 提交合同审核</button>}
              {mine && release.state === 'draft' && <button className="icon-text" disabled={busy || (!release.candidate && (!publishReady || release.review.status !== 'approved'))} onClick={() => void onReleaseAction('candidate', release)}>{release.candidate ? '撤回候选' : '加入候选集'}</button>}
              {mine && release.state === 'draft' && <button className="icon-text icon-text--primary" disabled={!publishReady || release.review.status !== 'approved'} title={release.review.status !== 'approved' ? '请先通过平台 Owner 合同审核' : publishReady ? undefined : '请先完成生命周期、安装验证和回退验证'} onClick={() => void onReleaseAction('publish', release)}><Rocket size={15} /> 发布</button>}
              {mine && (release.state === 'draft' || release.state === 'released') && <button className="icon-text icon-text--danger" onClick={() => void onReleaseAction('deprecate', release)}>{release.state === 'draft' ? '废弃草稿' : '废弃'}</button>}
              {mine && release.state === 'deprecated' && !release.releasedAt && <button className="icon-text" disabled={busy} onClick={() => void onReleaseAction('restore', release)}><Undo2 size={15} /> 恢复 Draft</button>}
              {mine && release.state === 'deprecated' && !release.releasedAt && <button className="icon-text icon-text--danger" disabled={busy} onClick={() => void onReleaseAction('delete', release)}><Trash2 size={15} /> 永久删除</button>}
            </div>
          </div>;
        })])}
      </div> : <EmptyState title="尚无发布版本" description="创建 Draft 并配置安装、验证和升级动作。" />}
    </article>

    <ComponentMappingOverview releases={releases} components={contractComponents} />
    {showDefaultHint && selected.latestRelease && contractRelease ? <p className="mapping-empty contract-hint">当前展示 {contractRelease.lineName} · {contractRelease.version}，因为它有参数映射。默认版本 {selected.latestRelease.lineName} · {selected.latestRelease.version} 没有映射。</p> : null}
    {contractEditor ?? (
      <div className="detail-stack component-contracts">
        <article className="panel" id="contract-dependencies">
          <header className="panel__header">
            <div><span className="panel__icon panel__icon--cyan"><GitBranch size={18} /></span><div><h2>直接依赖</h2><p>{contractRelease ? `${contractRelease.version} 锁定的上游，以及引用了哪个公开参数` : '锁定上游版本，并标明引用了哪个公开参数'}</p></div></div>
            {mine && contractRelease?.state === 'draft' ? <button type="button" className="button button--quiet" aria-label="编辑直接依赖" onClick={() => onEditContract('dependencies')}><PencilLine size={15} /> 编辑</button> : null}
          </header>
          <DependencyContractList dependencies={contractRelease?.dependencies ?? []} components={contractComponents} />
        </article>
        <article className="panel" id="contract-parameters">
          <header className="panel__header">
            <div><span className="panel__icon panel__icon--amber"><Shield size={18} /></span><div><h2>参数合同</h2><p>{contractRelease ? `${contractRelease.version} 的公开参数可被下游引用；内部参数只给本组件使用` : '公开参数可被下游引用；内部参数只给本组件使用'}</p></div></div>
            {mine && contractRelease?.state === 'draft' ? <button type="button" className="button button--quiet" aria-label="编辑参数合同" onClick={() => onEditContract('parameters')}><PencilLine size={15} /> 编辑</button> : null}
          </header>
          <ParameterContractList release={contractRelease} components={contractComponents} consumers={selected.readContext?.parameterConsumers} />
        </article>
      </div>
    )}
  </>;
}
