import { useEffect, useState } from "react";
import { api } from "../api/client";
import { displayError, useApp } from "../context/AppContext";
import { useApiData } from "../hooks/useApiData";
import { STATUS_LABELS, type RunSummary } from "../types/domain";
import { ErrorBlock, Modal, formatFileSize, formatTime } from "./Primitives";
const ARCHIVE_STATUS: Record<string, string> = {queued:"待归档",running:"归档中",archived:"已归档",failed:"归档失败"};
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
export function RunRetentionPanel({ runs }: { runs: RunSummary[] }) {
  const { user, notify, signalRefresh } = useApp();
  const { data, error, reload } = useApiData(
    (signal) => api.archiveHealth(signal),
    [user.id],
    "runs",
  );
  const [selected, setSelected] = useState<string[]>([]);
  const [preview, setPreview] = useState<CleanupItem[]>();
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
    <details className="panel retention-panel">
      <summary>
        运行历史管理 · 待归档 {data?.pending ?? 0} · 已归档{" "}
        {formatFileSize(data?.sizeBytes ?? 0)}
      </summary>
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
          className="filter-bar"
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
      <h3>当前页记录（最多 100 条）</h3>
      <div className="retention-selection">
        {runs
          .filter((r) => r.status === "succeeded" || r.status === "failed")
          .map((r) => (
            <label key={r.id}>
              <input
                type="checkbox"
                checked={selected.includes(r.id)}
                disabled={busy || r.archiveStatus === "archived"}
                onChange={(e) =>
                  setSelected((ids) =>
                    e.target.checked
                      ? [...ids, r.id]
                      : ids.filter((id) => id !== r.id),
                  )
                }
              />
              {r.name} · {STATUS_LABELS[r.status] ?? r.status} ·{" "}
              {r.finishedAt ? formatTime(r.finishedAt) : "无结束时间"} · {r.id}
            </label>
          ))}
      </div>
      <div className="row-actions">
        <button
          className="button button--quiet"
          disabled={
            busy ||
            !data?.configured ||
            !!data.storageError ||
            !chosen.length ||
            chosen.some((r) => r.status !== "succeeded")
          }
          onClick={() =>
            void act(() => api.archiveRuns(selected), "归档任务已受理")
          }
        >
          归档所选成功记录
        </button>
        <button
          className="button button--danger-soft"
          disabled={busy || !chosen.length}
          onClick={() =>
            void act(
              async () => setPreview(await api.cleanupPreview(selected)),
              "清理预览已生成",
            )
          }
        >
          预览清理
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
      {preview ? (
        <Modal
          title="确认清理失败记录"
          description="删除后不能恢复；整批重新校验，任一记录被保护则整批取消。"
          onClose={() => setPreview(undefined)}
        >
          <div className="modal-body">
            {preview.map((p) => (
              <p key={p.runId}>
                {p.runId}：{p.eligible ? "可以清理" : p.reasons.join("；")}
              </p>
            ))}
          </div>
          <footer className="modal-actions">
            <button disabled={busy} onClick={() => setPreview(undefined)}>
              取消
            </button>
            <button
              className="button button--danger-soft"
              disabled={
                busy || !preview.length || preview.some((p) => !p.eligible)
              }
              onClick={() =>
                void act(async () => {
                  await api.cleanupRuns(preview.map((p) => p.runId));
                  setPreview(undefined);
                  setSelected([]);
                }, "失败记录已清理")
              }
            >
              确认删除所选记录
            </button>
          </footer>
        </Modal>
      ) : null}
    </details>
  );
}
