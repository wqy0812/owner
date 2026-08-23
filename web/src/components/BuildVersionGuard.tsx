import { RefreshCw, TriangleAlert } from 'lucide-react';
import { type ReactNode, useEffect, useRef, useState } from 'react';

const DEFAULT_CHECK_INTERVAL_MS = 60_000;

interface VersionResponse {
  version?: unknown;
}

interface BuildVersionGuardProps {
  children: ReactNode;
  checkIntervalMs?: number;
  loadedVersion?: string;
  reload?: () => void;
}

function versionFromDocument() {
  return document.querySelector<HTMLMetaElement>('meta[name="clusterforge-build-version"]')?.content.trim() ?? '';
}

export function BuildVersionGuard({
  children,
  checkIntervalMs = DEFAULT_CHECK_INTERVAL_MS,
  loadedVersion = versionFromDocument(),
  reload = () => window.location.reload(),
}: BuildVersionGuardProps) {
  const [availableVersion, setAvailableVersion] = useState('');
  const checking = useRef(false);
  const content = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!loadedVersion) return undefined;
    let disposed = false;

    const check = async () => {
      if (checking.current || disposed) return;
      checking.current = true;
      try {
        const response = await fetch(`/version.json?t=${Date.now()}`, {
          cache: 'no-store',
          headers: { Accept: 'application/json' },
        });
        if (!response.ok) return;
        const data = await response.json() as VersionResponse;
        const current = typeof data.version === 'string' ? data.version.trim() : '';
        if (!disposed && current && current !== loadedVersion) setAvailableVersion(current);
      } catch {
        // A temporary network failure must not interrupt the current workflow.
      } finally {
        checking.current = false;
      }
    };

    const checkWhenVisible = () => {
      if (document.visibilityState === 'visible') void check();
    };
    const interval = window.setInterval(() => void check(), checkIntervalMs);
    window.addEventListener('focus', checkWhenVisible);
    document.addEventListener('visibilitychange', checkWhenVisible);
    void check();
    return () => {
      disposed = true;
      window.clearInterval(interval);
      window.removeEventListener('focus', checkWhenVisible);
      document.removeEventListener('visibilitychange', checkWhenVisible);
    };
  }, [checkIntervalMs, loadedVersion]);

  useEffect(() => {
    const node = content.current;
    if (!node) return undefined;
    if (availableVersion) {
      node.setAttribute('inert', '');
      node.setAttribute('aria-hidden', 'true');
    } else {
      node.removeAttribute('inert');
      node.removeAttribute('aria-hidden');
    }
    return () => {
      node.removeAttribute('inert');
      node.removeAttribute('aria-hidden');
    };
  }, [availableVersion]);

  return (
    <>
      <div ref={content} className="build-version-guard__content">{children}</div>
      {availableVersion ? (
        <div className="build-version-backdrop">
          <section
            className="build-version-notice"
            role="alertdialog"
            aria-modal="true"
            aria-labelledby="build-version-title"
            aria-describedby="build-version-description"
          >
            <span className="build-version-notice__icon"><TriangleAlert size={24} /></span>
            <div>
              <small>检测到平台更新</small>
              <h2 id="build-version-title">请刷新后继续操作</h2>
              <p id="build-version-description">当前页面仍在使用旧版前台。为避免按旧执行计划提交，请刷新加载最新版本。</p>
              <code>{loadedVersion} → {availableVersion}</code>
            </div>
            <button type="button" className="button button--primary" autoFocus onClick={reload}>
              <RefreshCw size={14} />刷新使用新版本
            </button>
          </section>
        </div>
      ) : null}
    </>
  );
}
