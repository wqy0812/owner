import type { ComponentRelease } from "./domain";
export function adaptationValues(value: unknown): string[] {
  return Array.isArray(value)
    ? value.filter((v): v is string => typeof v === "string")
    : typeof value === "string"
      ? [value]
      : [];
}
export function scenarioAdaptationIssues(
  selected: Record<string, unknown>,
  releases: Array<{ name: string; release: ComponentRelease }>,
  complete = true,
  label: (key: string, value?: string) => string = (key, value) => value ?? key,
): string[] {
  const issues: string[] = [];
  const common: Record<string, Set<string>> = {};
  for (const { name, release } of releases)
    for (const [key, raw] of Object.entries(
      release.environmentConstraints ?? {},
    )) {
      const allowed = adaptationValues(raw);
      if (!allowed.length) continue;
      common[key] = new Set(
        allowed.filter((v) => !common[key] || common[key].has(v)),
      );
      const actual = adaptationValues(selected[key]);
      if (!actual.length && complete)
        issues.push(
          `${name} 要求补选 ${label(key)}（支持：${allowed.map((v) => label(key, v)).join("、")}）`,
        );
      else if (actual.some((v) => !allowed.includes(v)))
        issues.push(
          `${name} 不支持 ${label(key)}=${actual.map((v) => label(key, v)).join("、")}（支持：${allowed.map((v) => label(key, v)).join("、")}）`,
        );
    }
  for (const [key, values] of Object.entries(common))
    if (!values.size) issues.push(`${label(key)} 没有组件共同支持的范围`);
  return [...new Set(issues)];
}
export function environmentAdaptationIssues(
  name: string,
  constraints: Record<string, unknown>,
  facts: Record<string, unknown>,
  label: (key: string, value?: string) => string = (key, value) => value ?? key,
): string[] {
  return Object.entries(constraints).flatMap(([key, raw]) => {
    const values = adaptationValues(raw);
    return !values.length || values.includes(String(facts[key] ?? ""))
      ? []
      : [
          `${name}：${label(key)} 要求 ${values.map((v) => label(key, v)).join("、")}，实际 ${facts[key] ? label(key, String(facts[key])) : "未填写"}`,
        ];
  });
}
export function commonAdaptationRanges(
  releases: Array<{ release: ComponentRelease }>,
): Record<string, string[]> {
  const common: Record<string, string[]> = {};
  for (const { release } of releases)
    for (const [key, raw] of Object.entries(
      release.environmentConstraints ?? {},
    )) {
      const values = adaptationValues(raw);
      if (values.length)
        common[key] = common[key]
          ? common[key].filter((v) => values.includes(v))
          : values;
    }
  return common;
}
