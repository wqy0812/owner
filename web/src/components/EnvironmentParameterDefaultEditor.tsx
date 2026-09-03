import { useState, type FormEvent } from 'react';
import { api } from '../api/client';
import { displayError, useApp } from '../context/AppContext';
import type { EnvironmentParameterDefinition, ParameterType } from '../types/domain';
import { Modal } from './Primitives';
import { ParameterValueEditor } from './ParameterEditors';

export function missingEnvironmentValue(value: unknown): boolean {
  return value === undefined || value === null || (typeof value === 'string' && !value.trim());
}

export function OptionalParameterDefaultInput({ type, choices, enabled, value, disabled, onEnabledChange, onChange }: {
  type: ParameterType; choices?: unknown[]; enabled: boolean; value: unknown; disabled?: boolean;
  onEnabledChange: (enabled: boolean) => void; onChange: (value: unknown) => void;
}) {
  return <div className="span-2 environment-default-input">
    <label className="checkbox-field"><input type="checkbox" checked={enabled} disabled={disabled} onChange={(event) => onEnabledChange(event.target.checked)} /><span>设置平台默认值</span></label>
    {enabled ? <label><span>默认值</span><ParameterValueEditor parameter={{ name: '平台默认值', type, enum: choices }} value={value} disabled={disabled} onChange={onChange} /><small>环境 Owner 可以修改；保存环境时记录实际值。</small></label> : <small>不设置默认值，环境 Owner 使用此字段时必须填写。</small>}
  </div>;
}

export function EnvironmentParameterDefaultEditor({ definition, onSaved }: { definition: EnvironmentParameterDefinition; onSaved: () => Promise<unknown> }) {
  const { notify } = useApp();
  const [open, setOpen] = useState(false);
  const [enabled, setEnabled] = useState(false);
  const [value, setValue] = useState<unknown>();
  const [busy, setBusy] = useState(false);
  async function save(event: FormEvent) {
    event.preventDefault();
    if (enabled && missingEnvironmentValue(value)) { notify('error', '请填写默认值或取消设置'); return; }
    setBusy(true);
    try {
      await api.updateEnvironmentParameterDefault(definition.id, enabled ? value : null);
      await onSaved(); setOpen(false); notify('success', enabled ? '平台默认值已保存' : '平台默认值已取消');
    } catch (error) { notify('error', '默认值保存失败', displayError(error)); } finally { setBusy(false); }
  }
  return <>
    <button type="button" className="icon-text" aria-label={`编辑${definition.label}默认值`} onClick={() => { setValue(definition.defaultValue); setEnabled(definition.defaultValue !== undefined); setOpen(true); }}>编辑默认值</button>
    {open && <Modal title={`${definition.label}默认值`} description="只影响之后采用默认值的环境保存；已有环境配置和历史运行保持原值。" onClose={() => !busy && setOpen(false)}>
      <form onSubmit={(event) => void save(event)}>
        <div className="modal-body"><OptionalParameterDefaultInput type={definition.type} choices={definition.enum} enabled={enabled} value={value} disabled={busy} onEnabledChange={setEnabled} onChange={setValue} /></div>
        <footer className="modal-actions"><button type="button" className="button button--quiet" disabled={busy} onClick={() => setOpen(false)}>取消</button><button className="button button--primary" disabled={busy || (enabled && missingEnvironmentValue(value))}>保存默认值</button></footer>
      </form>
    </Modal>}
  </>;
}
