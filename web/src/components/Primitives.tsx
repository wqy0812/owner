import type { ReactNode } from 'react';
import { AlertCircle, Inbox, Info, LoaderCircle, RefreshCcw } from 'lucide-react';
import { STATUS_LABELS } from '../types/domain';

export function StatusPill({ status, children }: { status: string; children?: ReactNode }) {
  const normalized = status?.toLowerCase().replaceAll('-', '_') ?? 'unknown';
  return <span className={`status-pill status-pill--${normalized}`}>{children ?? STATUS_LABELS[normalized] ?? status}</span>;
}

export function InfoNote({ title, children }: { title?: string; children: ReactNode }) {
  return <div className="info-note"><Info size={16} aria-hidden="true" /><div>{title && <strong>{title}</strong>}<p>{children}</p></div></div>;
}

export function PageHeader({
  eyebrow,
  title,
  description,
  actions,
}: {
  eyebrow?: string;
  title: string;
  description?: string;
  actions?: ReactNode;
}) {
  return (
    <header className="page-header">
      <div>
        {eyebrow && <div className="eyebrow">{eyebrow}</div>}
        <h1>{title}</h1>
        {description && <p>{description}</p>}
      </div>
      {actions && <div className="page-header__actions">{actions}</div>}
    </header>
  );
}

export function LoadingBlock({ label = '加载中' }: { label?: string }) {
  return (
    <div className="state-block" role="status" aria-live="polite">
      <LoaderCircle className="spin" size={24} />
      <span>{label}</span>
    </div>
  );
}

export function ErrorBlock({ message, onRetry, title = '暂时无法读取数据' }: { message: string; onRetry?: () => void; title?: string }) {
  return (
    <div className="state-block state-block--error" role="alert">
      <AlertCircle size={24} />
      <div>
        <strong>{title}</strong>
        <span>{message}</span>
      </div>
      {onRetry && (
        <button className="button button--quiet" type="button" onClick={onRetry}>
          <RefreshCcw size={15} /> 重试
        </button>
      )}
    </div>
  );
}

export function RefreshNotice({ loading, error, onRetry }: { loading: boolean; error?: string; onRetry?: () => void }) {
  if (loading) {
    return <div className="state-block state-block--refresh" role="status"><LoaderCircle className="spin" size={16} /><span>正在刷新数据…</span></div>;
  }
  if (!error) return null;
  return (
    <div className="state-block state-block--error state-block--refresh" role="alert">
      <AlertCircle size={16} />
      <div><strong>当前显示的是上次成功的数据</strong><span>{error}</span></div>
      {onRetry && <button className="button button--quiet" type="button" onClick={onRetry}><RefreshCcw size={15} /> 重试</button>}
    </div>
  );
}

export function EmptyState({ title, description, action }: { title: string; description?: string; action?: ReactNode }) {
  return (
    <div className="empty-state">
      <span className="empty-state__icon"><Inbox size={24} /></span>
      <strong>{title}</strong>
      {description && <p>{description}</p>}
      {action}
    </div>
  );
}

export function Modal({ title, description, children, onClose, size = 'default' }: { title: string; description?: string; children: ReactNode; onClose: () => void; size?: 'default' | 'wide' }) {
  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={(event) => event.currentTarget === event.target && onClose()}>
      <section className={`modal${size === 'wide' ? ' modal--wide' : ''}`} role="dialog" aria-modal="true" aria-label={title}>
        <header>
          <div>
            <h2>{title}</h2>
            {description && <p>{description}</p>}
          </div>
          <button className="icon-button" type="button" aria-label="关闭" onClick={onClose}>×</button>
        </header>
        {children}
      </section>
    </div>
  );
}

export function Metric({ label, value, note, tone = 'indigo' }: { label: string; value: string | number; note?: string; tone?: string }) {
  return (
    <article className={`metric metric--${tone}`}>
      <span>{label}</span>
      <strong>{value}</strong>
      {note && <small>{note}</small>}
    </article>
  );
}

export function formatTime(value?: string): string {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(date);
}

export function formatFileSize(bytes: number): string {
 if (bytes < 1024) return `${bytes} B`;
 if (bytes < 1048576) return `${(bytes / 1024).toFixed(1)} KB`;
 if (bytes < 1073741824) return `${(bytes / 1048576).toFixed(1)} MB`;
 return `${(bytes / 1073741824).toFixed(1)} GB`;
}
