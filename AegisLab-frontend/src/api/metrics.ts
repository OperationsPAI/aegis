import apiClient from './client';

export const metricsApi = {
  getInjectionMetrics: (params?: Record<string, unknown>) =>
    apiClient.get('/metrics/injections', { params }).then((r) => r.data.data),

  getExecutionMetrics: (params?: Record<string, unknown>) =>
    apiClient.get('/metrics/executions', { params }).then((r) => r.data.data),

  getAlgorithmMetrics: (params?: Record<string, unknown>) =>
    apiClient.get('/metrics/algorithms', { params }).then((r) => r.data.data),
};
