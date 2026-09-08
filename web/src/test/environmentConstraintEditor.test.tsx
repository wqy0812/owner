import { useState } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from '../api/client';
import { EnvironmentConstraintEditor } from '../components/EnvironmentConstraintEditor';
import { ScenarioCreateModal } from '../components/ScenarioCreateModal';
import { NewVersionModal } from '../features/components/releases/ReleaseDialogs';
import type { Component } from '../types/domain';
import type { ConstraintSelection } from '../types/environmentConstraints';
import { fireEvent, render, screen, waitFor, within } from './render';

const catalog = vi.hoisted(() => ({ failed: false, retry: vi.fn() }));
vi.mock('../context/AppContext', () => ({
  useApp: () => ({ notify: vi.fn(), signalRefresh: catalog.retry, platformOptionsLoading: false, platformOptionsError: catalog.failed ? '目录不可用' : undefined, platformOptionCategories: catalog.failed ? [] : [
    { id: 'arch', key: 'architecture', label: '架构', kind: 'environment_dimension', options: [{ id: 'amd64', value: 'amd64', label: 'x86/amd64' }] },
    { id: 'os', key: 'operatingSystem', label: '操作系统', kind: 'environment_dimension', options: [{ id: 'ubuntu', value: 'Ubuntu', label: 'Ubuntu' }] },
    { id: 'os-version', key: 'operatingSystemVersion', label: '操作系统版本', kind: 'environment_dimension', parentCategoryId: 'os', options: [{ id: 'ubuntu-24', parentOptionId: 'ubuntu', value: 'Ubuntu@24.04', label: '24.04' }] },
  ] }),
  displayError: String,
}));
afterEach(() => { vi.restoreAllMocks(); catalog.failed = false; catalog.retry.mockReset(); });

function Editor() {
  const [value, setValue] = useState<ConstraintSelection>({});
  return <EnvironmentConstraintEditor value={value} onChange={setValue} />;
}

describe('explicit unrestricted scope selection', () => {
  it.each(['scenario', 'component'] as const)('blocks %s creation after the initial directory request fails', (kind) => {
    catalog.failed = true;
    const createScenario = vi.spyOn(api, 'createScenario');
    const createBranch = vi.spyOn(api, 'previewReleaseDraft');
    const component: Component = { id: 'component', name: '组件', slug: 'component', ownerId: 'owner', layer: 'runtime_state', tags: [], releases: [] };
    render(kind === 'scenario'
      ? <ScenarioCreateModal scenarios={[]} onClose={vi.fn()} onDone={vi.fn()} />
      : <NewVersionModal component={component} blank onClose={vi.fn()} onDone={vi.fn()} />);
    expect(screen.getByRole('checkbox', { name: /确认以上适配范围/ })).toBeDisabled();
    const submit = screen.getByRole('button', { name: kind === 'scenario' ? '创建场景' : '创建分支' });
    expect(submit).toBeDisabled();
    fireEvent.submit(submit.closest('form')!);
    expect(createScenario).not.toHaveBeenCalled();
    expect(createBranch).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: '重新加载环境维度' }));
    expect(catalog.retry).toHaveBeenCalledWith('platform-options');
  });

  it('keeps unrestricted and specific tags mutually exclusive and clearing a tag undecided', () => {
    render(<Editor />);
    const arch = within(screen.getByRole('group', { name: '架构' }));
    const unrestricted = arch.getByRole('checkbox', { name: '不限制' });
    const specific = arch.getByRole('checkbox', { name: 'x86/amd64' });
    expect(unrestricted).not.toBeChecked();
    fireEvent.click(unrestricted);
    expect(unrestricted).toBeChecked();
    fireEvent.click(specific);
    expect(unrestricted).not.toBeChecked();
    expect(specific).toBeChecked();
    fireEvent.click(specific);
    expect(arch.getByText('未选择')).toBeInTheDocument();
    expect(unrestricted).not.toBeChecked();
  });

  it('requires an explicit version choice after choosing or clearing an operating system', () => {
    render(<Editor />);
    const version = within(screen.getByRole('group', { name: '操作系统版本' }));
    const unrestricted = version.getByRole('checkbox', { name: '不限制' });
    fireEvent.click(unrestricted);
    fireEvent.click(screen.getByRole('checkbox', { name: 'Ubuntu' }));
    expect(unrestricted).not.toBeChecked();
    expect(unrestricted).toBeDisabled();
    fireEvent.click(version.getByRole('checkbox', { name: '24.04' }));
    fireEvent.click(within(screen.getByRole('group', { name: '操作系统' })).getByRole('checkbox', { name: '不限制' }));
    expect(unrestricted).toBeEnabled();
    expect(unrestricted).not.toBeChecked();
    expect(version.getByText('未选择')).toBeInTheDocument();
  });

  it('blocks a blank scenario until all dimensions are chosen and submits explicit unrestricted scope', async () => {
    const create = vi.spyOn(api, 'createScenario').mockResolvedValue({ id: 'new', name: '新场景', slug: 'new-scenario', ownerId: 'owner' });
    render(<ScenarioCreateModal scenarios={[]} onClose={vi.fn()} onDone={vi.fn()} />);
    const confirm = screen.getByRole('checkbox', { name: /确认以上适配范围/ });
    const submit = screen.getByRole('button', { name: '创建场景' });
    expect(confirm).toBeDisabled();
    expect(submit).toBeDisabled();
    fireEvent.submit(submit.closest('form')!);
    expect(create).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('checkbox', { name: 'x86/amd64' }));
    expect(confirm).toBeDisabled();
    fireEvent.click(screen.getByRole('checkbox', { name: 'x86/amd64' }));
    expect(within(screen.getByRole('group', { name: '架构' })).getByText('未选择')).toBeInTheDocument();
    for (const option of screen.getAllByRole('checkbox', { name: '不限制' })) fireEvent.click(option);
    expect(confirm).toBeEnabled();
    fireEvent.click(confirm);
    fireEvent.click(within(screen.getByRole('group', { name: '架构' })).getByRole('checkbox', { name: '不限制' }));
    expect(confirm).not.toBeChecked();
    expect(submit).toBeDisabled();
    fireEvent.click(within(screen.getByRole('group', { name: '架构' })).getByRole('checkbox', { name: '不限制' }));
    fireEvent.click(confirm);
    fireEvent.change(screen.getByRole('textbox', { name: '场景名称' }), { target: { value: '新场景' } });
    fireEvent.change(screen.getByRole('textbox', { name: '标识' }), { target: { value: 'new-scenario' } });
    fireEvent.click(submit);
    await waitFor(() => expect(create).toHaveBeenCalledWith({ name: '新场景', slug: 'new-scenario', description: '', environmentConstraints: {} }));
  });
});
