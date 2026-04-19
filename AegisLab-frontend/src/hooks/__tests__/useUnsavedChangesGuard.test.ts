import { renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { useUnsavedChangesGuard } from '@/hooks/useUnsavedChangesGuard';

const { mockUseBlocker, mockConfirm } = vi.hoisted(() => ({
  mockUseBlocker: vi.fn(() => ({ state: 'unblocked' })),
  mockConfirm: vi.fn(),
}));

vi.mock('react-router-dom', () => ({
  useBlocker: mockUseBlocker,
}));

vi.mock('antd', () => ({
  Modal: { confirm: mockConfirm },
}));

describe('useUnsavedChangesGuard', () => {
  let addEventSpy: ReturnType<typeof vi.spyOn>;
  let removeEventSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    addEventSpy = vi.spyOn(window, 'addEventListener');
    removeEventSpy = vi.spyOn(window, 'removeEventListener');
    mockUseBlocker.mockReturnValue({ state: 'unblocked' });
    mockConfirm.mockClear();
  });

  afterEach(() => {
    addEventSpy.mockRestore();
    removeEventSpy.mockRestore();
  });

  it('adds beforeunload listener when isDirty is true', () => {
    renderHook(() => useUnsavedChangesGuard(true));

    const beforeunloadCalls = addEventSpy.mock.calls.filter(
      (call) => call[0] === 'beforeunload'
    );
    expect(beforeunloadCalls.length).toBeGreaterThan(0);
  });

  it('does not add beforeunload listener when isDirty is false', () => {
    renderHook(() => useUnsavedChangesGuard(false));

    const beforeunloadCalls = addEventSpy.mock.calls.filter(
      (call) => call[0] === 'beforeunload'
    );
    expect(beforeunloadCalls.length).toBe(0);
  });

  it('removes beforeunload listener when isDirty changes from true to false', () => {
    const { rerender } = renderHook(
      ({ isDirty }: { isDirty: boolean }) => useUnsavedChangesGuard(isDirty),
      { initialProps: { isDirty: true } }
    );

    const addCalls = addEventSpy.mock.calls.filter(
      (call) => call[0] === 'beforeunload'
    );
    expect(addCalls.length).toBeGreaterThan(0);

    rerender({ isDirty: false });

    const removeCalls = removeEventSpy.mock.calls.filter(
      (call) => call[0] === 'beforeunload'
    );
    expect(removeCalls.length).toBeGreaterThan(0);
  });

  it('cleans up beforeunload listener on unmount', () => {
    const { unmount } = renderHook(() => useUnsavedChangesGuard(true));

    unmount();

    const removeCalls = removeEventSpy.mock.calls.filter(
      (call) => call[0] === 'beforeunload'
    );
    expect(removeCalls.length).toBeGreaterThan(0);
  });

  it('calls useBlocker with true when isDirty is true', () => {
    renderHook(() => useUnsavedChangesGuard(true));
    expect(mockUseBlocker).toHaveBeenCalledWith(true);
  });

  it('calls useBlocker with false when isDirty is false', () => {
    renderHook(() => useUnsavedChangesGuard(false));
    expect(mockUseBlocker).toHaveBeenCalledWith(false);
  });
});
