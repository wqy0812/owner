export interface EnvironmentConstraintGroup {
  key: string;
  label: string;
  values: string[];
}

export interface EnvironmentConstraintOption {
  value: string;
  label: string;
}

export interface EnvironmentConstraintDimension {
  key: string;
  label: string;
  options: EnvironmentConstraintOption[];
}

export type ConstraintSelection = Record<string, string[]>;

export const ENVIRONMENT_CONSTRAINT_DIMENSIONS: EnvironmentConstraintDimension[] = [
  {
    key: 'architecture',
    label: '架构',
    options: [
      { value: 'amd64', label: 'x86/amd64' },
      { value: 'arm64', label: 'ARM/arm64' },
    ],
  },
  {
    key: 'operatingSystem',
    label: '操作系统',
    options: [
      { value: 'Ubuntu', label: 'Ubuntu' },
      { value: 'SUSE', label: 'SUSE' },
      { value: 'Kylin', label: 'Kylin' },
    ],
  },
  {
    key: 'operatingSystemVersion',
    label: '操作系统版本',
    options: [
      { value: '18.04', label: '18.04' },
      { value: '20.04', label: '20.04' },
      { value: '22.04', label: '22.04' },
      { value: '24.04', label: '24.04' },
      { value: '18.04 / 24.04', label: '18.04 / 24.04（混合）' },
    ],
  },
  {
    key: 'dockerVersion',
    label: 'Docker 版本',
    options: [
      { value: '20.10.21', label: '20.10.21' },
      { value: '20.10.24', label: '20.10.24' },
      { value: '24.0.9', label: '24.0.9' },
    ],
  },
  {
    key: 'ipFamily',
    label: 'IP 协议族',
    options: [
      { value: 'IPv4', label: 'IPv4' },
      { value: 'IPv6', label: 'IPv6' },
    ],
  },
  {
    key: 'hardwareProfile',
    label: '硬件类型',
    options: [
      { value: 'general', label: '通用主机' },
      { value: 'gpu', label: 'GPU' },
      { value: 'dpu', label: 'DPU' },
      { value: 'bms', label: 'BMS' },
    ],
  },
  {
    key: 'isolationRuntime',
    label: '容器隔离',
    options: [
      { value: 'runc', label: 'runc' },
      { value: 'kata', label: 'Kata' },
      { value: 'kata-cc', label: 'Kata CC' },
    ],
  },
  {
    key: 'deploymentMode',
    label: '部署形态',
    options: [
      { value: 'standard', label: 'standard' },
      { value: 'serverless', label: 'serverless' },
      { value: 'ingress', label: 'ingress' },
    ],
  },
];

const CONSTRAINT_LABELS: Record<string, string> = Object.fromEntries([
  ...ENVIRONMENT_CONSTRAINT_DIMENSIONS.map((dimension) => [dimension.key, dimension.label]),
]);

const CONSTRAINT_ORDER = ENVIRONMENT_CONSTRAINT_DIMENSIONS.map((dimension) => dimension.key);

const VALUE_LABELS: Record<string, string> = Object.fromEntries(
  ENVIRONMENT_CONSTRAINT_DIMENSIONS.flatMap((dimension) => dimension.options.map((option) => [option.value, option.label])),
);

function asValues(value: unknown): string[] {
  if (value == null || value === '') return [];
  if (Array.isArray(value)) return value.flatMap(asValues);
  if (typeof value === 'object') return [];
  return [String(value)];
}

export function formatConstraintValue(value: string) {
  return VALUE_LABELS[value.trim()] ?? value.trim();
}

export function environmentConstraintGroups(constraints?: Record<string, unknown> | null): EnvironmentConstraintGroup[] {
  if (!constraints) return [];
  return Object.keys(constraints)
    .sort((left, right) => {
      const leftIndex = CONSTRAINT_ORDER.indexOf(left);
      const rightIndex = CONSTRAINT_ORDER.indexOf(right);
      if (leftIndex === -1 && rightIndex === -1) return left.localeCompare(right);
      if (leftIndex === -1) return 1;
      if (rightIndex === -1) return -1;
      return leftIndex - rightIndex;
    })
    .flatMap((key) => {
      const values = asValues(constraints[key]).map(formatConstraintValue);
      if (!values.length) return [];
      return [{ key, label: CONSTRAINT_LABELS[key] ?? key, values }];
    });
}

function normalizeOptionValue(dimension: EnvironmentConstraintDimension, raw: string) {
  const normalized = raw.trim();
  return dimension.options.find((option) => option.value === normalized)?.value;
}

function unique(values: string[]) {
  return [...new Set(values)];
}

export function emptyConstraintSelection(): ConstraintSelection {
  return Object.fromEntries(ENVIRONMENT_CONSTRAINT_DIMENSIONS.map((dimension) => [dimension.key, []]));
}

export function parseConstraintSelection(constraints?: Record<string, unknown> | null): ConstraintSelection {
  const selection = emptyConstraintSelection();
  if (!constraints) return selection;
  for (const dimension of ENVIRONMENT_CONSTRAINT_DIMENSIONS) {
    const raw = constraints[dimension.key];
    selection[dimension.key] = unique(asValues(raw).flatMap((value) => {
      const option = normalizeOptionValue(dimension, value);
      return option ? [option] : [];
    }));
  }
  return selection;
}

export function serializeConstraintSelection(selection: ConstraintSelection): Record<string, unknown> {
  return Object.fromEntries(
    ENVIRONMENT_CONSTRAINT_DIMENSIONS
      .map((dimension) => [dimension.key, unique(selection[dimension.key] ?? [])] as const)
      .filter(([, values]) => values.length > 0),
  );
}

export function toggleConstraintValue(selection: ConstraintSelection, key: string, value: string): ConstraintSelection {
  const current = selection[key] ?? [];
  return {
    ...selection,
    [key]: current.includes(value) ? current.filter((item) => item !== value) : [...current, value],
  };
}
