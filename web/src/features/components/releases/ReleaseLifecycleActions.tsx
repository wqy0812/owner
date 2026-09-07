import { AlertTriangle, GitBranch } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { actionableExplanation, api } from '../../../api/client';
import { Modal } from '../../../components/Primitives';
import { StatusExplanationPanel } from '../../../components/StatusExplanationPanel';
import { displayError, useApp } from '../../../context/AppContext';
import type { ComponentRelease, ImpactPreview, WorkExplanation } from '../../../types/domain';
import { releaseReadyForPublish } from '../model';


export function useReleaseLifecycleActions({ scopeKey, onRestored, onDeleted }: {
  scopeKey: string;
  onRestored: (id: string) => void;
  onDeleted: (id: string) => void;
}) {
  const { notify, signalRefresh } = useApp();
  const [publishRelease, setPublishRelease] = useState<ComponentRelease>();
  const [deprecateRelease, setDeprecateRelease] = useState<ComponentRelease>();
  const [impact, setImpact] = useState<ImpactPreview>();
  const [operationExplanation, setOperationExplanation] = useState<WorkExplanation>();
  const [busy, setBusy] = useState(false);
  const previewGeneration = useRef(0);
  const scopeGeneration = useRef(0);
  useEffect(() => {
    previewGeneration.current += 1;
    scopeGeneration.current += 1;
    setPublishRelease(undefined); setDeprecateRelease(undefined);
    setImpact(undefined); setOperationExplanation(undefined); setBusy(false);
    return () => { previewGeneration.current += 1; scopeGeneration.current += 1; };
  }, [scopeKey]);
  const deprecationScenarioRunCount = deprecateRelease ? impact?.scenarioRunCount ?? 0 : 0;
  const deprecationBlocked = deprecateRelease?.state === 'released' && deprecationScenarioRunCount > 0;
  async function previewPublish(release: ComponentRelease) {
    const generation = ++previewGeneration.current;
    setPublishRelease(release);
    setDeprecateRelease(undefined);
    setImpact(undefined);
    setOperationExplanation(undefined);
    try {
      const result = await api.releaseImpact(release.id, 'publish');
      if (generation === previewGeneration.current) setImpact(result);
    } catch (reason) {
      if (generation === previewGeneration.current) notify('error', '影响分析失败', displayError(reason));
    }
  }

  async function previewDeprecate(release: ComponentRelease) {
    const generation = ++previewGeneration.current;
    setDeprecateRelease(release);
    setPublishRelease(undefined);
    setImpact(undefined);
    setOperationExplanation(undefined);
    try {
      const result = await api.releaseImpact(release.id, 'deprecate');
      if (generation === previewGeneration.current) setImpact(result);
    } catch (reason) {
      if (generation === previewGeneration.current) notify('error', '影响分析失败', displayError(reason));
    }
  }

  async function confirmPublish() {
    if (!publishRelease) return;
    if (!releaseReadyForPublish(publishRelease)) {
      notify('error', '暂不能发布', '请先完成生命周期、安装验证和回退验证。');
      return;
    }
    const generation = scopeGeneration.current;
    setBusy(true);
    setOperationExplanation(undefined);
    try {
      await api.publishRelease(publishRelease.id);
      if (generation !== scopeGeneration.current) { signalRefresh(['components', 'notifications', 'workbench']); return; }
      notify('success', '组件版本已发布', publishRelease.parentReleaseId ? '已向精确锁定父 Release 的下游 Owner 生成影响通知。' : '全新发布线已进入 Catalog，不替换现有锁定版本。');
      setPublishRelease(undefined);
      signalRefresh(['components', 'notifications', 'workbench']);
    } catch (reason) {
      if (generation !== scopeGeneration.current) return;
      setOperationExplanation(actionableExplanation(reason));
      notify('error', '发布失败', displayError(reason));
    } finally {
      if (generation === scopeGeneration.current) setBusy(false);
    }
  }

  async function toggleCandidate(release: ComponentRelease) {
    if (!release.candidate && !releaseReadyForPublish(release)) {
      notify('error', '暂不能加入候选集', '请先完成生命周期、安装验证和回退验证。');
      return;
    }
    const generation = scopeGeneration.current;
    setBusy(true);
    try {
      await api.setReleaseCandidate(release.id, !release.candidate);
      if (generation !== scopeGeneration.current) { signalRefresh(['components', 'notifications', 'workbench']); return; }
      notify('success', release.candidate ? '已撤回候选版本' : '已加入候选发布集', release.candidate ? '集群 Owner 将不再能新引用此 Draft。' : '集群 Owner 现在可以编排和测试；发布场景时将原子发布全部候选版本。');
      signalRefresh('components');
    } catch (reason) {
      if (generation !== scopeGeneration.current) return;
      notify('error', '候选状态更新失败', displayError(reason));
    } finally {
      if (generation === scopeGeneration.current) setBusy(false);
    }
  }

  async function submitReview(release: ComponentRelease) {
    const generation = scopeGeneration.current;
    setBusy(true);
    try {
      await api.submitReleaseReview(release.id);
      if (generation !== scopeGeneration.current) { signalRefresh(['components', 'notifications', 'workbench']); return; }
      notify('success', '参数合同已提交平台审核', '审核绑定当前参数、Action 和环境字段契约；后续修改会自动失效。');
      signalRefresh(['components', 'workbench']);
    } catch (reason) {
      if (generation !== scopeGeneration.current) return;
      notify('error', '提交审核失败', displayError(reason));
    } finally { if (generation === scopeGeneration.current) setBusy(false); }
  }

  async function confirmDeprecate() {
    if (!deprecateRelease) return;
    if (deprecationBlocked) {
      notify('error', '无法废弃', `该版本已经被 ${deprecationScenarioRunCount} 个场景 Run 锁定。`);
      return;
    }
    const generation = scopeGeneration.current;
    setBusy(true);
    setOperationExplanation(undefined);
    try {
      await api.deprecateRelease(deprecateRelease.id);
      if (generation !== scopeGeneration.current) { signalRefresh(['components', 'notifications', 'workbench']); return; }
      notify('success', deprecateRelease.state === 'draft' ? '组件草稿已废弃' : '组件版本已废弃', '历史 Run、Playbook、介质和已经锁定此 Release ID 的场景保持不变。');
      setDeprecateRelease(undefined);
      signalRefresh(['components', 'notifications']);
    } catch (reason) {
      if (generation !== scopeGeneration.current) return;
      setOperationExplanation(actionableExplanation(reason));
      notify('error', '废弃失败', displayError(reason));
    } finally {
      if (generation === scopeGeneration.current) setBusy(false);
    }
  }

  async function restoreRelease(release: ComponentRelease) {
    if (!window.confirm(`确认恢复 ${release.version}？\n该版本将重新成为可编辑 Draft；若发布线已有新的 Draft 或后继版本，后端会拒绝恢复。`)) return;
    const generation = scopeGeneration.current;
    setBusy(true);
    try {
      await api.restoreRelease(release.id);
      if (generation !== scopeGeneration.current) { signalRefresh(['components', 'notifications', 'workbench']); return; }
      notify('success', '组件草稿已恢复', `${release.version} 已恢复为 Draft，可继续编辑和验证。`);
      onRestored(release.id);
      signalRefresh(['components', 'workbench']);
    } catch (reason) {
      if (generation !== scopeGeneration.current) return;
      notify('error', '恢复失败', displayError(reason));
    } finally {
      if (generation === scopeGeneration.current) setBusy(false);
    }
  }

  async function deleteRelease(release: ComponentRelease) {
    if (!window.confirm(`确认永久删除 ${release.version}？\n将删除该未发布 Release 的合同、Action、托管 Playbook、介质和镜像登记。\n\n只有已废弃、从未发布且没有 Run、构建或任何引用的 Release 可以删除；此操作不可恢复。`)) return;
    const generation = scopeGeneration.current;
    setBusy(true);
    try {
      await api.deleteRelease(release.id);
      if (generation !== scopeGeneration.current) { signalRefresh(['components', 'notifications', 'workbench']); return; }
      onDeleted(release.id);
      notify('success', '组件版本已永久删除', `${release.version} 及其未发布内容已从目录移除。`);
      signalRefresh(['components', 'workbench']);
    } catch (reason) {
      if (generation !== scopeGeneration.current) return;
      notify('error', '永久删除失败', displayError(reason));
    } finally {
      if (generation === scopeGeneration.current) setBusy(false);
    }
  }

  function closeDialogs() {
    previewGeneration.current += 1;
    setPublishRelease(undefined); setDeprecateRelease(undefined);
    setImpact(undefined); setOperationExplanation(undefined);
  }

  return {
    busy, previewPublish, previewDeprecate, toggleCandidate, submitReview, restoreRelease, deleteRelease,
    dialogs: <>
      {publishRelease && <Modal title={`发布 ${publishRelease.version}`} description={publishRelease.parentReleaseId ? '发布后版本不可修改；只通知精确锁定父 Release 的下游 Owner。' : '这是全新发布线，不替换现有锁定 Release，也不发送影响通知。'} onClose={closeDialogs}>
        <div className="modal-body">
          {impact?.changeKind === 'new_line' ? <div className="warning-callout"><GitBranch size={19} /><div><strong>全新发布线 · {impact.lineName}</strong><p>兼容性不适用；现有组件和场景继续锁定原 Release。</p></div></div> : null}
          <div className="impact-grid"><div><span>下游组件 Owner</span><strong>{impact?.componentOwners?.length ?? '…'}</strong></div><div><span>相关集群 Owner</span><strong>{impact?.scenarioOwners?.length ?? '…'}</strong></div><div><span>受影响场景</span><strong>{impact?.scenarios?.length ?? '…'}</strong></div></div>
          {impact?.paths?.length ? <div className="impact-paths"><strong>影响路径</strong>{impact.paths.slice(0, 5).map((path, index) => <div key={index}>{path.join('  →  ')}</div>)}</div> : null}
        </div>
        <StatusExplanationPanel explanation={operationExplanation} title="发布操作被阻断" />
        <footer className="modal-actions"><button className="button button--quiet" onClick={closeDialogs}>取消</button><button disabled={busy || !impact} className="button button--primary" onClick={() => void confirmPublish()}>{busy ? '发布中…' : impact?.changeKind === 'new_line' ? '确认发布新线' : '确认发布并通知'}</button></footer>
      </Modal>}
      {deprecateRelease && <Modal title={`${deprecateRelease.state === 'draft' ? '废弃草稿' : '废弃'} ${deprecateRelease.version}`} description={deprecateRelease.state === 'draft' ? '未发布草稿可以随时废弃；废弃后仍可恢复为 Draft，或在没有 Run、构建和引用时永久删除。' : '没有场景 Run 锁定时，该版本可停止作为推荐版本。'} onClose={closeDialogs}>
        <div className="modal-body">
          <div className="warning-callout warning-callout--danger"><AlertTriangle size={19} /><div><strong>{deprecationBlocked ? '该组件版本不能废弃' : deprecateRelease.state === 'draft' ? '草稿将进入可恢复的废弃状态' : '这是影响下游选择的状态变更'}</strong><p>{deprecationBlocked ? `该版本已经被 ${deprecationScenarioRunCount} 个场景 Run 锁定；请保留该版本以维持运行记录与交付依据。` : deprecateRelease.state === 'draft' ? '废弃不会删除合同或证据；之后可以恢复，满足删除门禁时也可永久删除。' : '请先确认受影响组件和场景。仅被场景引用但从未运行的版本仍可废弃。'}</p></div></div>
          <div className="impact-grid"><div><span>下游组件 Owner</span><strong>{impact?.componentOwners?.length ?? '…'}</strong></div><div><span>相关集群 Owner</span><strong>{impact?.scenarioOwners?.length ?? '…'}</strong></div><div><span>受影响场景</span><strong>{impact?.scenarios?.length ?? '…'}</strong></div></div>
          {impact?.paths?.length ? <div className="impact-paths"><strong>影响路径</strong>{impact.paths.slice(0, 5).map((path, index) => <div key={index}>{path.join('  →  ')}</div>)}</div> : null}
        </div>
        <StatusExplanationPanel explanation={operationExplanation} title="废弃操作被阻断" />
        <footer className="modal-actions"><button className="button button--quiet" onClick={closeDialogs}>取消</button><button disabled={busy || !impact || deprecationBlocked} className="button button--danger" onClick={() => void confirmDeprecate()}>{busy ? '废弃中…' : deprecateRelease.state === 'draft' ? '确认废弃草稿' : '确认废弃版本'}</button></footer>
      </Modal>}
    </>,
  };
}
