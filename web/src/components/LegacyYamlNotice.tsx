import type { LegacyYamlSettings } from '../types/domain';

export function LegacyYamlNotice({ value, confirmed, onConfirm, checksInPrecheck }: { value?: LegacyYamlSettings; confirmed?: boolean; onConfirm?: (value: boolean) => void; checksInPrecheck?: boolean }) {
  if (!value) return null;
  return <section className="info-note span-2" aria-label="YAML 迁入待办">
    <strong>旧配置待迁入 YAML</strong>
    <p>{checksInPrecheck ? '运行条件与残留探测请迁入绑定的前置检查 YAML，facts 采集写入需要的动作 YAML。' : '请将以下逻辑写入当前检查或验收的 YAML。'}完成并确认保存后才能重新验证。普通保存会保留这些旧配置。</p>
    {value.gatherFacts && <p>主机 facts 采集：在需要的动作中显式调用 setup。</p>}
    {value.checks.length > 0 && <ul>{value.checks.map(check => <li key={check.id}>{check.id} · {check.kind} · <code>{check.target}</code>{check.providedByReleaseId && ` · 来源版本 ${check.providedByReleaseId}`}</li>)}</ul>}
    {onConfirm && <label className="checkbox-field"><input type="checkbox" checked={confirmed ?? false} onChange={event => onConfirm(event.target.checked)} /><span>已将上述逻辑迁入 YAML，保存时清除旧配置</span></label>}
  </section>;
}
