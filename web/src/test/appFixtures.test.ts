import { expect, it } from 'vitest';
import { installFetch, installReadModelFixtures } from './fixtures/appFixtures';
import { unexpectedRequests } from './testLifecycle';

it('exposes lightweight summaries and complete detail and contract read models', async () => {
  installFetch();
  installReadModelFixtures();
  const summary = (await (await fetch('/api/v1/components')).json()).items[0];
  const detail = (await (await fetch(`/api/v1/components/${summary.id}`)).json()).data;
  const contract = (await (await fetch('/api/v1/components?view=contracts')).json()).items[0];
  expect(summary).toMatchObject({ id: 'component-containerd', releaseCount: 1, hasDraft: false });
  expect(summary).not.toHaveProperty('releases');
  expect(summary).not.toHaveProperty('readContext');
  expect(detail.releases).toEqual(contract.releases);
  expect(detail.readContext).toEqual({ evidence: {}, workItems: [], parameterConsumers: [] });
});

it('rejects unmatched reads and mutations instead of returning an empty success response', async () => {
  installFetch();
  installReadModelFixtures();
  await expect(fetch('/api/v1/unknown')).rejects.toThrow('GET /api/v1/unknown');
  await expect(fetch('/api/v1/components/component-containerd/usage', { method: 'DELETE' })).rejects.toThrow('DELETE /api/v1/components/component-containerd/usage');
  expect(unexpectedRequests.splice(0)).toEqual([
    'Unexpected fixture request: GET /api/v1/unknown',
    'Unexpected fixture request: DELETE /api/v1/components/component-containerd/usage',
  ]);
});
