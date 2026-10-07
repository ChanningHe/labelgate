import { describe, expect, test } from 'vitest';
import { LABEL_PROPERTIES, RECIPES, RESERVED_SERVICE_NAMES, SERVICE_NAME_PATTERN } from './catalog';
import type { LabelLine } from './format';

const LABEL_TYPES = ['dns', 'tunnel', 'access'];

const keysOf = (lines: LabelLine[]) => lines.flatMap((l) => ('key' in l ? [l.key] : []));
const servicesOf = (lines: LabelLine[], type: string) =>
  new Set(keysOf(lines).filter((k) => k.startsWith(`${type}.`)).map((k) => k.split('.')[1]));

describe('label catalog', () => {
  test('recipe ids are unique', () => {
    const ids = RECIPES.map((r) => r.id);

    expect(new Set(ids).size).toBe(ids.length);
  });

  test('every kind has recipes and documented properties', () => {
    for (const kind of LABEL_TYPES) {
      expect(RECIPES.some((r) => r.kind === kind)).toBe(true);
      expect(LABEL_PROPERTIES[kind as keyof typeof LABEL_PROPERTIES].length).toBeGreaterThan(0);
    }
  });

  test.each(RECIPES.map((r) => [r.id, r] as const))('%s uses keys the parser accepts', (_id, recipe) => {
    for (const key of keysOf(recipe.lines)) {
      const [type, service, ...property] = key.split('.');
      expect(LABEL_TYPES).toContain(type);
      expect(service).toMatch(SERVICE_NAME_PATTERN);
      expect(RESERVED_SERVICE_NAMES).not.toContain(service);
      expect(property.length).toBeGreaterThan(0);
    }
  });

  test.each(RECIPES.map((r) => [r.id, r] as const))('%s gives every DNS and tunnel service a hostname', (_id, recipe) => {
    const keys = keysOf(recipe.lines);
    for (const type of ['dns', 'tunnel']) {
      for (const service of servicesOf(recipe.lines, type)) {
        expect(keys).toContain(`${type}.${service}.hostname`);
      }
    }
  });

  test.each(RECIPES.map((r) => [r.id, r] as const))('%s gives every tunnel service a backend', (_id, recipe) => {
    const keys = keysOf(recipe.lines);
    for (const service of servicesOf(recipe.lines, 'tunnel')) {
      expect(keys).toContain(`tunnel.${service}.service`);
    }
  });

  test.each(RECIPES.filter((r) => r.kind === 'access').map((r) => [r.id, r] as const))(
    '%s attaches its policy to a service',
    (_id, recipe) => {
      const policies = servicesOf(recipe.lines, 'access');
      const attached = recipe.lines.flatMap((l) => ('key' in l && l.key.endsWith('.access') ? [l.value] : []));
      for (const policy of policies) expect(attached).toContain(policy);
    },
  );
});
