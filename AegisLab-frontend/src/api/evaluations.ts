import type {
  EvaluateDatapackItem,
  EvaluateDatapackSpec,
  EvaluateDatasetSpec,
} from '@rcabench/client';

import apiClient from './client';

export interface EvaluationListResponse {
  items: EvaluateDatapackItem[];
  total: number;
}

export const evaluationApi = {
  getEvaluations: (params?: {
    page?: number;
    size?: number;
  }): Promise<EvaluationListResponse> =>
    apiClient
      .get('/evaluations', { params })
      .then((r) => r.data.data ?? { items: [], total: 0 }),

  getEvaluation: (id: number): Promise<EvaluateDatapackItem> =>
    apiClient
      .get(`/evaluations/${id}`)
      .then((r) => r.data.data ?? ({} as EvaluateDatapackItem)),

  deleteEvaluation: (id: number) =>
    apiClient.delete(`/evaluations/${id}`).then((r) => r.data),

  evaluateDatasets: (specs: EvaluateDatasetSpec[]) =>
    apiClient.post('/evaluations/datasets', { specs }).then((r) => r.data),

  evaluateDatapacks: (specs: EvaluateDatapackSpec[]) =>
    apiClient.post('/evaluations/datapacks', { specs }).then((r) => r.data),
};
