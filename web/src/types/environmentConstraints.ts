import type { PlatformOptionCategory } from './domain';

export interface EnvironmentConstraintGroup { key: string; label: string; values: string[] }
export interface EnvironmentConstraintOption { id: string; value: string; label: string; parentOptionId?: string; retiredAt?: string }
export interface EnvironmentConstraintDimension { id: string; key: string; label: string; parentCategoryId?: string; retiredAt?: string; options: EnvironmentConstraintOption[] }
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

export function emptyConstraintSelection(dimensions: EnvironmentConstraintDimension[]): ConstraintSelection {
  return Object.fromEntries(dimensions.map((dimension) => [dimension.key, []]));
}

export function parseConstraintSelection(constraints: Record<string, unknown> | null | undefined, dimensions: EnvironmentConstraintDimension[]): ConstraintSelection {
  const selection = emptyConstraintSelection(dimensions);
  for (const dimension of dimensions) {
    const allowed = new Set(dimension.options.map((option) => option.value));
    selection[dimension.key] = unique(asValues(constraints?.[dimension.key]).filter((value) => allowed.has(value)));
  }
  return selection;
}

export function serializeConstraintSelection(selection: ConstraintSelection, dimensions: EnvironmentConstraintDimension[]): Record<string, unknown> {
  return Object.fromEntries(dimensions.map((dimension) => [dimension.key, unique(selection[dimension.key] ?? [])] as const).filter(([, values]) => values.length));
}

export function toggleConstraintValue(selection: ConstraintSelection, key: string, value: string): ConstraintSelection {
  const current = selection[key] ?? [];
  return { ...selection, [key]: current.includes(value) ? current.filter((item) => item !== value) : [...current, value] };
}

export function toggleHierarchicalConstraintValue(selection: ConstraintSelection, dimensions: EnvironmentConstraintDimension[], key: string, value: string): ConstraintSelection {
  let next = toggleConstraintValue(selection, key, value);
  const dimension = dimensions.find((item) => item.key === key);
  if (!dimension || dimension.parentCategoryId) return next;
  for (const child of dimensions.filter((item) => item.parentCategoryId === dimension.id)) {
	const allowed = new Set(childOptionsForSelection(child, dimension, next[key]).map((option) => option.value));
    next = { ...next, [child.key]: (next[child.key] ?? []).filter((item) => allowed.has(item)) };
  }
  return next;
}
