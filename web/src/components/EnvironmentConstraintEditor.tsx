import { useApp } from '../context/AppContext';
import { activeEnvironmentConstraintDimensions, childOptionsForSelection, environmentConstraintDimensions, toggleHierarchicalConstraintValue, type ConstraintSelection } from '../types/environmentConstraints';

export function EnvironmentConstraintEditor({
  value,
  onChange,
}: {
  value: ConstraintSelection;
  onChange: (next: ConstraintSelection) => void;
}) {
  const { platformOptionCategories, platformOptionsLoading } = useApp();
  const allDimensions = environmentConstraintDimensions(platformOptionCategories);
  const dimensions = activeEnvironmentConstraintDimensions(platformOptionCategories);
  const retainedRetired = allDimensions.filter((dimension) => dimension.retiredAt && (value[dimension.key]?.length ?? 0) > 0);

  function toggle(dimensionKey: string, optionValue: string) {
    onChange(toggleHierarchicalConstraintValue(value, dimensions, dimensionKey, optionValue));
  }
  return (
    <fieldset className="constraint-editor">
      <legend>适配环境</legend>
      <p>选择该版本可安装的环境。某个维度不选，表示不限制该维度。</p>
      <div className="constraint-editor__grid">
        {platformOptionsLoading ? <span>正在加载环境维度…</span> : dimensions.map((dimension) => {
          const selected = value[dimension.key] ?? [];
          const parent = dimension.parentCategoryId ? dimensions.find((item) => item.id === dimension.parentCategoryId) : undefined;
          const options = childOptionsForSelection(dimension, parent, parent ? value[parent.key] ?? [] : []);
          const groups = parent ? parent.options.filter((option) => (value[parent.key] ?? []).includes(option.value)).map((parentOption) => ({ label: parentOption.label, options: options.filter((option) => option.parentOptionId === parentOption.id) })) : [{ label: '', options }];
          return (
            <div className="constraint-dimension" key={dimension.key}>
              <strong>{dimension.label}</strong>
              {parent && options.length === 0 ? <small>请先选择{parent.label}</small> : null}
              {groups.map((group) => <div key={group.label || dimension.key}>{group.label ? <small>{group.label}</small> : null}<div className="constraint-options" role="group" aria-label={group.label ? `${dimension.label} ${group.label}` : dimension.label}>
                {group.options.map((option) => {
                  const checked = selected.includes(option.value);
                  return (
                    <label key={option.value} className={checked ? 'active' : ''}>
                      <input
                        type="checkbox"
                        checked={checked}
                        onChange={() => toggle(dimension.key, option.value)}
                      />
                      {option.label}
                    </label>
                  );
                })}
              </div></div>)}
            </div>
          );
        })}
        {retainedRetired.map((dimension) => <div className="constraint-dimension" key={dimension.key}><strong>{dimension.label}（已退役，只读）</strong><div className="constraint-options">{(value[dimension.key] ?? []).map((selected) => <label key={selected} className="active"><input type="checkbox" checked disabled />{dimension.options.find((option) => option.value === selected)?.label ?? selected}</label>)}</div></div>)}
      </div>
    </fieldset>
  );
}
