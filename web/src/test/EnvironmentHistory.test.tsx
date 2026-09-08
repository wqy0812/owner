import { beforeEach, describe, expect, it, vi } from 'vitest';
import { user as userEvent } from './interactions';
import { act, screen, waitFor, within } from './render';
import { EventSourceMock } from './setup';

import { EnvironmentsPage } from '../pages/EnvironmentsPage';
import { admin, dave, installFetch, isEnvironmentList, json } from './fixtures/appFixtures';
import { pageRenderer } from './pageRenderer';
const renderApp = pageRenderer(EnvironmentsPage, '/environments');

beforeEach(() => { installFetch(); });
function historyFixture(options: { blocked?: boolean; failDelete?: boolean; viewer?: typeof dave; archived?: boolean } = {}) {
  const fallback = installFetch({ initialUser: options.viewer ?? dave });
  const r1 = { id: 'deletion-r1', environmentId: 'environment-test', revision: 1, facts: {}, hosts: [], parameters: {}, variables: {}, credentialRefs: [], changeReason: 'Revision 原始备注' };
  const r2 = { ...r1, id: 'deletion-r2', revision: 2, changeReason: '当前配置' };
  let revisions = [r2, r1];
  const preview = {
    environmentId: 'environment-test', environmentName: 'History Environment', revisionId: r1.id, revision: 1,
    current: false, archived: false, runCount: options.blocked ? 1 : 0, imageBuildCount: 0, healthCheckCount: 2, sshCheckCount: 1,
    canDelete: !options.blocked, blockers: options.blocked ? [{ code: 'environment_revision.run_history', message: '该版本被 1 个 Run 引用，必须保留' }] : [],
  };
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (isEnvironmentList(url)) return json([{
      id: 'environment-test', name: 'History Environment', ownerId: dave.id, currentRevisionId: r2.id, currentRevision: r2, revisions,
      ...(revisions.length > 1 ? { healthCheck: { id: 'historical-tcp', environmentId: 'environment-test', environmentRevisionId: r1.id, status: 'healthy', results: [], checkedAt: '2026-09-07T00:00:00Z' } } : {}),
      ...(options.archived ? { archivedAt: '2026-09-07T00:00:00Z' } : {})
    }]);
    if (url.endsWith('/deletion-r1/deletion-impact')) return json(preview);
    if (url.endsWith('/revisions/deletion-r1') && init?.method === 'DELETE') {
      if (options.failDelete) return json({ error: { code: 'conflict', message: '确认期间新增了运行引用，请重新核对' } }, 409);
      revisions = [r2]; return json({ deleted: true });
    }
    return fallback(input, init);
  });
  vi.stubGlobal('fetch', fetchMock);
  return { fetchMock, removeRemotely: () => { revisions = [r2]; } };
}


describe("environment history version deletion", () => {
  it('previews on demand, preserves the original note, cancels without deleting and refreshes after confirmation', async () => {
    const { fetchMock } = historyFixture();
    renderApp('/environments');
    await userEvent.click(await screen.findByRole('tab', { name: '版本历史' }));
    expect(await screen.findByRole('heading', { name: '版本历史' })).toBeInTheDocument();
    expect(screen.getByText('Revision 原始备注')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '删除版本 r2' })).not.toBeInTheDocument();
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes('deletion-impact'))).toBe(false);
    await userEvent.click(screen.getByRole('button', { name: '删除版本 r1' }));
    const dialog = await screen.findByRole('dialog', { name: '删除环境版本' });
    expect(await within(dialog).findByText('这是不可恢复的永久删除')).toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: '确认删除版本' })).toBeDisabled();
    await userEvent.click(within(dialog).getByRole('button', { name: '取消' }));
    expect(fetchMock.mock.calls.some(([, init]) => init?.method === 'DELETE')).toBe(false);
    await userEvent.click(screen.getByRole('button', { name: '删除版本 r1' }));
    await userEvent.click(await screen.findByRole('checkbox', { name: /我确认永久删除/ }));
    await userEvent.click(screen.getByRole('button', { name: '确认删除版本' }));
    await waitFor(() => expect(screen.queryByText('Revision 原始备注')).not.toBeInTheDocument());
    expect(screen.queryByRole('dialog', { name: '删除环境版本' })).not.toBeInTheDocument();
    expect(screen.getByText('当前配置')).toBeInTheDocument();
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'DELETE')).toHaveLength(1);
  });

  it('shows run blockers and keeps deletion disabled', async () => {
    const { fetchMock } = historyFixture({ blocked: true });
    renderApp('/environments');
    await userEvent.click(await screen.findByRole('tab', { name: '版本历史' }));
    await userEvent.click(await screen.findByRole('button', { name: '删除版本 r1' }));
    expect(await screen.findByText('该版本被 1 个 Run 引用，必须保留')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '确认删除版本' })).toBeDisabled();
    expect(screen.queryByRole('checkbox', { name: /我确认永久删除/ })).not.toBeInTheDocument();
    expect(fetchMock.mock.calls.some(([, init]) => init?.method === 'DELETE')).toBe(false);
  });

  it('retains the dialog and version on conflict and requires a new preview', async () => {
    const { fetchMock } = historyFixture({ failDelete: true });
    renderApp('/environments');
    await userEvent.click(await screen.findByRole('tab', { name: '版本历史' }));
    await userEvent.click(await screen.findByRole('button', { name: '删除版本 r1' }));
    await userEvent.click(await screen.findByRole('checkbox', { name: /我确认永久删除/ }));
    await userEvent.click(screen.getByRole('button', { name: '确认删除版本' }));
    expect(await screen.findByText('确认期间新增了运行引用，请重新核对')).toBeInTheDocument();
    expect(screen.getByText('版本删除未完成')).toBeInTheDocument();
    expect(screen.queryByRole('region', { name: '版本删除影响' })).not.toBeInTheDocument();
    expect(screen.getByRole('dialog', { name: '删除环境版本' })).toBeInTheDocument();
    expect(screen.getByText('Revision 原始备注')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '确认删除版本' })).toBeDisabled();
    await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: /重试/ }));
    await waitFor(() => expect(fetchMock.mock.calls.filter(([input]) => String(input).includes('deletion-impact'))).toHaveLength(2));
    expect(await screen.findByRole('checkbox', { name: /我确认永久删除/ })).not.toBeChecked();
  });

  it('disables deletion for unsaved edits and refreshes remote deletion events', async () => {
    const { removeRemotely } = historyFixture();
    renderApp('/environments');
    await userEvent.click(await screen.findByRole('tab', { name: '版本历史' }));
    const remove = await screen.findByRole('button', { name: '删除版本 r1' });
    await userEvent.click(screen.getByRole('tab', { name: '配置' }));
    await userEvent.click(screen.getByRole('tab', { name: '凭据引用' }));
    await userEvent.click(screen.getByRole('button', { name: '添加引用' }));
    expect(remove).toBeDisabled();
    await userEvent.click(screen.getByRole('button', { name: '放弃本页更改' }));
    expect(remove).toBeEnabled();
    await userEvent.click(screen.getByRole('button', { name: '添加引用' }));
    await userEvent.type(screen.getByRole('textbox', { name: '凭据名称' }), 'unsaved-name');
    removeRemotely();
    act(() => EventSourceMock.instances.at(-1)?.emit('environment.revision_deleted', { environmentId: 'environment-test', revisionId: 'deletion-r1' }));
    await waitFor(() => expect(screen.queryByText('Revision 原始备注')).not.toBeInTheDocument());
    expect(screen.getByRole('textbox', { name: '凭据名称' })).toHaveValue('unsaved-name');
    expect(screen.getByRole('button', { name: '保存新版本' })).toBeEnabled();
    expect(screen.getByText('尚未检查')).toBeInTheDocument();
  });

  it.each(['other-owner', 'admin', 'archived'])('hides deletion for %s', async mode => {
    historyFixture({ viewer: mode === 'other-owner' ? { ...dave, id: 'other-owner' } : mode === 'admin' ? admin : dave, archived: mode === 'archived' });
    renderApp('/environments');
    await userEvent.click(await screen.findByRole('tab', { name: '版本历史' }));
    expect(await screen.findByRole('heading', { name: '版本历史' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '删除版本 r1' })).not.toBeInTheDocument();
  });
});
