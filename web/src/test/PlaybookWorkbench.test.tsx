import { beforeEach, expect, it, vi } from 'vitest';
import { api } from '../api/client';
import { EditReleaseModal } from '../features/components/releases/ReleaseDialogs';
import type { ComponentRelease, PlaybookFile, PlaybookWorkspaceFile } from '../types/domain';
import { answerConfirm } from './antdInteractions';
import { act, fireEvent, render, screen, waitFor, within } from './render';
import { deferred } from './testLifecycle';

const { notify, signalRefresh } = vi.hoisted(() => ({ notify: vi.fn(), signalRefresh: vi.fn() }));
vi.mock('../context/AppContext', () => ({
  useApp: () => ({ notify, signalRefresh, platformOptionCategories: [] }),
  displayError: (reason: Error) => reason.message,
}));

const release: ComponentRelease = {
  id: 'release', componentId: 'component', lineId: 'line', lineName: 'Kubernetes', version: '1.17.5', state: 'draft',
  compatibility: 'not_applicable', riskLevel: 'low', releaseNotes: 'PKI lifecycle', definitionGeneration: 3,
  review: { status: 'not_submitted' }, readiness: { status: 'blocked', blockers: [] }, parameters: [],
  actions: [
    { id: 'install', name: '安装与配置', type: 'install', playbook: 'managed/test/tasks/install.yml', preCheckActionId: 'ready', postCheckActionId: 'ready' },
    { id: 'ready', name: '安装前检查', type: 'check', playbook: 'managed/test/tasks/checks/ready.yml' },
  ],
};
const helper: PlaybookWorkspaceFile = { releaseId: release.id, path: 'templates/ca.j2', sha256: 'helper-sha', sizeBytes: 7, mediaType: 'text/plain', editable: true, content: 'ca=true' };
const source = (id: string): PlaybookFile => ({
  path: release.actions!.find(action => action.id === id)!.playbook,
  filename: `${id}.yml`, content: `---\n- name: ${id}\n  debug: { msg: ${id} }\n`, sha256: `sha-${id}`,
});

beforeEach(() => {
  vi.restoreAllMocks();
  notify.mockClear();
  vi.spyOn(api, 'playbook').mockImplementation(async (_, id) => source(id));
  vi.spyOn(api, 'playbookWorkspace').mockResolvedValue({ root: 'managed/test/', treeSha256: 'tree-sha', files: [
    ...release.actions!.map(action => ({ releaseId: release.id, path: action.playbook.replace('managed/test/', ''), sha256: `sha-${action.id}`, sizeBytes: 50, mediaType: 'application/yaml' })), helper,
  ] });
  vi.spyOn(api, 'workspaceFile').mockResolvedValue(helper);
  vi.spyOn(api, 'component').mockResolvedValue({ id: 'component', name: 'PKI', slug: 'pki', ownerId: 'owner', layer: 'runtime_state', tags: [], releases: [release] });
  vi.spyOn(api, 'savePlaybook').mockImplementation(async (_, action, content) => ({ ...source(action.id!), content, sha256: 'saved-sha', action }));
});

function openEditor() {
  const close = vi.fn();
  render(<EditReleaseModal release={release} releases={[release]} onClose={close} onDone={vi.fn()} />);
  return close;
}

it('loads the real entry automatically and routes a workspace entry to its action before saving', async () => {
  openEditor();
  expect(await screen.findByDisplayValue(/name: install/)).toBeInTheDocument();
  expect(screen.queryByPlaceholderText(/Check required input/)).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '工作区文件' }));
  fireEvent.click(await screen.findByRole('button', { name: 'tasks/checks/ready.yml' }));
  const editor = await screen.findByDisplayValue(/name: ready/);
  expect(screen.getByRole('region', { name: '当前动作入口' })).toHaveTextContent('tasks/checks/ready.yml');
  expect(screen.getByRole('button', { name: '安装前检查' })).toHaveAttribute('aria-current', 'true');
  expect(screen.queryByLabelText('辅助文件在线编辑器')).not.toBeInTheDocument();
  fireEvent.change(editor, { target: { value: '---\n- assert: { that: true }' } });
  fireEvent.click(screen.getByRole('button', { name: '保存 Playbook' }));
  await waitFor(() => expect(api.savePlaybook).toHaveBeenCalledWith('release', expect.objectContaining({ id: 'ready' }), '---\n- assert: { that: true }', 'sha-ready', 'tree-sha'));
  await waitFor(() => expect(screen.getByRole('button', { name: '保存 Draft' })).toBeEnabled());
});

it('preserves both editors across view changes and guards closing while a helper is unsaved', async () => {
  const close = openEditor();
  const actionEditor = await screen.findByDisplayValue(/name: install/);
  fireEvent.change(actionEditor, { target: { value: 'unsaved entry' } });
  fireEvent.click(screen.getByRole('button', { name: '工作区文件' }));
  fireEvent.click(await screen.findByRole('button', { name: helper.path }));
  const helperEditor = await screen.findByLabelText('辅助文件在线编辑器');
  fireEvent.change(helperEditor, { target: { value: 'unsaved helper' } });
  fireEvent.click(screen.getByRole('button', { name: /动作入口 2/ }));
  expect(screen.getByLabelText('Playbook 在线编辑器')).toHaveValue('unsaved entry');
  fireEvent.click(screen.getByRole('button', { name: '保存 Playbook' }));
  await waitFor(() => expect(notify).toHaveBeenCalledWith('success', 'Action 已保存', expect.any(String)));
  expect(screen.getByRole('button', { name: '保存 Draft' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: /工作区文件/ }));
  expect(screen.getByLabelText('辅助文件在线编辑器')).toHaveValue('unsaved helper');
  fireEvent.click(screen.getByRole('button', { name: '关闭编辑器' }));
  await answerConfirm(false);
  expect(close).not.toHaveBeenCalled();
  expect(screen.getByLabelText('辅助文件在线编辑器')).toHaveValue('unsaved helper');
});

it('ignores an earlier action response that arrives after switching to a different entry', async () => {
  const first = deferred<PlaybookFile>();
  vi.mocked(api.playbook).mockImplementation(async (_, id) => id === 'install' ? first.promise : source(id));
  openEditor();
  expect(screen.getByText('正在读取当前动作的 YAML…')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '安装前检查' }));
  await screen.findByDisplayValue(/name: ready/);
  await act(async () => first.resolve(source('install')));
  expect(screen.getByLabelText('Playbook 在线编辑器')).toHaveValue(source('ready').content);
  expect(screen.getByRole('region', { name: '当前动作入口' })).toHaveTextContent('tasks/checks/ready.yml');
});

it('shows a load error without an editable placeholder and recovers with an explicit reload', async () => {
  vi.mocked(api.playbook).mockRejectedValueOnce(new Error('文件暂时不可读'));
  openEditor();
  expect(await screen.findByRole('alert')).toHaveTextContent('文件暂时不可读');
  expect(screen.queryByLabelText('Playbook 在线编辑器')).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: '保存 Playbook' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: '载入编辑器' }));
  await screen.findByDisplayValue(/name: install/);
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('keeps other actions dirty when only the currently selected action is saved', async () => {
  openEditor();
  await screen.findByDisplayValue(/name: install/);
  fireEvent.change(screen.getByLabelText('动作名称'), { target: { value: '安装新配置' } });
  fireEvent.click(screen.getByRole('button', { name: '安装前检查' }));
  await screen.findByDisplayValue(/name: ready/);
  fireEvent.change(screen.getByLabelText('动作名称'), { target: { value: '新的前置检查' } });
  fireEvent.click(screen.getByRole('button', { name: '保存 Playbook' }));
  await waitFor(() => expect(notify).toHaveBeenCalledWith('success', 'Action 已保存', expect.any(String)));
  expect(screen.getByRole('button', { name: '保存 Draft' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: '安装新配置' }));
  await screen.findByDisplayValue(/name: install/);
  expect(screen.getByLabelText('动作名称')).toHaveValue('安装新配置');
  expect(within(screen.getByRole('region', { name: '当前动作入口' })).getByRole('status')).toHaveTextContent('有未保存内容');
  fireEvent.click(screen.getByRole('button', { name: '保存 Playbook' }));
  await waitFor(() => expect(screen.getByRole('button', { name: '保存 Draft' })).toBeEnabled());
});

it('confirms before reloading edited content and keeps it when a save conflicts', async () => {
  vi.mocked(api.savePlaybook).mockRejectedValueOnce(new Error('文件已被更新'));
  openEditor();
  const editor = await screen.findByDisplayValue(/name: install/);
  fireEvent.change(editor, { target: { value: 'local changes' } });
  fireEvent.click(screen.getByRole('button', { name: '重新载入' }));
  await answerConfirm(false);
  expect(api.playbook).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole('button', { name: '保存 Playbook' }));
  await waitFor(() => expect(notify).toHaveBeenCalledWith('error', '保存 Playbook 失败', '文件已被更新'));
  expect(editor).toHaveValue('local changes');
  expect(screen.getByRole('button', { name: '保存 Draft' })).toBeDisabled();
});

it('locks action deletion while pending and retains the entry when deletion fails', async () => {
  const deletion = deferred<Awaited<ReturnType<typeof api.deleteActionPlaybook>>>();
  vi.spyOn(api, 'deleteActionPlaybook').mockReturnValue(deletion.promise);
  openEditor();
  await screen.findByDisplayValue(/name: install/);
  fireEvent.click(screen.getByRole('button', { name: '移除动作' }));
  await answerConfirm();
  await waitFor(() => expect(api.deleteActionPlaybook).toHaveBeenCalledWith('release', 'install', 'sha-install', 'tree-sha'));
  expect(screen.getByRole('button', { name: '关闭编辑器' })).toBeDisabled();
  expect(screen.getByRole('button', { name: '安装前检查' })).toBeDisabled();
  await act(async () => deletion.reject(new Error('工作区已变化')));
  await waitFor(() => expect(screen.getByRole('button', { name: '关闭编辑器' })).toBeEnabled());
  expect(screen.getByLabelText('Playbook 在线编辑器')).toHaveValue(source('install').content);
  expect(notify).toHaveBeenCalledWith('error', '删除动作入口失败', '工作区已变化');
});

it('keeps Draft submission locked until the saved action generation has been refreshed', async () => {
  const refreshed = deferred<Awaited<ReturnType<typeof api.component>>>();
  vi.mocked(api.component).mockReturnValueOnce(refreshed.promise);
  const update = vi.spyOn(api, 'updateRelease').mockResolvedValue(release);
  openEditor();
  await screen.findByDisplayValue(/name: install/);
  fireEvent.click(screen.getByRole('button', { name: '保存 Playbook' }));
  await waitFor(() => expect(api.component).toHaveBeenCalled());
  expect(screen.getByRole('button', { name: '保存 Draft' })).toBeDisabled();
  expect(screen.getByLabelText('动作名称')).toBeDisabled();
  await act(async () => refreshed.resolve({ id: 'component', name: 'PKI', slug: 'pki', ownerId: 'owner', layer: 'runtime_state', tags: [], releases: [{ ...release, definitionGeneration: 4 }] }));
  await waitFor(() => expect(screen.getByRole('button', { name: '保存 Draft' })).toBeEnabled());
  fireEvent.click(screen.getByRole('button', { name: '保存 Draft' }));
  await waitFor(() => expect(update).toHaveBeenCalledWith('release', expect.objectContaining({ definitionGeneration: 4 })));
});

it('blocks Draft submission after a generation refresh failure and permits an explicit recovery', async () => {
  vi.mocked(api.component).mockRejectedValueOnce(new Error('读取版本失败'));
  openEditor();
  await screen.findByDisplayValue(/name: install/);
  fireEvent.click(screen.getByRole('button', { name: '保存 Playbook' }));
  await waitFor(() => expect(notify).toHaveBeenCalledWith('error', expect.stringContaining('版本状态刷新失败'), '读取版本失败'));
  expect(screen.getByRole('button', { name: '保存 Draft' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: '重试读取版本状态' }));
  await waitFor(() => expect(screen.getByRole('button', { name: '保存 Draft' })).toBeEnabled());
  expect(api.component).toHaveBeenCalledTimes(2);
});

it('refreshes the Draft generation after saving a helper file', async () => {
  vi.spyOn(api, 'saveWorkspaceFile').mockResolvedValue({ ...helper, sha256: 'saved-helper-sha' });
  vi.mocked(api.component).mockResolvedValue({ id: 'component', name: 'PKI', slug: 'pki', ownerId: 'owner', layer: 'runtime_state', tags: [], releases: [{ ...release, definitionGeneration: 4 }] });
  const update = vi.spyOn(api, 'updateRelease').mockResolvedValue(release);
  openEditor();
  await screen.findByDisplayValue(/name: install/);
  fireEvent.click(screen.getByRole('button', { name: '工作区文件' }));
  fireEvent.click(await screen.findByRole('button', { name: helper.path }));
  const editor = await screen.findByLabelText('辅助文件在线编辑器');
  await waitFor(() => expect(editor).toBeEnabled());
  fireEvent.change(editor, { target: { value: 'ca=false' } });
  fireEvent.click(screen.getByRole('button', { name: '保存辅助文件' }));
  await waitFor(() => expect(notify).toHaveBeenCalledWith('success', '辅助文件已保存', helper.path));
  await waitFor(() => expect(screen.getByRole('button', { name: '保存 Draft' })).toBeEnabled());
  fireEvent.click(screen.getByRole('button', { name: '保存 Draft' }));
  await waitFor(() => expect(update).toHaveBeenCalledWith('release', expect.objectContaining({ definitionGeneration: 4 })));
});
