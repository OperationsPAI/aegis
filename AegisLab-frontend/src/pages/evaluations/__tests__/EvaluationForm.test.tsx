import { readFileSync } from 'fs';
import { resolve } from 'path';
import { describe, expect, it, vi } from 'vitest';

// Mock APIs before importing components
vi.mock('@/api/containers', () => ({
  containerApi: {
    getContainers: vi.fn().mockResolvedValue({
      items: [{ id: 1, name: 'test-algo', type: 'Algorithm' }],
    }),
    getVersions: vi.fn().mockResolvedValue({
      items: [{ name: 'v1.0' }, { name: 'v2.0' }],
    }),
  },
}));

vi.mock('@/api/evaluations', () => ({
  evaluationApi: {
    createEvaluation: vi.fn().mockResolvedValue({ id: 1 }),
  },
}));

vi.mock('@/api/projects', () => ({
  projectApi: {
    getProjects: vi.fn().mockResolvedValue({ items: [] }),
  },
}));

vi.mock('react-router-dom', async () => {
  const actual: Record<string, unknown> =
    await vi.importActual('react-router-dom');
  return {
    ...actual,
    useParams: () => ({ projectId: '1' }),
    useNavigate: () => vi.fn(),
  };
});

describe('EvaluationForm', () => {
  const sourceFile = resolve(__dirname, '../EvaluationForm.tsx');

  it('does not use setInterval for fake progress', () => {
    const source = readFileSync(sourceFile, 'utf-8');
    expect(source).not.toContain('setInterval');
  });

  it('submit button should use mutation loading state, not fake progress', () => {
    const source = readFileSync(sourceFile, 'utf-8');
    // Should not contain fake progress percentage tracking
    expect(source).not.toMatch(/setProgress\s*\(/);
    expect(source).not.toMatch(/progress.*%/);
  });
});
