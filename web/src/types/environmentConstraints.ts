import type { PlatformOptionCategory } from './domain';

export interface EnvironmentConstraintGroup { key: string; label: string; values: string[] }
export interface EnvironmentConstraintOption { id: string; value: string; label: string; parentOptionId?: string; retiredAt?: string }
export interface EnvironmentConstraintDimension { id: string; key: string; label: string; parentCategoryId?: string; retiredAt?: string; options: EnvironmentConstraintOption[] }
// Missing keys are undecided; an empty array is an explicit unrestricted choice.
export type ConstraintSelection = Record<string, string[]>;

export function environmentConstraintDimensions(categories: PlatformOptionCategory[]): EnvironmentConstraintDimension[] {
  return categories.filter((category) => category.kind === 'environment_dimension').map((category) => ({
    id: category.id,
    key: category.key,
    label: category.label,
    parentCategoryId: category.parentCategoryId,
    retiredAt: category.retiredAt,
    options: category.options.map((option) => ({ id: option.id, value: option.value, label: option.label, parentOptionId: option.parentOptionId, retiredAt: option.retiredAt })),
  }));
}

export function activeEnvironmentConstraintDimensions(categories: PlatformOptionCategory[]): EnvironmentConstraintDimension[] {
  return environmentConstraintDimensions(categories).filter((dimension) => !dimension.retiredAt);
}

export function childOptionsForSelection(dimension: EnvironmentConstraintDimension, parent: EnvironmentConstraintDimension | undefined, selectedParentValues: string[]): EnvironmentConstraintOption[] {
  if (!dimension.parentCategoryId || !parent) return dimension.options.filter((option) => !option.retiredAt);
  const parentIds = new Set(parent.options.filter((option) => selectedParentValues.includes(option.value)).map((option) => option.id));
  return dimension.options.filter((option) => !option.retiredAt && Boolean(option.parentOptionId) && parentIds.has(option.parentOptionId!));
}

function asValues(value: unknown): string[] {
  if (value == null || value === '') return [];
  if (Array.isArray(value)) return value.flatMap(asValues);
  if (typeof value === 'object') return [];
  return [String(value)];
}

function unique(values: string[]) { return [...new Set(values)]; }

export function environmentConstraintGroups(constraints: Record<string, unknown> | null | undefined, dimensions: EnvironmentConstraintDimension[]): EnvironmentConstraintGroup[] {
  if (!constraints) return [];
  return dimensions.flatMap((dimension) => {
    const labels = new Map(dimension.options.map((option) => [option.value, option.label]));
    const values = asValues(constraints[dimension.key]).map((value) => labels.get(value) ?? value);
    return values.length ? [{ key: dimension.key, label: dimension.label, values }] : [];
  });
}

export function parseConstraintSelection(constraints: Record<string, unknown> | null | undefined, dimensions: EnvironmentConstraintDimension[]): ConstraintSelection {
  const selection: ConstraintSelection = {};
  if (constraints == null) return selection;
  for (const dimension of dimensions) {
    const allowed = new Set(dimension.options.map((option) => option.value));
    selection[dimension.key] = unique(asValues(constraints?.[dimension.key]).filter((value) => allowed.has(value)));
  }
  return selection;
}

export function unselectedConstraintDimensions(selection: ConstraintSelection, dimensions: EnvironmentConstraintDimension[]): EnvironmentConstraintDimension[] {
  return dimensions.filter((dimension) => !dimension.retiredAt && selection[dimension.key] === undefined);
}

export function serializeConstraintSelection(selection: ConstraintSelection, dimensions: EnvironmentConstraintDimension[]): Record<string, unknown> {
  return Object.fromEntries(dimensions.map((dimension) => [dimension.key, unique(selection[dimension.key] ?? [])] as const).filter(([, values]) => values.length));
}

export function toggleConstraintValue(selection: ConstraintSelection, key: string, value: string): ConstraintSelection {
  const current = selection[key] ?? [];
  const values = current.includes(value) ? current.filter((item) => item !== value) : [...current, value];
  const next = { ...selection };
  if (values.length) next[key] = values;
  else delete next[key];
  return next;
}

export function toggleHierarchicalConstraintValue(selection: ConstraintSelection, dimensions: EnvironmentConstraintDimension[], key: string, value?: string): ConstraintSelection {
  const next = value === undefined ? { ...selection, [key]: [] } : toggleConstraintValue(selection, key, value);
  if (value === undefined && selection[key]?.length === 0) delete next[key];
  const dimension = dimensions.find((item) => item.key === key);
  if (!dimension || dimension.parentCategoryId) return next;
  for (const child of dimensions.filter((item) => item.parentCategoryId === dimension.id)) {
    const selected = next[child.key];
    if (selected === undefined) continue;
    const allowed = new Set(childOptionsForSelection(child, dimension, next[key] ?? []).map((option) => option.value));
    const retained = selected.filter((item) => allowed.has(item));
    if (retained.length) next[child.key] = retained;
    else if (selected.length || next[key]?.length) delete next[child.key];
  }
  return next;
}
