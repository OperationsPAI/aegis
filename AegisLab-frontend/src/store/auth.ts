import type { LoginResp, UserInfo } from '@rcabench/client';
import { create } from 'zustand';

import { authApi } from '@/api/auth';
import {
  getAccessToken,
  getRefreshToken,
  removeAccessToken,
  removeRefreshToken,
  setAccessToken,
  setRefreshToken,
} from '@/utils/authToken';

/**
 * Extended user type that includes fields returned by the API
 * but not yet present in the generated SDK types.
 */
export type User = UserInfo & {
  is_superuser?: boolean;
};

interface AuthState {
  user: User | null;
  accessToken: string | null;
  refreshToken: string | null;
  isAuthenticated: boolean;
  loading: boolean;

  // Actions
  login: (username: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  refreshAccessToken: () => Promise<void>;
  loadUser: () => Promise<void>;
  setUser: (user: User | null) => void;
}

export const useAuthStore = create<AuthState>((set, get) => ({
  user: null,
  accessToken: getAccessToken(),
  refreshToken: getRefreshToken(),
  isAuthenticated: !!getAccessToken(),
  loading: false,

  login: async (username: string, password: string) => {
    set({ loading: true });
    try {
      const response = await authApi.login({ username, password });
      const token = (response as LoginResp)?.token;
      const user = (response as LoginResp)?.user;

      // Backend returns 'token' instead of 'access_token'
      // Store the same token as both access and refresh token for now
      const rt = (response as Record<string, unknown>)?.refresh_token as
        | string
        | undefined;
      if (token) {
        setAccessToken(token);
        setRefreshToken(rt ?? token);
      }

      set({
        user,
        accessToken: token,
        refreshToken: rt ?? token,
        isAuthenticated: true,
        loading: false,
      });
    } catch (error) {
      set({ loading: false });
      throw error;
    }
  },

  logout: async () => {
    try {
      await authApi.logout();
    } catch {
      // Logout errors are non-critical
    } finally {
      removeAccessToken();
      removeRefreshToken();

      set({
        user: null,
        accessToken: null,
        refreshToken: null,
        isAuthenticated: false,
      });
    }
  },

  refreshAccessToken: async () => {
    const { refreshToken } = get();
    if (!refreshToken) {
      throw new Error('No refresh token available');
    }

    try {
      // Use the actual refresh endpoint instead of login
      const response = await authApi.refreshToken(refreshToken);
      const newAccessToken = response?.token ?? '';
      const newRefreshToken = response?.refresh_token ?? refreshToken;

      setAccessToken(newAccessToken);
      setRefreshToken(newRefreshToken);

      set({
        accessToken: newAccessToken,
        refreshToken: newRefreshToken,
      });
    } catch (error) {
      get().logout();
      throw error;
    }
  },

  loadUser: async () => {
    const { accessToken } = get();
    if (!accessToken) return;

    set({ loading: true });
    try {
      const response = await authApi.getProfile();
      set({
        user: response,
        loading: false,
      });
    } catch (error) {
      set({ loading: false });
      get().logout();
    }
  },

  setUser: (user: User | null) => {
    set({ user });
  },
}));
