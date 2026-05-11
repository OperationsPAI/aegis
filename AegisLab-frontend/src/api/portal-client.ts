import {
  AuthenticationApi,
  Configuration,
  ContainersApi,
  DatasetsApi,
  EvaluationsApi,
  ExecutionsApi,
  GroupsApi,
  InjectionsApi,
  LabelsApi,
  MetricsApi,
  NotificationsApi,
  ObservationApi,
  ProjectsApi,
  TasksApi,
  TeamsApi,
  TracesApi,
} from '@OperationsPAI/portal';

const config = new Configuration({
  basePath: '/api',
});

export const authenticationApi = new AuthenticationApi(config);
export const containersApi = new ContainersApi(config);
export const datasetsApi = new DatasetsApi(config);
export const evaluationsApi = new EvaluationsApi(config);
export const executionsApi = new ExecutionsApi(config);
export const groupsApi = new GroupsApi(config);
export const injectionsApi = new InjectionsApi(config);
export const labelsApi = new LabelsApi(config);
export const metricsApi = new MetricsApi(config);
export const notificationsApi = new NotificationsApi(config);
export const observationApi = new ObservationApi(config);
export const projectsApi = new ProjectsApi(config);
export const tasksApi = new TasksApi(config);
export const teamsApi = new TeamsApi(config);
export const tracesApi = new TracesApi(config);

export type {
  Configuration,
  ConfigurationParameters,
  ProjectProjectResp,
  TaskTaskResp,
  TraceTraceResp,
} from '@OperationsPAI/portal';
