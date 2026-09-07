import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { afterEach, expect, it, vi } from 'vitest';
import { AppProvider, useApp } from '../context/AppContext';
import { ComponentsPage } from '../pages/ComponentsPage';
import { EventSourceMock } from './setup';

const owner = { id: 'component-owner-a', name: '林晓', role: 'component_owner' };
const empty = (id: string, canDelete: boolean | undefined = true) => ({
  id, name: id, slug: id, ownerId: owner.id, ownerName: owner.name,
  layer: 'runtime_state', tags: [], releases: [], releaseLines: [],
  releaseCount: 0, hasDraft: false, needsAttention: true, canDelete,
});
type Entry = Omit<ReturnType<typeof empty>, 'canDelete'> & { canDelete?: boolean };
const response = (data: unknown) => new Response(JSON.stringify(Array.isArray(data) ? { items: data } : { data }), { headers: { 'Content-Type': 'application/json' } });

function fixture(entries: Entry[]) {
  const state = {
    entries,
    user: owner,
    remove: async (id: string): Promise<Response> => {
      state.entries = state.entries.filter(entry => entry.id !== id);
      return response({ deleted: true });
    },
  };
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), 'http://localhost');
    if (url.pathname.endsWith('/session/me')) return response(state.user);
    if (url.pathname.endsWith('/session/users')) return response([owner]);
    if (url.pathname.endsWith('/platform-option-categories')) return response([]);
    if (url.pathname === '/api/v1/components') return response(state.entries);
    if (url.pathname.endsWith('/usage')) return response({ components: [], scenarios: [], componentCount: 0, scenarioCount: 0 });
    if (/\/components\/[^/]+$/.test(url.pathname)) {
      const id = url.pathname.split('/').pop()!;
      if (init?.method === 'DELETE') return state.remove(id);
      const entry = state.entries.find(item => item.id === id);
      if (!entry) return new Response(JSON.stringify({ error: { code: 'not_found', message: '组件不存在' } }), { status: 404 });
      return response(entry);
    }
    throw new Error(`Unexpected request: ${init?.method ?? 'GET'} ${url.pathname}`);
  });
  vi.stubGlobal('fetch', fetch);
  return { state, fetch, deletes: () => fetch.mock.calls.filter(([, init]) => init?.method === 'DELETE') };
}

function ContextProbe() {
  const location = useLocation();
  const { refreshTokens } = useApp();
  return <><output data-testid="location">{location.pathname}{location.search}</output><output data-testid="workbench-refresh">{refreshTokens.workbench}</output></>;
}
function open(id = 'empty-a') {
  return render(<MemoryRouter initialEntries={[`/components?selected=${id}`]}><AppProvider><ComponentsPage /><ContextProbe /></AppProvider></MemoryRouter>);
}
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it('shows delete only when the backend confirms the owner can delete, and cancel sends no request', async () => {
  const f = fixture([empty('empty-a')]);
  const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
  open();
  await userEvent.click(await screen.findByRole('button', { name: '删除组件' }));
  expect(confirm).toHaveBeenCalledWith(expect.stringContaining('确认永久删除空组件“empty-a”'));
  expect(confirm).toHaveBeenCalledWith(expect.stringContaining('此操作不可恢复'));
  expect(f.deletes()).toHaveLength(0);
  expect(screen.getByRole('heading', { name: 'empty-a' })).toBeInTheDocument();
});

it.each(['nonempty', 'referenced', 'missing-eligibility', 'other-owner', 'admin'])('hides deletion for %s components', async (condition) => {
  const entry: Entry = empty('empty-a', condition === 'other-owner' || condition === 'admin');
  if (condition === 'missing-eligibility') entry.canDelete = undefined;
  if (condition === 'nonempty') entry.releaseCount = 1;
  if (condition === 'other-owner') entry.ownerId = 'component-owner-b';
  const f = fixture([entry]);
  if (condition === 'admin') f.state.user = { id: 'admin', name: 'Admin', role: 'platform_admin' };
  open();
  await screen.findByRole('heading', { name: 'empty-a' });
  expect(screen.queryByRole('button', { name: '删除组件' })).not.toBeInTheDocument();
  expect(f.deletes()).toHaveLength(0);
});

it('clears the deleted selection and uses the refreshed catalog order', async () => {
  const f = fixture([empty('empty-a'), empty('empty-b')]);
  f.state.remove = async () => {
    f.state.entries = [empty('empty-c'), empty('empty-b')];
    return response({ deleted: true });
  };
  vi.spyOn(window, 'confirm').mockReturnValue(true);
  open();
  await userEvent.click(await screen.findByRole('button', { name: '删除组件' }));
  expect(await screen.findByText('组件已永久删除')).toBeInTheDocument();
  expect(await screen.findByRole('heading', { name: 'empty-c' })).toBeInTheDocument();
  expect(screen.queryByRole('heading', { name: 'empty-a' })).not.toBeInTheDocument();
  expect(screen.getByTestId('location')).toHaveTextContent(/^\/components$/);
  expect(Number(screen.getByTestId('workbench-refresh').textContent)).toBeGreaterThan(0);
  expect(f.deletes()).toHaveLength(1);
});

it('shows the empty catalog after deleting the last component', async () => {
  fixture([empty('empty-a')]);
  vi.spyOn(window, 'confirm').mockReturnValue(true);
  open();
  await userEvent.click(await screen.findByRole('button', { name: '删除组件' }));
  expect(await screen.findByText('暂无组件')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '删除组件' })).not.toBeInTheDocument();
  expect(screen.queryByText('组件不存在')).not.toBeInTheDocument();
  expect(screen.getByTestId('location')).toHaveTextContent(/^\/components$/);
});

it('prevents duplicate deletion while the request is pending', async () => {
  const f = fixture([empty('empty-a')]);
  let finish!: (value: Response) => void;
  f.state.remove = () => new Promise(resolve => { finish = resolve; });
  vi.spyOn(window, 'confirm').mockReturnValue(true);
  open();
  const button = await screen.findByRole('button', { name: '删除组件' });
  fireEvent.click(button);
  fireEvent.click(button);
  expect(screen.getByRole('button', { name: '删除中…' })).toBeDisabled();
  expect(f.deletes()).toHaveLength(1);
  await act(async () => { f.state.entries = []; finish(response({ deleted: true })); });
  await screen.findByText('暂无组件');
});

it('refreshes a rejected deletion and hides the now-ineligible action', async () => {
  const f = fixture([empty('empty-a')]);
  f.state.remove = async () => {
    f.state.entries = [empty('empty-a', false)];
    return new Response(JSON.stringify({ error: { code: 'conflict', message: '该组件仍有引用，不能删除' } }), { status: 409 });
  };
  vi.spyOn(window, 'confirm').mockReturnValue(true);
  open();
  await userEvent.click(await screen.findByRole('button', { name: '删除组件' }));
  expect(await screen.findByText('删除组件失败')).toBeInTheDocument();
  expect(screen.getByText('该组件仍有引用，不能删除')).toBeInTheDocument();
  await waitFor(() => expect(screen.queryByRole('button', { name: '删除组件' })).not.toBeInTheDocument());
  expect(screen.getByRole('heading', { name: 'empty-a' })).toBeInTheDocument();
  expect(screen.getByTestId('location')).toHaveTextContent('selected=empty-a');
  expect(f.fetch.mock.calls.filter(([input, init]) => String(input) === '/api/v1/components/empty-a' && !init?.method)).toHaveLength(2);
});

it('preserves a new selection when an earlier deletion completes', async () => {
  const f = fixture([empty('empty-a'), empty('empty-b')]);
  let finish!: (value: Response) => void;
  f.state.remove = () => new Promise(resolve => { finish = resolve; });
  vi.spyOn(window, 'confirm').mockReturnValue(true);
  open();
  await userEvent.click(await screen.findByRole('button', { name: '删除组件' }));
  await userEvent.click(screen.getByRole('button', { name: 'empty-b 暂无标签' }));
  await screen.findByRole('heading', { name: 'empty-b' });
  await act(async () => { f.state.entries = [empty('empty-b')]; finish(response({ deleted: true })); });
  expect(screen.getByRole('heading', { name: 'empty-b' })).toBeInTheDocument();
  expect(screen.getByTestId('location')).toHaveTextContent('selected=empty-b');
  expect(screen.queryByText('组件已永久删除')).not.toBeInTheDocument();
});

it('refreshes component eligibility and the workbench on the deletion SSE event', async () => {
  fixture([empty('empty-a')]);
  open();
  await screen.findByRole('button', { name: '删除组件' });
  const before = Number(screen.getByTestId('workbench-refresh').textContent);
  act(() => EventSourceMock.instances.at(-1)?.emit('component.deleted', { componentId: 'other' }));
  await waitFor(() => expect(Number(screen.getByTestId('workbench-refresh').textContent)).toBeGreaterThan(before));
});
