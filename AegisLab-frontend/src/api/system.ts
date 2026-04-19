import apiClient from './client';

export const systemApi = {
  getSystemMetrics: () =>
    apiClient.get('/system/metrics').then((r) => r.data.data),

  getSystemMetricsHistory: () =>
    apiClient.get('/system/metrics/history').then((r) => r.data.data),
};

// Keep backward-compatible named exports
export const getSystemMetrics = systemApi.getSystemMetrics;
export const getSystemMetricsHistory = systemApi.getSystemMetricsHistory;

export default systemApi;
