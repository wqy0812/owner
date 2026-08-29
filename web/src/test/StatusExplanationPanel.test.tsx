import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { StatusExplanationPanel } from '../components/StatusExplanationPanel';

describe('StatusExplanationPanel', () => {
  it('keeps only the title visible until all reasons are expanded on demand', async () => {
    render(<MemoryRouter><StatusExplanationPanel explanation={{
      reasons: [
        { code: 'install-action', message: '缺少 Install 生命周期动作' },
        { code: 'verify-action', message: '缺少 Verify 生命周期动作' },
        { code: 'rollback-action', message: '缺少 Rollback 生命周期动作' },
        { code: 'install-evidence', message: '当前合同缺少安装及 Verify 成功证据' },
        { code: 'rollback-evidence', message: '当前合同缺少回滚及回滚后验证证据' },
        { code: 'dependency', message: '依赖 Release 不是 Released 或共享候选' },
      ],
      secondaryActions: [],
    }} /></MemoryRouter>);

    expect(screen.getByRole('heading', { name: '当前操作被阻断' })).toBeVisible();
    expect(screen.getByText('缺少 Install 生命周期动作')).not.toBeVisible();
    expect(screen.getByText('缺少 Verify 生命周期动作')).not.toBeVisible();
    expect(screen.getByText('依赖 Release 不是 Released 或共享候选')).not.toBeVisible();

    const toggle = screen.getByRole('button', { name: '展开 6 项原因' });
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    await userEvent.click(toggle);

    expect(screen.getByText('缺少 Install 生命周期动作')).toBeVisible();
    expect(screen.getByText('缺少 Verify 生命周期动作')).toBeInTheDocument();
    expect(screen.getByText('依赖 Release 不是 Released 或共享候选')).toBeInTheDocument();
    const collapse = screen.getByRole('button', { name: '收起原因' });
    expect(collapse).toHaveAttribute('aria-expanded', 'true');
    await userEvent.click(collapse);
    expect(screen.getByText('缺少 Install 生命周期动作')).not.toBeVisible();
  });
});
