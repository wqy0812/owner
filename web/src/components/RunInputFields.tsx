interface RunInputFieldsProps {
  names: string[];
  values: Record<string, string>;
  onChange: (name: string, value: string) => void;
}

export function RunInputFields({ names, values, onChange }: RunInputFieldsProps) {
  if (!names.length) return null;
  return <div className="run-input-fields">
    <div className="section-title"><div><h3>本次运行输入</h3><p>只显示组件/节点显式声明的 runInput；值支持 JSON，也可直接输入字符串。</p></div></div>
    <div className="form-grid">
      {names.map((name) => <label key={name}>
        <span>{name}</span>
        <input
          aria-label={`运行参数 ${name}`}
          value={values[name] ?? ''}
          onChange={(event) => onChange(name, event.target.value)}
          placeholder="本次运行值"
        />
      </label>)}
    </div>
  </div>;
}

export function parseRunInput(names: string[], values: Record<string, string>): Record<string, unknown> {
  const output: Record<string, unknown> = {};
  for (const name of names) {
    const raw = values[name]?.trim();
    if (!raw) continue;
    try {
      output[name] = JSON.parse(raw);
    } catch {
      output[name] = raw;
    }
  }
  return output;
}

export function uniqueRunInputs(values: Array<string | undefined>): string[] {
  return [...new Set(values.filter((value): value is string => Boolean(value?.trim())).map((value) => value.trim()))].sort();
}
