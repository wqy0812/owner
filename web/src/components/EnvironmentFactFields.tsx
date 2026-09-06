import type {PlatformOptionCategory} from '../types/domain';
import {activeEnvironmentConstraintDimensions,childOptionsForSelection,environmentConstraintDimensions} from '../types/environmentConstraints';
export function EnvironmentFactFields({ categories, facts, editable, onChange }: { categories: PlatformOptionCategory[]; facts: Record<string, unknown>; editable: boolean; onChange: (facts: Record<string, unknown>) => void }) {
  const allDimensions = environmentConstraintDimensions(categories);
  const dimensions = activeEnvironmentConstraintDimensions(categories);
  function setFact(dimensionKey: string, value: string) {
    const next = { ...facts };
    if (value) next[dimensionKey] = value; else delete next[dimensionKey];
    const dimension = dimensions.find((item) => item.key === dimensionKey);
    if (dimension && !dimension.parentCategoryId) {
      for (const child of dimensions.filter((item) => item.parentCategoryId === dimension.id)) delete next[child.key];
    }
    onChange(next);
  }
  return <div className="form-grid">{dimensions.map((dimension) => {
    const category = categories.find((item) => item.id === dimension.id);
    const parent = dimension.parentCategoryId ? dimensions.find((item) => item.id === dimension.parentCategoryId) : undefined;
    const parentValue = parent && typeof facts[parent.key] === 'string' ? String(facts[parent.key]) : '';
    const currentValue = typeof facts[dimension.key] === 'string' ? String(facts[dimension.key]) : '';
    const activeOptions = childOptionsForSelection(dimension, parent, parentValue ? [parentValue] : []);
    const currentRetired = dimension.options.find((option) => option.value === currentValue && option.retiredAt);
    return <label key={dimension.key}><span>{dimension.label}{category?.environmentRequired ? '（必填）' : '（可选）'}</span><select aria-label={`适配标签 · 实际环境 ${dimension.label}`} required={category?.environmentRequired} value={currentValue} disabled={!editable || Boolean(parent && !parentValue)} onChange={(event) => setFact(dimension.key, event.target.value)}><option value="">{parent && !parentValue ? `请先选择${parent.label}` : '未选择'}</option>{currentRetired ? <option value={currentRetired.value} disabled>{currentRetired.label}（已退役，只读）</option> : null}{activeOptions.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select></label>;
  })}{allDimensions.filter((dimension) => dimension.retiredAt && typeof facts[dimension.key] === 'string').map((dimension) => <label key={dimension.key}><span>{dimension.label}（已退役，只读）</span><input disabled value={dimension.options.find((option) => option.value === facts[dimension.key])?.label ?? String(facts[dimension.key])} /></label>)}</div>;
}
