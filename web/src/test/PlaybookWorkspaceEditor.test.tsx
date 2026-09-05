import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { api } from '../api/client';
import { PlaybookWorkspaceEditor } from '../pages/ComponentsPage';
import type { PlaybookWorkspaceFile } from '../types/domain';

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
