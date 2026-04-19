import { readFileSync } from 'fs';
import { resolve } from 'path';
import { describe, expect, it } from 'vitest';

describe('CreateExecutionForm', () => {
  const sourceFile = resolve(__dirname, '../CreateExecutionForm.tsx');

  it('uses Input.Search for datapack search', () => {
    const source = readFileSync(sourceFile, 'utf-8');
    expect(source).toContain('Input.Search');
  });

  it('uses Table with columns for datapack picker', () => {
    const source = readFileSync(sourceFile, 'utf-8');
    // Table with fault_type column for datapacks
    expect(source).toContain('fault_type');
  });
});
