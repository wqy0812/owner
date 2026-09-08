import { expect, it, vi } from 'vitest';
import { Link, MemoryRouter, useLocation } from 'react-router-dom';
import { api } from '../api/client';
import { AppProvider } from '../context/AppContext';
import { ComponentsPage } from '../pages/ComponentsPage';
import type { PlaybookWorkspaceFile } from '../types/domain';
import { answerConfirm } from './antdInteractions';
import { alice, installFetch, installReadModelFixtures, json } from './fixtures/appFixtures';
import { user } from './interactions';
import { render, screen, waitFor, within } from './render';

function Location() {
  return <><output data-testid="location">{useLocation().pathname}</output><Link to="/runs">离开组件</Link></>;
}

it('downloads helpers without discarding the contract and still guards subsequent navigation', async () => {
  const fallback = installFetch();
  const draft = { id: 'release-review', componentId: 'component-review', version: '1.0', status: 'draft', releaseNotes: 'notes', parameters: [], dependencies: [], actions: [] };
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => String(input).endsWith('/components')
    ? json([{ id: 'component-review', name: 'review component', slug: 'review', ownerId: alice.id, layer: 'runtime_state', tags: [], latestRelease: draft, releases: [draft] }])
    : fallback(input, init)));
  installReadModelFixtures();
  const file: PlaybookWorkspaceFile = { releaseId: draft.id, path: 'helper.txt', sha256: 'sha', sizeBytes: 4, mediaType: 'text/plain', content: 'test', editable: true };
  vi.spyOn(api, 'playbookWorkspace').mockResolvedValue({ root: 'managed/test/', treeSha256: 'tree', files: [file] });
  vi.spyOn(api, 'workspaceFile').mockResolvedValue(file);
  render(<MemoryRouter initialEntries={['/components?selected=component-review']}><AppProvider><ComponentsPage/><Location/></AppProvider></MemoryRouter>);
  await user.click(await screen.findByLabelText('编辑参数合同'));
  await user.click(await screen.findByRole('button', { name: '新增参数' }));
  await user.click(screen.getByRole('button', { name: '编辑版本与 Playbook' }));
  await user.click(await screen.findByRole('button', { name: 'helper.txt' }));
  const download = await screen.findByRole('link', { name: '下载' });
  // Observe the real click after the navigation guard; suppress only jsdom's
  // unsupported network navigation once the native download has been allowed.
  const nativeDownload = vi.fn((event: MouseEvent) => {
    expect(event.defaultPrevented).toBe(false);
    event.preventDefault();
  });
  download.addEventListener('click', nativeDownload, { once: true });
  expect(download).toHaveAttribute('href', api.workspaceFileDownloadURL(draft.id, file.path));
  await user.click(download);
  expect(nativeDownload).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole('dialog', { name: '确认操作' })).not.toBeInTheDocument();
  expect(screen.getByTestId('location')).toHaveTextContent('/components');
  await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '取消' }));
  await user.click(screen.getByRole('link', { name: '离开组件' }));
  await answerConfirm(false);
  expect(screen.getByTestId('location')).toHaveTextContent('/components');
  await user.click(screen.getByRole('link', { name: '离开组件' }));
  await answerConfirm(true);
  await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('/runs'));
});
