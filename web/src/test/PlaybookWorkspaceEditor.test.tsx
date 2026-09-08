import { beforeEach, expect, it, vi } from 'vitest';
import { api } from '../api/client';
import { PlaybookWorkspaceEditor } from '../features/components/contract/PlaybookWorkspaceEditor';
import type { PlaybookWorkspaceFile } from '../types/domain';
import { answerConfirm } from './antdInteractions';
import { fireEvent, render, screen, waitFor, within } from './render';

beforeEach(() => vi.restoreAllMocks());

it('saves a newly created helper using its returned file identity', async () => {
  const file: PlaybookWorkspaceFile = { releaseId: 'release', path: 'templates/example.j2', sha256: 'empty-file-sha', sizeBytes: 0, mediaType: 'text/plain', editable: true, content: '' };
  vi.spyOn(api, 'playbookWorkspace').mockResolvedValueOnce({ root: 'managed/test/', treeSha256: '', files: [] }).mockResolvedValue({ root: 'managed/test/', treeSha256: 'created-tree', files: [file] });
  vi.spyOn(api, 'workspaceFile').mockResolvedValue(file);
  const save = vi.spyOn(api, 'saveWorkspaceFile').mockResolvedValue(file);
  render(<PlaybookWorkspaceEditor releaseId="release" refreshToken="" notify={vi.fn()} />);
  await screen.findByText('工作区尚无文件。');
  fireEvent.click(screen.getByRole('button', { name: '新建文本文件' }));
  const editor = await screen.findByLabelText('辅助文件在线编辑器');
  await waitFor(() => expect(screen.getByRole('button', { name: '新建文本文件' })).not.toBeDisabled());
  fireEvent.change(editor, { target: { value: 'port=8080' } });
  fireEvent.click(screen.getByRole('button', { name: '保存辅助文件' }));
  await waitFor(() => expect(save).toHaveBeenLastCalledWith('release', file.path, 'port=8080', 'empty-file-sha', 'created-tree'));
});

it('uses the opened file metadata to keep binary content out of the text editor', async () => {
  const file = { releaseId: 'release', path: 'files/helper.bin', sha256: 'binary-sha', sizeBytes: 5, mediaType: 'application/octet-stream' };
  vi.spyOn(api, 'playbookWorkspace').mockResolvedValue({ root: 'managed/test/', treeSha256: 'tree', files: [file] });
  vi.spyOn(api, 'workspaceFile').mockResolvedValue({ ...file, editable: false });
  render(<PlaybookWorkspaceEditor releaseId="release" refreshToken="" notify={vi.fn()} />);
  fireEvent.click(await screen.findByRole('button', { name: /files\/helper.bin/ }));
  await screen.findByText('该文件较大或不是 UTF-8 文本，只能下载、替换或删除。');
  expect(screen.queryByLabelText('辅助文件在线编辑器')).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '保存辅助文件' })).not.toBeInTheDocument();
});

it('groups paths into collapsible folders, keeps empty folders, and opens files by their full path', async () => {
  const files = ['templates/example.j2', '123/123/123/.gitkeep', 'templates/123.j2', 'templates/123/123', 'README.md', 'files/.gitkeep']
    .map((path): PlaybookWorkspaceFile => ({ releaseId: 'release', path, sha256: path, sizeBytes: 0, mediaType: 'text/plain', editable: true }));
  vi.spyOn(api, 'playbookWorkspace').mockResolvedValue({ root: 'managed/test/', treeSha256: 'tree', files });
  const read = vi.spyOn(api, 'workspaceFile').mockImplementation(async (_, path) => ({ ...files.find((file) => file.path === path)!, content: path }));
  render(<PlaybookWorkspaceEditor releaseId="release" refreshToken="" notify={vi.fn()} />);
  const tree = within(screen.getByRole('navigation', { name: 'Ansible 工作区文件树' }));
  await tree.findByRole('button', { name: 'templates' });
  expect(tree.getAllByRole('button').map((button) => button.getAttribute('aria-label')).filter(label => !label?.startsWith('删除目录 '))).toEqual([
    '123', '123/123', '123/123/123', 'files', 'templates', 'templates/123', 'templates/123/123', 'templates/123.j2', 'templates/example.j2', 'README.md',
  ]);
  expect(tree.queryByText(/\.gitkeep/)).not.toBeInTheDocument();
  expect(tree.getByRole('button', { name: '123/123/123' })).toHaveTextContent('空目录');
  expect(tree.getByRole('button', { name: 'templates/123.j2' })).toHaveTextContent(/^123\.j2/);

  fireEvent.click(tree.getByRole('button', { name: 'templates' }));
  expect(tree.getByRole('button', { name: 'templates' })).toHaveAttribute('aria-expanded', 'false');
  expect(tree.queryByRole('button', { name: 'templates/example.j2' })).not.toBeInTheDocument();
  expect(read).not.toHaveBeenCalled();
  fireEvent.click(tree.getByRole('button', { name: 'templates' }));
  fireEvent.click(tree.getByRole('button', { name: 'templates/123/123' }));
  expect(await screen.findByLabelText('辅助文件在线编辑器')).toHaveValue('templates/123/123');
  expect(read).toHaveBeenCalledWith('release', 'templates/123/123');
});

it('deletes an empty directory through its directory action without exposing the marker', async () => {
  const file = { releaseId: 'release', path: 'files/empty/.gitkeep', sha256: 'empty', sizeBytes: 0, mediaType: 'text/plain' };
  vi.spyOn(api, 'playbookWorkspace').mockResolvedValue({ root: 'managed/test/', treeSha256: 'before', files: [file] });
  const remove = vi.spyOn(api, 'deleteWorkspaceDirectory').mockResolvedValue({ root: 'managed/test/', treeSha256: 'after', files: [] });
  render(<PlaybookWorkspaceEditor releaseId="release" refreshToken="" notify={vi.fn()} />);
  const button = await screen.findByRole('button', { name: '删除目录 files/empty' });
  fireEvent.click(button);
  await answerConfirm(false);
  expect(remove).not.toHaveBeenCalled();
  fireEvent.click(button);
  expect(await screen.findByText(/包含 0 个文件/)).toBeInTheDocument();
  await answerConfirm();
  await screen.findByText('工作区尚无文件。');
  expect(remove).toHaveBeenCalledWith('release', 'files/empty', 'before');
});

it('protects action directories and retains unsaved helper content when directory deletion fails', async () => {
  const file = { releaseId: 'release', path: 'files/nested/a.txt', sha256: 'a', sizeBytes: 1, mediaType: 'text/plain', editable: true, content: 'a' };
  const action = { ...file, path: 'tasks/install.yml' };
  vi.spyOn(api, 'playbookWorkspace').mockResolvedValue({ root: 'managed/test/', treeSha256: 'before', files: [file, action], references: { 'tasks/install.yml': { actions: [], staticReferences: [], dynamicReferencesUnknown: true, protectionReason: '动作入口' } } });
  vi.spyOn(api, 'workspaceFile').mockResolvedValue(file);
  const remove = vi.spyOn(api, 'deleteWorkspaceDirectory').mockRejectedValue(new Error('workspace changed'));
  const notify = vi.fn();
  render(<PlaybookWorkspaceEditor releaseId="release" refreshToken="" notify={notify} />);
  expect(await screen.findByRole('button', { name: '删除目录 tasks' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: file.path }));
  const editor = await screen.findByLabelText('辅助文件在线编辑器');
  await waitFor(() => expect(screen.getByRole('button', { name: '删除目录 files/nested' })).not.toBeDisabled());
  fireEvent.change(editor, { target: { value: 'unsaved' } });
  fireEvent.click(screen.getByRole('button', { name: '删除目录 files/nested' }));
  expect(await screen.findByText(/当前文件有未保存内容/)).toBeInTheDocument();
  await answerConfirm();
  await waitFor(() => expect(notify).toHaveBeenCalledWith('error', '删除目录失败', 'workspace changed'));
  expect(remove).toHaveBeenCalledWith('release', 'files/nested', 'before');
  expect(editor).toHaveValue('unsaved');
});

it('reveals a new empty directory without opening its marker or discarding unsaved file content', async () => {
  const file: PlaybookWorkspaceFile = { releaseId: 'release', path: 'templates/example.j2', sha256: 'file-sha', sizeBytes: 8, mediaType: 'text/plain', editable: true, content: 'original' };
  const marker: PlaybookWorkspaceFile = { releaseId: 'release', path: 'templates/new/nested/.gitkeep', sha256: 'marker-sha', sizeBytes: 0, mediaType: 'text/plain' };
  vi.spyOn(api, 'playbookWorkspace').mockResolvedValueOnce({ root: 'managed/test/', treeSha256: 'tree', files: [file] })
    .mockResolvedValue({ root: 'managed/test/', treeSha256: 'new-tree', files: [file, marker] });
  const read = vi.spyOn(api, 'workspaceFile').mockResolvedValue(file);
  const save = vi.spyOn(api, 'saveWorkspaceFile').mockResolvedValue(marker);
  const confirm = vi.spyOn(window, 'confirm');
  render(<PlaybookWorkspaceEditor releaseId="release" refreshToken="" notify={vi.fn()} />);
  fireEvent.click(await screen.findByRole('button', { name: file.path }));
  const editor = await screen.findByLabelText('辅助文件在线编辑器');
  await waitFor(() => expect(screen.getByRole('button', { name: 'templates' })).not.toBeDisabled());
  fireEvent.change(editor, { target: { value: 'unsaved changes' } });
  fireEvent.click(screen.getByRole('button', { name: 'templates' }));
  fireEvent.change(screen.getByLabelText('工作区相对路径'), { target: { value: 'templates/new/nested/' } });
  fireEvent.click(screen.getByRole('button', { name: '新建目录' }));
  expect(await screen.findByRole('button', { name: 'templates/new/nested' })).toHaveTextContent('空目录');
  expect(save).toHaveBeenCalledWith('release', marker.path, '', '', 'tree');
  expect(read).toHaveBeenCalledTimes(1);
  expect(editor).toHaveValue('unsaved changes');
  expect(confirm).not.toHaveBeenCalled();
});
