import type { RoleResp, UserResp } from '@rcabench/client';

/**
 * Extended UserResp – the backend enriches each user with nested roles,
 * but the generated SDK type does not include them. We intersect the
 * SDK type so that callers stay aligned with backend field names.
 */
export type UserRecord = UserResp & {
  /** Always present in list/detail responses */
  id: number;
  username: string;
  email: string;
  is_active: boolean;
  /** Roles attached by the backend (not in generated SDK type) */
  roles?: RoleRecord[];
};

/**
 * Extended RoleResp – the backend includes `scope`, `description`, and
 * `permissions_count` in role responses, but the generated SDK type
 * omits them. We intersect to keep field-name alignment.
 */
export type RoleRecord = RoleResp & {
  /** Always present in role responses */
  id: number;
  name: string;
  scope?: string;
  description?: string;
  permissions_count?: number;
  created_at?: string;
};
