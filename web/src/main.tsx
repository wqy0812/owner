import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';
import './styles.css';
import { App } from './App';
import { AppErrorBoundary } from './components/AppErrorBoundary';
import { BuildVersionGuard } from './components/BuildVersionGuard';
import { AppProvider } from './context/AppContext';

try {
  window.sessionStorage.removeItem('clusterforge:boot-retry');
} catch {
  // Storage can be unavailable in hardened browser profiles.
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <AppErrorBoundary>
        <AppProvider>
          <BuildVersionGuard>
            <App />
          </BuildVersionGuard>
        </AppProvider>
      </AppErrorBoundary>
    </BrowserRouter>
  </React.StrictMode>,
);
