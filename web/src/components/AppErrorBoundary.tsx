import { Component, type ErrorInfo, type ReactNode } from 'react';

interface Props {
  children: ReactNode;
}

interface State {
  error?: Error;
}

export class AppErrorBoundary extends Component<Props, State> {
  state: State = {};

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('ClusterForge render failed', error, info.componentStack);
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <main role="alert" style={{ minHeight: '100vh', display: 'grid', placeContent: 'center', gap: 12, padding: 32, boxSizing: 'border-box', background: '#f5f7fb', color: '#18233d', textAlign: 'center' }}>
        <h1 style={{ margin: 0 }}>页面加载失败</h1>
        <p style={{ margin: 0 }}>前台运行时发生异常，请重新加载页面。</p>
        <button type="button" className="button button--primary" style={{ justifySelf: 'center' }} onClick={() => window.location.reload()}>重新加载</button>
      </main>
    );
  }
}
