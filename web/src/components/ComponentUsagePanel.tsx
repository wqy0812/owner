import { useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import { useApp } from "../context/AppContext";
import { useApiData } from "../hooks/useApiData";
import { STATUS_LABELS, type Component } from "../types/domain";
import { EmptyState, ErrorBlock, LoadingBlock } from "./Primitives";
export function ComponentUsagePanel({ component }: { component: Component }) {
  const { user } = useApp();
  const [releaseId, setReleaseId] = useState("");
  const [history, setHistory] = useState(false);
  const { data, loading, error, reload } = useApiData(
    (signal) => api.componentUsage(component.id, releaseId, history, signal),
    [user.id, component.id, releaseId, history],
    ["components", "scenarios"],
  );
  return (
    <section className="panel usage-panel" aria-label="谁用了我">
      <header>
        <h2>谁用了我</h2>
        <p>已保存的直接引用；不要求运行过。</p>
      </header>
      <div className="filter-bar">
        <label>
          组件版本
          <select
            value={releaseId}
            onChange={(e) => setReleaseId(e.target.value)}
          >
            <option value="">全部版本</option>
            {component.releases?.map((r) => (
              <option key={r.id} value={r.id}>
                {r.lineName} · {r.version}
              </option>
            ))}
          </select>
        </label>
        <label>
          <input
            type="checkbox"
            checked={history}
            onChange={(e) => setHistory(e.target.checked)}
          />{" "}
          包含历史引用
        </label>
      </div>
      {history ? <p>包含旧 Revision、已废弃及已放弃记录。</p> : null}
      {error ? (
        <ErrorBlock message={error} onRetry={() => void reload()} />
      ) : loading && !data ? (
        <LoadingBlock />
      ) : data ? (
        <>
          <h3>下游组件（{data.componentCount}）</h3>
          {data.components.length ? (
            <div className="usage-list">
              {data.components.map((row) => (
                <article key={`${row.releaseId}:${row.upstreamReleaseId}`}>
                  <strong>
                    {row.name} · {row.version}
                  </strong>
                  <p>
                    {row.lineName} · {STATUS_LABELS[row.status] ?? row.status} ·{" "}
                    {row.ownerName}
                  </p>
                  <p>
                    使用 {row.upstreamVersion} ·{" "}
                    {row.dependencyKind === "configuration"
                      ? "配置依赖"
                      : "执行依赖"}
                  </p>
                  {row.canViewDetails ? (
                    <Link
                      to={`/components?selected=${encodeURIComponent(row.componentId)}&release=${encodeURIComponent(row.releaseId)}`}
                    >
                      查看版本
                    </Link>
                  ) : (
                    <small>仅引用摘要</small>
                  )}
                </article>
              ))}
            </div>
          ) : (
            <EmptyState title="暂无下游组件" />
          )}
          <h3>场景（{data.scenarioCount}）</h3>
          {data.scenarios.length ? (
            <div className="usage-list">
              {data.scenarios.map((row) => (
                <article key={`${row.revisionId}:${row.releaseId}`}>
                  <strong>
                    {row.name} · Revision {row.revision}
                  </strong>
                  <p>
                    {STATUS_LABELS[row.status] ?? row.status} · {row.ownerName}{" "}
                    · {row.current ? "当前 Revision" : "历史 Revision"}
                  </p>
                  <p>
                    使用 {row.version} · 引用 {row.references} 次
                  </p>
                  {row.canViewDetails ? (
                    <Link
                      to={`/scenarios?selected=${encodeURIComponent(row.scenarioId)}&revision=${encodeURIComponent(row.revisionId)}`}
                    >
                      查看 Revision
                    </Link>
                  ) : (
                    <small>仅引用摘要</small>
                  )}
                </article>
              ))}
            </div>
          ) : (
            <EmptyState title="暂无场景引用" />
          )}
        </>
      ) : null}
    </section>
  );
}
