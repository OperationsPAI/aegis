import { message } from 'antd';
import axios, { type AxiosInstance, type AxiosRequestConfig } from 'axios';

const BASE_PATH = '/api/v2';

let refreshPromise: Promise<string> | null = null;

function addAuthRequestInterceptor(client: AxiosInstance): void {
  client.interceptors.request.use(
    (config) => {
      const token = localStorage.getItem('access_token');
      if (token && config.headers) {
        config.headers.Authorization = `Bearer ${token}`;
      }
      return config;
    },
    (error) => Promise.reject(error)
  );
}

function addAuthResponseInterceptor(client: AxiosInstance): void {
  client.interceptors.response.use(
    (response) => response,
    async (error) => {
      const originalRequest = error.config as AxiosRequestConfig & {
        _retry?: boolean;
      };
      if (error.response?.status === 401 && !originalRequest._retry) {
        originalRequest._retry = true;
        try {
          const refreshToken = localStorage.getItem('refresh_token');
          if (refreshToken) {
            if (!refreshPromise) {
              refreshPromise = axios
                .post(`${BASE_PATH}/auth/refresh`, { token: refreshToken })
                .then((response) => {
                  const inner = response.data?.data ?? response.data;
                  const newToken = inner.token;
                  const newRefresh = inner.refresh_token ?? refreshToken;
                  localStorage.setItem('access_token', newToken);
                  localStorage.setItem('refresh_token', newRefresh);
                  return newToken;
                })
                .finally(() => {
                  refreshPromise = null;
                });
            }
            const token = await refreshPromise;
            if (originalRequest.headers) {
              originalRequest.headers.Authorization = `Bearer ${token}`;
            }
            return client(originalRequest);
          }
        } catch (refreshError) {
          localStorage.removeItem('access_token');
          localStorage.removeItem('refresh_token');
          window.location.href = '/login';
          return Promise.reject(refreshError);
        }
      }
      const errorMessage =
        (error.response?.data as { message?: string })?.message ||
        error.message ||
        'Request failed';
      message.error(errorMessage);
      return Promise.reject(error);
    }
  );
}

export const apiClient: AxiosInstance = axios.create({
  baseURL: BASE_PATH,
  timeout: 30000,
  headers: { 'Content-Type': 'application/json' },
});
addAuthRequestInterceptor(apiClient);
addAuthResponseInterceptor(apiClient);

export const fileClient: AxiosInstance = axios.create({
  baseURL: BASE_PATH,
  timeout: 60000,
  responseType: 'blob',
});
addAuthRequestInterceptor(fileClient);
addAuthResponseInterceptor(fileClient);

export const arrowClient: AxiosInstance = axios.create({
  baseURL: BASE_PATH,
  timeout: 60000,
  responseType: 'arraybuffer',
});
addAuthRequestInterceptor(arrowClient);
addAuthResponseInterceptor(arrowClient);

export default apiClient;
