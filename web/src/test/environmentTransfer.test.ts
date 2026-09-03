import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from '../api/client';
import { environmentDocumentContainsCredentialReferences } from '../pages/EnvironmentsPage';
import type { EnvironmentExportDocument } from '../types/domain';

afterEach(() => vi.unstubAllGlobals());

function document(reference?: string, declared = false): EnvironmentExportDocument {
  return {
    formatVersion: 'clusterforge-environment/v1',
    exportedAt: '2026-08-27T00:00:00Z',
    containsCredentialReferences: declared,
    source: { environmentId: 'environment-source', environmentName: 'Source', revisionId: 'revision-source', revision: 1 },
    snapshot: {
      facts: {}, hosts: [], parameters: {}, variables: {},
      credentialRefs: [{ name: 'SSH_KEY', kind: 'envVarRef', reference }],
    },
  };
}

describe('environment import credential confirmation', () => {
  it('derives confirmation from actual references instead of the document flag', () => {
    expect(environmentDocumentContainsCredentialReferences(document('SSH_KEY_ENV', false))).toBe(true);
    expect(environmentDocumentContainsCredentialReferences(document(undefined, true))).toBe(false);
  });
});

describe('environment import preview response', () => {
  const planResponse = (warnings: unknown, parameterCount: unknown = 0) => new Response(JSON.stringify({
    data: {
      planDigest: 'plan-1', targetKind: 'new', nextRevision: 1,
      hostCount: 0, variableCount: 0, parameterCount, credentialRefCount: 0,
      changes: ['创建新环境'], warnings,
    },
  }), { status: 200, headers: { 'Content-Type': 'application/json' } });

  it('requires warnings to follow the V1 array contract', async () => {
    vi.stubGlobal('fetch', vi.fn()
      .mockResolvedValueOnce(planResponse([]))
      .mockResolvedValueOnce(planResponse(null)));

    await expect(api.previewEnvironmentImport({
      document: document(undefined, false),
      target: { kind: 'new', name: 'Imported Environment' },
      changeReason: '导入环境',
    })).resolves.toMatchObject({ warnings: [], parameterCount: 0 });

    await expect(api.previewEnvironmentImport({
      document: document(undefined, false),
      target: { kind: 'new', name: 'Imported Environment' },
      changeReason: '导入环境',
    })).rejects.toMatchObject({ code: 'INVALID_RESPONSE' });
  });

  it('validates and preserves the parameter count', async () => {
    const input = { document: document(), target: { kind: 'new' as const, name: 'Imported' }, changeReason: 'Import settings' };
    vi.stubGlobal('fetch', vi.fn()
      .mockResolvedValueOnce(planResponse([], 3))
      .mockResolvedValueOnce(planResponse([], null))
      .mockResolvedValueOnce(planResponse([], '3')));
    await expect(api.previewEnvironmentImport(input)).resolves.toMatchObject({ parameterCount: 3 });
    await expect(api.previewEnvironmentImport(input)).rejects.toMatchObject({ code: 'INVALID_RESPONSE' });
    await expect(api.previewEnvironmentImport(input)).rejects.toMatchObject({ code: 'INVALID_RESPONSE' });
  });
});
