import { readFileSync } from 'fs';
import { resolve } from 'path';
import { describe, expect, it } from 'vitest';

describe('CreateProjectModal', () => {
  const sourceFile = resolve(__dirname, '../CreateProjectModal.tsx');

  it('does not contain team_id field in source', () => {
    const source = readFileSync(sourceFile, 'utf-8');
    expect(source).not.toMatch(/name=['"]team_id['"]/);
  });

  it('does not render a team_id form field', () => {
    const source = readFileSync(sourceFile, 'utf-8');
    // Should not have any reference to team_id in form items
    expect(source).not.toMatch(/team_id/);
  });
});
