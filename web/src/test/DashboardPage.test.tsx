import { beforeEach, expect, it, vi } from 'vitest';
import { DashboardPage } from '../pages/DashboardPage';
import { admin, alice, dave, defaultResponse, installFetch, json } from './fixtures/appFixtures';
import { user as userEvent } from './interactions';
import { pageRenderer } from './pageRenderer';
import { act, screen, waitFor, within } from './render';
import { pendingResponse } from './testLifecycle';
const renderApp = pageRenderer(DashboardPage, '/');
beforeEach(() => { installFetch(); });

it('loads platform-admin review work, requires a full preview, and refreshes after a digest-guarded decision', async () => {
  const baseFetch = installFetch({ initialUser: admin });
  let resolveWorkbench!: (response: Response) => void;
  let resolvePreview!: (response: Response) => void;
  let workbenchReads = 0;
  let previewReads = 0;
  let decisionAttempts = 0;
  let handled = false;
  const reviewRelease = {
    id: 'release-review-ui', componentId: 'component-review-ui', version: '1.0.0-p1', status: 'draft',
    lineId: 'line-review-ui', lineName: 'Kubernetes 1.17', compatibility: 'not_applicable', releaseNotes: '参数化独立基线', riskLevel: 'medium',
    environmentConstraints: { architecture: ['amd64'], operatingSystem: ['Ubuntu'] },
    review: { status: 'pending', contractDigest: 'contract-digest', submittedAt: '2026-09-02T08:00:00Z' },
    parameters: [{ name: 'join_ttl', description: '加入令牌有效期', type: 'string', required: true, visibility: 'internal', modifiable: false, valueProvider: 'component_owner', fixedValue: '2h' }],
    dependencies: [{ kind: 'execution', id: 'dependency-review', upstreamReleaseId: 'release-runtime', upstreamComponentId: 'component-runtime', upstreamComponentName: 'Runtime', upstreamVersion: '2.0.0-p1', purpose: '容器运行时', parameterMappings: [{ upstreamParameter: 'socket', targetParameter: 'runtime_socket' }] }],
    artifacts: [{ id: 'artifact-review', releaseId: 'release-review-ui', alias: 'package', filename: 'package.tgz', sha256: 'a'.repeat(64), sizeBytes: 128, sourceUrl: 'https://files.example.test/package.tgz', sourceUpdatedBy: alice.id, sourceUpdatedAt: '2026-09-02T07:00:00Z', createdBy: alice.id, createdAt: '2026-09-02T07:00:00Z' }],
    images: [{ id: 'image-review', releaseId: 'release-review-ui', logicalName: 'control-plane', digest: `sha256:${'b'.repeat(64)}`, sourceRef: 'registry.example.test/control-plane:1.0', sourceUpdatedBy: alice.id, sourceUpdatedAt: '2026-09-02T07:00:00Z', createdBy: alice.id, createdAt: '2026-09-02T07:00:00Z' }],
    actions: [{ id: 'action-install-review', releaseId: 'release-review-ui', name: 'Install', kind: 'install', playbook: 'managed/review/install.yml', hostGroup: 'control_plane', requiredCredentials: ['ansible_ssh_pass'], timeoutSeconds: 1800, riskLevel: 'medium', idempotent: true }],
  };
  const workbench = () => ({
    generatedAt: '2026-09-02T08:01:00Z', role: admin.role,
    summary: { critical: 0, actionRequired: handled ? 0 : 1, inProgress: 0, informational: 0 }, assets: { components: 0, scenarios: 0, environments: 0 },
    items: handled ? [] : [{
      id: 'component_review:release-review-ui', kind: 'component_review', priority: 'high', status: 'action_required',
      title: 'Kubernetes Host Bootstrap 1.0.0-p1 等待合同审核', subject: { type: 'component_release', id: 'release-review-ui', parentId: 'component-review-ui', name: 'Kubernetes Host Bootstrap', version: '1.0.0-p1' },
      reasons: [{ code: 'component_release.review_pending', message: '组件 Owner 已提交当前 Release 合同', cause: { kind: 'review_submission', summary: 'Kubernetes 1.17 · 合同摘要 contract-digest', at: '2026-09-02T08:00:00Z' } }],
      primaryAction: { label: '预览并审核', href: '/?review=release-review-ui' }, secondaryActions: [], updatedAt: '2026-09-02T08:00:00Z',
    }],
  });
  const preview = (digest: string) => ({
    componentId: 'component-review-ui', componentName: 'Kubernetes Host Bootstrap', ownerId: alice.id, ownerName: alice.name,
    release: reviewRelease,
    playbooks: [{ actionId: 'action-install-review', actionName: 'Install', actionKind: 'install', path: 'managed/review/install.yml', filename: 'install.yml', content: '---\n- hosts: all\n  tasks: []\n', sha256: 'c'.repeat(64) }],
    previewDigest: digest,
  });
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.endsWith('/workbench')) {
      workbenchReads += 1;
      if (workbenchReads === 1) return pendingResponse(resolve => { resolveWorkbench = resolve; }, init?.signal);
      return json(workbench());
    }
    if (url.endsWith('/component-releases/release-review-ui/review-preview')) {
      previewReads += 1;
      if (previewReads === 1) return pendingResponse(resolve => { resolvePreview = resolve; }, init?.signal);
      return json(preview('preview-digest-2'));
    }
    if (url.endsWith('/component-releases/release-review-ui/review-decision') && init?.method === 'POST') {
      decisionAttempts += 1;
      const body = JSON.parse(String(init.body));
      if (decisionAttempts === 1) {
        expect(body.expectedPreviewDigest).toBe('preview-digest-1');
        return json({ error: { code: 'CONFLICT', message: '预览摘要已变化，请重新预览' } }, 409);
      }
      expect(body).toMatchObject({ decision: 'approve', comment: '', expectedPreviewDigest: 'preview-digest-2' });
      handled = true;
      return json({ ...reviewRelease, review: { status: 'approved', contractDigest: 'contract-digest' } });
    }
    return baseFetch(input, init);
  });
  vi.stubGlobal('fetch', fetchMock);

  renderApp();
  expect(await screen.findByText('正在整理我的交付待办…')).toBeInTheDocument();
  expect(screen.queryByText('当前没有待办')).not.toBeInTheDocument();
  await act(async () => resolveWorkbench(await json(workbench())));
  expect(await screen.findByRole('heading', { name: /Kubernetes Host Bootstrap .*等待合同审核/ })).toBeInTheDocument();
  expect(screen.queryByRole('heading', { name: '平台管理' })).not.toBeInTheDocument();

  await userEvent.click(screen.getByRole('button', { name: /预览并审核/ }));
  const dialog = await screen.findByRole('dialog', { name: 'Component Release 合同审核' });
  expect(within(dialog).getByRole('status')).toHaveTextContent('正在加载待审组件');
  expect(within(dialog).queryByRole('button', { name: '批准' })).not.toBeInTheDocument();
  await act(async () => resolvePreview(await json(preview('preview-digest-1'))));

  expect(await within(dialog).findByText('Kubernetes 1.17')).toBeInTheDocument();
  expect(within(dialog).getByText('join_ttl')).toBeInTheDocument();
  expect(within(dialog).getByText('ansible_ssh_pass')).toBeInTheDocument();
  expect(within(dialog).getByText('install.yml')).toBeInTheDocument();
  expect(within(dialog).getByText(/hosts: all/)).toBeInTheDocument();
  expect(within(dialog).getByRole('button', { name: '驳回' })).toBeDisabled();
  expect(within(dialog).getByRole('button', { name: '批准' })).toBeEnabled();

  await userEvent.click(within(dialog).getByRole('button', { name: '批准' }));
  expect(await within(dialog).findByRole('alert')).toHaveTextContent('预览摘要已变化');
  expect(within(dialog).getByRole('button', { name: '批准' })).toBeDisabled();
  await userEvent.click(within(dialog).getByRole('button', { name: /重新预览/ }));
  await waitFor(() => expect(within(dialog).getByText('preview-digest-2')).toBeInTheDocument());
  await userEvent.click(within(dialog).getByRole('button', { name: '批准' }));

  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Component Release 合同审核' })).not.toBeInTheDocument());
  expect(await screen.findByText('当前没有待办')).toBeInTheDocument();
  expect(decisionAttempts).toBe(2);
});

it('shows catalog backup warnings on the 环境 Owner workbench', async () => {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url.endsWith('/session/me')) return json(dave);
    if (url.endsWith('/workbench')) return json({
      generatedAt: '2026-08-29T10:00:00Z', role: dave.role,
      summary: { critical: 1, actionRequired: 1, inProgress: 0, informational: 0 },
      assets: { components: 0, scenarios: 0, environments: 1 },
      items: [{
        id: 'catalog_backup:health', kind: 'catalog_backup', priority: 'critical', status: 'blocked',
        title: '发布目录灾备需要处理',
        subject: { type: 'catalog_repository', id: 'selected', name: '发布目录灾备' },
        reasons: [{
          code: 'catalog_backup.failed', message: '最近一次发布目录备份失败',
          cause: { kind: 'backup_failure', summary: 'git push failed' },
          nextAction: { label: '检查恢复点', href: '/environments#catalog-repository' },
        }],
        primaryAction: { label: '检查发布目录灾备', href: '/environments#catalog-repository' }, secondaryActions: [],
        updatedAt: '2026-08-29T09:00:00Z',
      }],
    });
    return defaultResponse(input, init);
  }));

  renderApp();

  expect(await screen.findByRole('heading', { name: '发布目录灾备需要处理' })).toBeInTheDocument();
  expect(screen.getByText('最近一次发布目录备份失败')).toBeInTheDocument();
  expect(screen.getByText('git push failed')).toBeInTheDocument();
  expect(screen.getByRole('link', { name: /检查发布目录灾备/ })).toHaveAttribute('href', '/environments#catalog-repository');
});
