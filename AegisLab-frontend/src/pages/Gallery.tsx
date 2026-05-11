import {
  LogoutOutlined,
  SettingOutlined,
  UserOutlined,
} from '@ant-design/icons';
import {
  Button,
  Form,
  Input,
  Modal,
  Progress,
  Select,
  Switch,
  Table,
  Tabs,
  Tag,
  Tooltip,
} from 'antd';
import { useState, type ReactNode } from 'react';

import {
  Avatar,
  BlastRadiusBar,
  Breadcrumb,
  Chip,
  CodeBlock,
  ControlListItem,
  DangerZone,
  DataTable,
  DropdownMenu,
  EmptyState,
  FormRow,
  KeyValueList,
  MetricCard,
  MetricLabel,
  MonoValue,
  PageHeader,
  Panel,
  PanelTitle,
  ProjectSelector,
  SectionDivider,
  SettingsSection,
  SparkLine,
  StatBlock,
  StatusDot,
  Tabs as RosettaTabs,
  Terminal,
  TimeDisplay,
  Toolbar,
  ToolCallCard,
  TrajectoryStep,
  TrajectoryTimeline,
  type KeyValueItem,
  type TerminalLine,
  type ToolCallData,
  type TrajectoryStepData,
} from '@/components/ui';
import './Gallery.css';

/* ── Static specimen data ──────────────────────────────────────────── */

const COLOR_TOKENS: Array<{
  name: string;
  value: string;
  desc: string;
  inverted?: boolean;
}> = [
  { name: '--bg-page', value: '#F5F5F7', desc: 'Page surface' },
  { name: '--bg-panel', value: '#FFFFFF', desc: 'Panel surface' },
  {
    name: '--bg-inverted',
    value: '#000000',
    desc: 'Active / inverted',
    inverted: true,
  },
  { name: '--bg-muted', value: '#E8E8ED', desc: 'Muted surface' },
  { name: '--bg-warning-soft', value: '#FEF2F2', desc: 'Warning wash' },
  { name: '--accent-warning', value: '#E11D48', desc: 'Anomaly accent' },
];

const TYPE_SAMPLES: Array<{
  family: 'brand' | 'ui' | 'mono';
  label: string;
  sample: string;
  hint: string;
}> = [
  {
    family: 'brand',
    label: 'Display · Geist',
    sample: 'Resilience under pressure',
    hint: 'Section voice — panel titles & hero',
  },
  {
    family: 'ui',
    label: 'UI · Inter',
    sample: 'The quick brown fox jumps over the lazy dog',
    hint: 'Default body / control text',
  },
  {
    family: 'mono',
    label: 'Data · JetBrains Mono',
    sample: '0.142 · 9 384 122 ms · v0.1.0-rc4',
    hint: 'Numbers, IDs, parameters — tabular nums',
  },
];

const SPARK_RISING = [42, 41, 44, 43, 47, 50, 49, 52, 55, 58, 62, 60, 64, 68, 71, 73];
const SPARK_DIP = [80, 78, 79, 81, 77, 70, 62, 51, 43, 38, 35, 41, 50, 58, 64, 67];
const SPARK_FLAT = [50, 51, 49, 52, 50, 51, 49, 50, 51, 50, 49, 51, 50, 50, 51, 49];

const KV_PARAMS: KeyValueItem[] = [
  { k: 'jitter', v: '40 ms' },
  { k: 'latency_cap', v: '200 ms' },
  { k: 'packet_loss', v: '0.02 %' },
  { k: 'threads', v: '128' },
];

const KV_META: KeyValueItem[] = [
  { k: 'Run ID', v: 'EXP-29F1-ALPHA' },
  { k: 'Duration', v: '00:14:22' },
  { k: 'Cluster', v: 'EU-WEST-01' },
  { k: 'Operator', v: 'rosetta@aegis' },
];

const TERMINAL_LINES: TerminalLine[] = [
  {
    ts: '14:22:01',
    prefix: 'AEGIS:',
    body: 'Initializing experiment "playing-the-world".',
  },
  {
    ts: '14:22:05',
    prefix: 'WORKER_ALPHA:',
    body: 'Connection established. Heartbeat 12 ms.',
  },
  {
    ts: '14:23:12',
    prefix: 'INJECTOR:',
    body: 'Executing "Clock Drift" on cluster EU-WEST-01.',
  },
  {
    ts: '14:23:13',
    prefix: 'OBSERVABILITY:',
    body: 'Detecting latency variance (+450 ms).',
  },
  {
    ts: '14:23:18',
    prefix: 'SYSTEM:',
    body: 'Warning. Data consistency thresholds breached in node 04.',
  },
];

/* ── Agent trajectory specimen data ───────────────────────────────── */

const TOOL_QUERY_METRICS: ToolCallData = {
  name: 'query_metrics',
  arguments: '{\n  "service": "catalog",\n  "metric": "latency_p99",\n  "window": "5m"\n}',
  result: '{\n  "value": 482,\n  "unit": "ms",\n  "baseline": 120,\n  "severity": "critical"\n}',
};

const TOOL_GET_TRACES: ToolCallData = {
  name: 'get_traces',
  arguments: '{\n  "service": "catalog",\n  "span_kind": "server",\n  "limit": 10\n}',
  result: '{\n  "traces": 10,\n  "avg_duration_ms": 412,\n  "slowest_span": "catalog→redis"\n}',
};

const TOOL_REPORT_RCA: ToolCallData = {
  name: 'report_rca',
  arguments: '{\n  "cause": "redis_pool_saturation",\n  "confidence": 0.92,\n  "evidence": [\n    "catalog p99 latency 482ms (4x baseline)",\n    "catalog→redis span latency 380ms (31x baseline)"\n  ]\n}',
  result: '{\n  "status": "reported",\n  "rca_id": "RCA-2024-0510-001"\n}',
};

const TRAJECTORY_STEPS: TrajectoryStepData[] = [
  {
    step: 1,
    timestamp: '14:22:01',
    durationMs: 1240,
    thought:
      'The user is reporting **high latency** in the `catalog` service. I need to first check the current metrics to understand the scope of the issue.\n\n> **Hypothesis**: The latency spike is caused by either:\n> 1. Database connection pool exhaustion\n> 2. Downstream dependency degradation\n> 3. Network partition between services',
    action: '`query_metrics(service="catalog", metric="latency_p99", window="5m")`',
    actionType: 'tool_call',
    toolCall: TOOL_QUERY_METRICS,
    observation:
      'p99 latency is **482 ms**, which is **4x** the baseline of `120 ms`. This is a significant anomaly.',
  },
  {
    step: 2,
    timestamp: '14:22:03',
    durationMs: 890,
    thought:
      'Latency spike confirmed. Let me check **error rates** and downstream service health to narrow the root cause.',
    action: '`query_metrics(service="catalog", metric="error_rate", window="5m")`',
    actionType: 'tool_call',
    toolCall: {
      name: 'query_metrics',
      arguments: '{\n  "service": "catalog",\n  "metric": "error_rate",\n  "window": "5m"\n}',
      result: '{\n  "value": 0.02,\n  "unit": "%",\n  "baseline": 0.01\n}',
    },
    observation:
      'Error rate is slightly elevated (`0.02%` vs baseline `0.01%`) but not the primary issue. Need to trace downstream calls.',
  },
  {
    step: 3,
    timestamp: '14:22:05',
    durationMs: 2100,
    thought:
      'Checking trace data for `catalog` service calls in the last 5 minutes to identify where latency is introduced.\n\n```\nExpected spans:\n- gateway → catalog\n- catalog → redis\n- catalog → postgres\n```',
    action: '`get_traces(service="catalog", span_kind="server", limit=10)`',
    actionType: 'tool_call',
    toolCall: TOOL_GET_TRACES,
    observation:
      'Traces show `catalog→redis` calls averaging **380 ms** (baseline: **12 ms**). Redis connection pool appears saturated.\n\nSee [redis dashboard](/redis) for details.',
  },
  {
    step: 4,
    timestamp: '14:22:08',
    durationMs: 560,
    thought:
      'I have enough evidence. The root cause is **redis connection pool saturation** causing the catalog service latency spike.\n\n**Confidence: 92%**',
    action: '`report_rca(cause="redis_pool_saturation", confidence=0.92, evidence=[...])`',
    actionType: 'tool_call',
    toolCall: TOOL_REPORT_RCA,
  },
];

const TABLE_DATA = [
  {
    key: 1,
    id: 'INJ-0001',
    type: 'NetworkLatency',
    target: 'order-svc',
    duration: '120 s',
    state: 'running',
  },
  {
    key: 2,
    id: 'INJ-0002',
    type: 'CPUStress',
    target: 'cart-svc',
    duration: '300 s',
    state: 'queued',
  },
  {
    key: 3,
    id: 'INJ-0003',
    type: 'PodKill',
    target: 'inventory',
    duration: '—',
    state: 'failed',
  },
];

const TABLE_COLUMNS = [
  {
    title: 'ID',
    dataIndex: 'id',
    key: 'id',
    render: (v: string) => (
      <MonoValue size="sm" weight="regular">
        {v}
      </MonoValue>
    ),
  },
  { title: 'Type', dataIndex: 'type', key: 'type' },
  { title: 'Target', dataIndex: 'target', key: 'target' },
  {
    title: 'Duration',
    dataIndex: 'duration',
    key: 'duration',
    render: (v: string) => (
      <MonoValue size="sm" weight="regular">
        {v}
      </MonoValue>
    ),
  },
  {
    title: 'State',
    dataIndex: 'state',
    key: 'state',
    render: (v: string) => {
      const tone = v === 'running' ? 'ink' : v === 'failed' ? 'warning' : 'default';
      return <Chip tone={tone}>{v}</Chip>;
    },
  },
];

/* ── Helpers ────────────────────────────────────────────────────────── */

interface SpecimenProps {
  caption: string;
  children: ReactNode;
  span?: 1 | 2 | 3;
}

function Specimen({ caption, children, span = 1 }: SpecimenProps) {
  return (
    <div
      className={`gallery__specimen gallery__specimen--span-${span}`}
    >
      <div className="gallery__specimen-stage">{children}</div>
      <MetricLabel as="div" size="xs" className="gallery__specimen-caption">
        {caption}
      </MetricLabel>
    </div>
  );
}

/* ── Roadmap previews ─────────────────────────────────────────────── */
/**
 * Each roadmap card is a placeholder for a component the experiment-observation
 * page is going to need but isn't built yet. The preview shows the visual
 * intent; the reference link points at the implementation pattern we'd build
 * on top of.
 */

interface RoadmapSpec {
  name: string;
  desc: string;
  status: string;
  reference?: { label: string; url: string };
  preview: ReactNode;
}

interface RoadmapGroup {
  label: string;
  cards: RoadmapSpec[];
}

const REF_ANTD = { label: 'ant-design/ant-design', url: 'https://github.com/ant-design/ant-design' };
const REF_ECHARTS = { label: 'apache/echarts', url: 'https://github.com/apache/echarts' };
const REF_MONACO = { label: 'microsoft/monaco-editor', url: 'https://github.com/microsoft/monaco-editor' };

const ROADMAP_GROUPS: RoadmapGroup[] = [
  {
    label: 'Shell & layout',
    cards: [
      {
        name: 'ExperimentHeader',
        desc: 'Sticky anchor: id, fault chips, time window, status, run-by — drives every panel below.',
        status: 'Composition',
        preview: (
          <div className="mock-exp-header">
            <StatusDot pulse />
            <Chip tone="ink">INJ-29F1</Chip>
            <span className="mock-exp-header__title">kafka loadgen drift</span>
            <MonoValue size="sm" weight="regular">14:22 → 14:36</MonoValue>
            <Chip>EU-WEST-01</Chip>
          </div>
        ),
      },
      {
        name: 'ExperimentSummaryCard',
        desc: 'Compact tile for list pages: id, fault chips, blast bar, status — the row in a multi-experiment list.',
        status: 'Composition',
        preview: (
          <div className="mock-summary">
            <div className="mock-summary__head">
              <PanelTitle size="sm">INJ-29F1</PanelTitle>
              <Chip tone="ink">running</Chip>
            </div>
            <div className="mock-summary__chips">
              <Chip>NetworkLatency</Chip>
              <Chip>order-svc</Chip>
            </div>
            <BlastRadiusBar value={42} hideTicks />
          </div>
        ),
      },
      {
        name: 'TabbedWorkbench',
        desc: 'Spec / Logs / Traces / Metrics / Code / Config / RCA — main scaffold of the experiment view.',
        status: 'Wraps AntD',
        reference: REF_ANTD,
        preview: (
          <div className="mock-tabs">
            <span className="mock-tabs__tab">Spec</span>
            <span className="mock-tabs__tab mock-tabs__tab--active">Logs</span>
            <span className="mock-tabs__tab">Traces</span>
            <span className="mock-tabs__tab">Metrics</span>
            <span className="mock-tabs__tab">Code</span>
            <span className="mock-tabs__tab">RCA</span>
          </div>
        ),
      },
      {
        name: 'DetailDrawer',
        desc: 'Slide-in side sheet to inspect a single span / log / event without leaving the experiment view.',
        status: 'Wraps AntD',
        reference: REF_ANTD,
        preview: (
          <div className="mock-drawer">
            <div className="mock-drawer__page">main view</div>
            <div className="mock-drawer__sheet">
              <MetricLabel size="xs">→ inspect</MetricLabel>
              <MonoValue size="sm" weight="regular">span-a3f29</MonoValue>
            </div>
          </div>
        ),
      },
      {
        name: 'SplitPane',
        desc: 'Two-pane resizable layout (chart on top, log on bottom — both share the time brush).',
        status: 'Planned',
        reference: { label: 'bvaughn/react-resizable-panels', url: 'https://github.com/bvaughn/react-resizable-panels' },
        preview: (
          <div className="mock-split">
            <div className="mock-split__pane">chart</div>
            <div className="mock-split__handle" aria-hidden>⋮</div>
            <div className="mock-split__pane">logs</div>
          </div>
        ),
      },
    ],
  },
  {
    label: 'Time & correlation',
    cards: [
      {
        name: 'FaultWindow',
        desc: 'Pre / fault / recovery / post horizontal bar; brushable; the experiment-wide time source.',
        status: 'Planned',
        reference: { label: 'd3/d3-brush', url: 'https://github.com/d3/d3-brush' },
        preview: (
          <div className="mock-fault-window">
            <div className="mock-fault-window__bar">
              <span className="mock-fault-window__seg mock-fault-window__seg--pre" style={{ flex: '1 1 25%' }}>pre</span>
              <span className="mock-fault-window__seg mock-fault-window__seg--fault" style={{ flex: '1 1 35%' }}>fault</span>
              <span className="mock-fault-window__seg mock-fault-window__seg--recover" style={{ flex: '1 1 25%' }}>recover</span>
              <span className="mock-fault-window__seg mock-fault-window__seg--post" style={{ flex: '1 1 15%' }}>post</span>
            </div>
            <div className="mock-fault-window__ticks">
              <span>14:00</span><span>14:08</span><span>14:24</span><span>14:30</span>
            </div>
          </div>
        ),
      },
      {
        name: 'CorrelationCursor',
        desc: 'Cross-pane vertical cursor — hover any chart, the rest sync. Killer feature, needs a TimeContext.',
        status: 'Planned',
        reference: { label: 'airbnb/visx', url: 'https://github.com/airbnb/visx' },
        preview: (
          <div className="mock-corr">
            <svg className="mock-corr__line" viewBox="0 0 200 26" preserveAspectRatio="none">
              <path d="M0,18 L20,16 L40,14 L60,12 L80,8 L100,5 L120,8 L140,14 L160,18 L180,22 L200,20" fill="none" stroke="currentColor" strokeWidth="1.2" />
            </svg>
            <svg className="mock-corr__line" viewBox="0 0 200 26" preserveAspectRatio="none">
              <path d="M0,14 L20,15 L40,13 L60,11 L80,16 L100,20 L120,18 L140,12 L160,10 L180,12 L200,14" fill="none" stroke="currentColor" strokeWidth="1.2" />
            </svg>
            <svg className="mock-corr__line" viewBox="0 0 200 26" preserveAspectRatio="none">
              <path d="M0,18 L20,16 L40,18 L60,12 L80,6 L100,4 L120,8 L140,14 L160,20 L180,22 L200,24" fill="none" stroke="currentColor" strokeWidth="1.2" />
            </svg>
            <div className="mock-corr__cursor" />
          </div>
        ),
      },
      {
        name: 'AnnotationBand',
        desc: 'Colored vertical band overlaid on any chart, marking the fault-active interval.',
        status: 'Wraps ECharts',
        reference: REF_ECHARTS,
        preview: (
          <svg className="mock-chart" viewBox="0 0 200 50" preserveAspectRatio="none">
            <rect x="60" y="0" width="60" height="50" fill="#e11d48" opacity="0.10" />
            <line x1="60" y1="0" x2="60" y2="50" stroke="#e11d48" strokeWidth="1" strokeDasharray="2 2" />
            <line x1="120" y1="0" x2="120" y2="50" stroke="#e11d48" strokeWidth="1" strokeDasharray="2 2" />
            <path d="M0,30 L20,28 L40,32 L60,30 L80,18 L100,10 L120,16 L140,30 L160,36 L180,32 L200,30" fill="none" stroke="currentColor" strokeWidth="1.5" />
          </svg>
        ),
      },
      {
        name: 'PivotChips',
        desc: 'Top-level service / pod / severity filter tokens — synchronously filter every pane.',
        status: 'Extends Chip',
        reference: REF_ANTD,
        preview: (
          <div className="mock-pivot">
            <span className="mock-pivot__chip">service: catalog<span className="mock-pivot__close">×</span></span>
            <span className="mock-pivot__chip">pod: cart-7d<span className="mock-pivot__close">×</span></span>
            <span className="mock-pivot__chip">severity: error<span className="mock-pivot__close">×</span></span>
          </div>
        ),
      },
      {
        name: 'JumpToTime',
        desc: '"Open this moment in [Logs / Traces / Metrics]" pill — used inside hover tooltips and evidence rows.',
        status: 'Composition',
        preview: (
          <div className="mock-jump">
            <Chip tone="ghost">→ Open in Logs</Chip>
            <Chip tone="ghost">→ Open in Traces</Chip>
          </div>
        ),
      },
    ],
  },
  {
    label: 'Logs',
    cards: [
      {
        name: 'LogTable',
        desc: 'Virtualized structured-log table with severity colors, service tags, expandable JSON payload.',
        status: 'Planned',
        reference: { label: 'bvaughn/react-window', url: 'https://github.com/bvaughn/react-window' },
        preview: (
          <div className="mock-log-table">
            <div className="mock-log-row">
              <span className="mock-log-ts">14:22:01</span>
              <StatusDot tone="ink" />
              <span className="mock-log-svc">catalog</span>
              <span className="mock-log-msg">request received</span>
            </div>
            <div className="mock-log-row">
              <span className="mock-log-ts">14:22:02</span>
              <StatusDot tone="warning" />
              <span className="mock-log-svc">cart</span>
              <span className="mock-log-msg">DB timeout (2/5)</span>
            </div>
            <div className="mock-log-row">
              <span className="mock-log-ts">14:22:02</span>
              <StatusDot tone="warning" pulse />
              <span className="mock-log-svc">payment</span>
              <span className="mock-log-msg">500 internal</span>
            </div>
          </div>
        ),
      },
      {
        name: 'LogDensityHistogram',
        desc: 'Compact bar chart above the log list — click a bar to brush the time range.',
        status: 'Wraps ECharts',
        reference: REF_ECHARTS,
        preview: (
          <div className="mock-histo">
            {[0.2, 0.3, 0.4, 0.5, 0.7, 0.9, 1, 0.85, 0.6, 0.45, 0.3, 0.5, 0.4, 0.3, 0.2].map((h, i) => (
              <span key={i} className="mock-histo__bar" style={{ height: `${h * 100}%` }} />
            ))}
          </div>
        ),
      },
      {
        name: 'LogSearchBar',
        desc: 'Query input + severity facets (info / warn / error / fatal) + regex toggle.',
        status: 'Composition',
        preview: (
          <div className="mock-search">
            <div className="mock-search__input">message:* timeout</div>
            <Chip tone="ghost">info</Chip>
            <Chip tone="ink">warn</Chip>
            <Chip tone="warning">error</Chip>
          </div>
        ),
      },
    ],
  },
  {
    label: 'Traces',
    cards: [
      {
        name: 'TraceWaterfall',
        desc: 'Span stack with timing bars (service · op · duration · status) — canonical distributed trace viz.',
        status: 'Planned',
        reference: { label: 'jaegertracing/jaeger-ui', url: 'https://github.com/jaegertracing/jaeger-ui' },
        preview: (
          <div className="mock-trace">
            <div className="mock-trace__row">
              <span className="mock-trace__bar" style={{ marginLeft: '0%', width: '90%' }} />
              <span className="mock-trace__label">gateway</span>
            </div>
            <div className="mock-trace__row">
              <span className="mock-trace__bar" style={{ marginLeft: '8%', width: '45%' }} />
              <span className="mock-trace__label">catalog</span>
            </div>
            <div className="mock-trace__row">
              <span className="mock-trace__bar mock-trace__bar--warn" style={{ marginLeft: '20%', width: '20%' }} />
              <span className="mock-trace__label">redis</span>
            </div>
            <div className="mock-trace__row">
              <span className="mock-trace__bar" style={{ marginLeft: '55%', width: '30%' }} />
              <span className="mock-trace__label">cart</span>
            </div>
          </div>
        ),
      },
      {
        name: 'TraceList',
        desc: 'Recent traces with latency / error markers — picks one to feed into TraceWaterfall.',
        status: 'Composition',
        preview: (
          <div className="mock-trace-list">
            <div className="mock-trace-list__row">
              <MonoValue size="sm" weight="regular">a3f291b</MonoValue>
              <div className="mock-trace-list__track">
                <span className="mock-trace-list__bar" style={{ width: '70%' }} />
              </div>
              <span className="mock-trace-list__num">642ms</span>
            </div>
            <div className="mock-trace-list__row">
              <MonoValue size="sm" weight="regular">b8c2419</MonoValue>
              <div className="mock-trace-list__track">
                <span className="mock-trace-list__bar" style={{ width: '45%' }} />
              </div>
              <span className="mock-trace-list__num">412ms</span>
            </div>
            <div className="mock-trace-list__row">
              <MonoValue size="sm" weight="regular">c91d234</MonoValue>
              <div className="mock-trace-list__track">
                <span className="mock-trace-list__bar mock-trace-list__bar--warn" style={{ width: '95%' }} />
              </div>
              <span className="mock-trace-list__num mock-trace-list__num--warn">1.2s</span>
            </div>
          </div>
        ),
      },
      {
        name: 'ServiceMap',
        desc: 'Mini topology of services touched by selected traces — nodes + call edges, error edges in red.',
        status: 'Wraps Cytoscape',
        reference: { label: 'cytoscape/cytoscape.js', url: 'https://github.com/cytoscape/cytoscape.js' },
        preview: (
          <svg viewBox="0 0 200 80" className="mock-map">
            <line x1="40" y1="40" x2="100" y2="20" stroke="currentColor" opacity="0.3" />
            <line x1="40" y1="40" x2="100" y2="60" stroke="currentColor" opacity="0.3" />
            <line x1="100" y1="20" x2="160" y2="40" stroke="currentColor" opacity="0.3" />
            <line x1="100" y1="60" x2="160" y2="40" stroke="#e11d48" strokeDasharray="3 2" />
            <circle cx="40" cy="40" r="6" fill="currentColor" />
            <circle cx="100" cy="20" r="6" fill="currentColor" />
            <circle cx="100" cy="60" r="6" fill="#e11d48" />
            <circle cx="160" cy="40" r="6" fill="currentColor" />
            <text x="40" y="58" fontSize="9" textAnchor="middle" fill="currentColor" opacity="0.5">gw</text>
            <text x="100" y="14" fontSize="9" textAnchor="middle" fill="currentColor" opacity="0.5">catalog</text>
            <text x="100" y="76" fontSize="9" textAnchor="middle" fill="currentColor" opacity="0.5">cart</text>
            <text x="160" y="58" fontSize="9" textAnchor="middle" fill="currentColor" opacity="0.5">payment</text>
          </svg>
        ),
      },
    ],
  },
  {
    label: 'Metrics',
    cards: [
      {
        name: 'MetricChart',
        desc: 'Full-size time series — axes, tooltip, multi-series, AnnotationBand slot, ThresholdLine slot.',
        status: 'Wraps ECharts',
        reference: REF_ECHARTS,
        preview: (
          <svg className="mock-chart" viewBox="0 0 200 60" preserveAspectRatio="none">
            <rect x="80" y="0" width="40" height="60" fill="#e11d48" opacity="0.08" />
            <line x1="0" y1="20" x2="200" y2="20" stroke="#e11d48" strokeWidth="0.8" strokeDasharray="3 3" />
            <path d="M0,40 L20,38 L40,42 L60,40 L80,36 L100,30 L120,32 L140,38 L160,42 L180,40 L200,38" fill="none" stroke="currentColor" strokeOpacity="0.4" strokeWidth="1" strokeDasharray="2 2" />
            <path d="M0,38 L20,36 L40,30 L60,32 L80,18 L100,10 L120,15 L140,28 L160,40 L180,45 L200,42" fill="none" stroke="currentColor" strokeWidth="1.5" />
          </svg>
        ),
      },
      {
        name: 'CompareSeries',
        desc: 'Baseline (dashed) overlaid on current (solid) — same chart, two series.',
        status: 'Wraps ECharts',
        reference: REF_ECHARTS,
        preview: (
          <svg className="mock-chart" viewBox="0 0 200 50" preserveAspectRatio="none">
            <path d="M0,30 L20,28 L40,30 L60,28 L80,32 L100,30 L120,28 L140,30 L160,32 L180,30 L200,28" fill="none" stroke="currentColor" strokeOpacity="0.4" strokeWidth="1" strokeDasharray="3 2" />
            <path d="M0,32 L20,30 L40,28 L60,26 L80,12 L100,8 L120,14 L140,26 L160,36 L180,40 L200,38" fill="none" stroke="currentColor" strokeWidth="1.5" />
          </svg>
        ),
      },
      {
        name: 'ThresholdLine',
        desc: 'Horizontal SLO / breach marker on a metric chart — dashed warning red.',
        status: 'Wraps ECharts',
        reference: REF_ECHARTS,
        preview: (
          <svg className="mock-chart" viewBox="0 0 200 50" preserveAspectRatio="none">
            <line x1="0" y1="14" x2="200" y2="14" stroke="#e11d48" strokeWidth="1" strokeDasharray="3 3" />
            <text x="195" y="11" fontSize="8" textAnchor="end" fill="#e11d48">SLO 200ms</text>
            <path d="M0,40 L20,38 L40,30 L60,26 L80,16 L100,10 L120,14 L140,28 L160,38 L180,40 L200,38" fill="none" stroke="currentColor" strokeWidth="1.5" />
          </svg>
        ),
      },
      {
        name: 'MetricGrid',
        desc: 'Responsive grid of MetricChart / MetricCard, sharing the same time axis. Layout-only.',
        status: 'Composition',
        preview: (
          <div className="mock-grid">
            <div className="mock-grid__cell">latency p99<strong>142</strong></div>
            <div className="mock-grid__cell">error rate<strong>0.42%</strong></div>
            <div className="mock-grid__cell">throughput<strong>9 384</strong></div>
            <div className="mock-grid__cell">cpu sat<strong>71%</strong></div>
          </div>
        ),
      },
    ],
  },
  {
    label: 'Code & config',
    cards: [
      {
        name: 'CodeBlock',
        desc: 'Read-only code with line numbers and copy button (Python / Go / TypeScript / shell).',
        status: 'Wraps Monaco',
        reference: REF_MONACO,
        preview: (
          <pre className="mock-code">
            <span className="mock-code__line"><em>1</em>def main():</span>
            <span className="mock-code__line"><em>2</em>  try:</span>
            <span className="mock-code__line"><em>3</em>    run()</span>
            <span className="mock-code__line"><em>4</em>  except Exception:</span>
          </pre>
        ),
      },
      {
        name: 'DiffViewer',
        desc: 'Two-pane file diff — config diff, deployment diff, manifest changes.',
        status: 'Wraps Monaco',
        reference: REF_MONACO,
        preview: (
          <div className="mock-diff">
            <pre className="mock-diff__col">
              <span className="mock-diff__line mock-diff__line--rm">- timeout: 30s</span>
              <span className="mock-diff__line mock-diff__line--rm">- retries: 3</span>
              <span className="mock-diff__line">  qos: best</span>
            </pre>
            <pre className="mock-diff__col">
              <span className="mock-diff__line mock-diff__line--add">+ timeout: 60s</span>
              <span className="mock-diff__line mock-diff__line--add">+ retries: 5</span>
              <span className="mock-diff__line">  qos: best</span>
            </pre>
          </div>
        ),
      },
      {
        name: 'ConfigTree',
        desc: 'Collapsible nested viewer for k8s YAML / app config / trace payloads.',
        status: 'Wraps Monaco',
        reference: REF_MONACO,
        preview: (
          <pre className="mock-tree">
            <span className="mock-tree__line">▾ kafka:</span>
            <span className="mock-tree__line" style={{ paddingLeft: '14px' }}>▸ topics: …</span>
            <span className="mock-tree__line" style={{ paddingLeft: '14px' }}>▾ network:</span>
            <span className="mock-tree__line" style={{ paddingLeft: '28px' }}>jitter: 40ms</span>
            <span className="mock-tree__line" style={{ paddingLeft: '28px' }}>loss: 0.02%</span>
          </pre>
        ),
      },
      {
        name: 'CommitLink',
        desc: 'Short SHA + author + relative time, opens repo on click.',
        status: 'Composition',
        preview: (
          <div className="mock-commit">
            <MonoValue size="sm" weight="regular">a3f291b</MonoValue>
            <span className="mock-commit__sep">·</span>
            <span className="mock-commit__author">alice</span>
            <span className="mock-commit__sep">·</span>
            <MetricLabel size="xs">2h ago</MetricLabel>
          </div>
        ),
      },
      {
        name: 'ManifestPreview',
        desc: 'Compact YAML view with section folds — k8s manifests, fault specs, chaos-mesh CRDs.',
        status: 'Wraps Monaco',
        reference: REF_MONACO,
        preview: (
          <pre className="mock-manifest">
            <span className="mock-manifest__line">apiVersion: chaos-mesh.org/v1alpha1</span>
            <span className="mock-manifest__line">kind: NetworkChaos</span>
            <span className="mock-manifest__line">metadata:</span>
            <span className="mock-manifest__line" style={{ paddingLeft: '14px' }}>name: kafka-drift</span>
            <span className="mock-manifest__line">spec: <span className="mock-manifest__fold">{'{…}'}</span></span>
          </pre>
        ),
      },
    ],
  },
  {
    label: 'RCA',
    cards: [
      {
        name: 'EvidenceList',
        desc: 'Ranked "what we noticed" — each row has modality icon, brief, and an Open-in button.',
        status: 'Composition',
        preview: (
          <div className="mock-evidence">
            <div className="mock-evidence__row">
              <StatusDot tone="warning" />
              <span className="mock-evidence__msg">catalog→cart latency +480 ms</span>
              <Chip tone="ghost">metric</Chip>
            </div>
            <div className="mock-evidence__row">
              <StatusDot tone="warning" pulse />
              <span className="mock-evidence__msg">payment 500 rate +30%</span>
              <Chip tone="ghost">log</Chip>
            </div>
            <div className="mock-evidence__row">
              <StatusDot tone="ink" />
              <span className="mock-evidence__msg">cart DB timeout cluster</span>
              <Chip tone="ghost">trace</Chip>
            </div>
          </div>
        ),
      },
      {
        name: 'AlarmEvidenceCard',
        desc: 'Templated alarm + matched conditions — pairs with the backend alarm-evidence concept.',
        status: 'Composition',
        preview: (
          <div className="mock-alarm">
            <div className="mock-alarm__head">
              <PanelTitle size="sm">high_latency</PanelTitle>
              <Chip tone="warning">matched</Chip>
            </div>
            <KeyValueList
              ruled={false}
              uppercaseKeys
              items={[
                { k: 'rule', v: 'p99 > 200ms · 5m' },
                { k: 'targets', v: 'catalog, cart' },
              ]}
            />
          </div>
        ),
      },
      {
        name: 'SuspectChip',
        desc: 'RCA root-cause candidate with confidence — used inside ranked suspect lists.',
        status: 'Extends Chip',
        preview: (
          <div className="mock-suspects">
            <span className="mock-suspect">
              <span>catalog</span>
              <span className="mock-suspect__pct">92%</span>
            </span>
            <span className="mock-suspect">
              <span>payments</span>
              <span className="mock-suspect__pct">68%</span>
            </span>
            <span className="mock-suspect mock-suspect--low">
              <span>cart</span>
              <span className="mock-suspect__pct">41%</span>
            </span>
          </div>
        ),
      },
    ],
  },
];

function RoadmapCard({ name, desc, status, reference, preview }: RoadmapSpec) {
  return (
    <article className="gallery__roadmap-card">
      <div className="gallery__roadmap-stage">{preview}</div>
      <div className="gallery__roadmap-meta">
        <PanelTitle size="sm">{name}</PanelTitle>
        <p className="gallery__roadmap-desc">{desc}</p>
        <div className="gallery__roadmap-foot">
          <Chip tone="ghost">{status}</Chip>
          {reference && (
            <a
              className="gallery__roadmap-link"
              href={reference.url}
              target="_blank"
              rel="noreferrer"
            >
              ↗ {reference.label}
            </a>
          )}
        </div>
      </div>
    </article>
  );
}

/* ── App ────────────────────────────────────────────────────────────── */

function App() {
  const [active, setActive] = useState<string>('item-2');
  const [modalOpen, setModalOpen] = useState(false);
  const [switchOn, setSwitchOn] = useState(true);

  return (
    <div className="page-wrapper gallery">
      <header className="gallery__header">
        <div>
          <PanelTitle size="hero" as="h1">
            Aegis Rosetta
          </PanelTitle>
          <MetricLabel as="div" className="gallery__header-tag">
            UI System Specimen · v0
          </MetricLabel>
        </div>
        <p className="gallery__intro">
          Editorial serif paired with measured mono — pure ink against an
          off‑white surface. Activation is expressed by surface inversion, not
          accent color. Anomaly red is reserved for actual anomalies.
        </p>
      </header>

      {/* ── Color tokens ───────────────────────────────────────────── */}
      <Panel title={<PanelTitle size="lg">Color &amp; surface tokens</PanelTitle>}>
        <div className="gallery__swatches">
          {COLOR_TOKENS.map((t) => (
            <div className="gallery__swatch" key={t.name}>
              <div
                className="gallery__swatch-chip"
                style={{
                  background: `var(${t.name})`,
                  outline: t.value === '#FFFFFF' ? '1px solid var(--border-hairline)' : undefined,
                }}
              />
              <div className="gallery__swatch-meta">
                <MonoValue size="sm" weight="regular">
                  {t.name}
                </MonoValue>
                <MetricLabel size="xs">{t.value}</MetricLabel>
                <span className="gallery__swatch-desc">{t.desc}</span>
              </div>
            </div>
          ))}
        </div>
      </Panel>

      {/* ── Typography ─────────────────────────────────────────────── */}
      <Panel title={<PanelTitle size="lg">Typography</PanelTitle>}>
        <div className="gallery__type-list">
          {TYPE_SAMPLES.map((s) => (
            <div className="gallery__type-row" key={s.family}>
              <MetricLabel as="div" className="gallery__type-label">
                {s.label}
              </MetricLabel>
              <div className={`gallery__type-sample gallery__type-sample--${s.family}`}>
                {s.sample}
              </div>
              <span className="gallery__type-hint">{s.hint}</span>
            </div>
          ))}
        </div>

        <SectionDivider>PanelTitle scale</SectionDivider>
        <div className="gallery__stack">
          <PanelTitle size="hero" as="h2">
            Hero · 42
          </PanelTitle>
          <PanelTitle size="lg" as="h3">
            Large · 24
          </PanelTitle>
          <PanelTitle size="base">Base · 16</PanelTitle>
          <PanelTitle size="sm">Small · 14</PanelTitle>
        </div>

        <SectionDivider>MonoValue scale</SectionDivider>
        <div className="gallery__row">
          <Specimen caption="sm · regular">
            <MonoValue size="sm" weight="regular">
              0.142
            </MonoValue>
          </Specimen>
          <Specimen caption="sm · medium">
            <MonoValue size="sm">0.142</MonoValue>
          </Specimen>
          <Specimen caption="base · medium">
            <MonoValue size="base">0.142</MonoValue>
          </Specimen>
          <Specimen caption="lg · medium">
            <MonoValue size="lg">0.142</MonoValue>
          </Specimen>
        </div>
      </Panel>

      {/* ── Surface (Panel) ────────────────────────────────────────── */}
      <Panel title={<PanelTitle size="lg">Surface — Panel</PanelTitle>}>
        <div className="gallery__row gallery__row--panels">
          <Panel
            title="Default panel"
            extra={<MetricLabel>label</MetricLabel>}
          >
            <p className="gallery__panel-body">
              Hairline border, 16 px radius, ultra-subtle shadow. Title slot
              accepts string or rich node.
            </p>
          </Panel>

          <Panel
            inverted
            title={<PanelTitle size="base">Inverted panel</PanelTitle>}
            extra={<MetricLabel inverted>active</MetricLabel>}
          >
            <p className="gallery__panel-body gallery__panel-body--inverted">
              Surface flips to ink. Reserved for the currently-active card,
              not for accent decoration.
            </p>
          </Panel>

          <Panel padded={false} title="Unpadded">
            <div className="gallery__panel-no-pad">
              Body without inner padding — host owns the layout.
            </div>
          </Panel>
        </div>
      </Panel>

      {/* ── Indicators ─────────────────────────────────────────────── */}
      <Panel title={<PanelTitle size="lg">Indicators</PanelTitle>}>
        <SectionDivider>StatusDot</SectionDivider>
        <div className="gallery__row">
          <Specimen caption="ink">
            <StatusDot />
          </Specimen>
          <Specimen caption="ink · pulse">
            <StatusDot pulse />
          </Specimen>
          <Specimen caption="warning">
            <StatusDot tone="warning" />
          </Specimen>
          <Specimen caption="warning · pulse">
            <StatusDot tone="warning" pulse />
          </Specimen>
          <Specimen caption="size 10">
            <StatusDot size={10} />
          </Specimen>
        </div>

        <SectionDivider>Chip</SectionDivider>
        <div className="gallery__row">
          <Specimen caption="default">
            <Chip>queued</Chip>
          </Specimen>
          <Specimen caption="ink">
            <Chip tone="ink">running</Chip>
          </Specimen>
          <Specimen caption="warning">
            <Chip tone="warning">failed</Chip>
          </Specimen>
          <Specimen caption="ghost">
            <Chip tone="ghost">draft</Chip>
          </Specimen>
          <Specimen caption="with leading dot">
            <Chip leading={<StatusDot pulse size={6} />}>active</Chip>
          </Specimen>
        </div>

        <SectionDivider>BlastRadiusBar</SectionDivider>
        <div className="gallery__stack">
          <BlastRadiusBar value={20} centerLabel="Node Group A · 20 %" />
          <BlastRadiusBar value={65} centerLabel="Node Group A · 65 %" />
          <BlastRadiusBar value={92} centerLabel="Node Group A · 92 %" />
          <BlastRadiusBar value={50} hideTicks />
        </div>
      </Panel>

      {/* ── Data display ───────────────────────────────────────────── */}
      <Panel title={<PanelTitle size="lg">Data display</PanelTitle>}>
        <SectionDivider>StatBlock</SectionDivider>
        <div className="gallery__row">
          <Specimen caption="horizontal">
            <StatBlock label="latency_p99" value="142" unit="ms" />
          </Specimen>
          <Specimen caption="vertical · emphasized">
            <StatBlock
              label="error_rate"
              value="0.42"
              unit="%"
              direction="vertical"
              emphasized
            />
          </Specimen>
          <Specimen caption="inverted">
            <div className="gallery__inverted-host">
              <StatBlock label="throughput" value="9 384" unit="rps" inverted />
            </div>
          </Specimen>
        </div>

        <SectionDivider>KeyValueList</SectionDivider>
        <div className="gallery__row gallery__row--wide">
          <Specimen caption="mono keys (parameters)" span={2}>
            <KeyValueList items={KV_PARAMS} />
          </Specimen>
          <Specimen caption="uppercase keys (metadata)" span={2}>
            <KeyValueList items={KV_META} uppercaseKeys />
          </Specimen>
        </div>

        <SectionDivider>SparkLine</SectionDivider>
        <div className="gallery__row">
          <Specimen caption="rising">
            <div className="gallery__spark-host">
              <SparkLine points={SPARK_RISING} />
            </div>
          </Specimen>
          <Specimen caption="dip / recover">
            <div className="gallery__spark-host">
              <SparkLine points={SPARK_DIP} />
            </div>
          </Specimen>
          <Specimen caption="flat (low signal)">
            <div className="gallery__spark-host">
              <SparkLine points={SPARK_FLAT} />
            </div>
          </Specimen>
          <Specimen caption="inverted">
            <div className="gallery__spark-host gallery__spark-host--inverted">
              <SparkLine points={SPARK_RISING} inverted />
            </div>
          </Specimen>
        </div>

        <SectionDivider>MetricCard</SectionDivider>
        <div className="gallery__row gallery__row--wide">
          <Specimen caption="value only">
            <MetricCard label="active injections" value="3" />
          </Specimen>
          <Specimen caption="value + unit">
            <MetricCard label="latency p99" value="142" unit="ms" />
          </Specimen>
          <Specimen caption="value + sparkline">
            <MetricCard
              label="throughput"
              value="9 384"
              unit="rps"
              sparkline={SPARK_RISING}
            />
          </Specimen>
          <Specimen caption="inverted + sparkline">
            <MetricCard
              label="error budget"
              value="0.42"
              unit="%"
              sparkline={SPARK_DIP}
              inverted
            />
          </Specimen>
        </div>

        <SectionDivider>EmptyState</SectionDivider>
        <div className="gallery__row">
          <Specimen caption="default">
            <EmptyState
              title="No injections"
              description="Create your first fault injection to begin."
            />
          </Specimen>
          <Specimen caption="with action">
            <EmptyState
              title="No projects"
              description="Projects group experiments and their results."
              action={<Chip tone="ink">+ New project</Chip>}
            />
          </Specimen>
        </div>

        <SectionDivider>TimeDisplay</SectionDivider>
        <div className="gallery__row">
          <Specimen caption="absolute">
            <TimeDisplay value="2026-05-10T14:22:01Z" mode="absolute" />
          </Specimen>
          <Specimen caption="relative">
            <TimeDisplay value={Date.now() - 120000} mode="relative" />
          </Specimen>
          <Specimen caption="duration">
            <TimeDisplay value={2840} mode="duration" />
          </Specimen>
        </div>
      </Panel>

      {/* ── Lists / rows ───────────────────────────────────────────── */}
      <Panel title={<PanelTitle size="lg">Lists &amp; rows</PanelTitle>}>
        <SectionDivider extra={<MetricLabel>click any row</MetricLabel>}>
          ControlListItem
        </SectionDivider>
        <div className="gallery__list">
          {[
            { id: 'item-1', name: 'Network Partition', desc: 'Isolate node clusters' },
            { id: 'item-2', name: 'CPU Stress', desc: 'Resource exhaustion' },
            { id: 'item-3', name: 'Clock Drift', desc: 'Synchronicity failure' },
            { id: 'item-4', name: 'Process Killer', desc: 'Random PID termination' },
          ].map((it) => {
            const isActive = active === it.id;
            return (
              <ControlListItem
                key={it.id}
                active={isActive}
                onClick={() => setActive(isActive ? '' : it.id)}
                left={
                  <div>
                    <div className="gallery__list-name">{it.name}</div>
                    <MetricLabel size="xs" inverted={isActive}>
                      {it.desc}
                    </MetricLabel>
                  </div>
                }
                right={
                  <MetricLabel inverted={isActive}>
                    {isActive ? 'Stop' : 'Run'}
                  </MetricLabel>
                }
              />
            );
          })}
        </div>

        <SectionDivider>Static rows (no onClick)</SectionDivider>
        <div className="gallery__list">
          <ControlListItem
            left={
              <>
                <StatusDot pulse />
                <span>worker-alpha</span>
              </>
            }
            right={<MetricLabel>Ready</MetricLabel>}
          />
          <ControlListItem
            left={
              <>
                <StatusDot />
                <span>worker-beta</span>
              </>
            }
            right={<MetricLabel>Idle</MetricLabel>}
          />
          <ControlListItem
            left={
              <>
                <StatusDot tone="warning" pulse />
                <span>worker-gamma</span>
              </>
            }
            right={<MetricLabel>Recovering</MetricLabel>}
          />
        </div>
      </Panel>

      {/* ── Tables & Toolbar ───────────────────────────────────────── */}
      <Panel title={<PanelTitle size="lg">Tables & Toolbar</PanelTitle>}>
        <SectionDivider>Toolbar</SectionDivider>
        <Specimen caption="search + filters + action" span={3}>
          <Toolbar
            searchPlaceholder="Search injections…"
            searchValue="latency"
            filters={[
              { key: 'type', label: 'type: NetworkLatency' },
              { key: 'target', label: 'target: order-svc' },
            ]}
            action={<Chip tone="ink">+ New injection</Chip>}
          />
        </Specimen>

        <SectionDivider>DataTable · with data</SectionDivider>
        <Specimen caption="sortable columns, hover, alignment" span={3}>
          <DataTable
            columns={[
              {
                key: 'id',
                header: 'ID',
                render: (row) => <MonoValue size="sm">{row.id}</MonoValue>,
              },
              {
                key: 'type',
                header: 'Type',
                render: (row) => row.type,
              },
              {
                key: 'target',
                header: 'Target',
                render: (row) => row.target,
              },
              {
                key: 'duration',
                header: 'Duration',
                align: 'right',
                render: (row) => (
                  <MonoValue size="sm">{row.duration}</MonoValue>
                ),
              },
              {
                key: 'state',
                header: 'State',
                align: 'center',
                render: (row) => {
                  const tone =
                    row.state === 'running'
                      ? 'ink'
                      : row.state === 'failed'
                        ? 'warning'
                        : 'default';
                  return <Chip tone={tone}>{row.state}</Chip>;
                },
              },
            ]}
            data={[
              { id: 'INJ-0001', type: 'NetworkLatency', target: 'order-svc', duration: '120 s', state: 'running' },
              { id: 'INJ-0002', type: 'CPUStress', target: 'cart-svc', duration: '300 s', state: 'queued' },
              { id: 'INJ-0003', type: 'PodKill', target: 'inventory', duration: '—', state: 'failed' },
            ]}
            rowKey={(row) => row.id}
          />
        </Specimen>

        <SectionDivider>DataTable · loading</SectionDivider>
        <Specimen caption="skeleton shimmer" span={3}>
          <DataTable
            columns={[
              { key: 'id', header: 'ID', render: () => '' },
              { key: 'type', header: 'Type', render: () => '' },
              { key: 'status', header: 'Status', render: () => '' },
            ]}
            data={[]}
            rowKey={(_, i) => i}
            loading
          />
        </Specimen>

        <SectionDivider>DataTable · empty</SectionDivider>
        <Specimen caption="EmptyState inline" span={3}>
          <DataTable
            columns={[
              { key: 'id', header: 'ID', render: () => '' },
              { key: 'type', header: 'Type', render: () => '' },
            ]}
            data={[]}
            rowKey={(_, i) => i}
            emptyTitle="No executions"
            emptyDescription="Run an experiment to see results here."
          />
        </Specimen>
      </Panel>

      {/* ── Agent trajectory ───────────────────────────────────────── */}
      <Panel
        title={<PanelTitle size="lg">Agent trajectory</PanelTitle>}
        extra={<MetricLabel>observation · reasoning · action</MetricLabel>}
      >
        <SectionDivider>ToolCallCard</SectionDivider>
        <div className="gallery__row gallery__row--wide">
          <Specimen caption="with result" span={2}>
            <ToolCallCard data={TOOL_QUERY_METRICS} />
          </Specimen>
          <Specimen caption="no result yet" span={2}>
            <ToolCallCard
              data={{
                name: 'fetch_logs',
                arguments:
                  '{\n  "service": "payment",\n  "level": "error",\n  "limit": 50\n}',
              }}
            />
          </Specimen>
        </div>

        <SectionDivider>TrajectoryStep</SectionDivider>
        <div className="gallery__row gallery__row--wide">
          <Specimen caption="collapsed" span={2}>
            <TrajectoryStep
              data={{
                step: 1,
                timestamp: '14:22:01',
                durationMs: 1240,
                actionType: 'tool_call',
                action:
                  'query_metrics(service="catalog", metric="latency_p99")',
              }}
            />
          </Specimen>
          <Specimen caption="expanded · full step" span={2}>
            <TrajectoryStep
              data={TRAJECTORY_STEPS[0]}
              defaultExpanded
            />
          </Specimen>
        </div>

        <SectionDivider>TrajectoryTimeline</SectionDivider>
        <div className="gallery__trajectory-host">
          <TrajectoryTimeline
            agentName="rca-agent"
            status="completed"
            totalDurationMs={4790}
            steps={TRAJECTORY_STEPS}
          />
        </div>
      </Panel>

      {/* ── Logs / Terminal ────────────────────────────────────────── */}
      <Panel title={<PanelTitle size="lg">Logs — Terminal</PanelTitle>}>
        <SectionDivider>Terminal · plain</SectionDivider>
        <Terminal lines={TERMINAL_LINES} />

        <SectionDivider>Terminal · with log levels</SectionDivider>
        <Terminal
          lines={[
            { ts: '14:22:01', prefix: 'debug', level: 'debug', body: 'Worker pool initialized with 4 threads.' },
            { ts: '14:22:05', prefix: 'info', level: 'info', body: 'Experiment "playing-the-world" started.' },
            { ts: '14:23:12', prefix: 'warn', level: 'warn', body: 'Latency variance exceeds baseline by 15%.' },
            { ts: '14:23:18', prefix: 'error', level: 'error', body: 'Data consistency thresholds breached in node 04.' },
          ]}
        />

        <SectionDivider>CodeBlock</SectionDivider>
        <div className="gallery__row">
          <Specimen caption="json" span={2}>
            <CodeBlock
              language="json"
              code='{\n  "service": "catalog",\n  "metric": "latency_p99",\n  "value": 482,\n  "unit": "ms"\n}'
            />
          </Specimen>
          <Specimen caption="bash" span={2}>
            <CodeBlock
              language="bash"
              code="kubectl apply -f injection.yaml\nwatch -n 1 'kubectl get pods'"
            />
          </Specimen>
        </div>
      </Panel>

      {/* ── Navigation & identity primitives ───────────────────────── */}
      <Panel title={<PanelTitle size="lg">Navigation · Rosetta</PanelTitle>}>
        <SectionDivider>Tabs</SectionDivider>
        <RosettaTabs
          items={[
            { key: 'overview', label: 'Overview' },
            { key: 'params', label: 'Parameters' },
            { key: 'logs', label: 'Logs' },
          ]}
          defaultActiveKey="overview"
        >
          <p className="gallery__panel-body">Tab panel content goes here.</p>
        </RosettaTabs>

        <SectionDivider>Breadcrumb</SectionDivider>
        <div className="gallery__row">
          <Specimen caption="simple">
            <Breadcrumb items={[{ label: 'Dashboard' }]} />
          </Specimen>
          <Specimen caption="with links" span={2}>
            <Breadcrumb
              items={[
                { label: 'Projects', to: '/projects' },
                { label: 'catalog-service', to: '/projects/1' },
                { label: 'Injections' },
              ]}
            />
          </Specimen>
        </div>

        <SectionDivider>Avatar</SectionDivider>
        <div className="gallery__row">
          <Specimen caption="initials · sm">
            <Avatar size="sm" name="Ada Lovelace" />
          </Specimen>
          <Specimen caption="initials · base">
            <Avatar size="base" name="Grace Hopper" />
          </Specimen>
          <Specimen caption="initials · lg">
            <Avatar size="lg" name="Alan Turing" />
          </Specimen>
          <Specimen caption="icon fallback">
            <Avatar size="base" icon={<UserOutlined />} />
          </Specimen>
        </div>

        <SectionDivider>DropdownMenu</SectionDivider>
        <div className="gallery__row">
          <Specimen caption="basic">
            <DropdownMenu
              trigger={<Chip tone="ink">Open menu</Chip>}
              items={[
                { key: 'view', label: 'View details' },
                { key: 'edit', label: 'Edit' },
                { key: 'del', label: 'Delete', danger: true },
              ]}
            />
          </Specimen>
          <Specimen caption="with icons" span={2}>
            <DropdownMenu
              trigger={<Chip tone="default">User menu</Chip>}
              items={[
                { key: 'profile', label: 'Profile', icon: <UserOutlined /> },
                { key: 'settings', label: 'Settings', icon: <SettingOutlined /> },
                { key: 'logout', label: 'Logout', icon: <LogoutOutlined />, danger: true },
              ]}
            />
          </Specimen>
        </div>

        <SectionDivider>ProjectSelector</SectionDivider>
        <div className="gallery__row">
          <Specimen caption="with projects" span={2}>
            <ProjectSelector
              projects={[
                { id: '1', name: 'catalog-service' },
                { id: '2', name: 'order-platform' },
                { id: '3', name: 'inventory-v2' },
              ]}
              selectedId="1"
              onSelect={() => { /* gallery specimen — no-op */ }}
            />
          </Specimen>
          <Specimen caption="empty state">
            <ProjectSelector
              projects={[]}
              onSelect={() => { /* gallery specimen — no-op */ }}
              placeholder="No projects"
            />
          </Specimen>
        </div>
      </Panel>

      {/* ── Layouts ────────────────────────────────────────────────── */}
      <Panel title={<PanelTitle size="lg">Layouts</PanelTitle>}>
        <SectionDivider>PageHeader</SectionDivider>
        <div className="gallery__row">
          <Specimen caption="title only" span={2}>
            <PageHeader title="Users" />
          </Specimen>
          <Specimen caption="with description" span={2}>
            <PageHeader
              title="API Keys"
              description="Manage SDK and service API keys for programmatic access."
            />
          </Specimen>
          <Specimen caption="with action" span={2}>
            <PageHeader
              title="Teams"
              description="Organize members into teams."
              action={<Chip tone="ink">+ Create team</Chip>}
            />
          </Specimen>
        </div>

        <SectionDivider>SettingsSection + FormRow</SectionDivider>
        <SettingsSection
          title="Profile"
          description="Update your personal information and preferences."
        >
          <FormRow label="Display name" description="Shown across the platform.">
            <input
              type="text"
              defaultValue="Ada Lovelace"
              className="gallery__demo-input"
            />
          </FormRow>
          <FormRow label="Email" description="Used for notifications and login.">
            <input
              type="text"
              defaultValue="ada@aegislab.io"
              className="gallery__demo-input"
            />
          </FormRow>
          <FormRow label="Timezone" description="All times are displayed in this zone.">
            <select className="gallery__demo-input">
              <option>UTC</option>
              <option>Asia/Shanghai</option>
              <option>America/New_York</option>
            </select>
          </FormRow>
        </SettingsSection>
        <SettingsSection
          title="Notifications"
          description="Choose what you want to be notified about."
        >
          <FormRow label="Email alerts" description="Receive email for critical events.">
            <label className="gallery__demo-toggle">
              <input type="checkbox" defaultChecked />
              <span className="gallery__demo-toggle-track" />
            </label>
          </FormRow>
          <FormRow label="Slack integration" description="Push notifications to your Slack channel.">
            <label className="gallery__demo-toggle">
              <input type="checkbox" />
              <span className="gallery__demo-toggle-track" />
            </label>
          </FormRow>
        </SettingsSection>

        <SectionDivider>DangerZone</SectionDivider>
        <DangerZone
          description="Once you delete a project, all associated data will be permanently removed. This action cannot be undone."
        >
          <div className="gallery__danger-row">
            <span>Delete project <MonoValue size="sm">catalog-service</MonoValue></span>
            <button type="button" className="gallery__danger-btn">Delete project</button>
          </div>
        </DangerZone>
      </Panel>

      {/* ── AntD widgets under our theme ───────────────────────────── */}
      <Panel
        title={<PanelTitle size="lg">AntD widgets · themed</PanelTitle>}
        extra={<MetricLabel>ConfigProvider</MetricLabel>}
      >
        <SectionDivider>Buttons</SectionDivider>
        <div className="gallery__row">
          <Specimen caption="primary">
            <Button type="primary">Initialize Lab</Button>
          </Specimen>
          <Specimen caption="default">
            <Button>Cancel</Button>
          </Specimen>
          <Specimen caption="dashed">
            <Button type="dashed">+ Add target</Button>
          </Specimen>
          <Specimen caption="text">
            <Button type="text">Skip</Button>
          </Specimen>
          <Specimen caption="primary · loading">
            <Button type="primary" loading>
              Running
            </Button>
          </Specimen>
          <Specimen caption="primary · disabled">
            <Button type="primary" disabled>
              Locked
            </Button>
          </Specimen>
        </div>

        <SectionDivider>Inputs</SectionDivider>
        <div className="gallery__row gallery__row--wide">
          <Specimen caption="Input" span={2}>
            <Input placeholder="experiment-name" />
          </Specimen>
          <Specimen caption="Input.Search" span={2}>
            <Input.Search placeholder="search injections…" allowClear />
          </Specimen>
          <Specimen caption="Select" span={2}>
            <Select
              placeholder="select target"
              style={{ width: '100%' }}
              options={[
                { value: 'order', label: 'order-svc' },
                { value: 'cart', label: 'cart-svc' },
                { value: 'inv', label: 'inventory-svc' },
              ]}
            />
          </Specimen>
          <Specimen caption="Switch">
            <Switch
              checked={switchOn}
              onChange={setSwitchOn}
            />
          </Specimen>
        </div>

        <SectionDivider>Tags &amp; Progress</SectionDivider>
        <div className="gallery__row">
          <Specimen caption="Tag — neutral">
            <Tag>queued</Tag>
          </Specimen>
          <Specimen caption="Tag — primary">
            <Tag color="black">running</Tag>
          </Specimen>
          <Specimen caption="Tag — error">
            <Tag color="error">failed</Tag>
          </Specimen>
          <Specimen caption="Progress 30%" span={2}>
            <Progress percent={30} />
          </Specimen>
          <Specimen caption="Progress 78%" span={2}>
            <Progress percent={78} />
          </Specimen>
        </div>

        <SectionDivider>Tabs</SectionDivider>
        <Tabs
          items={[
            {
              key: 'overview',
              label: 'Overview',
              children: (
                <p className="gallery__panel-body">
                  Tab content uses the same base typography as panels.
                </p>
              ),
            },
            {
              key: 'params',
              label: 'Parameters',
              children: <KeyValueList items={KV_PARAMS} />,
            },
            {
              key: 'log',
              label: 'Log',
              children: <Terminal lines={TERMINAL_LINES.slice(0, 3)} />,
            },
          ]}
        />

        <SectionDivider>Table</SectionDivider>
        <Table
          dataSource={TABLE_DATA}
          columns={TABLE_COLUMNS}
          pagination={false}
          size="middle"
          scroll={{ x: 'max-content' }}
        />

        <SectionDivider>Tooltip &amp; Modal</SectionDivider>
        <div className="gallery__row">
          <Specimen caption="Tooltip on hover">
            <Tooltip title="Inverted spotlight tooltip" placement="top">
              <Button>Hover me</Button>
            </Tooltip>
          </Specimen>
          <Specimen caption="Modal">
            <Button onClick={() => setModalOpen(true)}>Open modal</Button>
            <Modal
              title="Confirm injection"
              open={modalOpen}
              onOk={() => setModalOpen(false)}
              onCancel={() => setModalOpen(false)}
              okText="Inject"
            >
              <p>
                This will execute <MonoValue size="sm">Clock Drift</MonoValue>{' '}
                against <MonoValue size="sm">EU‑WEST‑01</MonoValue>.
              </p>
            </Modal>
          </Specimen>
        </div>

        <SectionDivider extra={<MetricLabel>vertical layout</MetricLabel>}>
          Form
        </SectionDivider>
        <Form
          layout="vertical"
          initialValues={{
            name: 'playing-the-world',
            cluster: 'EU-WEST-01',
            autoRestart: true,
          }}
          requiredMark
          className="gallery__form"
        >
          <Form.Item
            label="Experiment name"
            name="name"
            required
            tooltip="Lower-case slug, used as the run identifier."
            rules={[{ required: true, message: 'name is required' }]}
          >
            <Input placeholder="experiment-name" />
          </Form.Item>

          <Form.Item
            label="Target service"
            name="target"
            required
            validateStatus="error"
            help="Target service is required."
          >
            <Select
              placeholder="select target"
              options={[
                { value: 'order', label: 'order-svc' },
                { value: 'cart', label: 'cart-svc' },
                { value: 'inv', label: 'inventory-svc' },
              ]}
            />
          </Form.Item>

          <Form.Item label="Run on cluster" name="cluster">
            <Input />
          </Form.Item>

          <Form.Item
            label="Auto-restart on failure"
            name="autoRestart"
            valuePropName="checked"
            extra="If a worker fails mid-run, restart it once and continue."
          >
            <Switch />
          </Form.Item>

          <Form.Item className="gallery__form-actions">
            <Button>Cancel</Button>
            <Button type="primary">Save &amp; queue</Button>
          </Form.Item>
        </Form>
      </Panel>

      {/* ── Roadmap · planned components ───────────────────────────── */}
      <Panel
        title={<PanelTitle size="lg">Roadmap · planned components</PanelTitle>}
        extra={<MetricLabel>experiment-observation page</MetricLabel>}
      >
        <p className="gallery__panel-body">
          Composition + wrapper layer for the multimodal experiment view —
          logs, traces, metrics, code and config bound on a shared time axis.
          Each card is a placeholder; the link points at the implementation
          pattern we&apos;ll build on.
        </p>
        {ROADMAP_GROUPS.map((group) => (
          <div key={group.label}>
            <SectionDivider>{group.label}</SectionDivider>
            <div className="gallery__roadmap-grid">
              {group.cards.map((card) => (
                <RoadmapCard key={card.name} {...card} />
              ))}
            </div>
          </div>
        ))}
      </Panel>

      <footer className="gallery__footer">
        <MetricLabel as="div">
          aegis · rosetta · ui specimen · review only
        </MetricLabel>
      </footer>
    </div>
  );
}

export default App;
