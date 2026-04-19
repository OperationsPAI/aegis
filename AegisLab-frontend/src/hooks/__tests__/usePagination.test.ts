import { act, renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { usePagination } from '../usePagination';

describe('usePagination', () => {
  it('returns default initial state', () => {
    const { result } = renderHook(() => usePagination());
    expect(result.current.current).toBe(1);
    expect(result.current.pageSize).toBe(20);
  });

  it('accepts custom default page size', () => {
    const { result } = renderHook(() => usePagination({ defaultPageSize: 10 }));
    expect(result.current.pageSize).toBe(10);
  });

  it('updates page and pageSize on onChange', () => {
    const { result } = renderHook(() => usePagination());
    act(() => {
      result.current.onChange(3, 50);
    });
    expect(result.current.current).toBe(3);
    expect(result.current.pageSize).toBe(50);
  });

  it('resets to page 1 on reset', () => {
    const { result } = renderHook(() => usePagination());
    act(() => {
      result.current.onChange(5, 20);
    });
    expect(result.current.current).toBe(5);
    act(() => {
      result.current.reset();
    });
    expect(result.current.current).toBe(1);
    expect(result.current.pageSize).toBe(20);
  });
});
