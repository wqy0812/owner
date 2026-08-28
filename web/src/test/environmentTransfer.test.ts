import { describe, expect, it } from 'vitest';
import { environmentDocumentContainsCredentialReferences } from '../pages/EnvironmentsPage';
import type { EnvironmentExportDocument } from '../types/domain';

function document(reference?: string, declared = false): EnvironmentExportDocument {
  return {
    formatVersion: 'clusterforge-environment/v1',
    exportedAt: '2026-08-27T00:00:00Z',
    containsCredentialReferences: declared,
    source: { environmentId: 'environment-source', environmentName: 'Source', revisionId: 'revision-source', revision: 1 },
    snapshot: {
      facts: {}, hosts: [], variables: {},
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
