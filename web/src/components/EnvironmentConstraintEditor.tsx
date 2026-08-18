import { ENVIRONMENT_CONSTRAINT_DIMENSIONS, toggleConstraintValue, type ConstraintSelection } from '../types/environmentConstraints';

export function EnvironmentConstraintEditor({
  value,
  onChange,
}: {
  value: ConstraintSelection;
  onChange: (next: ConstraintSelection) => void;
}) {
  return (
    <fieldset className="constraint-editor">
      <legend>适配环境</legend>
      <p>选择该版本可安装的环境。某个维度不选，表示不限制该维度。</p>
      <div className="constraint-editor__grid">
        {ENVIRONMENT_CONSTRAINT_DIMENSIONS.map((dimension) => {
          const selected = value[dimension.key] ?? [];
          return (
            <div className="constraint-dimension" key={dimension.key}>
              <strong>{dimension.label}</strong>
              <div className="constraint-options" role="group" aria-label={dimension.label}>
                {dimension.options.map((option) => {
                  const checked = selected.includes(option.value);
                  return (
                    <label key={option.value} className={checked ? 'active' : ''}>
                      <input
                        type="checkbox"
                        checked={checked}
                        onChange={() => onChange(toggleConstraintValue(value, dimension.key, option.value))}
                      />
                      {option.label}
                    </label>
                  );
                })}
              </div>
            </div>
          );
        })}
      </div>
    </fieldset>
  );
}
