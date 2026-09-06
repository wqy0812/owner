import { render, screen } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { api } from '../api/client';
import { ExecutionPreparationPanel, type PreparationRequest, type PreparationSession } from '../components/ExecutionPreparationPanel';

vi.mock('../context/AppContext', () => ({ useApp: () => ({ user: { id: 'owner' } }), displayError: (error: unknown) => String(error) }));
beforeEach(() => { vi.restoreAllMocks(); sessionStorage.clear(); });

it('restores an existing preparation without inventing dates for unperformed or historical zero-time checks', async () => {
  const request: PreparationRequest = { kind: 'component_test', subjectId: 'release', environmentId: 'env' };
  sessionStorage.setItem('preparation:owner:component_test:release:env', JSON.stringify({ id: 'saved', signature: JSON.stringify(request) }));
  const session: PreparationSession = { id: 'saved', input: request, status: 'cancelled', output: { checks: [
    { id: 'ssh', category: 'connectivity', label: 'SSH', status: 'passed', elapsedMs: 12, startedAt: '2026-09-06T09:00:00Z' },
    { id: 'pending', category: 'prerequisite', label: '尚待检查', status: 'pending', elapsedMs: 0, startedAt: '0001-01-01T00:00:00Z' },
    { id: 'provided', category: 'prerequisite', label: '前置步骤提供', status: 'provided', elapsedMs: 0 },
  ] } };
  const read = vi.spyOn(api, 'preparation').mockResolvedValue(session);
  const create = vi.spyOn(api, 'createPreparation');
  render(<ExecutionPreparationPanel request={request} onPlan={vi.fn()} />);
  expect(await screen.findByText('已取消')).toBeVisible();
  expect(read).toHaveBeenCalledWith('saved');
  expect(create).not.toHaveBeenCalled();
  expect(screen.getAllByText(/检查时间：/)).toHaveLength(1);
  expect(screen.getByText('待检查')).toBeVisible();
  expect(screen.getByText('由前置步骤提供')).toBeVisible();
});
