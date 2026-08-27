import { useEffect, useRef, useState } from 'react';
import { BookmarkPlus, Trash2 } from 'lucide-react';
import { api } from '../api/client';
import { displayError, useApp } from '../context/AppContext';
import type { RunInputPreset } from '../types/domain';

export function RunInputPresetPicker({ resourceType, resourceId, context, values, onApply }: {
  resourceType: RunInputPreset['resourceType'];
  resourceId: string;
  context: RunInputPreset['context'];
  values: RunInputPreset['values'];
  onApply: (values: RunInputPreset['values']) => void;
}) {
  const { notify } = useApp();
  const [presets, setPresets] = useState<RunInputPreset[]>([]);
  const [selected, setSelected] = useState('');
  const [busy, setBusy] = useState(false);
  const generation = useRef(0);

  async function reload(expectedGeneration: number, signal?: AbortSignal) {
    try {
      const loaded = await api.runInputPresets(resourceType, resourceId, context, signal);
      if (generation.current === expectedGeneration) setPresets(loaded);
    } catch (reason) {
      if (generation.current === expectedGeneration && !(reason instanceof DOMException && reason.name === 'AbortError')) {
        notify('error', '参数预设加载失败', displayError(reason));
      }
    }
  }

  useEffect(() => {
    const expectedGeneration = generation.current + 1;
    generation.current = expectedGeneration;
    const controller = new AbortController();
    setPresets([]);
    setSelected('');
    setBusy(false);
    void reload(expectedGeneration, controller.signal);
    return () => controller.abort();
  }, [context, resourceId, resourceType]);

  async function save() {
    const name = window.prompt('预设名称');
    if (!name?.trim()) return;
    const expectedGeneration = generation.current;
    setBusy(true);
    try {
      await api.saveRunInputPreset({ resourceType, resourceId, context, name: name.trim(), values });
      if (generation.current !== expectedGeneration) return;
      notify('success', '运行参数预设已保存', '预设仅属于当前用户和当前资源，不包含环境或凭据。');
      await reload(expectedGeneration);
    } catch (reason) {
      if (generation.current === expectedGeneration) notify('error', '预设保存失败', displayError(reason));
    } finally {
      if (generation.current === expectedGeneration) setBusy(false);
    }
  }

  async function remove() {
    if (!selected) return;
    const expectedGeneration = generation.current;
    setBusy(true);
    try {
      await api.deleteRunInputPreset(selected);
      if (generation.current !== expectedGeneration) return;
      setSelected('');
      await reload(expectedGeneration);
    } catch (reason) {
      if (generation.current === expectedGeneration) notify('error', '预设删除失败', displayError(reason));
    } finally {
      if (generation.current === expectedGeneration) setBusy(false);
    }
  }

  return <div className="preset-picker">
    <label><span>个人参数预设</span><select value={selected} disabled={busy} onChange={(event) => {
      const id = event.target.value; setSelected(id);
      const preset = presets.find((item) => item.id === id);
      if (preset && !preset.stale) onApply(preset.values);
    }}><option value="">不使用预设</option>{presets.map((preset) => <option key={preset.id} value={preset.id} disabled={preset.stale}>{preset.name}{preset.stale ? ' · 已过期' : ''}</option>)}</select></label>
    <button type="button" className="button button--quiet" disabled={busy} onClick={() => void save()}><BookmarkPlus size={14} /> 保存当前参数</button>
    {selected && <button type="button" className="icon-button icon-button--danger" aria-label="删除当前参数预设" disabled={busy} onClick={() => void remove()}><Trash2 size={14} /></button>}
  </div>;
}
