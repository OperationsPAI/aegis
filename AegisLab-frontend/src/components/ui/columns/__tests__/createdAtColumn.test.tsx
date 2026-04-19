import { describe, expect, it } from 'vitest';

import { createdAtColumn } from '../createdAtColumn';

describe('createdAtColumn', () => {
  it('returns a column definition with correct properties', () => {
    const column = createdAtColumn();
    expect(column.title).toBe('Created At');
    expect('dataIndex' in column && column.dataIndex).toBe('created_at');
    expect(column.key).toBe('created_at');
  });

  it('renders formatted date string', () => {
    const column = createdAtColumn();
    const render = column.render as (val: string) => string;
    const result = render('2024-01-15T10:30:00Z');
    expect(result).toContain('2024');
    expect(result).toContain('01');
    expect(result).toContain('15');
  });

  it('renders dash for missing value', () => {
    const column = createdAtColumn();
    const render = column.render as (val: string) => string;
    expect(render('')).toBe('-');
  });

  it('has a sorter function', () => {
    const column = createdAtColumn();
    expect(typeof column.sorter).toBe('function');
  });
});
