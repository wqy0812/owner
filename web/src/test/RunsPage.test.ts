import { describe, expect, it } from 'vitest';
import { runFailureSummary } from '../pages/RunsPage';
import type { Run } from '../types/domain';

describe('runFailureSummary', () => {
  it('surfaces the failing component, host, and useful log line', () => {
    const run: Run = {
      id: 'run-failed', environmentId: 'environment-1', status: 'failed',
      steps: [{ id: 'step-coredns', name: 'CoreDNS verify', componentName: 'CoreDNS', action: 'verify', status: 'failed', summary: 'exit code 2' }],
      logTail: ['[stdout] ok: [node-1]', '[stdout] fatal: [master-1]: FAILED! => {"msg":"DNS resolution failed"}'],
    };
    expect(runFailureSummary(run)).toMatchObject({
      title: 'CoreDNS · verify失败', host: 'master-1', stepId: 'step-coredns',
    });
    expect(runFailureSummary(run)?.detail).toContain('DNS resolution failed');
  });

  it('does not create a failure card for successful runs', () => {
    expect(runFailureSummary({ id: 'run-ok', environmentId: 'environment-1', status: 'succeeded' })).toBeUndefined();
  });
});
