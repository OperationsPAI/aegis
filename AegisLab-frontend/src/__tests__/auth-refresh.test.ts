import MockAdapter from 'axios-mock-adapter';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// ----------------------------------------------------------------
// Test 1: authApi.refreshToken correctly unwraps the response
// ----------------------------------------------------------------
describe('authApi.refreshToken', () => {
  it('unwraps .data.data from the axios response', async () => {
    // We mock the default apiClient that authApi imports
    const { default: apiClient } = await import('@/api/client');
    const mock = new MockAdapter(apiClient);

    // apiClient.post('/auth/refresh', ...) returns AxiosResponse
    // where AxiosResponse.data is the server JSON body.
    // Server returns { data: { token, refresh_token } }
    mock.onPost('/auth/refresh').reply(200, {
      data: { token: 'new-access', refresh_token: 'new-refresh' },
    });

    const { authApi } = await import('@/api/auth');
    const result = await authApi.refreshToken('old-refresh');

    expect(result).toEqual({
      token: 'new-access',
      refresh_token: 'new-refresh',
    });

    mock.restore();
  });
});

// ----------------------------------------------------------------
// Test 2: Token storage separates access and refresh tokens
// ----------------------------------------------------------------
describe('auth store token storage', () => {
  beforeEach(() => {
    localStorage.clear();
    vi.resetModules();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('login stores separate access_token and refresh_token', async () => {
    // Mock the authApi module
    vi.doMock('@/api/auth', () => ({
      authApi: {
        login: vi.fn().mockResolvedValue({
          token: 'access-123',
          refresh_token: 'refresh-456',
          user: { id: 1, username: 'testuser' },
        }),
        logout: vi.fn().mockResolvedValue(undefined),
        refreshToken: vi.fn(),
        getProfile: vi.fn(),
      },
    }));

    // Import store after mock is set up
    const { useAuthStore } = await import('@/store/auth');

    await useAuthStore.getState().login('testuser', 'password');

    expect(localStorage.getItem('access_token')).toBe('access-123');
    expect(localStorage.getItem('refresh_token')).toBe('refresh-456');

    // Also check zustand state
    const state = useAuthStore.getState();
    expect(state.accessToken).toBe('access-123');
    expect(state.refreshToken).toBe('refresh-456');
    expect(state.isAuthenticated).toBe(true);

    vi.doUnmock('@/api/auth');
  });

  it('refreshAccessToken updates both tokens when backend rotates refresh_token', async () => {
    localStorage.setItem('access_token', 'old-access');
    localStorage.setItem('refresh_token', 'old-refresh');

    vi.doMock('@/api/auth', () => ({
      authApi: {
        login: vi.fn(),
        logout: vi.fn().mockResolvedValue(undefined),
        refreshToken: vi.fn().mockResolvedValue({
          token: 'new-access',
          refresh_token: 'new-refresh',
        }),
        getProfile: vi.fn(),
      },
    }));

    const { useAuthStore } = await import('@/store/auth');
    // Re-initialize state from localStorage
    useAuthStore.setState({
      accessToken: 'old-access',
      refreshToken: 'old-refresh',
      isAuthenticated: true,
    });

    await useAuthStore.getState().refreshAccessToken();

    expect(localStorage.getItem('access_token')).toBe('new-access');
    expect(localStorage.getItem('refresh_token')).toBe('new-refresh');

    const state = useAuthStore.getState();
    expect(state.accessToken).toBe('new-access');
    expect(state.refreshToken).toBe('new-refresh');

    vi.doUnmock('@/api/auth');
  });

  it('refreshAccessToken preserves original refresh_token when backend does not rotate', async () => {
    localStorage.setItem('access_token', 'old-access');
    localStorage.setItem('refresh_token', 'original-refresh');

    vi.doMock('@/api/auth', () => ({
      authApi: {
        login: vi.fn(),
        logout: vi.fn().mockResolvedValue(undefined),
        refreshToken: vi.fn().mockResolvedValue({
          token: 'new-access',
          // No refresh_token in response
        }),
        getProfile: vi.fn(),
      },
    }));

    const { useAuthStore } = await import('@/store/auth');
    useAuthStore.setState({
      accessToken: 'old-access',
      refreshToken: 'original-refresh',
      isAuthenticated: true,
    });

    await useAuthStore.getState().refreshAccessToken();

    expect(localStorage.getItem('access_token')).toBe('new-access');
    // refresh_token should be preserved
    expect(localStorage.getItem('refresh_token')).toBe('original-refresh');

    const state = useAuthStore.getState();
    expect(state.refreshToken).toBe('original-refresh');

    vi.doUnmock('@/api/auth');
  });
});

// ----------------------------------------------------------------
// Test 3: Concurrent 401s result in only one refresh call
// ----------------------------------------------------------------
describe('apiClient 401 interceptor mutex', () => {
  it('concurrent 401 responses trigger only one refresh call', async () => {
    // We need a fresh axios import for the raw refresh call
    const axios = (await import('axios')).default;
    const { apiClient } = await import('@/api/client');
    const mock = new MockAdapter(apiClient);

    // Set up tokens
    localStorage.setItem('access_token', 'expired-token');
    localStorage.setItem('refresh_token', 'valid-refresh');

    let refreshCallCount = 0;

    // The interceptor in client.ts uses raw axios.post (not apiClient)
    // for the refresh call. We need to intercept that.
    // Since MockAdapter only mocks the specific instance, we create a
    // separate mock for the raw axios calls used by the interceptor.
    const axiosMock = new MockAdapter(axios);

    axiosMock.onPost('/api/v2/auth/refresh').reply(() => {
      refreshCallCount++;
      return [
        200,
        { data: { token: 'fresh-token', refresh_token: 'fresh-refresh' } },
      ];
    });

    // First call returns 401, retry returns 200
    let callCount = 0;
    mock.onGet('/test-endpoint').reply(() => {
      callCount++;
      if (callCount <= 2) {
        // First two calls get 401 (the original concurrent requests)
        return [401, { message: 'Unauthorized' }];
      }
      // Retried requests succeed
      return [200, { data: 'ok' }];
    });

    // Fire two requests concurrently
    const [res1, res2] = await Promise.all([
      apiClient.get('/test-endpoint'),
      apiClient.get('/test-endpoint'),
    ]);

    expect(res1.status).toBe(200);
    expect(res2.status).toBe(200);
    // The mutex should ensure only one refresh call
    expect(refreshCallCount).toBe(1);

    // Tokens should be updated
    expect(localStorage.getItem('access_token')).toBe('fresh-token');
    expect(localStorage.getItem('refresh_token')).toBe('fresh-refresh');

    mock.restore();
    axiosMock.restore();
  });
});

// ----------------------------------------------------------------
// Test 4: fileClient and arrowClient retry on 401 after refresh
// ----------------------------------------------------------------
describe('fileClient and arrowClient 401 interceptors', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('fileClient retries request after successful token refresh', async () => {
    const axios = (await import('axios')).default;
    const { fileClient } = await import('@/api/client');
    const mock = new MockAdapter(fileClient);
    const axiosMock = new MockAdapter(axios);

    localStorage.setItem('access_token', 'expired-token');
    localStorage.setItem('refresh_token', 'valid-refresh');

    axiosMock.onPost('/api/v2/auth/refresh').reply(200, {
      data: { token: 'fresh-token', refresh_token: 'fresh-refresh' },
    });

    let callCount = 0;
    mock.onGet('/files/download').reply(() => {
      callCount++;
      if (callCount === 1) {
        return [401, { message: 'Unauthorized' }];
      }
      return [200, new Blob(['file-content'])];
    });

    const response = await fileClient.get('/files/download');
    expect(response.status).toBe(200);
    expect(callCount).toBe(2); // original + retry

    mock.restore();
    axiosMock.restore();
  });

  it('arrowClient retries request after successful token refresh', async () => {
    const axios = (await import('axios')).default;
    const { arrowClient } = await import('@/api/client');
    const mock = new MockAdapter(arrowClient);
    const axiosMock = new MockAdapter(axios);

    localStorage.setItem('access_token', 'expired-token');
    localStorage.setItem('refresh_token', 'valid-refresh');

    axiosMock.onPost('/api/v2/auth/refresh').reply(200, {
      data: { token: 'fresh-token', refresh_token: 'fresh-refresh' },
    });

    let callCount = 0;
    mock.onGet('/data/arrow').reply(() => {
      callCount++;
      if (callCount === 1) {
        return [401, { message: 'Unauthorized' }];
      }
      return [200, new ArrayBuffer(8)];
    });

    const response = await arrowClient.get('/data/arrow');
    expect(response.status).toBe(200);
    expect(callCount).toBe(2); // original + retry

    mock.restore();
    axiosMock.restore();
  });
});
