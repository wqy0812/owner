import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { BuildVersionGuard } from '../components/BuildVersionGuard';

afterEach(() => {
  vi.unstubAllGlobals();
});

function response(version: string) {
  return { ok: true, json: async () => ({ version }) } as Response;
}

describe('BuildVersionGuard', () => {
  it('keeps the current UI available when the server build matches', async () => {
    const fetchMock = vi.fn().mockResolvedValue(response('build-a'));
    vi.stubGlobal('fetch', fetchMock);
    render(<BuildVersionGuard loadedVersion="build-a"><button>旧页面动作</button></BuildVersionGuard>);

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: '旧页面动作' })).toBeEnabled();
  });

  it('blocks the old UI and offers a forced refresh when a new build appears', async () => {
    const reload = vi.fn();
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response('build-b')));
    const { container } = render(
      <BuildVersionGuard loadedVersion="build-a" reload={reload}><button>旧回滚提交</button></BuildVersionGuard>,
    );

    expect(await screen.findByRole('alertdialog')).toHaveTextContent('请刷新后继续操作');
    expect(container.querySelector('.build-version-guard__content')).toHaveAttribute('inert');
    expect(container.querySelector('.build-version-guard__content')).toHaveAttribute('aria-hidden', 'true');
    await userEvent.click(screen.getByRole('button', { name: '刷新使用新版本' }));
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it('checks again when a background page becomes active', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response('build-a'))
      .mockResolvedValueOnce(response('build-b'));
    vi.stubGlobal('fetch', fetchMock);
    render(<BuildVersionGuard loadedVersion="build-a"><span>内容</span></BuildVersionGuard>);
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));

    fireEvent.focus(window);
    expect(await screen.findByRole('alertdialog')).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('ignores temporary version endpoint failures', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('offline')));
    render(<BuildVersionGuard loadedVersion="build-a"><span>内容</span></BuildVersionGuard>);

    await waitFor(() => expect(screen.getByText('内容')).toBeInTheDocument());
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
  });
});
