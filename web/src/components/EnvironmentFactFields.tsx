import { Field } from './Field';
import { Select, Input } from 'antd';
import type { PlatformOptionCategory } from '../types/domain';
import { activeEnvironmentConstraintDimensions, childOptionsForSelection, environmentConstraintDimensions } from '../types/environmentConstraints';
export function EnvironmentFactFields({ categories, facts, editable, onChange }: {
    categories: PlatformOptionCategory[];
    facts: Record<string, unknown>;
    editable: boolean;
    onChange: (facts: Record<string, unknown>) => void;
}) {
    const allDimensions = environmentConstraintDimensions(categories);
    const dimensions = activeEnvironmentConstraintDimensions(categories);
    function setFact(dimensionKey: string, value: string) {
        const next = { ...facts };
        if (value)
            next[dimensionKey] = value;
        else
            delete next[dimensionKey];
        const dimension = dimensions.find((item) => item.key === dimensionKey);
        if (dimension && !dimension.parentCategoryId) {
            for (const child of dimensions.filter((item) => item.parentCategoryId === dimension.id))
                delete next[child.key];
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
            return <Field key={dimension.key} label={<>{dimension.label}{category?.environmentRequired ? '（必填）' : '（可选）'}</>} required={category?.environmentRequired}><Select aria-label={`环境标签 ${dimension.label}`} value={currentValue} disabled={!editable || Boolean(parent && !parentValue)} onChange={(selectedValue) => setFact(dimension.key, selectedValue)} popupMatchSelectWidth={true}><Select.Option value="">{parent && !parentValue ? `请先选择${parent.label}` : '未选择'}</Select.Option>{currentRetired ? <Select.Option value={currentRetired.value} disabled>{currentRetired.label}（已退役，只读）</Select.Option> : null}{activeOptions.map((option) => <Select.Option key={option.value} value={option.value}>{option.label}</Select.Option>)}</Select></Field>;
        })}{allDimensions.filter((dimension) => dimension.retiredAt && typeof facts[dimension.key] === 'string').map((dimension) => <Field key={dimension.key} label={<>{dimension.label}（已退役，只读）</>}><Input disabled value={dimension.options.find((option) => option.value === facts[dimension.key])?.label ?? String(facts[dimension.key])}/></Field>)}</div>;
}
