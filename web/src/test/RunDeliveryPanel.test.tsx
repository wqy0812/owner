import { render, screen } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { api } from '../api/client';
import { RunDeliveryPanel } from '../components/RunDeliveryPanel';
import type { Run } from '../types/domain';

vi.mock('../context/AppContext', () => ({ useApp: () => ({ user: { id: 'owner' } }), displayError: (error: unknown) => String(error) }));
beforeEach(() => vi.restoreAllMocks());
it('shows the locked download scope before every stage has begun', async () => {
  vi.spyOn(api, 'verifiedJobEligibility').mockResolvedValue({ eligible: false, reason: '业务验收尚未完成', stageCount: 0, evidenceRunIds: [] });
  const run: Run = { id: 'root', environmentId: 'env', status: 'running', jobDigest: 'bundle', steps: [{ id: 'first', name: 'First', status: 'running' }], purposeCounts: { components: 45, finalVerification: 15, acceptance: 1, total: 61 } };
  render(<RunDeliveryPanel run={run} />);
  expect(screen.getByText('本次锁定的执行范围 · 61 步')).toBeVisible();
  expect(await screen.findByText('业务验收尚未完成')).toBeVisible();
  expect(screen.queryByRole('link', { name: '下载已验证完整作业' })).not.toBeInTheDocument();
});
it('separates the current retry scope from the verified root and evidence chain', async () => {
  vi.spyOn(api, 'verifiedJobEligibility').mockResolvedValue({ eligible: true, rootRunId: 'root', stageCount: 61, evidenceRunIds: ['root', 'retry'] });
  render(<RunDeliveryPanel run={{ id: 'retry', environmentId: 'env', status: 'succeeded', jobDigest: 'retry-bundle', retryOfRunId: 'root', purposeCounts: { components: 0, finalVerification: 1, acceptance: 1, total: 2 } }} />);
  expect(screen.getByText('本次续跑选择的阶段 · 2 步')).toBeVisible();
  expect(await screen.findByText('根作业：root · 完整流程 61 步')).toBeVisible();
  expect(screen.getByText('证据链：root → retry')).toBeVisible();
  expect(screen.getByRole('link', { name: '下载已验证完整作业' })).toHaveAttribute('href', '/api/v1/runs/retry/job-bundle?verified=true');
});
