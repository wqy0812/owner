import { expect, it, vi } from 'vitest';
import { api } from '../api/client';
import { AppProvider } from '../context/AppContext';
import { ArtifactModal } from '../features/components/media/ArtifactModal';
import type { ComponentArtifact, ComponentRelease } from '../types/domain';
import { installFetch, installReadModelFixtures, json } from './fixtures/appFixtures';
import { fillField, user } from './interactions';
import { render, screen, waitFor, within } from './render';

const artifact: ComponentArtifact = {
  id: 'media-1', releaseId: 'release-containerd-2', alias: 'media', filename: 'runtime.tgz',
  sha256: 'a'.repeat(64), sizeBytes: 128, sourceUrl: 'https://files.test/runtime.tgz',
  sourceUpdatedBy: 'owner', sourceUpdatedAt: '', createdBy: 'owner', createdAt: '',
};
async function open(
  mutate: (url: string, init?: RequestInit) => Promise<Response> | undefined,
  state: ComponentRelease['state'] = 'draft', artifacts: ComponentArtifact[] = [artifact],
) {
  const fallback = installFetch();
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url === '/api/v1/environments') return json([
      { id: 'no-station', name: 'No station', ownerId: 'owner', currentRevision: { id: 'no-station-r1', environmentId: 'no-station', revision: 1, facts: {}, hosts: [], credentialRefs: [], variables: {} } },
      { id: 'station', name: 'Media station', ownerId: 'owner', currentRevision: { id: 'station-r2', environmentId: 'station', revision: 2, facts: {}, hosts: [], credentialRefs: [], variables: { FILE_STATION: 'files.test:8080' } } },
    ]);
    return mutate(url, init) ?? fallback(input, init);
  });
  vi.stubGlobal('fetch', fetchMock);
  installReadModelFixtures();
  const component = await api.component('component-containerd');
  render(<AppProvider><ArtifactModal release={{ ...component.releases![0], state, artifacts: structuredClone(artifacts) }} onClose={vi.fn()} /></AppProvider>);
  const dialog = await screen.findByRole('dialog', { name: '组件介质 · v2.1.1' });
  return { dialog, editor: within(dialog), fetchMock };
}

it('uploads the selected artifact with its checksum to the environment that provides FILE_STATION', async () => {
  let submitted: FormData | undefined;
  const { editor } = await open((url, init) => {
    if (url.endsWith('/artifacts/upload') && init?.method === 'POST') {
      submitted = init.body as FormData;
      return json(artifact);
    }
  }, 'draft', []);
  const save = editor.getByRole('button', { name: '上传并校验' });
  expect(save).toBeDisabled();
  await editor.findByText(/上传入口：http:\/\/files.test:8080/);
  const picker = editor.getByLabelText('介质文件').closest('.file-picker')!.querySelector<HTMLInputElement>('input[type=file]')!;
  await user.upload(picker, new File(['runtime bytes'], 'runtime.tgz'));
  await fillField(editor.getByLabelText('SHA-256', { exact: true }), artifact.sha256);
  await user.click(save);
  await screen.findByText('组件介质已保存');
  expect(submitted?.get('environmentId')).toBe('station');
  expect(submitted?.get('alias')).toBe('media');
  expect(submitted?.get('sha256')).toBe(artifact.sha256);
  expect((submitted?.get('artifact') as File).name).toBe('runtime.tgz');
  expect(editor.getByText(artifact.sourceUrl)).toBeInTheDocument();
});

it('preserves failed registration input and replaces the same alias only after a successful retry', async () => {
  let attempts = 0;
  const replacement = { ...artifact, id: 'media-2', filename: 'replacement.tgz', sourceUrl: 'https://files.test/replacement.tgz' };
  const { editor, fetchMock } = await open((url, init) => {
    if (url.endsWith('/artifacts/register') && init?.method === 'POST') return ++attempts === 1
      ? json({ error: { code: 'conflict', message: 'Checksum does not match' } }, 409)
      : json(replacement);
  });
  await user.click(editor.getByRole('tab', { name: '登记已有路径' }));
  await fillField(editor.getByLabelText('文件名'), replacement.filename);
  await fillField(editor.getByLabelText('来源 URL'), replacement.sourceUrl);
  await fillField(editor.getByLabelText('SHA-256', { exact: true }), replacement.sha256);
  await user.click(editor.getByRole('button', { name: '探测并登记' }));
  await screen.findByText('保存组件介质失败');
  expect(editor.getByLabelText('来源 URL')).toHaveValue(replacement.sourceUrl);
  expect(editor.getByText(artifact.sourceUrl)).toBeInTheDocument();
  await user.click(editor.getByRole('button', { name: '探测并登记' }));
  await screen.findByText('组件介质已保存');
  expect(editor.queryByText(artifact.sourceUrl)).not.toBeInTheDocument();
  expect(editor.getAllByRole('button', { name: '移除介质 media' })).toHaveLength(1);
  const writes = fetchMock.mock.calls.filter(([, init]) => init?.method === 'POST');
  expect(writes).toHaveLength(2);
  expect(JSON.parse(String(writes[1][1]?.body))).toEqual({ alias: 'media', filename: replacement.filename, sourceUrl: replacement.sourceUrl, sha256: replacement.sha256 });
});

it('keeps published content immutable while allowing a cancelled or completed source repair', async () => {
  const nextSource = 'https://mirror.test/runtime.tgz';
  const { editor, fetchMock } = await open((url, init) => {
    if (url.endsWith('/artifacts/media/source') && init?.method === 'PATCH') return json({ ...artifact, sourceUrl: nextSource });
  }, 'released');
  expect(editor.getByText('已发布内容身份不可修改')).toBeInTheDocument();
  expect(editor.queryByRole('button', { name: /上传并校验|移除介质/ })).not.toBeInTheDocument();
  const repair = editor.getByRole('button', { name: '修复来源' });
  await user.click(repair);
  let prompt = await screen.findByRole('dialog', { name: '编辑内容' });
  await user.click(within(prompt).getByRole('button', { name: '取消' }));
  await waitFor(() => expect(prompt).not.toBeInTheDocument());
  expect(fetchMock.mock.calls.some(([, init]) => init?.method === 'PATCH')).toBe(false);
  await user.click(repair);
  prompt = await screen.findByRole('dialog', { name: '编辑内容' });
  await fillField(within(prompt).getByRole('textbox'), nextSource);
  await user.click(within(prompt).getByRole('button', { name: '保存' }));
  await waitFor(() => expect(prompt).not.toBeInTheDocument());
  await screen.findByText('介质来源已更新');
  expect(editor.getByText(nextSource)).toBeInTheDocument();
  expect(editor.getByText(`sha256:${artifact.sha256} · 128 bytes`)).toBeInTheDocument();
  const write = fetchMock.mock.calls.find(([, init]) => init?.method === 'PATCH')!;
  expect(JSON.parse(String(write[1]?.body))).toEqual({ sourceUrl: nextSource });
});

it('retains an artifact after a failed detach and removes only its reference on retry', async () => {
  let attempts = 0;
  const { editor } = await open((url, init) => {
    if (url.endsWith('/artifacts/media') && init?.method === 'DELETE') return ++attempts === 1
      ? json({ error: { code: 'conflict', message: 'Artifact is in use' } }, 409)
      : json({ deleted: true });
  });
  await user.click(editor.getByRole('button', { name: '移除介质 media' }));
  await screen.findByText('移除介质引用失败');
  expect(editor.getByText(artifact.sourceUrl)).toBeInTheDocument();
  await user.click(editor.getByRole('button', { name: '移除介质 media' }));
  await screen.findByText('介质引用已移除');
  expect(editor.getByText('尚未录入介质')).toBeInTheDocument();
  expect(screen.getByText('file-station 上的物理文件未删除。')).toBeInTheDocument();
});
