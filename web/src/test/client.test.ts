import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api } from '../api/client';

function response(body: string, contentType: string, status = 200) {
  return new Response(body, { status, headers: { 'Content-Type': contentType } });
}

afterEach(() => vi.unstubAllGlobals());

describe('API response contract', () => {
  it('accepts the simplified Component DTO with tags and derived Release readiness', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response(JSON.stringify({ items: [{
      id: 'component-1', name: 'Runtime', slug: 'runtime', ownerId: 'owner-1', layer: 'runtime_state', tags: ['runtime'],
      releases: [{
        id: 'release-1', componentId: 'component-1', lineId: 'line-1', lineName: 'Runtime 1.0', compatibility: 'not_applicable', version: '1.0.0', status: 'draft',
        readiness: { status: 'blocked', blockers: [{ code: 'install_evidence_missing', message: '缺少安装证据', actionUrl: '/components?selected=component-1&action=validate' }] },
        parameters: [], dependencies: [], actions: [], artifacts: [], images: [],
      }],
    }] }), 'application/json')));

    await expect(api.components()).resolves.toMatchObject([{
      id: 'component-1', tags: ['runtime'],
      latestRelease: { readiness: { status: 'blocked', blockers: [{ code: 'install_evidence_missing' }] } },
    }]);
  });

  it('rejects a successful HTML response instead of returning a fabricated object', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response('<html>proxy error</html>', 'text/html')));

    await expect(api.components()).rejects.toMatchObject({ status: 200, code: 'INVALID_RESPONSE' } satisfies Partial<ApiError>);
  });

  it('rejects malformed JSON from a successful response', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response('{bad json', 'application/json')));

    await expect(api.components()).rejects.toMatchObject({ status: 200, code: 'INVALID_RESPONSE' } satisfies Partial<ApiError>);
  });

  it('rejects the old raw-array list envelope', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response(JSON.stringify([]), 'application/json')));

    await expect(api.components()).rejects.toMatchObject({ status: 200, code: 'INVALID_RESPONSE' } satisfies Partial<ApiError>);
  });

  it('keeps only valid top-level impact paths', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response(JSON.stringify({
      data: {
        componentOwners: [{ id: 'owner-1', name: 'Owner' }],
        scenarioOwners: [],
        scenarios: [],
        paths: [['containerd', 'kubernetes'], ['valid', 1], { componentNames: ['legacy'] }],
      },
    }), 'application/json')));

    await expect(api.releaseImpact('release-1')).resolves.toEqual({
      componentOwners: [{ id: 'owner-1', name: 'Owner' }],
      scenarioOwners: [],
      scenarios: [],
      paths: [['containerd', 'kubernetes']],
      scenarioRunCount: 0,
      changeKind: 'new_line',
      lineId: undefined,
      lineName: undefined,
      fromReleaseId: undefined,
      toReleaseId: undefined,
    });
  });

  it('serializes the frontend CredentialRef type as the backend kind field', async () => {
    const fetchMock = vi.fn().mockResolvedValue(response(JSON.stringify({
      data: {
        id: 'environment-1',
        name: 'Test Environment',
        ownerId: 'environment-owner',
        currentRevision: {
          id: 'environment-1-r2',
          environmentId: 'environment-1',
          revision: 2,
          facts: {},
          hosts: [],
          variables: {},
          credentialRefs: [{ name: 'ANSIBLE_PASSWORD', kind: 'envVarRef', reference: 'NEWPLATFORM_ANSIBLE_PASSWORD' }],
        },
      },
    }), 'application/json'));
    vi.stubGlobal('fetch', fetchMock);

    await api.updateCredentialRefs('environment-1', [{
      name: 'ANSIBLE_PASSWORD',
      type: 'envVarRef',
      reference: 'NEWPLATFORM_ANSIBLE_PASSWORD',
    }], '配置 Ansible 凭据');

    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({
      credentialRefs: [{
        name: 'ANSIBLE_PASSWORD',
        kind: 'envVarRef',
        reference: 'NEWPLATFORM_ANSIBLE_PASSWORD',
      }],
      changeReason: '配置 Ansible 凭据',
    });
  });

  it('round-trips the release risk level through the API contract', async () => {
    const fetchMock = vi.fn().mockResolvedValue(response(JSON.stringify({
      data: {
        id: 'release-1', componentId: 'component-1', lineId: 'line-1', lineName: 'Runtime 1.0', compatibility: 'not_applicable', version: '1.0.0', status: 'draft',
        riskLevel: 'high', readiness: { status: 'blocked', blockers: [] }, parameters: [], dependencies: [], actions: [], artifacts: [], images: [],
      },
    }), 'application/json'));
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.createReleaseDraft('component-1', { mode: 'new_line', lineName: 'Runtime 1.0', version: '1.0.0', releaseNotes: 'baseline', compatibility: 'not_applicable', riskLevel: 'high', expectedPlanDigest: 'plan-1' })).resolves.toMatchObject({ riskLevel: 'high' });
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toMatchObject({ riskLevel: 'high' });
  });

  it('serializes content-source repair requests and normalizes immutable identities', async () => {
    const artifact = {
      id: 'artifact-1', releaseId: 'release-1', alias: 'runtime', filename: 'runtime.tgz', sha256: 'a'.repeat(64), sizeBytes: 42,
      sourceUrl: 'https://source-b.test/runtime.tgz', sourceUpdatedBy: 'owner-1', sourceUpdatedAt: '2026-08-28T10:00:00Z',
      createdBy: 'owner-1', createdAt: '2026-08-28T09:00:00Z',
    };
    const image = {
      id: 'image-1', releaseId: 'release-1', logicalName: 'main', digest: `sha256:${'b'.repeat(64)}`,
      sourceRef: 'registry-b.test/runtime:1.0.0', sourceUpdatedBy: 'owner-1', sourceUpdatedAt: '2026-08-28T10:00:00Z',
      createdBy: 'owner-1', createdAt: '2026-08-28T09:00:00Z',
    };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response(JSON.stringify({ data: artifact }), 'application/json'))
      .mockResolvedValueOnce(response(JSON.stringify({ data: image }), 'application/json'));
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.updateArtifactSource('release-1', 'runtime/linux amd64', artifact.sourceUrl)).resolves.toMatchObject({ sha256: artifact.sha256, sourceUrl: artifact.sourceUrl });
    await expect(api.registerImage('release-1', { logicalName: 'main', sourceRef: image.sourceRef, digest: image.digest })).resolves.toMatchObject({ digest: image.digest, sourceRef: image.sourceRef });

    expect(fetchMock.mock.calls[0]?.[0]).toContain('/component-releases/release-1/artifacts/runtime%2Flinux%20amd64/source');
    expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ method: 'PATCH' });
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({ sourceUrl: artifact.sourceUrl });
    expect(JSON.parse(String(fetchMock.mock.calls[1]?.[1]?.body))).toEqual({ logicalName: 'main', sourceRef: image.sourceRef, digest: image.digest });
  });

  it('carries approval reason and per-item delivery decisions and validates the response', async () => {
    const run = {
      id: 'run-1', status: 'running', environmentId: 'environment-1',
      deliveryRequirements: [{
        id: 'artifact:release-1:runtime', kind: 'artifact', name: 'runtime', identity: `sha256:${'a'.repeat(64)}`,
        source: 'https://source.test/runtime.tgz', target: 'http://fss.test/components/runtime.tgz', sourceReadable: true,
        targetPresent: false, transferAvailable: true, componentName: 'Runtime',
      }],
      deliveryDecisions: [{ requirementId: 'artifact:release-1:runtime', mode: 'transfer', decidedBy: 'environment-owner', decidedAt: '2026-08-28T10:00:00Z' }],
      deliveryResults: [{ requirementId: 'artifact:release-1:runtime', mode: 'transfer', status: 'pending' }],
      artifactTransfers: [{
        alias: 'runtime', sourceUrl: 'https://source.test/runtime.tgz', targetStation: 'fss.test',
        relativePath: 'components/runtime.tgz', sha256: 'a'.repeat(64),
      }],
    };
    const fetchMock = vi.fn().mockResolvedValue(response(JSON.stringify({ data: run }), 'application/json'));
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.approve('approval-1', '允许复制到环境侧', [{ requirementId: 'artifact:release-1:runtime', mode: 'transfer' }])).resolves.toMatchObject({
      id: 'run-1', deliveryRequirements: [{ kind: 'artifact', transferAvailable: true }],
      deliveryDecisions: [{ mode: 'transfer', decidedBy: 'environment-owner' }], deliveryResults: [{ status: 'pending' }],
      artifactTransfers: [{ sourceUrl: 'https://source.test/runtime.tgz', targetStation: 'fss.test' }],
    });
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toEqual({
      reason: '允许复制到环境侧', deliveryDecisions: [{ requirementId: 'artifact:release-1:runtime', mode: 'transfer' }],
    });
  });

  it('rejects malformed Release readiness and delivery result enums', async () => {
    const invalidRelease = {
      id: 'component-1', name: 'Runtime', slug: 'runtime', ownerId: 'owner-1', layer: 'runtime_state', tags: [],
      releases: [{ id: 'release-1', componentId: 'component-1', version: '1.0.0', status: 'draft', readiness: { status: 'unknown', blockers: [] } }],
    };
    const invalidRun = {
      id: 'run-1', status: 'running', environmentId: 'environment-1',
      deliveryResults: [{ requirementId: 'artifact:release-1:runtime', mode: 'transfer', status: 'copied' }],
    };
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response(JSON.stringify({ items: [invalidRelease] }), 'application/json'))
      .mockResolvedValueOnce(response(JSON.stringify({ data: invalidRun }), 'application/json'));
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.components()).rejects.toMatchObject({ code: 'INVALID_RESPONSE' });
    await expect(api.run('run-1')).rejects.toMatchObject({ code: 'INVALID_RESPONSE' });
  });

  it('validates and normalizes the workbench response', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response(JSON.stringify({ data: {
      generatedAt: '2026-08-25T10:00:00Z', role: 'component_owner',
      summary: { critical: 1, actionRequired: 1, inProgress: 0, informational: 0 },
      assets: { components: 1, scenarios: 0, environments: 0 },
      items: [{
        id: 'component_draft:release-1', kind: 'component_draft', priority: 'critical', status: 'blocked', title: 'Draft 尚不可发布',
        subject: { type: 'component_release', id: 'release-1', parentId: 'component-1', name: 'containerd', version: '1.0.0' },
        reasons: [{ code: 'release.validation_failed', message: '环境验证失败', evidenceRunId: 'run-1' }],
        primaryAction: { label: '查看失败运行', href: '/runs?selected=run-1' }, secondaryActions: [], updatedAt: '2026-08-25T09:00:00Z',
      }],
    } }), 'application/json')));

    await expect(api.workbench()).resolves.toMatchObject({
      role: 'component_owner', summary: { critical: 1 },
      items: [{ id: 'component_draft:release-1', reasons: [{ evidenceRunId: 'run-1' }] }],
    });
  });

  it('keeps structured why and next-step data on actionable API conflicts', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response(JSON.stringify({ error: {
      code: 'conflict', message: '执行计划已变化',
      explanation: {
        reasons: [{
          code: 'execution.plan_changed', message: '环境或版本变化后旧预览已失效',
          cause: { kind: 'platform_rule', summary: '提交时重新规划得到不同摘要' },
          nextAction: { label: '重新预览', href: '/components?selected=component-1&release=release-1&action=validate' },
        }],
        primaryAction: { label: '重新预览', href: '/components?selected=component-1&release=release-1&action=validate' },
        secondaryActions: [],
      },
    } }), 'application/json', 409)));

    await expect(api.publishRelease('release-1')).rejects.toMatchObject({
      status: 409,
      explanation: {
        reasons: [{ code: 'execution.plan_changed', message: '环境或版本变化后旧预览已失效', cause: { kind: 'platform_rule', summary: '提交时重新规划得到不同摘要' }, nextAction: { label: '重新预览', href: '/components?selected=component-1&release=release-1&action=validate' } }],
        primaryAction: { label: '重新预览', href: '/components?selected=component-1&release=release-1&action=validate' },
        secondaryActions: [],
      },
    } satisfies Partial<ApiError>);
  });
});
