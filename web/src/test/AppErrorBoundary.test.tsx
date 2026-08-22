import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { AppErrorBoundary } from '../components/AppErrorBoundary';

function BrokenPage(): never {
  throw new Error('render failed');
}

describe('AppErrorBoundary', () => {
  it('keeps an accessible recovery UI when rendering fails', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    render(<AppErrorBoundary><BrokenPage /></AppErrorBoundary>);
    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '页面加载失败' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '重新加载' })).toBeInTheDocument();
  });
});
