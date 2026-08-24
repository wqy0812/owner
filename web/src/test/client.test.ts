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
});
