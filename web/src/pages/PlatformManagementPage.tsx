import { ArchiveRestore, Ban, Plus, Trash2 } from 'lucide-react';
import { useRef, useState, type FormEvent, type KeyboardEvent, type ReactNode } from 'react';
import { api } from '../api/client';
import { useApp, displayError } from '../context/AppContext';
import { useApiData } from '../hooks/useApiData';
import type { PlatformOption, PlatformOptionCategory, PlatformOptionUsage } from '../types/domain';
import { EmptyState, LoadingBlock, PageHeader } from '../components/Primitives';

function usageTotal(usage: PlatformOptionUsage) {
  return usage.componentReleases + usage.scenarioRevisions + usage.environmentRevisions;
}

function usageText(usage: PlatformOptionUsage) {
  const parts = [
    usage.componentReleases ? `组件 ${usage.componentReleases}` : '',
    usage.scenarioRevisions ? `场景 ${usage.scenarioRevisions}` : '',
    usage.environmentRevisions ? `环境 ${usage.environmentRevisions}` : '',
  ].filter(Boolean);
  return parts.length ? parts.join(' · ') : '无引用';
}

type RenameTarget = {
  kind: 'category' | 'option';
  id: string;
  originalLabel: string;
  label: string;
};

export function PlatformManagementPage() {
  const { user, platformOptionCategories, platformOptionsLoading, notify, scheduleRefresh } = useApp();
  const [categoryForm, setCategoryForm] = useState({ label: '', parentCategoryId: '', environmentRequired: false });
  const [optionLabels, setOptionLabels] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState('');
  const [renameTarget, setRenameTarget] = useState<RenameTarget | null>(null);
  const renameSubmitting = useRef(false);
  const [variableForm, setVariableForm] = useState({ name: '', label: '', description: '' });
  const { data: variableDefinitions, reload: reloadVariableDefinitions } = useApiData((signal) => api.environmentVariableDefinitions(signal), [user.id], 'environment-variable-definitions');
  const admin = user.role === 'platform_admin';

  const categoryById = new Map(platformOptionCategories.map((category) => [category.id, category]));
  const visibleCategories = platformOptionCategories.filter((category) => !category.parentCategoryId || !categoryById.has(category.parentCategoryId));

  function childCategories(categoryId: string) {
    return platformOptionCategories.filter((category) => category.parentCategoryId === categoryId);
  }

  async function createCategory(event: FormEvent) {
    event.preventDefault();
    if (!categoryForm.label.trim()) return;
    setBusy('category:new');
    try {
      await api.createPlatformOptionCategory({ label: categoryForm.label.trim(), parentCategoryId: categoryForm.parentCategoryId || undefined, environmentRequired: categoryForm.environmentRequired });
      setCategoryForm({ label: '', parentCategoryId: '', environmentRequired: false });
      scheduleRefresh('platform-options');
      notify('success', '类别已新增');
    } catch (error) { notify('error', '新增类别失败', displayError(error)); } finally { setBusy(''); }
  }

  async function createOption(categoryId: string, draftKey: string, parentOptionId?: string) {
    const label = optionLabels[draftKey]?.trim();
    if (!label) return;
    setBusy(`option:new:${draftKey}`);
    try {
      await api.createPlatformOption(categoryId, label, parentOptionId);
      setOptionLabels((current) => ({ ...current, [draftKey]: '' }));
      scheduleRefresh('platform-options');
      notify('success', '选项已新增');
    } catch (error) { notify('error', '新增选项失败', displayError(error)); } finally { setBusy(''); }
  }

  async function setRetired(kind: 'category' | 'option', id: string, retired: boolean) {
    setBusy(`retire:${kind}:${id}`);
    try {
      if (kind === 'category') await api.setPlatformOptionCategoryRetired(id, retired);
      else await api.setPlatformOptionRetired(id, retired);
      scheduleRefresh('platform-options');
      notify('success', retired ? '已退役；历史引用继续保留' : '已恢复');
    } catch (error) { notify('error', retired ? '退役失败' : '恢复失败', displayError(error)); } finally { setBusy(''); }
  }

  async function deleteCategory(id: string, label: string) {
    if (!window.confirm(`确认删除类别“${label}”及其全部未引用选项？`)) return;
    setBusy(`category:${id}`);
    try {
      await api.deletePlatformOptionCategory(id);
      scheduleRefresh('platform-options');
      notify('success', '类别已删除');
    } catch (error) { notify('error', '删除类别失败', displayError(error)); } finally { setBusy(''); }
  }

  async function deleteOption(id: string, label: string) {
    if (!window.confirm(`确认删除选项“${label}”？`)) return;
    setBusy(`option:${id}`);
    try {
      await api.deletePlatformOption(id);
      scheduleRefresh('platform-options');
      notify('success', '选项已删除');
    } catch (error) { notify('error', '删除选项失败', displayError(error)); } finally { setBusy(''); }
  }

  function startRename(kind: RenameTarget['kind'], id: string, label: string) {
    if (busy) return;
    setRenameTarget({ kind, id, originalLabel: label, label });
  }

  async function commitRename(target: RenameTarget) {
    if (renameSubmitting.current) return;
    const label = target.label.trim();
    if (!label) {
      notify('error', '更名失败', '显示名不能为空。');
      return;
    }
    if (label === target.originalLabel) {
      setRenameTarget(null);
      return;
    }
    renameSubmitting.current = true;
    setBusy(`rename:${target.kind}:${target.id}`);
    try {
      if (target.kind === 'category') await api.renamePlatformOptionCategory(target.id, label);
      else await api.renamePlatformOption(target.id, label);
      setRenameTarget(null);
      scheduleRefresh('platform-options');
      notify('success', target.kind === 'category' ? '类别已更名' : '选项已更名');
    } catch (error) {
      notify('error', '更名失败', displayError(error));
    } finally {
      renameSubmitting.current = false;
      setBusy('');
    }
  }

  function renameKeyDown(event: KeyboardEvent<HTMLInputElement>, target: RenameTarget) {
    if (event.key === 'Enter') {
      event.preventDefault();
      void commitRename(target);
    } else if (event.key === 'Escape') {
      event.preventDefault();
      setRenameTarget(null);
    }
  }

  function renameInput(target: RenameTarget) {
    const subject = target.kind === 'category' ? '类别' : '选项';
    return <input
      className={`inline-rename-input${target.kind === 'category' ? ' inline-rename-input--heading' : ''}`}
      aria-label={`更名${subject} ${target.originalLabel}`}
      value={target.label}
      maxLength={64}
      autoFocus
      onFocus={(event) => event.currentTarget.select()}
      onChange={(event) => setRenameTarget({ ...target, label: event.target.value })}
      onKeyDown={(event) => renameKeyDown(event, target)}
      onBlur={() => void commitRename(target)}
    />;
  }

  function categoryLabel(category: PlatformOptionCategory, heading = false): ReactNode {
    if (renameTarget?.kind === 'category' && renameTarget.id === category.id) return renameInput(renameTarget);
    const content = <>{category.label}{category.retiredAt ? '（已退役）' : ''}</>;
    const props = {
      className: 'renamable-label', title: '双击更名', tabIndex: 0,
      onDoubleClick: () => startRename('category' as const, category.id, category.label),
      onKeyDown: (event: KeyboardEvent<HTMLElement>) => { if (event.key === 'Enter') startRename('category', category.id, category.label); },
    };
    return heading ? <h2 {...props}>{content}</h2> : <strong {...props}>{content}</strong>;
  }

  function optionLabel(option: PlatformOption) {
    if (renameTarget?.kind === 'option' && renameTarget.id === option.id) return renameInput(renameTarget);
    return <strong className="renamable-label" title="双击更名" tabIndex={0} onDoubleClick={() => startRename('option', option.id, option.label)} onKeyDown={(event) => { if (event.key === 'Enter') startRename('option', option.id, option.label); }}>
      {option.label}{option.retiredAt ? '（已退役）' : ''}
    </strong>;
  }

  function requiredPill(category: PlatformOptionCategory, qualified = false) {
    const label = category.environmentRequired ? '环境必填' : '环境可选';
    return <span className={`category-status${category.environmentRequired ? ' category-status--required' : ''}`}>{qualified ? `${category.label}：${label}` : label}</span>;
  }

  function categoryActions(category: PlatformOptionCategory, hasChildren = false) {
    const protectedCategory = category.kind === 'host_group';
    const categoryUsed = usageTotal(category.usage) > 0;
    const deleteTitle = protectedCategory ? '主机组类别受系统保护' : categoryUsed ? usageText(category.usage) : hasChildren ? '存在从属类别，不能删除' : '彻底删除类别';
    return <div className="form-actions category-actions">
      {!protectedCategory ? <button type="button" className="button button--quiet button--small" disabled={Boolean(busy)} onClick={() => void setRetired('category', category.id, !category.retiredAt)}>
        {category.retiredAt ? <ArchiveRestore size={14} /> : <Ban size={14} />}{category.retiredAt ? '恢复' : '退役'}
      </button> : null}
      <button type="button" className="button button--danger button--small" disabled={protectedCategory || categoryUsed || hasChildren || Boolean(busy)} title={deleteTitle} onClick={() => void deleteCategory(category.id, category.label)}>
        <Trash2 size={14} /> 彻底删除
      </button>
    </div>;
  }

  function optionActions(category: PlatformOptionCategory, option: PlatformOption, descendants: PlatformOption[] = []) {
    const protectedCategory = category.kind === 'host_group';
    const retireBlocked = !option.retiredAt && descendants.some((child) => !child.retiredAt);
    const deleteBlocked = descendants.length > 0;
    const deleteTitle = protectedCategory ? '主机组选项受系统保护' : usageTotal(option.usage) ? usageText(option.usage) : deleteBlocked ? '仍有关联子选项，不能删除' : '彻底删除选项';
    return <div className="form-actions option-actions">
      {!protectedCategory ? <button type="button" className="button button--quiet button--small" disabled={Boolean(busy) || retireBlocked} title={retireBlocked ? '请先退役关联子选项' : option.retiredAt ? '恢复选项' : '退役选项'} onClick={() => void setRetired('option', option.id, !option.retiredAt)}>{option.retiredAt ? '恢复' : '退役'}</button> : null}
      <button type="button" className="button button--quiet button--small" disabled={protectedCategory || usageTotal(option.usage) > 0 || deleteBlocked || Boolean(busy)} title={deleteTitle} onClick={() => void deleteOption(option.id, option.label)}><Trash2 size={14} /> 彻底删除</button>
    </div>;
  }

  function optionForm(category: PlatformOptionCategory, draftKey: string, options: { parent?: PlatformOption; hierarchicalRoot?: boolean } = {}) {
    const { parent, hierarchicalRoot } = options;
    const subject = parent ? `${parent.label}新增${category.label}` : `${category.label}新增选项`;
    const buttonLabel = parent ? `新增${category.label}` : hierarchicalRoot ? `新增${category.label}` : '新增选项';
    return <div className={parent ? 'hierarchy-inline-form' : 'inline-form'}>
      <input aria-label={`为${subject}`} value={optionLabels[draftKey] ?? ''} onChange={(event) => setOptionLabels((current) => ({ ...current, [draftKey]: event.target.value }))} placeholder={parent ? `输入${parent.label}的${category.label}` : `输入${category.label}显示名`} />
      <button type="button" className="button button--secondary" disabled={Boolean(busy)} onClick={() => void createOption(category.id, draftKey, parent?.id)}><Plus size={15} /> {buttonLabel}</button>
    </div>;
  }

  function flatCategoryCard(category: PlatformOptionCategory) {
    const orphaned = Boolean(category.parentCategoryId && !categoryById.has(category.parentCategoryId));
    const protectedCategory = category.kind === 'host_group';
    return <section className={`panel category-card${orphaned ? ' category-card--orphaned' : ''}`} key={category.id}>
      <div className="section-heading">
        <div>{categoryLabel(category, true)}<div className="category-status-row">{requiredPill(category)}{protectedCategory ? <span className="category-status">系统保护 · 主机组</span> : null}</div></div>
        {categoryActions(category)}
      </div>
      {orphaned ? <div className="inline-warning">关联的父类别不存在；当前类别仅供查看，请先修复目录关系。</div> : null}
      <div className="table-wrap"><table><thead><tr><th>显示名</th><th>引用</th><th /></tr></thead><tbody>
        {category.options.map((option) => <tr key={option.id}><td>{optionLabel(option)}</td><td>{usageText(option.usage)}</td><td>{optionActions(category, option)}</td></tr>)}
        {!category.options.length && <tr><td colSpan={3}>暂无选项</td></tr>}
      </tbody></table></div>
      {!category.retiredAt && !orphaned ? optionForm(category, category.id) : null}
    </section>;
  }

  function hierarchicalCategoryCard(root: PlatformOptionCategory, children: PlatformOptionCategory[]) {
    const rootOptionIds = new Set(root.options.map((option) => option.id));
    const orphanOptions = children.flatMap((child) => child.options.filter((option) => !option.parentOptionId || !rootOptionIds.has(option.parentOptionId)).map((option) => ({ child, option })));
    return <section className="panel category-card category-card--hierarchical" key={root.id}>
      <div className="section-heading hierarchy-heading">
        <div>
          <div className="category-path">{categoryLabel(root, true)}{children.map((child) => <span className="category-path__child" key={child.id}><span aria-hidden="true">→</span>{categoryLabel(child)}</span>)}</div>
          <div className="category-status-row">{requiredPill(root)}{children.map((child) => <span key={child.id}>{requiredPill(child, true)}</span>)}</div>
        </div>
        {categoryActions(root, true)}
      </div>
      <div className="hierarchy-child-settings" aria-label="从属类别管理">
        {children.map((child) => <div key={child.id}><span><strong>{child.label}</strong><small>从属 {root.label}</small></span>{categoryActions(child)}</div>)}
      </div>
      <div className="hierarchy-option-list">
        {root.options.map((parent) => {
          const descendants = children.flatMap((child) => child.options.filter((option) => option.parentOptionId === parent.id));
          return <article className={`hierarchy-parent${parent.retiredAt ? ' hierarchy-parent--retired' : ''}`} key={parent.id}>
            <header className="hierarchy-parent__header"><div>{optionLabel(parent)}<small>{usageText(parent.usage)}</small></div><span>{descendants.length} 个关联选项</span>{optionActions(root, parent, descendants)}</header>
            <div className="hierarchy-children">
              {children.map((child) => {
                const linked = child.options.filter((option) => option.parentOptionId === parent.id);
                const canAdd = !root.retiredAt && !parent.retiredAt && !child.retiredAt;
                const draftKey = `${child.id}:${parent.id}`;
                return <section className="hierarchy-child-section" key={child.id}>
                  <header><strong>{child.label}</strong><span>{linked.length ? `${linked.length} 项` : '暂无'}</span></header>
                  <div className="hierarchy-child-rows">
                    {linked.map((option) => <div className="hierarchy-child-row" key={option.id}><div>{optionLabel(option)}<small>{usageText(option.usage)}</small></div>{optionActions(child, option)}</div>)}
                    {!linked.length ? <div className="hierarchy-empty">尚未录入{parent.label}的{child.label}</div> : null}
                  </div>
                  {canAdd ? optionForm(child, draftKey, { parent }) : null}
                </section>;
              })}
            </div>
          </article>;
        })}
        {!root.options.length ? <div className="hierarchy-empty hierarchy-empty--root">尚未录入{root.label}；请先新增父选项。</div> : null}
      </div>
      {orphanOptions.length ? <section className="hierarchy-orphans"><header><strong>未关联选项</strong><span>父选项不存在，仅保留历史查看</span></header>{orphanOptions.map(({ child, option }) => <div key={option.id}><span><small>{child.label}</small>{optionLabel(option)}</span>{optionActions(child, option)}</div>)}</section> : null}
      {!root.retiredAt ? optionForm(root, root.id, { hierarchicalRoot: true }) : null}
    </section>;
  }

  async function createVariableDefinition(event: FormEvent) {
    event.preventDefault(); setBusy('variable:new');
    try {
      await api.createEnvironmentVariableDefinition(variableForm);
      setVariableForm({ name: '', label: '', description: '' });
      await reloadVariableDefinitions(); notify('success', '环境变量字段已新增');
    } catch (error) { notify('error', '新增环境变量字段失败', displayError(error)); } finally { setBusy(''); }
  }

  return <div className="page-stack">
    <PageHeader eyebrow="Platform directory" title="平台管理" description="集中维护环境适配维度与主机组。技术键和值由平台生成，已被任何历史业务快照引用的数据不能删除。" />
    {!admin ? <EmptyState title="仅平台 Owner 可管理目录" description="其他 Owner 可以在各自表单中选择目录数据，但不能新增或删除。" /> : <>
      <div className="two-column">
        <section className="panel"><div className="section-heading"><div><h2>环境变量字段</h2><p>环境 Owner 只能从此目录选择键。</p></div></div><form className="form-grid" onSubmit={(event) => void createVariableDefinition(event)}><label><span>变量名</span><input required pattern="[A-Z_][A-Z0-9_]*" value={variableForm.name} onChange={(event) => setVariableForm((current) => ({ ...current, name: event.target.value.toUpperCase() }))} /></label><label><span>显示名</span><input required value={variableForm.label} onChange={(event) => setVariableForm((current) => ({ ...current, label: event.target.value }))} /></label><label className="span-2"><span>说明</span><input value={variableForm.description} onChange={(event) => setVariableForm((current) => ({ ...current, description: event.target.value }))} /></label><button className="button button--primary" disabled={Boolean(busy)}><Plus size={15} /> 新增变量字段</button></form><div className="managed-definition-list">{variableDefinitions?.map((item) => <div key={item.id}><div><strong>{item.label}</strong><small>{item.name} · 引用 {item.usage}</small></div><button className="icon-text" disabled={item.usage > 0 || Boolean(busy)} onClick={() => void api.deleteEnvironmentVariableDefinition(item.id).then(() => reloadVariableDefinitions()).catch((error) => notify('error', '删除失败', displayError(error)))}><Trash2 size={14} /> 删除</button></div>)}</div></section>
      </div>
      <form className="panel category-create-form" onSubmit={(event) => void createCategory(event)}>
        <label><span>新增环境维度类别</span><input value={categoryForm.label} onChange={(event) => setCategoryForm((current) => ({ ...current, label: event.target.value }))} placeholder="例如 CPU 厂商" required /></label>
        <label><span>父类别（可选）</span><select value={categoryForm.parentCategoryId} onChange={(event) => setCategoryForm((current) => ({ ...current, parentCategoryId: event.target.value }))}><option value="">无（根类别）</option>{platformOptionCategories.filter((item) => item.kind === 'environment_dimension' && !item.parentCategoryId && !item.retiredAt).map((item) => <option key={item.id} value={item.id}>{item.label}</option>)}</select></label>
        <div className="category-create-actions"><label className="required-toggle"><input type="checkbox" checked={categoryForm.environmentRequired} onChange={(event) => setCategoryForm((current) => ({ ...current, environmentRequired: event.target.checked }))} /><span className="required-toggle__track" aria-hidden="true"><span /></span><span>环境录入时必填</span></label><button className="button button--primary" disabled={busy === 'category:new'}><Plus size={16} /> 新增类别</button></div>
      </form>
      {platformOptionsLoading ? <LoadingBlock label="正在加载平台目录…" /> : <div className="card-grid">
        {visibleCategories.map((category) => {
          const children = childCategories(category.id);
          return children.length ? hierarchicalCategoryCard(category, children) : flatCategoryCard(category);
        })}
      </div>}
    </>}
  </div>;
}
