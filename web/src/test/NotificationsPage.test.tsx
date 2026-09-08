import { beforeEach, describe, expect, it } from 'vitest';
import { user as userEvent } from './interactions';
import { screen, waitFor } from './render';

import { NotificationsPage } from '../pages/NotificationsPage';
import { alice, installFetch } from './fixtures/appFixtures';
import { pageRenderer } from './pageRenderer';
const renderApp = pageRenderer(NotificationsPage, '/notifications');

beforeEach(() => { installFetch(); });

describe("NotificationsPage", () => {
  it('filters impact notifications and marks a selected notification as read', async () => {
    const fetchMock = installFetch({
      notifications: [
        {
          id: 'notification-1', userId: alice.id, type: 'release.updated', title: 'containerd 发布新版本',
          body: '场景可能受到上游版本影响', read: false, resourceUrl: '/components?selected=component-containerd',
          payload: { componentId: 'component-containerd', componentName: 'containerd', oldVersion: '2.1.0', newVersion: '2.1.1', breaking: true, paths: [['containerd', 'kubelet']], scenarioIds: ['scenario-kubernetes'] },
          createdAt: '2026-08-30T00:00:00Z',
        },
      ]
    });
    renderApp('/notifications?selected=notification-1');

    expect(await screen.findByRole('heading', { name: '通知中心' })).toBeInTheDocument();
    expect(await screen.findByText('containerd 发布新版本')).toBeInTheDocument();
    expect(screen.getByText(/containerd\s+→\s+kubelet/, { selector: '.notification-impact span' })).toBeInTheDocument();
    expect(screen.getByText('BREAKING')).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: /标为已读/ }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining('/notifications/notification-1'), expect.objectContaining({ method: 'PATCH' })));
    await userEvent.click(screen.getByRole('tab', { name: /^未读/ }));
    expect(await screen.findByText('没有未读通知')).toBeInTheDocument();
  });
});
