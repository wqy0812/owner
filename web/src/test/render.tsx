import { render as testingRender, type RenderOptions } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import type { ReactElement, ReactNode } from 'react';
import { UIProvider } from '../components/UIProvider';

export * from '@testing-library/react';
export function render(ui: ReactElement, options?: RenderOptions) {
  const ExistingWrapper = options?.wrapper;
  return testingRender(ui, { ...options, wrapper: ({children}: {children: ReactNode}) => <ConfigProvider virtual={false} theme={{ token: { motion: false } }}><UIProvider>{ExistingWrapper ? <ExistingWrapper>{children}</ExistingWrapper> : children}</UIProvider></ConfigProvider> });
}
