import { readFileSync } from 'fs';
import { resolve } from 'path';
import { describe, expect, it } from 'vitest';

describe('ProjectList', () => {
  const sourceFile = resolve(__dirname, '../ProjectList.tsx');

  it('does not use Popconfirm for delete', () => {
    const source = readFileSync(sourceFile, 'utf-8');
    expect(source).not.toContain('Popconfirm');
  });

  it('uses Modal for delete confirmation', () => {
    const source = readFileSync(sourceFile, 'utf-8');
    // Should use Modal (typed-confirmation) for destructive actions
    expect(source).toContain('Modal');
  });
});
