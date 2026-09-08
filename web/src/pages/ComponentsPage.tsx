import { ActionMenu } from '../components/ActionMenu';
import { useDialogs } from '../components/UIProvider';
import { Button } from 'antd';
import { Plus } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { api } from '../api/client';
import { defaultContractRelease } from '../components/ParameterEditors';
import { EmptyState, ErrorBlock, LoadingBlock, Modal, PageHeader, RefreshNotice } from '../components/Primitives';
import { useApp } from '../context/AppContext';
import { ComponentCatalog } from '../features/components/catalog/ComponentCatalog';
import { ReleaseDetail, type ReleaseAction } from '../features/components/catalog/ReleaseDetail';
import { ReleaseReviewDetails } from '../features/components/catalog/ReleasePresentation';
import { ReleaseContractEditor } from '../features/components/contract/ReleaseContractEditor';
import { ComponentTemplateImportModal } from '../features/components/import/ComponentTemplateImportModal';
import { ArtifactModal } from '../features/components/media/ArtifactModal';
import { ImageBuildModal } from '../features/components/media/ImageBuildModal';
import { type ContractEditIntent, type ContractSection } from '../features/components/model';
import { CreateComponentModal, EditComponentModal, EditReleaseModal, InspectReleaseModal, NewVersionModal } from '../features/components/releases/ReleaseDialogs';
import { useReleaseLifecycleActions } from '../features/components/releases/ReleaseLifecycleActions';
import { useComponentSelection } from '../features/components/useComponentSelection';
import { useComponentDeletion } from '../features/components/useComponentDeletion';
import { ReleaseRunEvidenceModal, TestReleaseModal } from '../features/components/verification/ReleaseVerification';
import { activeWorkbench } from '../hooks/activeWork';
import { useApiData } from '../hooks/useApiData';
import type { Component, ComponentRelease } from '../types/domain';
export function ComponentsPage() {
    const { confirm } = useDialogs();
    const navigateTo = useNavigate();
    const { user, notify, signalRefresh } = useApp();
    const selection = useComponentSelection();
    const { searchParams, setSearchParams, selectedId, selectedReleaseId } = selection;
    const { data: catalog, loading, error, isRefreshing, reload, setData: setCatalog } = useApiData((signal) => api.componentSummaries(signal), [user.id], 'components');
    const [deletedComponentIds, setDeletedComponentIds] = useState<ReadonlySet<string>>(() => new Set());
    // A catalog read already in flight must not resurrect a confirmed deletion.
    const components = catalog?.filter(component => !deletedComponentIds.has(component.id));
    const [createOpen, setCreateOpen] = useState(false);
    const [componentImportOpen, setComponentImportOpen] = useState(false);
    const [versionBase, setVersionBase] = useState<Component>();
    const [blankVersionBase, setBlankVersionBase] = useState<Component>();
    const [editComponent, setEditComponent] = useState<Component>();
    const [editRelease, setEditRelease] = useState<ComponentRelease>();
    const [testRelease, setTestRelease] = useState<ComponentRelease>();
    const [evidenceRelease, setEvidenceRelease] = useState<ComponentRelease>();
    const [inspectRelease, setInspectRelease] = useState<ComponentRelease>();
    const [reviewReleaseId, setReviewReleaseId] = useState<string>();
    const [imageRelease, setImageRelease] = useState<ComponentRelease>();
    const [artifactRelease, setArtifactRelease] = useState<ComponentRelease>();
    const [contractReleaseId, setContractReleaseId] = useState<string>();
    const [editingContract, setEditingContract] = useState(false);
    const [contractDirty, setContractDirty] = useState(false);
    async function discardContract() {
        if (contractDirty && !await confirm('合同有未保存修改，确认放弃后切换？'))
            return false;
        setContractDirty(false);
        return true;
    }
    useEffect(() => {
        if (!contractDirty)
            return;
        const unload = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = ''; };
        const navigate = (event: MouseEvent) => {
            const anchor = (event.target as Element)?.closest?.('a[href]') as HTMLAnchorElement | null;
            if (!anchor || anchor.href === window.location.href || anchor.target || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return;
            const target = new URL(anchor.href);
            // Downloads do not leave the editor and must retain their native request.
            if (anchor.hasAttribute('download') || !['http:', 'https:'].includes(target.protocol)
                || (target.origin === window.location.origin && target.pathname.startsWith('/api/'))) return;
            event.preventDefault(); event.stopPropagation();
            void discardContract().then(accepted => {
                if (!accepted) return;
                if (target.origin === window.location.origin) navigateTo(target.pathname + target.search + target.hash);
                else window.location.assign(target.href);
            });
        };
        window.addEventListener('beforeunload', unload);
        document.addEventListener('click', navigate, true);
        return () => { window.removeEventListener('beforeunload', unload); document.removeEventListener('click', navigate, true); };
    }, [contractDirty]);
    const [contractFocus, setContractFocus] = useState<ContractSection>();
    const [contractDraftIntent, setContractDraftIntent] = useState<ContractEditIntent>();
    const [pendingContractRelease, setPendingContractRelease] = useState<ComponentRelease>();
    const detailId = selectedId ?? components?.[0]?.id;
    const detailQuery = useApiData((signal) => detailId ? api.component(detailId, signal) : Promise.resolve(undefined), [user.id, detailId], ['components', 'runs', 'workbench'], component => activeWorkbench({ items: component?.readContext?.workItems ?? [] }));
    const selected = detailQuery.data;
    const deletion = useComponentDeletion({
        scopeKey: `${user.id}:${detailId ?? ''}`,
        onDeleted: (id) => {
            setDeletedComponentIds(current => new Set([...current, id]));
            setCatalog(undefined);
            detailQuery.setData(undefined);
            setSearchParams({}, { replace: true });
        },
        onRejected: () => detailQuery.setData(current => current ? { ...current, canDelete: false } : current),
    });
    const { data: editorComponents, error: editorComponentsError, loading: editorComponentsLoading, reload: reloadEditorComponents } = useApiData((signal) => editingContract ? api.components(signal) : Promise.resolve(undefined), [user.id, editingContract], 'components');
    const releases = selected?.releases?.length ? selected.releases : selected?.latestRelease ? [selected.latestRelease] : [];
    const reviewRelease = releases.find((release) => release.id === reviewReleaseId && release.review.status === 'rejected');
    const contractRelease = releases.find((release) => release.id === (contractReleaseId ?? selectedReleaseId))
        ?? (pendingContractRelease?.id === contractReleaseId ? pendingContractRelease : undefined)
        ?? releases.find((release) => release.id === components?.find((component) => component.id === selected?.id)?.defaultReleaseId)
        ?? defaultContractRelease(releases, selected?.latestRelease);
    const editableDrafts = releases.filter((release) => release.state === 'draft');
    const editableDraft = editableDrafts[0];
    const activeDraft = contractRelease?.state === 'draft' ? contractRelease : undefined;
    const visibleEditRelease = editRelease?.componentId === selected?.id ? editRelease : undefined;
    const showContractEditor = Boolean(editingContract && contractRelease?.state === 'draft');
    const mine = selected?.ownerId === user.id && user.role === 'component_owner';
    const canTest = mine || user.role === 'environment_owner';
    const lifecycle = useReleaseLifecycleActions({
        scopeKey: `${user.id}:${selected?.id ?? ''}:${contractRelease?.id ?? ''}`,
        onRestored: selectContractRelease,
        onDeleted: (id) => {
            if (contractReleaseId === id || selectedReleaseId === id) {
                setContractReleaseId(undefined);
                if (selected)
                    setSearchParams({ selected: selected.id }, { replace: true });
            }
        },
    });
    const { busy, previewPublish, previewDeprecate, toggleCandidate, submitReview, restoreRelease, deleteRelease } = lifecycle;
    useEffect(() => {
        setContractReleaseId(selectedReleaseId);
        setEvidenceRelease((current) => current?.id === selectedReleaseId ? current : undefined);
        setTestRelease((current) => current?.id === selectedReleaseId ? current : undefined);
    }, [selectedReleaseId]);
    useEffect(() => {
        setContractReleaseId(undefined);
        setEditingContract(false);
        setContractFocus(undefined);
        setContractDraftIntent(undefined);
        setBlankVersionBase(undefined);
        setPendingContractRelease(undefined);
        setEditComponent(undefined);
        setTestRelease(undefined);
        setEvidenceRelease(undefined);
        setInspectRelease(undefined);
        setEditRelease(undefined);
        setImageRelease(undefined);
        setArtifactRelease(undefined);
    }, [selected?.id, user.id]);
    useEffect(() => {
        if (!selected || !contractRelease)
            return;
        selection.consumeAction(selected.id, contractRelease.id, (action) => {
            if (action === 'validate' && canTest)
                setTestRelease(contractRelease);
            if (action === 'publish' && mine)
                void previewPublish(contractRelease);
            if (action === 'contract' && mine)
                startEditingContract();
            if (action === 'lifecycle' && mine && contractRelease.state === 'draft')
                setEditRelease(contractRelease);
        });
    }, [canTest, contractRelease, mine, searchParams, selected]);
    useEffect(() => {
        if (contractRelease)
            selection.focusParameters();
    }, [contractRelease, searchParams]);
    async function selectContractRelease(id: string) {
        if (id !== contractRelease?.id && !await discardContract())
            return false;
        const release = releases.find((item) => item.id === id);
        setContractReleaseId(id);
        if (selected)
            setSearchParams({ selected: selected.id, release: id });
        if (pendingContractRelease?.id !== id)
            setPendingContractRelease(undefined);
        if (release?.state !== 'draft') {
            setEditingContract(false);
            setContractFocus(undefined);
        }
        return true;
    }
    async function startEditingContract(section?: ContractSection) {
        if (!selected || !mine)
            return;
        if (editingContract && section !== contractFocus && !await discardContract())
            return;
        if (activeDraft) {
            setPendingContractRelease(undefined);
            setEditingContract(true);
            setContractFocus(section ?? 'dependencies');
            return;
        }
        if (editableDrafts.length === 1 && editableDraft) {
            setPendingContractRelease(undefined);
            setContractReleaseId(editableDraft.id);
            setEditingContract(true);
            setContractFocus(section ?? 'dependencies');
            notify('info', '已切换到可编辑 Draft', `${contractRelease?.version ?? '当前版本'} 已不可修改，正在编辑 ${editableDraft.version}。`);
            return;
        }
        if (editableDrafts.length > 1) {
            notify('info', '请先选择 Draft', '发布历史中有多个可编辑 Draft，请先选择目标版本再编辑合同。');
            return;
        }
        setContractFocus(section ?? 'dependencies');
        notify('info', '请先新增版本', '选择可编辑草稿后，在对应区域编辑合同。');
    }
    async function selectComponent(id: string) {
        if (id === selected?.id)
            return true;
        if (!await discardContract())
            return false;
        if (editRelease) {
            notify('info', '请先完成当前 Draft 编辑', '保存或关闭 Playbook 弹窗后才能切换组件。');
            return false;
        }
        setSearchParams({ selected: id });
        return true;
    }
    function handleReleaseAction(action: ReleaseAction, release: ComponentRelease) {
        switch (action) {
            case 'edit':
                setEditRelease(release);
                break;
            case 'image':
                setImageRelease(release);
                break;
            case 'artifact':
                setArtifactRelease(release);
                break;
            case 'test':
                setTestRelease(release);
                break;
            case 'evidence':
                setEvidenceRelease(release);
                break;
            case 'inspect':
                setInspectRelease(release);
                break;
            case 'review':
                setReviewReleaseId(release.id);
                break;
            case 'publish':
                void previewPublish(release);
                break;
            case 'deprecate':
                void previewDeprecate(release);
                break;
            case 'candidate':
                void toggleCandidate(release);
                break;
            case 'review-submit':
                void submitReview(release);
                break;
            case 'restore':
                void restoreRelease(release);
                break;
            case 'delete':
                void deleteRelease(release);
                break;
        }
    }
    return (<div className="page">
      <PageHeader eyebrow="Component registry" title="组件中心" description="组件 Owner 在这里维护不可变发布、依赖关系和 Ansible 生命周期动作。" actions={user.role === 'component_owner' ? <><ActionMenu ariaLabel="组件目录更多操作" items={[{ key: "import", label: "批量导入", onClick: () => setComponentImportOpen(true) }]}/><Button className="button button--primary" onClick={() => setCreateOpen(true)} htmlType={"button"} type="primary"><Plus size={16}/> 新建组件</Button></> : undefined}/>
      {loading && !components ? <LoadingBlock label="正在读取组件目录…"/> : error && !components ? <ErrorBlock message={error} onRetry={() => void reload()}/> : (<>
          <RefreshNotice loading={isRefreshing} error={components ? error : undefined} onRetry={() => void reload()}/>
          <ComponentCatalog components={components} selected={selected} detailId={detailId} editRelease={editRelease} onSelect={selectComponent}>
            {({ selectedInCatalog, clearFilters }) => <>

              {detailQuery.error && !selected ? <section className="panel"><ErrorBlock message={detailQuery.error} onRetry={() => void detailQuery.reload()}/></section> : detailId && !selected ? <section className="panel"><LoadingBlock label="正在读取组件详情…"/></section> : selected ? <section className="detail-stack">
                <RefreshNotice loading={detailQuery.isRefreshing} error={detailQuery.error} onRetry={() => void detailQuery.reload()}/>
                {!selectedInCatalog ? <div className="catalog-selection-notice"><span>当前详情不在目录筛选结果中：{selected.name}</span><Button className="icon-text" onClick={clearFilters} htmlType={"button"} type="text">清除筛选</Button></div> : null}
                <ReleaseDetail key={selected.id} selected={selected} contractRelease={contractRelease} mine={mine} canTest={canTest} busy={busy} showDefaultHint={!contractReleaseId && !selectedReleaseId && Boolean(selected.latestRelease && contractRelease && selected.latestRelease.id !== contractRelease.id)} selectContractRelease={selectContractRelease} onReleaseAction={handleReleaseAction} onEditComponent={() => setEditComponent(selected)} onNewVersion={() => setVersionBase(selected)} onNewBranch={() => setBlankVersionBase(selected)} deletingComponent={deletion.deleting} onDeleteComponent={detailQuery.error ? undefined : () => void deletion.deleteComponent(selected)} onEditContract={async (section, release) => {
                        if (release) {
                            if (!await selectContractRelease(release.id)) return;
                            setContractFocus(section);
                            setEditingContract(true);
                        }
                        else
                            startEditingContract(section);
                    }} contractEditor={showContractEditor && contractRelease ? (editorComponentsError ? <ErrorBlock message={editorComponentsError} onRetry={() => void reloadEditorComponents()}/> : editorComponentsLoading || !editorComponents ? <LoadingBlock label="正在读取可引用的组件合同…"/> : <ReleaseContractEditor key={`${contractRelease.id}:${contractFocus}`} onDirtyChange={setContractDirty} release={contractRelease} components={editorComponents} focusSection={contractFocus} onCancel={() => { setContractDirty(false); setEditingContract(false); setContractFocus(undefined); }} onSaved={() => { setContractDirty(false); setEditingContract(false); setContractFocus(undefined); signalRefresh('components'); }}/>) : undefined}/>
              </section> : <section className="panel"><EmptyState title="请选择组件"/></section>}
            </>}
          </ComponentCatalog>
        </>)}

      {createOpen && <CreateComponentModal onClose={() => setCreateOpen(false)} onDone={(component) => { setCreateOpen(false); setSearchParams({ selected: component.id }); setContractDraftIntent(undefined); setVersionBase(component); signalRefresh('components'); }}/>}
      {componentImportOpen && <ComponentTemplateImportModal onClose={() => setComponentImportOpen(false)} onDone={() => { setComponentImportOpen(false); signalRefresh('components'); }}/>}
      {editComponent && <EditComponentModal component={editComponent} onClose={() => setEditComponent(undefined)} onDone={() => { setEditComponent(undefined); signalRefresh('components'); }}/>}
      {versionBase && <NewVersionModal component={versionBase} baseRelease={contractRelease ?? versionBase.latestRelease} contractIntent={contractDraftIntent} onClose={() => { setVersionBase(undefined); setContractDraftIntent(undefined); setContractFocus(undefined); }} onDone={(release) => {
                const intent = contractDraftIntent;
                setVersionBase(undefined);
                setContractDraftIntent(undefined);
                signalRefresh('components');
                if (!release)
                    return;
                setContractReleaseId(release.id);
                setSearchParams({ selected: release.componentId, release: release.id });
                if (intent) {
                    setPendingContractRelease(release);
                    setContractFocus(intent === 'all' ? undefined : intent);
                    setEditingContract(true);
                }
                else {
                    setEditRelease(release);
                }
            }}/>}
      {blankVersionBase && <NewVersionModal component={blankVersionBase} blank contractIntent="all" onClose={() => setBlankVersionBase(undefined)} onDone={(release) => {
                setBlankVersionBase(undefined);
                signalRefresh('components');
                if (!release)
                    return;
                setPendingContractRelease(release);
                setContractReleaseId(release.id);
                setSearchParams({ selected: release.componentId, release: release.id });
                setContractFocus(undefined);
                setEditingContract(true);
            }}/>}
      {inspectRelease && <InspectReleaseModal release={inspectRelease} components={components ?? []} onClose={() => setInspectRelease(undefined)} onEdit={mine && inspectRelease.state === 'draft' ? () => { setInspectRelease(undefined); setContractReleaseId(inspectRelease.id); setEditingContract(true); } : undefined}/>}
      {reviewRelease && <Modal busy={Boolean(busy)} title={`${reviewRelease.version} 审批意见`} description="查看当前版本合同的平台 Owner 审核结果与完整备注。" onClose={() => setReviewReleaseId(undefined)} footer={<footer className="modal-actions"><Button className="button button--quiet" onClick={() => setReviewReleaseId(undefined)} htmlType={"button"} type="default">关闭</Button></footer>}>
        <div className="modal-body"><ReleaseReviewDetails release={reviewRelease}/></div>

      </Modal>}
      {visibleEditRelease && <EditReleaseModal key={visibleEditRelease.id} release={visibleEditRelease} releases={releases} onClose={() => setEditRelease(undefined)} onDone={() => { setEditRelease(undefined); signalRefresh('components'); }}/>}
      {testRelease && <TestReleaseModal release={testRelease} onClose={() => setTestRelease(undefined)} onDone={() => { setTestRelease(undefined); signalRefresh(['components', 'runs']); }}/>}
      {evidenceRelease && <ReleaseRunEvidenceModal release={evidenceRelease} onClose={() => setEvidenceRelease(undefined)}/>}
      {imageRelease && <ImageBuildModal release={imageRelease} onClose={() => setImageRelease(undefined)}/>}
      {artifactRelease && <ArtifactModal release={artifactRelease} onClose={() => setArtifactRelease(undefined)}/>}
      {lifecycle.dialogs}
    </div>);
}
