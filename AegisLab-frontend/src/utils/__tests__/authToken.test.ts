import { beforeEach, describe, expect, it } from 'vitest';

import {
  getAccessToken,
  getRefreshToken,
  removeAccessToken,
  removeRefreshToken,
  setAccessToken,
  setRefreshToken,
} from '../authToken';

describe('authToken', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  describe('getAccessToken', () => {
    it('returns token from localStorage', () => {
      localStorage.setItem('access_token', 'test-token');
      expect(getAccessToken()).toBe('test-token');
    });

    it('returns null when no token', () => {
      expect(getAccessToken()).toBeNull();
    });
  });

  describe('getRefreshToken', () => {
    it('returns refresh token from localStorage', () => {
      localStorage.setItem('refresh_token', 'refresh-test');
      expect(getRefreshToken()).toBe('refresh-test');
    });

    it('returns null when no token', () => {
      expect(getRefreshToken()).toBeNull();
    });
  });

  describe('setAccessToken', () => {
    it('stores token in localStorage', () => {
      setAccessToken('new-token');
      expect(localStorage.getItem('access_token')).toBe('new-token');
    });
  });

  describe('setRefreshToken', () => {
    it('stores refresh token in localStorage', () => {
      setRefreshToken('new-refresh');
      expect(localStorage.getItem('refresh_token')).toBe('new-refresh');
    });
  });

  describe('removeAccessToken', () => {
    it('removes token from localStorage', () => {
      localStorage.setItem('access_token', 'to-remove');
      removeAccessToken();
      expect(localStorage.getItem('access_token')).toBeNull();
    });
  });

  describe('removeRefreshToken', () => {
    it('removes refresh token from localStorage', () => {
      localStorage.setItem('refresh_token', 'to-remove');
      removeRefreshToken();
      expect(localStorage.getItem('refresh_token')).toBeNull();
    });
  });
});
