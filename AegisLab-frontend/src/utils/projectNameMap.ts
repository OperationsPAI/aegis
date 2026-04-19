/**
 * Project name→id mapping cache utility
 * Persisted in localStorage so lookups survive page refreshes.
 */
import type { ProjectResp } from '@rcabench/client';

export interface ProjectNameMapEntry {
  id: number;
  name: string;
  cachedAt: number;
}

const STORAGE_KEY = 'aegislab_project_name_map';
const TTL_MS = 30 * 60 * 1000; // 30 minutes

function readMap(): Record<string, ProjectNameMapEntry> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    return raw ? JSON.parse(raw) : {};
  } catch {
    return {};
  }
}

function writeMap(map: Record<string, ProjectNameMapEntry>): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(map));
  } catch {
    // localStorage might be full – ignore
  }
}

/**
 * Update name→id cache from project list (persists to localStorage)
 */
export function updateProjectNameMap(
  projects: ProjectResp[] | undefined
): void {
  if (!projects || projects.length === 0) return;

  const map = readMap();
  const now = Date.now();
  projects.forEach((project) => {
    if (project.id && project.name) {
      map[project.name] = { id: project.id, name: project.name, cachedAt: now };
    }
  });
  writeMap(map);
}

/**
 * Get project ID from localStorage cache (TTL: 30 min)
 */
export function getProjectIdFromName(
  projectName: string | undefined
): number | undefined {
  if (!projectName) return undefined;

  const map = readMap();
  const entry = map[projectName];
  if (entry && Date.now() - entry.cachedAt < TTL_MS) {
    return entry.id;
  }
  return undefined;
}
