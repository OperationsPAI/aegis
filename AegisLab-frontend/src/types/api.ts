// Re-export commonly used types from @rcabench/client
export type {
  ExecutionResp,
  ProjectResp,
  ProjectDetailResp,
  UserDetailResp,
  UserResp,
  ListTeamResp,
} from '@rcabench/client';

export { ExecutionState, FaultType } from '@rcabench/client';

/**
 * Note: ProjectDetailResp should include the following fields for team/owner navigation:
 * - team_name?: string - The name of the team that owns the project
 * - owner_name?: string - The name of the owner (user or team)
 * These fields are used in breadcrumb navigation to show: teamName > Projects > projectName > page
 *
 * When backend API is updated, ensure these fields are included in the ProjectDetailResp type.
 */

// ============================================================================
// Frontend-only types (no backend equivalent)
// ============================================================================

/**
 * Parameter definition for a fault type configuration.
 * Used by the injection creation UI to render dynamic forms.
 */
export interface FaultParameter {
  name: string;
  type: 'string' | 'number' | 'boolean' | 'select' | 'range';
  label: string;
  description?: string;
  required?: boolean;
  default?: string | number | boolean;
  options?: string[];
  min?: number;
  max?: number;
  step?: number;
}

/**
 * Frontend representation of a fault type with its configuration parameters.
 * Built from injection metadata API response for use in injection creation UI.
 * NOT the same as the SDK's FaultType enum.
 */
export interface FaultTypeConfig {
  id?: number;
  name: string;
  type: string;
  category?: string;
  description?: string;
  parameters: FaultParameter[];
}

// Project Visibility
export type ProjectVisibility = 'private' | 'team' | 'public';

// ============================================================================
// Team types (frontend-specific shapes not matching SDK responses directly)
// TODO: Migrate consumers to use SDK's TeamDetailResp / TeamMemberResp where possible
// ============================================================================

export interface Team {
  id: number;
  name: string;
  display_name?: string;
  description?: string;
  avatar_url?: string;
  created_at: string;
  updated_at: string;
  member_count: number;
  project_count: number;
  settings?: TeamSettings;
}

export interface TeamSettings {
  customization_enabled: boolean;
  default_ttl?: number;
  ttl_permissions?: 'admins' | 'members';
}

export type TeamRole = 'owner' | 'admin' | 'member';

export interface TeamMember {
  id: number;
  user_id: number;
  team_id: number;
  role: TeamRole;
  joined_at: string;
  user: {
    id: number;
    username: string;
    display_name: string;
    email: string;
    avatar_url?: string;
  };
}

export interface TeamSecret {
  id: number;
  name: string;
  created_at: string;
  updated_at: string;
  created_by: string;
}

// ============================================================================
// Profile types (used by profile components)
// TODO: Replace with SDK types when backend profile API is finalized
// ============================================================================

export interface ActivityContribution {
  date: string; // YYYY-MM-DD
  count: number;
}

export interface ProjectWithStats {
  id: number;
  name: string;
  is_public?: boolean;
  visibility?: ProjectVisibility;
  updated_at?: string;
  created_at?: string;
  run_count?: number;
  last_run_at?: string;
  is_starred?: boolean;
}
