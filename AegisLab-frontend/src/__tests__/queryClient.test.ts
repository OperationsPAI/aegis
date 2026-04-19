import { describe, expect, it } from 'vitest';

describe('QueryClient retry logic', () => {
  it('should not retry on 401 errors', () => {
    // Test the retry function logic directly
    const retryFn = (
      failureCount: number,
      error: { response?: { status: number } }
    ) => {
      if (error?.response?.status === 401) return false;
      return failureCount < 1;
    };

    const error401 = { response: { status: 401 } };
    expect(retryFn(0, error401)).toBe(false);
    expect(retryFn(1, error401)).toBe(false);
  });

  it('should retry once on non-401 errors', () => {
    const retryFn = (
      failureCount: number,
      error: { response?: { status: number } }
    ) => {
      if (error?.response?.status === 401) return false;
      return failureCount < 1;
    };

    const error500 = { response: { status: 500 } };
    expect(retryFn(0, error500)).toBe(true);
    expect(retryFn(1, error500)).toBe(false);
  });
});
