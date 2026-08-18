export interface EnvironmentConstraintGroup {
  key: string;
  label: string;
  values: string[];
}

export interface EnvironmentConstraintOption {
  value: string;
  label: string;
  aliases?: string[];
}

export interface EnvironmentConstraintDimension {
  key: string;
  label: string;
  aliases: string[];
  options: EnvironmentConstraintOption[];
}

export type ConstraintSelection = Record<string, string[]>;

export const ENVIRONMENT_CONSTRAINT_DIMENSIONS: EnvironmentConstraintDimension[] = [
  {
    key: 'architecture',
    label: '架构',
    aliases: ['arch', 'cpuArch'],
    options: [
      { value: 'amd64', label: 'x86/amd64', aliases: ['x86', 'x86_64', 'x86/amd64'] },
      { value: 'arm64', label: 'ARM/arm64', aliases: ['arm', 'aarch64', 'arm/arm64'] },
    ],
  },
  {
    key: 'operatingSystem',
    label: '操作系统',
    aliases: ['os', 'osDistro', 'distribution'],
    options: [
      { value: 'SUSE', label: 'SUSE', aliases: ['sles'] },
      { value: 'Kylin', label: 'Kylin', aliases: ['kylin v10', 'kylin linux'] },
    ],
  },
  {
    key: 'ipFamily',
    label: 'IP 协议族',
    aliases: ['network'],
    options: [
      { value: 'IPv4', label: 'IPv4', aliases: ['ipv4'] },
      { value: 'IPv6', label: 'IPv6', aliases: ['ipv6'] },
    ],
  },
  {
    key: 'hardwareProfile',
    label: '硬件类型',
    aliases: [],
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
    aliases: [],
    options: [
      { value: 'runc', label: 'runc' },
      { value: 'kata', label: 'Kata' },
      { value: 'kata-cc', label: 'Kata CC', aliases: ['kata confidential containers'] },
    ],
  },
  {
    key: 'deploymentMode',
    label: '部署形态',
    aliases: [],
    options: [
      { value: 'standard', label: 'standard' },
      { value: 'serverless', label: 'serverless' },
      { value: 'ingress', label: 'ingress' },
    ],
  },
];

const CONSTRAINT_LABELS: Record<string, string> = Object.fromEntries([
  ...ENVIRONMENT_CONSTRAINT_DIMENSIONS.flatMap((dimension) => [
    [dimension.key, dimension.label],
    ...dimension.aliases.map((alias) => [alias, dimension.label] as const),
  ]),
]);

const CONSTRAINT_ORDER = ENVIRONMENT_CONSTRAINT_DIMENSIONS.flatMap((dimension) => [dimension.key, ...dimension.aliases]);

const VALUE_LABELS: Record<string, string> = Object.fromEntries(
  ENVIRONMENT_CONSTRAINT_DIMENSIONS.flatMap((dimension) => dimension.options.flatMap((option) => [
    [option.value.toLowerCase(), option.label],
    ...(option.aliases ?? []).map((alias) => [alias.toLowerCase(), option.label] as const),
  ])),
);

function asValues(value: unknown): string[] {
  if (value == null || value === '') return [];
  if (Array.isArray(value)) return value.flatMap(asValues);
  if (typeof value === 'object') return [];
  return [String(value)];
}

export function formatConstraintValue(value: string) {
  return VALUE_LABELS[value.trim().toLowerCase()] ?? value.trim();
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
  const normalized = raw.trim().toLowerCase();
  return dimension.options.find((option) => (
    option.value.toLowerCase() === normalized
    || option.label.toLowerCase() === normalized
    || (option.aliases ?? []).some((alias) => alias.toLowerCase() === normalized)
  ))?.value;
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
    const raw = constraints[dimension.key] ?? dimension.aliases.map((alias) => constraints[alias]).find((value) => value != null);
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
