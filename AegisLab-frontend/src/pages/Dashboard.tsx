import { useEffect, useState } from 'react';

import {
  Chip,
  DataTable,
  EmptyState,
  MetricCard,
  MetricLabel,
  MonoValue,
  Panel,
  PanelTitle,
  SectionDivider,
  StatusDot,
  Terminal,
  TimeDisplay,
  TrajectoryTimeline,
  type TerminalLine,
  type TrajectoryStepData,
} from '@/components/ui';
import {
  projectsApi,
  tasksApi,
  tracesApi,
  type ProjectProjectResp,
  type TaskTaskResp,
  type TraceTraceResp,
} from '@/api/portal-client';
import './Dashboard.css';

/* ── Mock trajectory data (demo until API surfaces real steps) ───── */

const DEMO_TRAJECTORY: TrajectoryStepData[] = [
  {
    step: 1,
    timestamp: '14:22:01',
    durationMs: 1240,
    actionType: 'tool_call',
    action: 'query_metrics(service="catalog", metric="latency_p99")',
    thought:
      'The user reports **high latency** in the catalog service. I should first check the `p99 latency` metric to quantify the severity.',
    toolCall: {
      name: 'query_metrics',
      arguments:
        '{\n  "service": "catalog",\n  "metric": "latency_p99",\n  "range": "1h"\n}',
      result:
        '{\n  "value": 2840,\n  "unit": "ms",\n  "baseline": 120\n}',
    },
    observation:
      '> **Observation**: p99 latency spiked to **2.84 s** (baseline 120 ms) at 14:18.',
  },
  {
    step: 2,
    timestamp: '14:22:03',
    durationMs: 890,
    actionType: 'internal',
    action: 'Hypothesis: downstream dependency bottleneck',
    thought:
      'Latency jump is ~24x baseline. Need to check downstream calls — likely database or cache.',
  },
  {
    step: 3,
    timestamp: '14:22:05',
    durationMs: 1560,
    actionType: 'tool_call',
    action: 'get_traces(service="catalog", min_duration_ms=1000)',
    thought:
      'Querying distributed traces for catalog service to identify slow spans.',
    toolCall: {
      name: 'get_traces',
      arguments:
        '{\n  "service": "catalog",\n  "min_duration_ms": 1000,\n  "limit": 10\n}',
      result:
        '{\n  "traces": [\n    { "trace_id": "a1b2c3", "root_span": "GET /products", "duration_ms": 2840 }\n  ]\n}',
    },
    observation:
      'Root span `GET /products` is slow; child span `SELECT * FROM inventory` accounts for **92 %** of total time.',
  },
  {
    step: 4,
    timestamp: '14:22:08',
    durationMs: 2100,
    actionType: 'message',
    action: 'Report RCA conclusion',
    thought:
      'Evidence points to an unindexed query on the inventory table. I will surface this as the root cause.',
    observation:
      '> **Root Cause**: Missing index on `inventory.sku` caused full-table scan under load.\n> **Recommendation**: Add composite index `(sku, warehouse_id)`.',
  },
];

const DEMO_TERMINAL_LINES: TerminalLine[] = [
  { ts: '14:22:01', prefix: 'agent', level: 'info', body: 'query_metrics → catalog.latency_p99' },
  { ts: '14:22:02', prefix: 'metric', level: 'info', body: 'p99=2840ms baseline=120ms delta=+2367%' },
  { ts: '14:22:03', prefix: 'agent', level: 'info', body: 'hypothesis → downstream bottleneck' },
  { ts: '14:22:05', prefix: 'agent', level: 'info', body: 'get_traces → catalog (min=1000ms)' },
  { ts: '14:22:06', prefix: 'trace', level: 'debug', body: 'root=GET /products duration=2840ms' },
  { ts: '14:22:07', prefix: 'trace', level: 'debug', body: 'child=SELECT inventory duration=2612ms' },
  { ts: '14:22:08', prefix: 'agent', level: 'warn', body: 'RCA conclusion → missing index on inventory.sku' },
];

/* ── Dashboard page ──────────────────────────────────────────────── */

export default function Dashboard() {
  const [projects, setProjects] = useState<ProjectProjectResp[]>([]);
  const [tasks, setTasks] = useState<TaskTaskResp[]>([]);
  const [traces, setTraces] = useState<TraceTraceResp[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      try {
        setLoading(true);
        setError(null);

        const [projRes, taskRes, traceRes] = await Promise.all([
          projectsApi.listProjects({ page: 1, size: 20 }),
          tasksApi.listTasks({ page: 1, size: 20 }),
          tracesApi.listTraces({ page: 1, size: 20 }),
        ]);

        if (cancelled) return;

        setProjects(projRes.data.data?.items ?? []);
        setTasks(taskRes.data.data?.items ?? []);
        setTraces(traceRes.data.data?.items ?? []);
      } catch (e) {
        if (cancelled) return;
        setError(e instanceof Error ? e.message : 'Failed to load dashboard data');
      } finally {
        if (!cancelled) setLoading(false);
      }
    }

    load();
    return () => {
      cancelled = true;
    };
  }, []);

  const totalExecutions = projects.reduce(
    (sum, p) => sum + (p.execution_count ?? 0),
    0,
  );
  const runningTasks = tasks.filter((t) => t.state === 'Running').length;

  return (
    <div className="page-wrapper dashboard">
      {/* ── Header ─────────────────────────────────────────────── */}
      <header className="dashboard__header">
        <div className="dashboard__header-left">
          <h1 className="dashboard__title">
            <PanelTitle size="hero" as="span">
              Dashboard
            </PanelTitle>
          </h1>
          <MetricLabel>RCABench · landing page template</MetricLabel>
        </div>
        <div className="dashboard__header-right">
          {loading && <StatusDot size={8} pulse />}
          {error && <Chip tone="warning">API unavailable</Chip>}
          {!loading && !error && <Chip tone="ink">live</Chip>}
        </div>
      </header>

      {/* ── KPI Row ────────────────────────────────────────────── */}
      <section className="dashboard__kpi-row">
        <MetricCard
          label="Projects"
          value={projects.length}
          sparkline={[2, 4, 3, 5, 4, 6, 5, 7, 6, 8, 7, projects.length || 0]}
        />
        <MetricCard
          label="Executions"
          value={totalExecutions}
          sparkline={[10, 15, 12, 20, 18, 25, 22, 30, 28, 35, 32, totalExecutions || 0]}
        />
        <MetricCard
          label="Running Tasks"
          value={runningTasks}
          unit={
            <span className="dashboard__kpi-live">
              {runningTasks > 0 && <StatusDot size={6} pulse />}
              {runningTasks > 0 ? 'active' : 'idle'}
            </span>
          }
        />
        <MetricCard
          label="Traces"
          value={traces.length}
          sparkline={[5, 8, 6, 12, 10, 15, 13, 18, 16, 22, 20, traces.length || 0]}
        />
      </section>

      {/* ── Two-column: Executions + Tasks ─────────────────────── */}
      <section className="dashboard__two-col">
        <Panel
          title={<PanelTitle size="base">Recent Executions</PanelTitle>}
          extra={<MetricLabel>{totalExecutions} total</MetricLabel>}
          className="dashboard__panel"
        >
          {error ? (
            <EmptyState
              title="API error"
              description="Check backend status and retry."
            />
          ) : (
            <DataTable
              columns={[
                {
                  key: 'name',
                  header: 'Project',
                  render: (p) => <MonoValue size="sm">{p.name}</MonoValue>,
                },
                {
                  key: 'executions',
                  header: 'Executions',
                  align: 'right',
                  render: (p) => (
                    <MonoValue size="sm">{p.execution_count ?? 0}</MonoValue>
                  ),
                },
                {
                  key: 'last_run',
                  header: 'Last Run',
                  align: 'right',
                  render: (p) =>
                    p.last_execution_at ? (
                      <TimeDisplay value={p.last_execution_at} />
                    ) : (
                      '—'
                    ),
                },
                {
                  key: 'status',
                  header: 'Status',
                  align: 'center',
                  render: (p) => (
                    <Chip
                      tone={
                        p.status === 'active'
                          ? 'ink'
                          : p.status === 'archived'
                            ? 'ghost'
                            : 'default'
                      }
                    >
                      {p.status ?? 'unknown'}
                    </Chip>
                  ),
                },
              ]}
              data={projects.slice(0, 6)}
              rowKey={(p) => p.id ?? p.name ?? 'unknown'}
              emptyTitle="No projects"
              emptyDescription="Create a project to start running experiments."
            />
          )}
        </Panel>

        <Panel
          title={<PanelTitle size="base">Recent Tasks</PanelTitle>}
          extra={<MetricLabel>{tasks.length} total</MetricLabel>}
          className="dashboard__panel"
        >
          {error ? (
            <EmptyState
              title="API error"
              description="Check backend status and retry."
            />
          ) : tasks.length === 0 ? (
            <EmptyState
              title="No tasks"
              description="Tasks appear when experiments are running."
            />
          ) : (
            <div className="dashboard__task-list">
              {tasks.slice(0, 8).map((t) => (
                <div key={t.id} className="dashboard__task-item">
                  <div className="dashboard__task-left">
                    <StatusDot
                      size={6}
                      pulse={t.state === 'Running'}
                      tone={
                        t.state === 'Running'
                          ? 'ink'
                          : t.state === 'Failed'
                            ? 'warning'
                            : 'muted'
                      }
                    />
                    <MonoValue size="sm">{t.id}</MonoValue>
                  </div>
                  <MetricLabel size="xs">{t.state}</MetricLabel>
                </div>
              ))}
            </div>
          )}
        </Panel>
      </section>

      {/* ── Agent Trajectory ───────────────────────────────────── */}
      <section>
        <SectionDivider>Agent Trajectory · demo</SectionDivider>
        <div className="dashboard__trajectory-host">
          <TrajectoryTimeline
            agentName="rca-agent"
            status="completed"
            totalDurationMs={4790}
            steps={DEMO_TRAJECTORY}
          />
        </div>
      </section>

      {/* ── Terminal ───────────────────────────────────────────── */}
      <section>
        <SectionDivider>Live Logs · demo</SectionDivider>
        <Terminal lines={DEMO_TERMINAL_LINES} />
      </section>

      {/* ── API Debug ──────────────────────────────────────────── */}
      <Panel
        title={<PanelTitle size="base">API Debug</PanelTitle>}
        extra={<MetricLabel>@OperationsPAI/portal v1.3.0</MetricLabel>}
        className="dashboard__panel"
      >
        {error ? (
          <div className="dashboard__debug-error">
            <pre>{error}</pre>
          </div>
        ) : (
          <div className="dashboard__debug-grid">
            <div className="dashboard__debug-col">
              <MetricLabel size="xs">projects</MetricLabel>
              <MonoValue size="sm">{projects.length} items</MonoValue>
            </div>
            <div className="dashboard__debug-col">
              <MetricLabel size="xs">tasks</MetricLabel>
              <MonoValue size="sm">{tasks.length} items</MonoValue>
            </div>
            <div className="dashboard__debug-col">
              <MetricLabel size="xs">traces</MetricLabel>
              <MonoValue size="sm">{traces.length} items</MonoValue>
            </div>
          </div>
        )}
      </Panel>
    </div>
  );
}
