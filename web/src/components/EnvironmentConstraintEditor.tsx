import { Button, Checkbox } from 'antd';
import { useApp } from '../context/AppContext';
import { activeEnvironmentConstraintDimensions, childOptionsForSelection, environmentConstraintDimensions, toggleHierarchicalConstraintValue, type ConstraintSelection, type EnvironmentConstraintOption } from '../types/environmentConstraints';
export function EnvironmentConstraintEditor({ value, onChange, title = "支持范围", }: {
    value: ConstraintSelection;
    title?: string;
    onChange: (next: ConstraintSelection) => void;
}) {
    const { platformOptionCategories, platformOptionsLoading, platformOptionsError, signalRefresh } = useApp();
    const allDimensions = environmentConstraintDimensions(platformOptionCategories);
    const dimensions = activeEnvironmentConstraintDimensions(platformOptionCategories);
    const retainedRetired = allDimensions.filter((dimension) => dimension.retiredAt && (value[dimension.key]?.length ?? 0) > 0);
    function toggle(dimensionKey: string, optionValue?: string) {
        onChange(toggleHierarchicalConstraintValue(value, dimensions, dimensionKey, optionValue));
    }
    return (<fieldset className="constraint-editor">
      <legend>适配标签 · {title}</legend>
      <p>请为每个维度选择具体标签或“不限制”；未选择时不能确认创建。</p>
      {platformOptionsError && <div role="alert"><p>环境维度加载失败，暂时不能确认适配范围。{platformOptionsError}</p><Button className="button button--quiet" disabled={platformOptionsLoading} onClick={() => signalRefresh('platform-options')} htmlType={"button"} type="default">重新加载环境维度</Button></div>}
      <div className="constraint-editor__grid">
        {platformOptionsLoading && !allDimensions.length ? <span>正在加载环境维度…</span> : dimensions.map((dimension) => {
            const selected = value[dimension.key] ?? [];
            const unrestricted = value[dimension.key]?.length === 0;
            const parent = dimension.parentCategoryId ? dimensions.find((item) => item.id === dimension.parentCategoryId) : undefined;
            const requiresChild = Boolean(parent && value[parent.key]?.length);
            const options = childOptionsForSelection(dimension, parent, parent ? value[parent.key] ?? [] : []);
            const groups = parent ? parent.options.filter((option) => (value[parent.key] ?? []).includes(option.value)).map((parentOption) => ({ label: parentOption.label, options: options.filter((option) => option.parentOptionId === parentOption.id) })) : [];
            const renderOption = (option: EnvironmentConstraintOption) => (<Checkbox key={option.value} className={selected.includes(option.value) ? 'active' : ''} checked={selected.includes(option.value)} onChange={() => toggle(dimension.key, option.value)}>{option.label}</Checkbox>);
            return (<div className="constraint-dimension" key={dimension.key} role="group" aria-label={dimension.label}>
              <strong>{dimension.label}</strong><small>{selected.length ? `已选择 ${selected.length} 项` : unrestricted ? '已选择不限制' : '未选择'}</small>
              <div className="constraint-options">
                <Checkbox className={unrestricted ? 'active' : ''} checked={unrestricted} disabled={requiresChild} onChange={() => toggle(dimension.key)}>
                  不限制
                </Checkbox>
                {!parent && options.map(renderOption)}
              </div>
              {parent ? <small>{requiresChild ? `已选择${parent.label}，请为每项选择对应的${dimension.label}。` : `如需指定${dimension.label}，请先选择${parent.label}。`}</small> : null}
              {groups.map((group) => <div key={group.label || dimension.key}>{group.label ? <small>{group.label}</small> : null}<div className="constraint-options">
                {group.options.map(renderOption)}
              </div></div>)}
            </div>);
        })}
        {retainedRetired.map((dimension) => <div className="constraint-dimension" key={dimension.key}><strong>{dimension.label}（已退役，只读）</strong><div className="constraint-options">{(value[dimension.key] ?? []).map((selected) => <Checkbox key={selected} className="active" checked disabled>{dimension.options.find((option) => option.value === selected)?.label ?? selected}</Checkbox>)}</div></div>)}
      </div>
    </fieldset>);
}
