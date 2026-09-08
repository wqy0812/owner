import { beforeEach, expect, it, vi } from 'vitest';
import { api } from '../api/client';
import { ScenarioAcceptanceEditor } from '../components/ScenarioAcceptanceEditor';
import type { ScenarioAcceptance } from '../types/domain';
import { answerConfirm, selectOption } from './antdInteractions';
import { act, fireEvent, render, screen, waitFor } from './render';
import { deferred } from './testLifecycle';

const { notify } = vi.hoisted(() => ({ notify: vi.fn() }));
vi.mock('../context/AppContext', () => ({ useApp: () => ({ notify, platformOptionCategories: [] }), displayError: (error: unknown) => String(error) }));
beforeEach(() => { vi.restoreAllMocks(); notify.mockClear(); });

const file = { releaseId: 'revision-a', path: 'templates/check.j2', sha256: 'file-sha', sizeBytes: 3, mediaType: 'text/plain', editable: true, content: 'old' };
function definition(patch: Partial<ScenarioAcceptance> = {}): ScenarioAcceptance {
  return { revisionId: 'revision-a', revisionDigest: 'digest-a', editable: true, jobs: [],
    parameters: [{ name: 'endpoint', description: '业务地址', type: 'string', visibility: 'internal', modifiable: true, valueProvider: 'scenario_owner' }],
    values: { endpoint: 'https://service.example.test' }, bindings: [], workspace: { root: 'scenario/', treeSha256: 'tree-a', files: [file] }, ...patch };
}
function editor(revisionId = 'revision-a', onSaved = vi.fn(), editable = true) {
  return <ScenarioAcceptanceEditor revisionId={revisionId} editable={editable} nodes={[]} components={[]} environments={[]} onSaved={onSaved} />;
}

it.each([false, true])('renames the parameter and its value or source binding (bound=%s)', async bound => {
  const initial = definition(bound ? { values: {}, bindings: [{ parameter: 'endpoint', source: 'environment', sourceParameter: 'api_url' }] } : {});
  vi.spyOn(api, 'scenarioAcceptance').mockResolvedValue(initial);
  const save = vi.spyOn(api, 'saveScenarioAcceptance').mockImplementation(async (_id, input) => ({ ...initial, ...input }));
  render(editor());
  fireEvent.change(await screen.findByLabelText('验收参数名称 1'), { target: { value: 'service_url' } });
  fireEvent.click(screen.getByRole('button', { name: '保存业务验收' }));
  await waitFor(() => expect(save).toHaveBeenCalledWith('revision-a', expect.objectContaining({
    expectedRevisionDigest: 'digest-a', parameters: [expect.objectContaining({ name: 'service_url', type: 'string' })],
    values: bound ? {} : { service_url: 'https://service.example.test' },
    bindings: bound ? [{ parameter: 'service_url', source: 'environment', sourceParameter: 'api_url' }] : [],
  })));
});

it('clears incompatible values and bindings when switching a parameter type and source', async () => {
  const initial = definition({ bindings: [{ parameter: 'endpoint', source: 'environment', sourceParameter: 'url' }], values: {} });
  vi.spyOn(api, 'scenarioAcceptance').mockResolvedValue(initial);
  const save = vi.spyOn(api, 'saveScenarioAcceptance').mockResolvedValue(initial);
  render(editor());
  await screen.findByLabelText('验收参数名称 1');
  await selectOption(screen.getByRole('combobox', { name: '类型' }), '整数');
  fireEvent.click(screen.getByRole('button', { name: '保存业务验收' }));
  await waitFor(() => expect(save).toHaveBeenCalledWith('revision-a', expect.objectContaining({ values: {}, bindings: [], parameters: [expect.objectContaining({ type: 'integer' })] })));
});

it('blocks all parameter mutations while saving and keeps the draft after a conflict', async () => {
  vi.spyOn(api, 'scenarioAcceptance').mockResolvedValue(definition());
  const pending = deferred<ScenarioAcceptance>();
  vi.spyOn(api, 'saveScenarioAcceptance').mockReturnValue(pending.promise);
  const saved = vi.fn();
  render(editor('revision-a', saved));
  fireEvent.change(await screen.findByLabelText('验收参数名称 1'), { target: { value: 'local_endpoint' } });
  fireEvent.click(screen.getByRole('button', { name: '保存业务验收' }));
  expect(screen.getByRole('button', { name: '删除参数' })).toBeDisabled();
  fireEvent.mouseDown(screen.getByRole('combobox', { name: '类型' }).closest('.ant-select')!.querySelector('.ant-select-content')!);
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
  await act(async () => pending.reject(new Error('409 revision changed')));
  expect(screen.getByLabelText('验收参数名称 1')).toHaveValue('local_endpoint');
  expect(screen.getByRole('button', { name: '保存业务验收' })).toBeEnabled();
  expect(notify).toHaveBeenCalledWith('error', '保存业务验收失败', expect.stringContaining('409'));
  expect(saved).not.toHaveBeenCalled();
});

it('keeps read-only selectors closed while allowing existing files to be inspected', async () => {
  vi.spyOn(api, 'scenarioAcceptance').mockResolvedValue(definition({ editable: false }));
  vi.spyOn(api, 'scenarioAcceptanceFile').mockResolvedValue(file);
  render(editor('revision-a', vi.fn(), false));
  await screen.findByLabelText('验收参数名称 1');
  fireEvent.mouseDown(screen.getByRole('combobox', { name: '类型' }).closest('.ant-select')!.querySelector('.ant-select-content')!);
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '保存业务验收' })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: file.path }));
  expect(await screen.findByLabelText('验收文件在线编辑器')).toBeDisabled();
});

it('preserves edited file contents after a failed delete and a cancelled new-file action', async () => {
  vi.spyOn(api, 'scenarioAcceptance').mockResolvedValue(definition());
  vi.spyOn(api, 'scenarioAcceptanceFile').mockResolvedValue(file);
  const remove = vi.spyOn(api, 'deleteScenarioAcceptanceFile').mockRejectedValue(new Error('409 file changed'));
  render(editor());
  fireEvent.click(await screen.findByRole('button', { name: file.path }));
  const input = await screen.findByLabelText('验收文件在线编辑器');
  fireEvent.change(input, { target: { value: 'local file' } });
  fireEvent.click(screen.getByRole('button', { name: '删除文件' }));
  await answerConfirm(true);
  await waitFor(() => expect(notify).toHaveBeenCalledWith('error', '删除验收文件失败', expect.stringContaining('409')));
  expect(remove).toHaveBeenCalledWith('revision-a', { path: file.path, expectedSha256: 'file-sha', expectedTreeSha256: 'tree-a', expectedRevisionDigest: 'digest-a' });
  expect(input).toHaveValue('local file');
  fireEvent.click(screen.getByRole('button', { name: '新建文本文件' }));
  await answerConfirm(false);
  expect(input).toHaveValue('local file');
});

it('ignores an unabortable definition response after switching revisions', async () => {
  const old = deferred<ScenarioAcceptance>();
  vi.spyOn(api, 'scenarioAcceptance').mockReturnValueOnce(old.promise).mockResolvedValue(definition({ revisionId: 'revision-b', parameters: [], values: {} }));
  const view = render(editor());
  view.rerender(editor('revision-b'));
  await screen.findByText('尚未配置验收参数');
  await act(async () => old.resolve(definition()));
  expect(screen.queryByLabelText('验收参数名称 1')).not.toBeInTheDocument();
});

it('ignores a late file response and releases busy state when the revision changes', async () => {
  const old = deferred<typeof file>();
  vi.spyOn(api, 'scenarioAcceptance').mockResolvedValueOnce(definition()).mockResolvedValue(definition({ revisionId: 'revision-b', parameters: [], values: {}, workspace: { root: 'new/', files: [], treeSha256: '' } }));
  vi.spyOn(api, 'scenarioAcceptanceFile').mockReturnValue(old.promise);
  const view = render(editor());
  fireEvent.click(await screen.findByRole('button', { name: file.path }));
  view.rerender(editor('revision-b'));
  await screen.findByText('尚未配置验收参数');
  expect(screen.getByRole('button', { name: '添加验收参数' })).toBeEnabled();
  await act(async () => old.resolve(file));
  expect(screen.queryByLabelText('验收文件在线编辑器')).not.toBeInTheDocument();
});
