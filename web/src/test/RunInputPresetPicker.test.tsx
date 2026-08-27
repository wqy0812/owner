import { act, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { RunInputPresetPicker } from '../components/RunInputPresetPicker';
import type { RunInputPreset } from '../types/domain';

const mocks = vi.hoisted(() => ({
  runInputPresets: vi.fn(),
  saveRunInputPreset: vi.fn(),
  deleteRunInputPreset: vi.fn(),
  notify: vi.fn(),
}));

vi.mock('../api/client', () => ({
  api: {
    runInputPresets: mocks.runInputPresets,
    saveRunInputPreset: mocks.saveRunInputPreset,
    deleteRunInputPreset: mocks.deleteRunInputPreset,
  },
}));

vi.mock('../context/AppContext', () => ({
  displayError: (reason: unknown) => String(reason),
  useApp: () => ({ notify: mocks.notify }),
}));

interface PendingRequest {
  signal?: AbortSignal;
  resolve: (presets: RunInputPreset[]) => void;
}

function preset(id: string, resourceId: string): RunInputPreset {
  return {
    id, createdBy: 'component-owner', resourceType: 'component_release', resourceId,
    context: 'component_install_verify', name: id, values: { runInput: {} },
    definitionDigest: 'digest', stale: false,
    createdAt: '2026-08-27T00:00:00Z', updatedAt: '2026-08-27T00:00:00Z',
  };
}

afterEach(() => vi.clearAllMocks());

describe('RunInputPresetPicker', () => {
  it('aborts and ignores an older resource request after the context changes', async () => {
    const pending: PendingRequest[] = [];
    mocks.runInputPresets.mockImplementation((_type: string, _id: string, _context: string, signal?: AbortSignal) => new Promise<RunInputPreset[]>((resolve) => {
      pending.push({ signal, resolve });
    }));
    const view = render(<RunInputPresetPicker resourceType="component_release" resourceId="release-a" context="component_install_verify" values={{}} onApply={() => {}} />);
    await waitFor(() => expect(pending).toHaveLength(1));

    view.rerender(<RunInputPresetPicker resourceType="component_release" resourceId="release-b" context="component_install_verify" values={{}} onApply={() => {}} />);
    await waitFor(() => expect(pending).toHaveLength(2));
    expect(pending[0].signal?.aborted).toBe(true);

    await act(async () => pending[1].resolve([preset('preset-b', 'release-b')]));
    expect(await screen.findByRole('option', { name: 'preset-b' })).toBeInTheDocument();

    await act(async () => pending[0].resolve([preset('preset-a', 'release-a')]));
    expect(screen.queryByRole('option', { name: 'preset-a' })).not.toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'preset-b' })).toBeInTheDocument();
  });
});
