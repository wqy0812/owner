import { useApp } from '../context/AppContext';
import { environmentConstraintDimensions, environmentConstraintGroups } from '../types/environmentConstraints';

export function BranchScope({ scope, full = false }: { scope?: Record<string, unknown>; full?: boolean }) {
  const { platformOptionCategories } = useApp();
  const dimensions = environmentConstraintDimensions(platformOptionCategories);
  const groups = environmentConstraintGroups(scope, dimensions);
  const entries = full ? dimensions.map(dimension => groups.find(group => group.key === dimension.key) ?? {key:dimension.key,label:dimension.label,values:['不限制']}) : groups;
  const render = (items: typeof entries) => items.map(group => <span className="env-constraint" key={group.key}><em>{group.label}</em>{group.values.map(value => <b key={value}>{value}</b>)}</span>);
  return <div className="branch-scope" aria-label="分支适配标签"><strong>适配标签</strong>{entries.length ? render(full ? entries : entries.slice(0,3)) : <span>不限制适配范围</span>}{!full && entries.length > 3 && <details><summary>其余 {entries.length - 3} 类</summary><div className="env-constraints">{render(entries.slice(3))}</div></details>}</div>;
}
