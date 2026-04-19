/**
 * SDK configuration for @rcabench/client.
 *
 * The generated SDK API classes (e.g. AuthenticationApi, ProjectsApi) accept
 * (configuration, basePath, axios) in their constructor. We pass our shared
 * apiClient so that SDK calls flow through the same interceptors (auth header,
 * token refresh, error handling).
 *
 * NOTE: The existing hand-rolled API modules (src/api/auth.ts, projects.ts, etc.)
 * unwrap responses as `.then(r => r.data.data)`, returning the inner payload
 * directly. The SDK methods return the raw AxiosResponse<GenericResponse<T>>,
 * so migrating callers would require updating every call site. For now we keep
 * the hand-rolled modules and expose sdkConfig + sdkAxios for incremental
 * adoption in new code.
 *
 * Usage for new API integrations:
 *   import { SomeApi } from '@rcabench/client';
 *   import { sdkConfig, sdkAxios } from '@/api/sdk';
 *   const someApi = new SomeApi(sdkConfig, '', sdkAxios);
 *   const resp = await someApi.someMethod({ ... });
 *   const data = resp.data.data; // unwrap GenericResponse
 */
import { Configuration } from '@rcabench/client';

import apiClient from './client';

export const sdkConfig = new Configuration({
  basePath: '',
});

// Re-export apiClient so SDK classes route through our interceptors
export { apiClient as sdkAxios };
