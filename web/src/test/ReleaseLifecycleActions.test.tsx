import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { api } from '../api/client';
import { useReleaseLifecycleActions } from '../features/components/releases/ReleaseLifecycleActions';
import type { ComponentRelease, ImpactPreview } from '../types/domain';

const app = vi.hoisted(() => ({ notify: vi.fn(), signalRefresh: vi.fn() }));
vi.mock('../context/AppContext', () => ({ useApp: () => app, displayError: (error: unknown) => String(error) }));
const release = { id: 'release-a', version: '1.0', state: 'deprecated' } as ComponentRelease;
const restored = vi.fn();
function Harness({ scope }: { scope: string }) {
  const flow = useReleaseLifecycleActions({ scopeKey: scope, onRestored: restored, onDeleted: vi.fn() });
  return <><button onClick={() => void flow.previewDeprecate(release)}>Preview</button><button onClick={() => void flow.restoreRelease(release)}>Restore</button>{flow.dialogs}</>;
}
afterEach(() => { vi.restoreAllMocks(); vi.clearAllMocks(); });
it('ignores an obsolete impact response after closing and reopening the preview', async () => {
  let finishFirst!: (value: ImpactPreview) => void;
  const first = new Promise<ImpactPreview>(resolve => { finishFirst = resolve; });
  vi.spyOn(api, 'releaseImpact').mockReturnValueOnce(first).mockResolvedValueOnce({ componentOwners: [], scenarioOwners: [], scenarios: [] } as unknown as ImpactPreview);
  render(<Harness scope="a" />);
  fireEvent.click(screen.getByText('Preview'));
  fireEvent.click(screen.getByText('取消'));
  fireEvent.click(screen.getByText('Preview'));
  await screen.findByRole('button', { name: '确认废弃版本' });
  await act(async () => { finishFirst({ componentOwners: ['stale', 'stale'], scenarioOwners: [], scenarios: [] } as unknown as ImpactPreview); });
  expect(screen.queryByText('2')).not.toBeInTheDocument();
});
it('does not restore the previous selection when an operation completes after scope changed', async () => {
  let finish!: () => void;
  vi.spyOn(window, 'confirm').mockReturnValue(true);
  vi.spyOn(api, 'restoreRelease').mockImplementation(() => new Promise(resolve => { finish = () => resolve(release); }));
  const view = render(<Harness scope="a" />);
  fireEvent.click(screen.getByText('Restore'));
  view.rerender(<Harness scope="b" />);
  await act(async () => { finish(); });
  expect(restored).not.toHaveBeenCalled();
  expect(app.notify).not.toHaveBeenCalled();
  expect(app.signalRefresh).toHaveBeenCalled();
});
