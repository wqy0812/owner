import type { ComponentDependency, ResourceClaim, ResourceContract } from '../types/domain';
import { FolderCheck, Plus, Trash2 } from 'lucide-react';
import { InfoNote } from './Primitives';

export function ResourceContractEditor({ value, dependencies = [], onChange }: { value?: ResourceContract; dependencies?: ComponentDependency[]; onChange: (value: ResourceContract | undefined) => void }) {
  const update = (index: number, patch: Partial<ResourceClaim>) => onChange({ ...value, version: 1, noManagedPaths: false, claims: (value?.claims ?? []).map((claim, i) => i === index ? { ...claim, ...patch } : claim) });
  const lines = (value: string) => value.split('\n').map(line => line.trim()).filter(Boolean);
  return <section className="resource-contract span-2" aria-label="资源管理范围"><header className="editor-section-heading"><span className="panel__icon"><FolderCheck size={18} aria-hidden="true" /></span><div><h4>资源管理范围</h4><p>声明此动作管理或检查的路径。目录共享必须明确子路径分工；仅增加执行顺序不能解除写入冲突。</p></div><span className="editor-count">{!value ? '待声明' : value.noManagedPaths ? '无受管路径' : `${value.claims.length} 项范围`}</span></header>
    <label><span>声明方式</span><select aria-label="资源声明方式" value={!value ? 'pending' : value.noManagedPaths ? 'none' : 'claims'} onChange={event => onChange(event.target.value === 'pending' ? undefined : { ...value, version: 1, noManagedPaths: event.target.value === 'none', claims: [] })}><option value="pending">尚未声明</option><option value="none">确认此动作没有受管路径</option><option value="claims">填写管理与检查范围</option></select></label>
    {!value && <p className="inline-warning">未声明的动作不能用于新的测试、发布或执行。</p>}
    {value?.noManagedPaths && <InfoNote>此动作已声明没有受管路径。运行条件与残留探测请录入前置检查 YAML。</InfoNote>}
    {value && !value.noManagedPaths && !value.claims.length && <div className="editor-empty-state">尚未添加资源范围，请填写动作管理或检查的绝对路径。</div>}
    {value && !value.noManagedPaths && <>{value.claims.map((claim, index) => <fieldset key={index} className="resource-claim"><legend>范围 {index + 1}</legend><div className="form-grid">
      <label><span>范围名称</span><input aria-label={`资源名称 ${index + 1}`} value={claim.id} onChange={event => update(index, { id: event.target.value })} /></label>
      <label><span>绝对路径</span><input aria-label={`资源路径 ${index + 1}`} placeholder="/opt/cni/bin" value={claim.path} onChange={event => update(index, { path: event.target.value })} /></label>
      <label><span>范围</span><select value={claim.scope} onChange={event => update(index, { scope: event.target.value as ResourceClaim['scope'], excludes: [], sharedPaths: [] })}><option value="file">单个文件</option><option value="tree">目录及子路径</option></select></label>
      <label><span>用途</span><select value={claim.access} onChange={event => update(index, { access: event.target.value as ResourceClaim['access'] })}><option value="manage">创建、修改或删除</option><option value="read">只读使用</option><option value="verify">验证状态</option></select></label>
      <label className="checkbox-field"><input type="checkbox" checked={claim.exclusive ?? false} onChange={event => update(index, { exclusive: event.target.checked })} /><span>要求整个范围保持独占状态</span></label>
      {claim.scope === 'tree' && <><label><span>不管理的子路径（每行一项）</span><textarea value={(claim.excludes ?? []).join('\n')} onChange={event => update(index, { excludes: lines(event.target.value) })} /></label><label><span>允许下游接管的子路径（每行一项）</span><textarea value={(claim.sharedPaths ?? []).join('\n')} onChange={event => update(index, { sharedPaths: lines(event.target.value) })} /></label></>}
      <label><span>共享来源版本</span><select value={claim.sharedWith?.releaseId ?? ''} onChange={event => update(index, { sharedWith: event.target.value ? { releaseId: event.target.value, claimId: '' } : undefined })}><option value="">自有范围</option>{dependencies.map(dependency => <option key={dependency.id ?? dependency.releaseId} value={dependency.releaseId}>{dependency.componentName || dependency.componentId} · {dependency.version || dependency.releaseId}</option>)}</select></label>
      {claim.sharedWith && <label><span>上游范围名称</span><input value={claim.sharedWith.claimId} onChange={event => update(index, { sharedWith: { ...claim.sharedWith!, claimId: event.target.value } })} /></label>}
    </div><div className="editor-row-actions"><button type="button" className="button button--quiet" onClick={() => onChange({ ...value, claims: value.claims.filter((_, i) => i !== index) })}><Trash2 size={14} aria-hidden="true" />移除此范围</button></div></fieldset>)}<button type="button" className="button button--secondary" onClick={() => onChange({ ...value, claims: [...value.claims, { id: `path_${value.claims.length + 1}`, path: '', scope: 'file', access: 'manage' }] })}><Plus size={14} aria-hidden="true" />添加资源范围</button></>}

  </section>;
}
