import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, api } from '../api/client';

function response(body: string, contentType: string, status = 200) {
  return new Response(body, { status, headers: { 'Content-Type': contentType } });
}

afterEach(() => vi.unstubAllGlobals());

describe('API response contract', () => {
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
        id: 'release-1', componentId: 'component-1', version: '1.0.0', type: 'atomic', status: 'draft',
        riskLevel: 'high', parameters: [], dependencies: [], actions: [], artifacts: [],
      },
    }), 'application/json'));
    vi.stubGlobal('fetch', fetchMock);

    await expect(api.createRelease('component-1', { version: '1.0.0', type: 'atomic', riskLevel: 'high' })).resolves.toMatchObject({ riskLevel: 'high' });
    expect(JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body))).toMatchObject({ riskLevel: 'high' });
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
