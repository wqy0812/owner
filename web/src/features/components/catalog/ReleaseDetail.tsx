import { ActionMenu } from '../../../components/ActionMenu';
import { WorkspaceTabs } from '../../../components/WorkspaceTabs';
import { Button, Select, Table } from 'antd';
import { useState } from 'react';
import { Archive, Beaker, Boxes, Container, FileCode2, GitBranch, History, PencilLine, Plus, Rocket, Shield, Trash2, Undo2, UserRound } from 'lucide-react';
import { BranchScope } from '../../../components/BranchScope';
import { ComponentUsagePanel } from '../../../components/ComponentUsagePanel';
import { ComponentMappingOverview, DependencyContractList, mappingCount, ParameterContractList } from '../../../components/ParameterEditors';
import { EmptyState, formatTime, StatusPill } from '../../../components/Primitives';
import { StatusExplanationPanel } from '../../../components/StatusExplanationPanel';
import { useApp } from '../../../context/AppContext';
import { componentLayer } from '../../../types/componentClassification';
import type { Component, ComponentRelease } from '../../../types/domain';
import { lifecycleSummary, publicCount, releaseEvidence, releaseReadyForPublish, type ContractSection } from '../model';
import { EvidenceLink } from '../verification/ReleaseVerification';
import { CompatibilityBadge, DraftReadiness, EnvironmentConstraints } from './ReleasePresentation';
import type { ReactNode } from 'react';
export type ReleaseAction = 'edit' | 'image' | 'artifact' | 'test' | 'evidence' | 'inspect' | 'review' | 'publish' | 'deprecate' | 'candidate' | 'review-submit' | 'restore' | 'delete';
export function ReleaseDetail({ selected, contractRelease, mine, canTest, busy, showDefaultHint, contractEditor, selectContractRelease, onReleaseAction, onEditContract, onEditComponent, onNewVersion, onNewBranch, deletingComponent, onDeleteComponent }: {
    selected: Component;
    contractRelease?: ComponentRelease;
    mine: boolean;
    canTest: boolean;
    busy: boolean;
    showDefaultHint: boolean;
    contractEditor?: ReactNode;
    selectContractRelease: (id: string) => Promise<boolean>;
    onReleaseAction: (action: ReleaseAction, release: ComponentRelease) => void;
    onEditContract: (section: ContractSection, release?: ComponentRelease) => void;
    onEditComponent: () => void;
    onNewVersion: () => void;
    onNewBranch: () => void;
    deletingComponent: boolean;
    onDeleteComponent?: () => void;
}) {
    const { user } = useApp();
    const [detailTab, setDetailTab] = useState("contract");
    const releases = selected.releases?.length ? selected.releases : selected.latestRelease ? [selected.latestRelease] : [];
    const releaseGroups = selected.releaseLines?.length ? selected.releaseLines : releases.length ? [{ id: 'default', name: '默认发布线', releases }] : [];
    const contractComponents = [selected];
    const activeDraft = contractRelease?.state === 'draft' ? contractRelease : undefined;
    const releaseWorkItem = selected.readContext?.workItems.find(item => item.subject.type === 'component_release' && item.subject.id === contractRelease?.id);
    function renderActions(release: ComponentRelease) {
      const publishReady = releaseReadyForPublish(release);
      return <div className="row-actions" onClick={(event) => event.stopPropagation()}>
              {canTest && <Button className="icon-text" onClick={async () => { if (await selectContractRelease(release.id)) onReleaseAction('test', release); }} htmlType={"button"} type="text"><Beaker size={15}/> 环境验证</Button>}
              {mine && <Button className="icon-text" onClick={() => onReleaseAction('evidence', release)} htmlType={"button"} type="text"><History size={15}/> Run 证据</Button>}
              <Button className="icon-text" onClick={async () => { if (await selectContractRelease(release.id)) onReleaseAction('inspect', release); }} htmlType={"button"} type="text">查看详情</Button>
              {release.review.status === 'rejected' && <Button className="icon-text" onClick={() => onReleaseAction('review', release)} htmlType={"button"} type="text"><Shield size={15}/> 审批意见</Button>}
              {mine && release.state === 'draft' && <Button className="icon-text" onClick={() => onReleaseAction('edit', release)} htmlType={"button"} type="text"><FileCode2 size={15}/> Playbook</Button>}
              {mine && <Button className="icon-text" onClick={() => onReleaseAction('image', release)} htmlType={"button"} type="text"><Container size={15}/> 构建镜像</Button>}
              {mine && <Button className="icon-text" onClick={() => onReleaseAction('artifact', release)} htmlType={"button"} type="text"><Archive size={15}/> 组件介质</Button>}
              {mine && release.state === 'draft' && release.review.status !== 'pending' && release.review.status !== 'approved' && <Button className="icon-text" disabled={busy} onClick={() => void onReleaseAction('review-submit', release)} htmlType={"button"} type="text"><Shield size={15}/> 提交合同审核</Button>}
              {mine && release.state === 'draft' && <Button className="icon-text" disabled={busy || (!release.candidate && (!publishReady || release.review.status !== 'approved'))} onClick={() => void onReleaseAction('candidate', release)} htmlType={"button"} type="text">{release.candidate ? '撤回候选' : '加入候选集'}</Button>}
              {mine && release.state === 'draft' && <Button className="icon-text icon-text--primary" disabled={!publishReady || release.review.status !== 'approved'} title={release.review.status !== 'approved' ? '请先通过平台 Owner 合同审核' : publishReady ? undefined : '请先完成生命周期、安装验证和回退验证'} onClick={() => void onReleaseAction('publish', release)} htmlType={"button"} type="text"><Rocket size={15}/> 发布</Button>}


              <ActionMenu items={[(mine && (release.state === 'draft' || release.state === 'released')) && {key:'action-0',label:<>{release.state === 'draft' ? '废弃草稿' : '废弃'}</>,onClick:() => void onReleaseAction('deprecate', release),danger:true},(mine && release.state === 'deprecated' && !release.releasedAt) && {key:'action-1',label:<><Undo2 size={15}/>恢复 Draft</>,onClick:() => void onReleaseAction('restore', release),disabled:busy},(mine && release.state === 'deprecated' && !release.releasedAt) && {key:'action-2',label:<><Trash2 size={15}/>永久删除</>,onClick:() => void onReleaseAction('delete', release),disabled:busy,danger:true}]} />
            </div>;
    }
    return <>
    <article className="panel component-hero">
      <div className="component-hero__title">
        <span className="component-logo"><Boxes size={26}/></span>
        <div><div className="eyebrow">{componentLayer(selected.layer).code} · {selected.slug ?? 'component'}</div><h2>{selected.name}</h2><p>{selected.description ?? '暂无组件说明'}</p><div className="classification-badges"><span>{componentLayer(selected.layer).label}</span>{selected.tags.map((tag) => <span key={tag}>{tag}</span>)}</div></div>
      </div>
      <div className="component-hero__meta">
        <span><UserRound size={15}/> {selected.ownerName ?? selected.ownerId}</span>
        <span><GitBranch size={15}/> {selected.releaseCount ?? releases.length} 个版本</span>
        {contractRelease && <><strong>{contractRelease.lineName} · {contractRelease.version}</strong><StatusPill status={contractRelease.state}/></>}
      </div>
      <BranchScope scope={contractRelease?.environmentConstraints}/>
      {mine && <div className="row-actions"><Button className="button button--quiet" onClick={() => onEditComponent()} htmlType={"button"} type="default"><PencilLine size={16}/> 编辑组件</Button><ActionMenu items={[(selected.canDelete && onDeleteComponent) && {key:'action-0',label:<><Trash2 size={16}/>{deletingComponent ? '删除中…' : '删除组件'}</>,onClick:onDeleteComponent,disabled:deletingComponent,danger:true}]} /><Button className="button button--secondary" disabled={!selected.releaseLines?.some(line => line.id === contractRelease?.lineId && line.evolutionEligible)} title={selected.releaseLines?.find(line => line.id === contractRelease?.lineId)?.evolutionBlockedReason ?? '请先发布当前分支首版'} onClick={() => onNewVersion()} htmlType={"button"} type="default"><Plus size={16}/> 新增版本</Button><Button className="button button--quiet" onClick={() => onNewBranch()} htmlType={"button"} type="default"><GitBranch size={16}/> 新增分支</Button>{activeDraft ? <Button className="button button--quiet" onClick={() => onReleaseAction('edit', activeDraft)} htmlType={"button"} type="default"><FileCode2 size={16}/> 编辑版本与 Playbook</Button> : null}</div>}
    </article>
<StatusExplanationPanel item={releaseWorkItem}/>
<div className="detail-release-selector"><span>当前版本</span><Select aria-label="当前组件版本" value={contractRelease?.id} onChange={async id => { if (await selectContractRelease(id)) setDetailTab("contract"); }} options={releases.map(release => ({value: release.id, label: `${release.lineName} · ${release.version}`}))} /></div>

<WorkspaceTabs activeKey={detailTab} onChange={setDetailTab} items={[{key: 'contract', label: '版本与合同', children: <>{contractRelease && <div className="panel release-current-actions">{renderActions(contractRelease)}</div>}{mine && activeDraft ? <DraftReadiness key={`readiness:${activeDraft.id}`} release={activeDraft} evidence={selected.readContext?.evidence[activeDraft.id]} onContract={() => onEditContract('parameters', activeDraft)} onReview={() => onReleaseAction('review', activeDraft)} onLifecycle={() => onReleaseAction('edit', activeDraft)} onImage={() => onReleaseAction('image', activeDraft)} onArtifact={() => onReleaseAction('artifact', activeDraft)} onValidate={async () => { if (await selectContractRelease(activeDraft.id)) onReleaseAction('test', activeDraft); }} onPublish={() => void onReleaseAction('publish', activeDraft)}/> : null}
<ComponentMappingOverview releases={releases} components={contractComponents}/>
{showDefaultHint && selected.latestRelease && contractRelease ? <p className="mapping-empty contract-hint">当前展示 {contractRelease.lineName} · {contractRelease.version}，因为它有参数映射。默认版本 {selected.latestRelease.lineName} · {selected.latestRelease.version} 没有映射。</p> : null}
{contractEditor ?? (<div className="detail-stack component-contracts">
        <article className="panel" id="contract-dependencies">
          <header className="panel__header">
            <div><span className="panel__icon panel__icon--cyan"><GitBranch size={18}/></span><div><h2>直接依赖</h2><p>{contractRelease ? `${contractRelease.version} 锁定的上游，以及引用了哪个公开参数` : '锁定上游版本，并标明引用了哪个公开参数'}</p></div></div>
            {mine && contractRelease?.state === 'draft' ? <Button className="button button--quiet" aria-label="编辑直接依赖" onClick={() => onEditContract('dependencies')} htmlType={"button"} type="default"><PencilLine size={15}/> 编辑</Button> : null}
          </header>
          <DependencyContractList dependencies={contractRelease?.dependencies ?? []} components={contractComponents}/>
        </article>
        <article className="panel" id="contract-parameters">
          <header className="panel__header">
            <div><span className="panel__icon panel__icon--amber"><Shield size={18}/></span><div><h2>参数合同</h2><p>{contractRelease ? `${contractRelease.version} 的公开参数可被下游引用；内部参数只给本组件使用` : '公开参数可被下游引用；内部参数只给本组件使用'}</p></div></div>
            {mine && contractRelease?.state === 'draft' ? <Button className="button button--quiet" aria-label="编辑参数合同" onClick={() => onEditContract('parameters')} htmlType={"button"} type="default"><PencilLine size={15}/> 编辑</Button> : null}
          </header>
          <ParameterContractList release={contractRelease} components={contractComponents} consumers={selected.readContext?.parameterConsumers}/>
        </article>
      </div>)}</>},
{key: 'history', label: '版本历史', children: <><article className="panel">
      <header className="panel__header"><div><span className="panel__icon"><Rocket size={18}/></span><div><h2>发布历史</h2><p>点击版本可切换下方依赖和参数合同；已发布版本不可修改</p></div></div></header>
      {releases.length ? <div className="release-history-tables">{releaseGroups.map(line => <section key={line.id}>
        <div className="release-line-header"><GitBranch size={15}/><strong>{line.name}</strong><span>{line.releases.length} 个版本</span></div>
        <Table rowKey="id" dataSource={line.releases} pagination={false} scroll={{x: 1240}}
          rowClassName={release => release.id === contractRelease?.id ? 'release-history-active' : ''}
          columns={[
            {title:'版本',key:'version',width:300,render:(_,release)=><div className="release-version-cell">
              <Button type="link" onClick={async()=>{if(await selectContractRelease(release.id))setDetailTab('contract');}} aria-label={`查看 ${release.version} 的依赖和参数合同`} aria-pressed={release.id === contractRelease?.id}>{release.version}</Button>
              <div className="row-actions"><StatusPill status={release.state}/>{release.candidate && <StatusPill status="candidate">候选集</StatusPill>}<StatusPill status={release.review.status}>{release.review.status === 'approved' ? '合同已审核' : release.review.status === 'pending' ? '合同审核中' : release.review.status === 'rejected' ? '合同已驳回' : '合同待提交'}</StatusPill><CompatibilityBadge release={release}/></div>
              <p>{release.releaseNotes ?? '未填写发布说明'}</p>
            </div>},
            {title:'适配标签',key:'constraints',width:180,render:(_,release)=><EnvironmentConstraints constraints={release.environmentConstraints}/>},
            {title:'验证',key:'verification',width:160,render:(_,release)=><div className="release-verification"><StatusPill status={release.readiness.status}/><EvidenceLink run={releaseEvidence(selected.readContext?.evidence[release.id],'install')}/></div>},
            {title:'依赖与动作',key:'contracts',width:180,render:(_,release)=>{const lifecycle=lifecycleSummary(release.actions);return <div className="release-facts"><span>{release.dependencies?.length ?? 0} 项依赖 · {mappingCount(release)} 个参数映射</span><span>{publicCount(release)} 个公开参数</span><span>动作 {lifecycle.completed}/{lifecycle.total}：{lifecycle.labels}</span></div>;}},
            {title:'创建 / 发布时间',key:'time',width:140,render:(_,release)=>release.releasedAt ? formatTime(release.releasedAt) : `创建 ${formatTime(release.createdAt)}`},
            {title:'操作',key:'actions',width:440,render:(_,release)=>renderActions(release)},
          ]}/>
      </section>)}</div> : <EmptyState title="尚无发布版本" description="创建 Draft 并配置安装、验证和升级动作。"/>}
    </article></>},
{key: 'usage', label: '引用关系', children: <>{(mine || user.role === "platform_admin") && <ComponentUsagePanel key={selected.id} component={selected}/>}</>}]} />

  </>;
}
