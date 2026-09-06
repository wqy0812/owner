import { useEffect, useState } from "react";
import { api } from "../api/client";
import { displayError, useApp } from "../context/AppContext";
import { useApiData } from "../hooks/useApiData";
import { STATUS_LABELS, type RunSummary } from "../types/domain";
import { EmptyState, ErrorBlock, LoadingBlock, formatFileSize, formatTime } from "./Primitives";
import { RunCleanupModal } from "./RunCleanupModal";
const ARCHIVE_STATUS: Record<string, string> = {unarchived:"未归档",queued:"待归档",running:"归档中",archived:"已归档",failed:"归档失败"};
export interface RetentionPolicy {
  autoArchive: boolean;
  autoCleanup: boolean;
  archiveDays: number;
  cleanupDays: number;
}
export interface ArchiveInfo {
  runId: string;
  status: string;
  source: string;
  actorId: string;
  error?: string;
  updatedAt: string;
  archivedAt?: string;
  sizeBytes: number;
  sha256?: string;
}
export interface CleanupItem {
  runId: string;
  eligible: boolean;
  reasons: string[];
}
export interface ArchiveHealth {
  cleanupResults?: Array<{
    runId: string;
    status: string;
    reason: string;
    at: string;
  }>;
  storageError?: string;
  configured: boolean;
  policy: RetentionPolicy;
  pending: number;
  sizeBytes: number;
  tasks: ArchiveInfo[];
  lastScanAt?: string;
  lastScanResult?: string;
}
export function RunRetentionPanel({ runs, onDeleted }: { runs: RunSummary[]; onDeleted: (ids: string[]) => void }) {
  const { user, notify, signalRefresh } = useApp();
  const { data, loading, error, reload } = useApiData(
    (signal) => api.archiveHealth(signal),
    [user.id],
    "runs",
  );
  const [selected, setSelected] = useState<string[]>([]);
  const [cleanupIds, setCleanupIds] = useState<string[]>();
  const [busy, setBusy] = useState(false);
  const [policy, setPolicy] = useState<RetentionPolicy>();
  const pageKey = runs.map((r) => r.id).join(":");
  useEffect(() => setSelected([]), [pageKey]);
  useEffect(() => {
    if (data) setPolicy(data.policy);
  }, [
    data?.policy.autoArchive,
    data?.policy.autoCleanup,
    data?.policy.archiveDays,
    data?.policy.cleanupDays,
  ]);
  useEffect(() => {
    if (!data?.pending) return;
    const timer = window.setInterval(() => void reload(), 3000);
    return () => clearInterval(timer);
  }, [data?.pending, reload]);
  async function act(fn: () => Promise<unknown>, message: string) {
    setBusy(true);
    try {
      await fn();
      notify("success", message);
      signalRefresh([
        "runs",
        "workbench",
        "components",
        "scenarios",
        "environments",
        "notifications",
      ]);
      void reload();
    } catch (e) {
      notify("error", "操作失败", displayError(e));
    } finally {
      setBusy(false);
    }
  }
  const chosen = runs.filter((r) => selected.includes(r.id));
  return (
    <div className="retention-panel">
      <div className="retention-overview"><span>待归档 <strong>{data?.pending ?? 0}</strong></span><span>归档占用 <strong>{formatFileSize(data?.sizeBytes ?? 0)}</strong></span><span>成功记录压缩归档，可下载留存</span></div>
      {loading && !data ? <LoadingBlock label="正在读取历史管理配置…" /> : null}
      {error ? (
        <ErrorBlock message={error} onRetry={() => void reload()} />
      ) : null}
      {data?.storageError ? <p role="alert">{data.storageError}</p> : null}
      <p>
        {data?.configured
          ? "归档目录已配置"
          : "尚未配置持久化归档目录，归档功能不可用。"}
      </p>
      {policy ? (
        <form
          className="retention-policy"
          onSubmit={(e) => {
            e.preventDefault();
            void act(() => api.saveRetention(policy), "自动处理配置已保存");
          }}
        >
          <label>
            <input
              type="checkbox"
              checked={policy.autoArchive}
              disabled={!data?.configured}
              onChange={(e) =>
                setPolicy({ ...policy, autoArchive: e.target.checked })
              }
            />{" "}
            自动归档成功记录
          </label>
          <label>
            归档期限（天）
            <input
              type="number"
              min="1"
              max="36500"
              value={policy.archiveDays}
              onChange={(e) =>
                setPolicy({ ...policy, archiveDays: Number(e.target.value) })
              }
            />
          </label>
          <label>
            <input
              type="checkbox"
              checked={policy.autoCleanup}
              onChange={(e) =>
                setPolicy({ ...policy, autoCleanup: e.target.checked })
              }
            />{" "}
            自动清理失败记录
          </label>
          <label>
            清理期限（天）
            <input
              type="number"
              min="1"
              max="36500"
              value={policy.cleanupDays}
              onChange={(e) =>
                setPolicy({ ...policy, cleanupDays: Number(e.target.value) })
              }
            />
          </label>
          <button className="button button--quiet" disabled={busy}>
            保存配置
          </button>
        </form>
      ) : null}
      <p>{data?.lastScanResult || "自动处理尚未运行"}</p>
      <h3>当前页记录 · 已选 {chosen.length} 条</h3>
      <p>列表与运行中心当前筛选一致。成功记录可归档；失败记录须超过保留期限且无引用，删除后保留最小审计。</p>
      <div className="retention-selection">
        {runs
          .filter((r) => r.status === "succeeded" || r.status === "failed")
          .map((r) => (
            <label key={r.id}>
              <input
                type="checkbox"
                checked={selected.includes(r.id)}
                disabled={busy || ["archived", "queued", "running"].includes(r.archiveStatus ?? "")}
                onChange={(e) =>
                  setSelected((ids) =>
                    e.target.checked
                      ? [...ids, r.id]
                      : ids.filter((id) => id !== r.id),
                  )
                }
              />
              {r.name} · {STATUS_LABELS[r.status] ?? r.status}{r.archiveStatus ? ` · ${ARCHIVE_STATUS[r.archiveStatus] ?? r.archiveStatus}` : ""} ·{" "}
              {r.finishedAt ? formatTime(r.finishedAt) : "无结束时间"} · {r.id}
            </label>
          ))}
      </div>
      {!runs.some(r => r.status === "succeeded" || r.status === "failed") && <EmptyState title="当前页没有可管理的已结束记录" />}
      <div className="row-actions">
        <button
          className="button button--quiet"
          disabled={
            busy ||
            !data?.configured ||
            !!data.storageError ||
            !chosen.length ||
            chosen.some((r) => r.status !== "succeeded" || ["archived", "queued", "running"].includes(r.archiveStatus ?? ""))
          }
          onClick={() =>
            void act(async () => { await api.archiveRuns(selected); setSelected([]); }, "归档任务已受理")
          }
        >
          归档所选成功记录
        </button>
        <button
          className="button button--danger-soft"
          disabled={busy || !chosen.length || chosen.some(r => r.status !== 'failed')}
          onClick={() => setCleanupIds([...selected])}
        >
          删除所选失败记录
        </button>
      </div>
      <h3>最近归档任务</h3>
      <div className="usage-list">
        {data?.tasks.map((t) => (
          <article key={t.runId}>
            <strong>{t.runId}</strong>
            <p>
              {ARCHIVE_STATUS[t.status] ?? t.status}{" "}
              · {formatTime(t.updatedAt)}
            </p>
            {t.error ? <p role="alert">{t.error}</p> : null}
            {t.status === "failed" ? (
              <button
                disabled={busy || !data.configured}
                onClick={() =>
                  void act(() => api.archiveRuns([t.runId]), "重试任务已受理")
                }
              >
                重试归档
              </button>
            ) : null}
          </article>
        ))}
      </div>
      {data?.cleanupResults?.length ? (
        <>
          <h3>最近清理结果</h3>
          <div className="usage-list">
            {data.cleanupResults.map((result, index) => (
              <article key={`${result.runId}:${formatTime(result.at)}:${index}`}>
                <strong>{result.runId}</strong>
                <p>
                  {result.status === "cleaned"
                    ? "已清理"
                    : result.status === "skipped"
                      ? "已跳过"
                      : "处理失败"}{" "}
                  · {formatTime(result.at)}
                </p>
                <p>{result.reason}</p>
              </article>
            ))}
          </div>
        </>
      ) : null}
      {cleanupIds && <RunCleanupModal runIds={cleanupIds} onClose={() => setCleanupIds(undefined)} onDeleted={(ids) => { setSelected([]); onDeleted(ids); }} />}
    </div>
  );
}
