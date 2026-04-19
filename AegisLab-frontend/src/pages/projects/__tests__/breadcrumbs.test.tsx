import { readFileSync } from 'fs';
import { resolve } from 'path';
import { describe, expect, it } from 'vitest';

const pages = [
  'ProjectDetail.tsx',
  'ProjectSettings.tsx',
  'ProjectDatapacks.tsx',
  'ProjectExecutions.tsx',
  'ProjectEvaluations.tsx',
];

describe('duplicate breadcrumbs removed', () => {
  pages.forEach((page) => {
    it(`${page} does not render its own <Breadcrumb>`, () => {
      const source = readFileSync(resolve(__dirname, '..', page), 'utf-8');
      expect(source).not.toMatch(/<Breadcrumb[\s\n]/);
    });
  });
});
