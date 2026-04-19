import { readFileSync } from 'fs';
import { resolve } from 'path';
import { describe, expect, it } from 'vitest';

describe('InjectionWizard', () => {
  const sourceFile = resolve(__dirname, '../InjectionWizard.tsx');

  it('uses FAULT_ACTIONS constants for Select options', () => {
    const source = readFileSync(sourceFile, 'utf-8');
    expect(source).toContain('FAULT_ACTIONS');
  });

  it('uses FAULT_MODES constants for Select options', () => {
    const source = readFileSync(sourceFile, 'utf-8');
    expect(source).toContain('FAULT_MODES');
  });

  it('does not use plain Input with pod-kill placeholder for action', () => {
    const source = readFileSync(sourceFile, 'utf-8');
    expect(source).not.toMatch(/placeholder=['"]e\.g\. pod-kill['"]/);
  });

  it('does not use plain Input with "one" placeholder for mode', () => {
    const source = readFileSync(sourceFile, 'utf-8');
    expect(source).not.toMatch(/placeholder=['"]e\.g\. one['"]/);
  });
});
