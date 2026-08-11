// Eval Dashboard — batch evaluation monitoring page

// ── Progress Bar ─────────────────────────────────────────────────────
function EvalProgressBar({ summary }) {
  const total = summary.total || 0;
  if (total === 0) return null;
  const segments = [
    { key: 'completed', count: summary.completed || 0, color: C.green, label: 'Completed' },
    { key: 'running', count: summary.running || 0, color: C.teal, label: 'Running' },
    { key: 'failed', count: summary.failed || 0, color: C.red, label: 'Failed' },
    { key: 'skipped', count: summary.skipped || 0, color: C.yellow, label: 'Skipped' },
    { key: 'pending', count: summary.pending || 0, color: C.muted, label: 'Pending' },
  ];
  const done = (summary.completed || 0) + (summary.failed || 0) + (summary.skipped || 0);
  const pct = total > 0 ? Math.round((done / total) * 100) : 0;

  return (
    <div style={{ padding: '12px 16px', borderBottom: `1px solid ${C.line}` }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 8 }}>
        <span style={{ fontWeight: 600, fontSize: 14 }}>Eval Progress</span>
        <span style={{ color: C.muted, fontSize: 12 }}>{done}/{total} ({pct}%)</span>
      </div>
      {/* Bar */}
      <div style={{ height: 8, borderRadius: 4, background: C.line, display: 'flex', overflow: 'hidden', marginBottom: 10 }}>
        {segments.map(seg => seg.count > 0 && (
          <div key={seg.key} style={{
            width: `${(seg.count / total) * 100}%`,
            background: seg.color,
            transition: 'width 0.3s ease',
          }} />
        ))}
      </div>
      {/* Stats */}
      <div style={{ display: 'flex', gap: 16, flexWrap: 'wrap' }}>
        {segments.map(seg => (
          <div key={seg.key} style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 12 }}>
            <span style={{ width: 8, height: 8, borderRadius: '50%', background: seg.color, display: 'inline-block' }} />
            <span style={{ color: C.muted }}>{seg.label}:</span>
            <span style={{ color: seg.color, fontWeight: 600 }}>{seg.count}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

// ── Sample Table ─────────────────────────────────────────────────────
function EvalSampleTable({ samples, total, onSelect, selectedId, statusFilter, onStatusFilter, searchText, onSearch, offset, onPageChange, pageSize }) {
  const statuses = ['all', 'pending', 'running', 'completed', 'failed', 'skipped'];
  const totalPages = Math.ceil(total / pageSize);
  const currentPage = Math.floor(offset / pageSize) + 1;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      {/* Filter bar */}
      <div style={{ padding: '8px 12px', borderBottom: `1px solid ${C.line}`, display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
        {statuses.map(s => {
          const isActive = statusFilter === s || (s === 'all' && !statusFilter);
          const color = s === 'all' ? C.teal : (STATUS_COLORS[s] || C.muted);
          return (
            <button
              key={s}
              onClick={() => onStatusFilter(s === 'all' ? null : s)}
              style={{
                background: isActive ? color + '20' : 'transparent',
                border: `1px solid ${isActive ? color : C.line}`,
                color: isActive ? color : C.muted,
                padding: '2px 10px',
                borderRadius: 3,
                cursor: 'pointer',
                fontSize: 11,
                fontFamily: 'inherit',
                fontWeight: isActive ? 600 : 400,
              }}
            >{s}</button>
          );
        })}
        <div style={{ flex: 1 }} />
        <input
          type="text"
          placeholder="Search samples..."
          value={searchText}
          onChange={e => onSearch(e.target.value)}
          style={{
            background: C.bg, color: C.text, border: `1px solid ${C.line}`,
            padding: '3px 8px', borderRadius: 3, fontSize: 11, fontFamily: 'inherit',
            width: 180, outline: 'none',
          }}
        />
        <span style={{ fontSize: 11, color: C.muted }}>{total} total</span>
      </div>
      {/* Table */}
      <div style={{ flex: 1, overflowY: 'auto' }}>
        {samples.length === 0 && (
          <div style={{ padding: 40, textAlign: 'center', color: C.muted, fontSize: 12 }}>
            {total === 0 ? 'No samples registered yet' : 'No matching samples'}
          </div>
        )}
        {samples.map(s => {
          const isSelected = selectedId === s.sample_id;
          const statusColor = STATUS_COLORS[s.status] || C.muted;
          return (
            <div
              key={s.sample_id}
              onClick={() => onSelect(s.sample_id)}
              style={{
                padding: '6px 12px',
                cursor: 'pointer',
                background: isSelected ? C.teal + '15' : 'transparent',
                borderLeft: isSelected ? `2px solid ${C.teal}` : '2px solid transparent',
                borderBottom: `1px solid ${C.line}22`,
                display: 'flex',
                alignItems: 'center',
                gap: 10,
              }}
            >
              <StatusDot status={s.status} />
              <span style={{ fontSize: 12, color: C.muted, minWidth: 32 }}>#{s.dataset_index}</span>
              <span style={{ fontSize: 12, color: C.text, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {s.sample_id}
              </span>
              <span style={{ fontSize: 11, color: C.muted, maxWidth: 200, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {s.data_dir}
              </span>
              {s.duration_seconds != null && (
                <span style={{ fontSize: 11, color: C.green, minWidth: 50, textAlign: 'right' }}>{s.duration_seconds.toFixed(1)}s</span>
              )}
              <Tag text={s.status} color={statusColor} />
            </div>
          );
        })}
      </div>
      {/* Pagination */}
      {totalPages > 1 && (
        <div style={{ padding: '6px 12px', borderTop: `1px solid ${C.line}`, display: 'flex', justifyContent: 'center', alignItems: 'center', gap: 8 }}>
          <button
            disabled={currentPage <= 1}
            onClick={() => onPageChange(offset - pageSize)}
            style={{ background: 'none', border: `1px solid ${C.line}`, color: currentPage <= 1 ? C.muted + '50' : C.muted, cursor: currentPage <= 1 ? 'default' : 'pointer', padding: '2px 8px', borderRadius: 3, fontSize: 11, fontFamily: 'inherit' }}
          >Prev</button>
          <span style={{ fontSize: 11, color: C.muted }}>
            Page {currentPage} / {totalPages}
          </span>
          <button
            disabled={currentPage >= totalPages}
            onClick={() => onPageChange(offset + pageSize)}
            style={{ background: 'none', border: `1px solid ${C.line}`, color: currentPage >= totalPages ? C.muted + '50' : C.muted, cursor: currentPage >= totalPages ? 'default' : 'pointer', padding: '2px 8px', borderRadius: 3, fontSize: 11, fontFamily: 'inherit' }}
          >Next</button>
        </div>
      )}
    </div>
  );
}

// ── Event tree builder ───────────────────────────────────────────────

function buildEventTree(events) {
  // Map agentId -> { dispatchEvent, taskId, taskType, agentId, children[], status, duration }
  const agentGroups = new Map();
  // Set of known subagent IDs (from task_dispatch events)
  const subagentIds = new Set();
  // Ordered root-level items: either an event object or { type: 'group', agentId }
  const rootItems = [];

  // First pass: collect all task_dispatch events to know subagent IDs
  for (const ev of events) {
    if (ev.event_type === 'task_dispatch') {
      const data = ev.data || {};
      const agentId = data.agent_id || '';
      const taskId = data.task_id || '';
      if (agentId) {
        subagentIds.add(agentId);
        agentGroups.set(agentId, {
          dispatchEvent: ev,
          taskId,
          taskType: data.task_type || 'task',
          agentId,
          children: [],
          status: 'running',
          duration: null,
        });
      }
    }
  }

  // Second pass: distribute events
  for (const ev of events) {
    const eventType = ev.event_type || '';
    const agentPath = ev.agent_path || [];
    const lastAgent = agentPath[agentPath.length - 1] || '';
    const data = ev.data || {};

    // task_complete / task_fail / task_abort update group status
    if (eventType === 'task_complete' || eventType === 'task_fail' || eventType === 'task_abort') {
      const agentId = data.agent_id || lastAgent;
      const group = agentGroups.get(agentId);
      if (group) {
        group.status = eventType === 'task_complete' ? 'completed'
          : eventType === 'task_abort' ? 'aborted' : 'failed';
        group.duration = data.duration_seconds || null;
        continue;
      }
    }

    // task_dispatch: insert group placeholder into root
    if (eventType === 'task_dispatch') {
      const agentId = data.agent_id || '';
      if (agentGroups.has(agentId)) {
        rootItems.push({ type: 'group', agentId });
        continue;
      }
    }

    // Check if this event belongs to a subagent
    if (subagentIds.has(lastAgent) && agentGroups.has(lastAgent)) {
      agentGroups.get(lastAgent).children.push(ev);
      continue;
    }

    // Orchestrator-level event
    rootItems.push({ type: 'event', event: ev });
  }

  return { rootItems, agentGroups };
}

// ── Single event row (shared by root and subagent views) ────────────

function EventRow({ ev, index }) {
  const eventType = ev.event_type || '';
  const agentPath = ev.agent_path || [];
  const agentId = agentPath[agentPath.length - 1] || '';
  const data = ev.data || {};
  const ts = ev.timestamp || '';
  const shortTs = ts.length > 19 ? ts.substring(11, 19) : ts;

  let icon = '\u25B8';
  let color = C.teal;
  let detail = eventType;

  switch (eventType) {
    case 'llm_start': icon = '\u25B7'; color = C.blue || '#6cb6ff'; detail = `LLM call (${data.new_message_count || 0} new msgs)`; break;
    case 'tool_call': icon = '\u2192'; color = C.yellow; detail = data.tool_name || 'tool'; break;
    case 'tool_result': icon = '\u2190'; color = C.green; detail = `${data.tool_name || 'tool'}: ${(typeof data.result === 'string' ? data.result : '').slice(0, 80)}`; break;
    case 'llm_end': icon = '\u25C7'; color = C.purple; detail = (data.content || '').slice(0, 80) || `${(data.tool_calls || []).length} tool call(s)`; break;
    case 'task_dispatch': icon = '\u25B6'; color = C.teal; detail = `dispatch: ${data.task_type || 'task'}`; break;
    case 'task_complete': icon = '\u2713'; color = C.green; detail = 'completed'; break;
    case 'task_fail': icon = '\u2717'; color = C.red; detail = data.error || 'failed'; break;
    case 'task_abort': icon = '\u2298'; color = C.yellow; detail = data.reason || 'aborted'; break;
    case 'hypothesis_update': icon = '\u25C8'; color = C.purple; detail = `${data.hypothesis_id || ''} \u2192 ${data.status || ''}`; break;
  }

  return (
    <CollapsibleContent
      key={index}
      title={
        <span style={{ display: 'flex', gap: 8, alignItems: 'center', fontSize: 12 }}>
          <span style={{ color: C.muted, fontSize: 11, minWidth: 50 }}>{shortTs}</span>
          <span style={{ color }}>{icon}</span>
          <span style={{ color: C.text, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{detail}</span>
        </span>
      }
    >
      <JsonCard data={ev.data} title={eventType} />
    </CollapsibleContent>
  );
}

// ── Agent event group (collapsible subagent section) ────────────────

const AGENT_COLORS = [C.teal, C.purple, C.orange, C.yellow, C.green, C.red];

function AgentEventGroup({ group, colorIndex }) {
  const [open, setOpen] = useState(false);
  const accentColor = AGENT_COLORS[colorIndex % AGENT_COLORS.length];
  const statusColor = group.status === 'completed' ? C.green
    : group.status === 'failed' ? C.red
    : group.status === 'aborted' ? C.yellow
    : C.teal;
  const visibleChildren = group.children;
  const eventCount = visibleChildren.length;

  return (
    <div style={{
      borderLeft: `2px solid ${accentColor}`,
      marginLeft: 6,
      marginTop: 2,
      marginBottom: 2,
      borderRadius: '0 4px 4px 0',
      overflow: 'hidden',
    }}>
      {/* Group header */}
      <div
        onClick={() => setOpen(!open)}
        style={{
          padding: '7px 12px',
          cursor: 'pointer',
          display: 'flex',
          alignItems: 'center',
          gap: 8,
          background: accentColor + '0a',
          userSelect: 'none',
          transition: 'background 0.15s',
        }}
        onMouseEnter={e => e.currentTarget.style.background = accentColor + '15'}
        onMouseLeave={e => e.currentTarget.style.background = accentColor + '0a'}
      >
        <span style={{
          fontSize: 10,
          color: accentColor,
          transition: 'transform 0.2s',
          transform: open ? 'rotate(90deg)' : 'rotate(0)',
          display: 'inline-block',
        }}>{'\u25B6'}</span>
        <span style={{
          width: 8, height: 8, borderRadius: '50%',
          background: statusColor,
          animation: group.status === 'running' ? 'pulse 1.5s infinite' : 'none',
          flexShrink: 0,
        }} />
        <span style={{ color: accentColor, fontWeight: 600, fontSize: 12 }}>
          {group.agentId}
        </span>
        <Tag text={group.taskType} color={accentColor} />
        <span style={{ color: C.muted, fontSize: 11 }}>
          {eventCount} event{eventCount !== 1 ? 's' : ''}
        </span>
        {group.duration != null && (
          <span style={{ color: C.green, fontSize: 11 }}>{group.duration.toFixed(1)}s</span>
        )}
        <div style={{ flex: 1 }} />
        <Tag text={group.status} color={statusColor} />
      </div>
      {/* Expanded children */}
      {open && (
        <div style={{ paddingLeft: 8, animation: 'fadeIn 0.15s ease-out' }}>
          {visibleChildren.length === 0 && (
            <div style={{ padding: '12px 16px', color: C.muted, fontSize: 11 }}>
              No events recorded for this agent
            </div>
          )}
          {visibleChildren.map((ev, i) => (
            <EventRow key={`${group.agentId}-${i}`} ev={ev} index={i} />
          ))}
        </div>
      )}
    </div>
  );
}

// ── Agent timeline bar ──────────────────────────────────────────────

function AgentTimeline({ events, agentGroups }) {
  if (agentGroups.size === 0) return null;

  // Compute time range from all events using a loop (safe for large arrays,
  // unlike Math.min/max(...spread) which can overflow the call stack).
  let minTs = Infinity;
  let maxTs = -Infinity;
  for (const e of events) {
    if (!e.timestamp) continue;
    const t = new Date(e.timestamp).getTime();
    if (t < minTs) minTs = t;
    if (t > maxTs) maxTs = t;
  }
  if (!isFinite(minTs) || !isFinite(maxTs)) return null;

  const range = maxTs - minTs;
  if (range <= 0) return null;

  const groups = Array.from(agentGroups.values());

  return (
    <div style={{
      padding: '8px 12px',
      borderBottom: `1px solid ${C.line}`,
    }}>
      <div style={{ fontSize: 10, color: C.muted, marginBottom: 6 }}>Agent Timeline</div>
      {/* Orchestrator bar */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
        <span style={{ fontSize: 10, color: C.muted, minWidth: 80, textAlign: 'right' }}>orchestrator</span>
        <div style={{ flex: 1, height: 6, background: C.line, borderRadius: 3, position: 'relative' }}>
          <div style={{ width: '100%', height: '100%', background: C.teal + '40', borderRadius: 3 }} />
        </div>
      </div>
      {/* Subagent bars */}
      {groups.map((g, i) => {
        let start = Infinity;
        let end = -Infinity;
        for (const e of g.children) {
          if (!e.timestamp) continue;
          const t = new Date(e.timestamp).getTime();
          if (t < start) start = t;
          if (t > end) end = t;
        }
        if (!isFinite(start)) return null;
        const left = ((start - minTs) / range) * 100;
        const width = Math.max(((end - start) / range) * 100, 1);
        const color = AGENT_COLORS[i % AGENT_COLORS.length];
        const statusColor = g.status === 'completed' ? C.green
          : g.status === 'failed' ? C.red
          : g.status === 'aborted' ? C.yellow : C.teal;

        return (
          <div key={g.agentId} style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 2 }}>
            <span style={{ fontSize: 10, color, minWidth: 80, textAlign: 'right', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {g.agentId}
            </span>
            <div style={{ flex: 1, height: 6, background: C.line + '40', borderRadius: 3, position: 'relative' }}>
              <div style={{
                position: 'absolute',
                left: `${left}%`,
                width: `${width}%`,
                height: '100%',
                background: color + '80',
                borderRadius: 3,
                border: `1px solid ${color}`,
              }} />
            </div>
            <span style={{
              width: 6, height: 6, borderRadius: '50%',
              background: statusColor,
              flexShrink: 0,
            }} />
          </div>
        );
      })}
    </div>
  );
}

// ── Sample Detail ────────────────────────────────────────────────────
function EvalSampleDetail({ sampleId, onClose }) {
  const [info, setInfo] = useState(null);
  const [events, setEvents] = useState([]);
  const [loading, setLoading] = useState(true);
  const [activeTab, setActiveTab] = useState('timeline');
  const pollRef = useRef(null);
  const scrollRef = useRef(null);

  // Fetch sample info
  useEffect(() => {
    if (!sampleId) return;
    setLoading(true);
    setEvents([]);
    setActiveTab('timeline');
    fetch(`/api/eval/samples/${encodeURIComponent(sampleId)}`)
      .then(r => r.json())
      .then(data => { setInfo(data); setLoading(false); })
      .catch(() => setLoading(false));
  }, [sampleId]);

  // Load events: one-time REST fetch for history + WebSocket for real-time
  useEffect(() => {
    if (!sampleId) return;

    // One-time REST fetch for already-buffered events (covers reconnect/refresh)
    fetch(`/api/eval/samples/${encodeURIComponent(sampleId)}/events`)
      .then(r => r.json())
      .then(data => {
        const initial = data.events || [];
        if (initial.length > 0) setEvents(initial);
      })
      .catch(() => {});

    // WebSocket listener for real-time trajectory events
    function handleTrajectoryEvent(e) {
      const event = e.detail;
      if (!event || event.channel !== 'eval') return;
      if (event.event_type !== 'sample_trajectory_event') return;
      if (event.sample_id !== sampleId) return;
      const traj = event.data;
      if (traj) setEvents(prev => [...prev, traj]);
    }

    window.addEventListener('eval_event', handleTrajectoryEvent);
    return () => window.removeEventListener('eval_event', handleTrajectoryEvent);
  }, [sampleId]);

  // Auto-scroll events
  useEffect(() => {
    const el = scrollRef.current;
    if (el && activeTab === 'timeline') el.scrollTop = el.scrollHeight;
  }, [events.length, activeTab]);

  // Build hierarchical tree from flat events
  const tree = useMemo(() => buildEventTree(events), [events]);

  // Extract system prompts from all agents' first llm_start events
  // LangChain messages use "type" (not "role"): "system", "human", "ai", "tool"
  const agentPrompts = useMemo(() => {
    const seen = new Set();
    const result = [];
    for (const ev of events) {
      if (ev.event_type !== 'llm_start') continue;
      const agentPath = ev.agent_path || [];
      const agentKey = agentPath.join('/') || 'unknown';
      if (seen.has(agentKey)) continue;
      seen.add(agentKey);
      const msgs = ev.data?.messages || [];
      const sys = msgs.find(m => m.type === 'system' || m.role === 'system');
      if (sys && sys.content) {
        result.push({ agent: agentKey, content: sys.content });
      }
    }
    return result;
  }, [events]);

  if (loading) {
    return (
      <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', color: C.muted }}>
        Loading...
      </div>
    );
  }

  if (!info) {
    return (
      <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', color: C.muted }}>
        Sample not found
      </div>
    );
  }

  const statusColor = STATUS_COLORS[info.status] || C.muted;
  const visibleEventCount = events.length;
  const agentCount = tree.agentGroups.size;
  const groundTruth = info.ground_truth || info.groundtruth;
  const normalizedRootCauseServices = [];
  const serviceCandidates = [
    info.root_cause_services,
    info.rootCauseServices,
    groundTruth?.service,
  ];
  for (const candidate of serviceCandidates) {
    if (Array.isArray(candidate)) {
      for (const item of candidate) {
        const value = String(item || '').trim();
        if (value && !normalizedRootCauseServices.includes(value)) {
          normalizedRootCauseServices.push(value);
        }
      }
    } else if (typeof candidate === 'string') {
      const value = candidate.trim();
      if (value && !normalizedRootCauseServices.includes(value)) {
        normalizedRootCauseServices.push(value);
      }
    }
    if (normalizedRootCauseServices.length > 0) break;
  }
  const rootCauseServices = normalizedRootCauseServices;
  // Track color index for subagent groups
  let groupColorIndex = 0;

  const TABS = [
    { id: 'timeline', label: 'Timeline' },
    { id: 'prompt', label: 'Prompt' },
  ];

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
      {/* Header */}
      <div style={{ padding: '10px 16px', borderBottom: `1px solid ${C.line}`, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <StatusDot status={info.status} />
          <span style={{ fontWeight: 600, fontSize: 14 }}>{info.sample_id}</span>
          <Tag text={info.status} color={statusColor} />
          {info.duration_seconds != null && (
            <span style={{ fontSize: 11, color: C.green }}>{info.duration_seconds.toFixed(1)}s</span>
          )}
        </div>
        <button onClick={onClose} style={{ background: 'none', border: 'none', color: C.muted, cursor: 'pointer', fontSize: 14, fontFamily: 'inherit' }}>[ESC]</button>
      </div>
      {/* Meta */}
      <div style={{ padding: '8px 16px', borderBottom: `1px solid ${C.line}22`, display: 'flex', gap: 16, fontSize: 11, color: C.muted, flexWrap: 'wrap' }}>
        <span>Index: <span style={{ color: C.text }}>{info.dataset_index}</span></span>
        <span>Dir: <span style={{ color: C.text }}>{info.data_dir}</span></span>
        {info.run_id && <span>Run: <span style={{ color: C.text }}>{info.run_id}</span></span>}
        {rootCauseServices.length > 0 && (
          <span>
            Root Cause(Service): <span style={{ color: C.red }}>{rootCauseServices.join(', ')}</span>
          </span>
        )}
        {info.error && <span>Error: <span style={{ color: C.red }}>{info.error}</span></span>}
      </div>
      {/* Tab bar */}
      <div style={{ display: 'flex', borderBottom: `1px solid ${C.line}`, paddingLeft: 12 }}>
        {TABS.map(tab => {
          const isActive = activeTab === tab.id;
          return (
            <button
              key={tab.id}
              onClick={() => setActiveTab(tab.id)}
              style={{
                padding: '6px 14px',
                background: 'none',
                border: 'none',
                borderBottom: isActive ? `2px solid ${C.teal}` : '2px solid transparent',
                color: isActive ? C.teal : C.muted,
                cursor: 'pointer',
                fontSize: 12,
                fontWeight: isActive ? 600 : 400,
                fontFamily: 'inherit',
                transition: 'all 0.15s',
              }}
            >{tab.label}</button>
          );
        })}
      </div>
      {/* Tab content */}
      {activeTab === 'timeline' && (
        <>
          {/* Agent timeline */}
          <AgentTimeline events={events} agentGroups={tree.agentGroups} />
          {/* Events summary */}
          <div style={{ padding: '6px 12px', borderBottom: `1px solid ${C.line}`, fontSize: 12, color: C.muted, display: 'flex', gap: 12, alignItems: 'center' }}>
            <span>Events: {visibleEventCount}</span>
            {agentCount > 0 && <span>Agents: {agentCount}</span>}
            {info.status === 'running' && <span style={{ color: C.teal, animation: 'pulse 1.5s infinite' }}>polling...</span>}
          </div>
          {/* Hierarchical event list */}
          <div ref={scrollRef} style={{ flex: 1, overflowY: 'auto' }}>
            {events.length === 0 && (
              <div style={{ padding: 40, textAlign: 'center', color: C.muted, fontSize: 12 }}>
                {info.status === 'pending' ? 'Waiting to start...' : info.status === 'skipped' ? 'Sample was skipped' : 'No events recorded'}
              </div>
            )}
            {tree.rootItems.map((item, i) => {
              if (item.type === 'group') {
                const group = tree.agentGroups.get(item.agentId);
                if (!group) return null;
                const ci = groupColorIndex++;
                return <AgentEventGroup key={`group-${item.agentId}`} group={group} colorIndex={ci} />;
              }
              // Root-level event
              return <EventRow key={`root-${i}`} ev={item.event} index={i} />;
            })}
          </div>
        </>
      )}
      {activeTab === 'prompt' && (
        <PromptTabContent agentPrompts={agentPrompts} />
      )}
    </div>
  );
}

// ── Prompt Tab Content ──────────────────────────────────────────────
function PromptTabContent({ agentPrompts }) {
  const [selectedAgent, setSelectedAgent] = useState(null);
  const [copied, setCopied] = useState(false);

  // Auto-select first agent
  useEffect(() => {
    if (agentPrompts.length > 0 && !selectedAgent) {
      setSelectedAgent(agentPrompts[0].agent);
    }
  }, [agentPrompts, selectedAgent]);

  const current = agentPrompts.find(p => p.agent === selectedAgent);
  const content = current?.content || '';

  const handleCopy = useCallback(() => {
    if (!content) return;
    navigator.clipboard.writeText(content).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }).catch(() => {});
  }, [content]);

  if (agentPrompts.length === 0) {
    return (
      <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', color: C.muted, fontSize: 12, padding: 40 }}>
        No system prompts found. Prompts are extracted from llm_start events.
      </div>
    );
  }

  const charCount = content.length;
  const wordCount = content.split(/\s+/).filter(Boolean).length;

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
      {/* Agent selector + toolbar */}
      <div style={{ padding: '6px 12px', borderBottom: `1px solid ${C.line}`, display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
          {agentPrompts.map((p, i) => {
            const isActive = selectedAgent === p.agent;
            const color = AGENT_COLORS[i % AGENT_COLORS.length];
            return (
              <button
                key={p.agent}
                onClick={() => { setSelectedAgent(p.agent); setCopied(false); }}
                style={{
                  background: isActive ? color + '20' : 'transparent',
                  border: `1px solid ${isActive ? color : C.line}`,
                  color: isActive ? color : C.muted,
                  padding: '2px 10px',
                  borderRadius: 3,
                  cursor: 'pointer',
                  fontSize: 11,
                  fontWeight: isActive ? 600 : 400,
                  fontFamily: 'inherit',
                }}
              >{p.agent}</button>
            );
          })}
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <span style={{ fontSize: 11, color: C.muted }}>
            {wordCount.toLocaleString()} words / {charCount.toLocaleString()} chars
          </span>
          <button
            onClick={handleCopy}
            style={{
              background: copied ? C.green + '20' : C.line,
              border: `1px solid ${copied ? C.green : C.line}`,
              color: copied ? C.green : C.text,
              padding: '2px 10px',
              borderRadius: 3,
              cursor: 'pointer',
              fontSize: 11,
              fontFamily: 'inherit',
              transition: 'all 0.15s',
            }}
          >{copied ? 'Copied!' : 'Copy'}</button>
        </div>
      </div>
      {/* Prompt content */}
      <div style={{ flex: 1, overflowY: 'auto', padding: '12px 16px' }}>
        <pre style={{
          margin: 0,
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-word',
          fontSize: 12,
          lineHeight: 1.5,
          color: C.text,
          fontFamily: 'inherit',
        }}>{content}</pre>
      </div>
    </div>
  );
}

// ── Eval Page (main) ─────────────────────────────────────────────────
function EvalPage() {
  const [summary, setSummary] = useState({ total: 0, pending: 0, running: 0, completed: 0, failed: 0, skipped: 0 });
  const [samples, setSamples] = useState([]);
  const [total, setTotal] = useState(0);
  const [selectedId, setSelectedId] = useState(null);
  const [statusFilter, setStatusFilter] = useState(null);
  const [searchText, setSearchText] = useState('');
  const [offset, setOffset] = useState(0);
  const pageSize = 50;
  // Track whether a page re-fetch is needed (debounced)
  const refetchTimerRef = useRef(null);
  const filterRef = useRef({ statusFilter, searchText, offset });
  filterRef.current = { statusFilter, searchText, offset };

  // Helper: fetch current page of samples from server
  const fetchSamplesPage = useCallback(() => {
    const { statusFilter: sf, searchText: st, offset: off } = filterRef.current;
    const params = new URLSearchParams({ offset: String(off), limit: String(pageSize) });
    if (sf) params.set('status', sf);
    if (st) params.set('search', st);
    fetch(`/api/eval/samples?${params}`).then(r => r.json()).then(data => {
      setSamples(data.samples || []);
      setTotal(data.total || 0);
    }).catch(() => {});
  }, [pageSize]);

  // Schedule a debounced page re-fetch (coalesce rapid WS events)
  const scheduleRefetch = useCallback(() => {
    if (refetchTimerRef.current) return; // already scheduled
    refetchTimerRef.current = setTimeout(() => {
      refetchTimerRef.current = null;
      fetchSamplesPage();
    }, 300);
  }, [fetchSamplesPage]);

  // Initial fetch
  useEffect(() => {
    fetch('/api/eval/status').then(r => r.json()).then(data => {
      if (data.enabled) {
        const { enabled, ...rest } = data;
        setSummary(rest);
      }
    }).catch(() => {});
  }, []);

  // Fetch samples with filters
  useEffect(() => {
    fetchSamplesPage();
  }, [offset, statusFilter, searchText, fetchSamplesPage]);

  // Reset offset when filters change
  useEffect(() => { setOffset(0); }, [statusFilter, searchText]);

  // Listen for WebSocket eval events (attached via window)
  useEffect(() => {
    function handleEvalEvent(e) {
      const event = e.detail;
      if (!event || event.channel !== 'eval') return;

      if (event.event_type === 'eval_snapshot') {
        const data = event.data || {};
        if (data.summary) setSummary(data.summary);
        if (data.samples) { setSamples(data.samples); setTotal(data.total || 0); }
        return;
      }

      if (event.event_type === 'sample_status') {
        const s = event.data || {};
        // Update summary from the event payload — no HTTP round-trip
        if (event.summary) setSummary(event.summary);
        // Update in-place if sample is on current page; otherwise schedule a
        // debounced re-fetch so newly visible samples appear without flooding
        // the server with requests.
        setSamples(prev => {
          const idx = prev.findIndex(x => x.sample_id === s.sample_id);
          if (idx >= 0) {
            const updated = [...prev];
            updated[idx] = s;
            return updated;
          }
          // Sample not on current page — schedule a background re-fetch
          // instead of blindly appending (which caused unbounded growth).
          scheduleRefetch();
          return prev;
        });
      }
    }

    window.addEventListener('eval_event', handleEvalEvent);
    return () => {
      window.removeEventListener('eval_event', handleEvalEvent);
      if (refetchTimerRef.current) clearTimeout(refetchTimerRef.current);
    };
  }, [scheduleRefetch]);

  const [leftWidth, setLeftWidth] = useState(40);
  const containerRef = useRef(null);

  const handleDrag = useCallback((clientX) => {
    const container = containerRef.current;
    if (!container) return;
    const rect = container.getBoundingClientRect();
    const pct = ((clientX - rect.left) / rect.width) * 100;
    setLeftWidth(Math.max(20, Math.min(80, pct)));
  }, []);

  return (
    <div style={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
      <EvalProgressBar summary={summary} />
      <div ref={containerRef} style={{ flex: 1, display: 'flex', overflow: 'hidden' }}>
        {/* Sample list */}
        <div style={{ width: selectedId ? `${leftWidth}%` : '100%', minWidth: 200, display: 'flex', flexDirection: 'column', transition: selectedId ? 'none' : 'width 0.2s' }}>
          <EvalSampleTable
            samples={samples}
            total={total}
            onSelect={setSelectedId}
            selectedId={selectedId}
            statusFilter={statusFilter}
            onStatusFilter={setStatusFilter}
            searchText={searchText}
            onSearch={setSearchText}
            offset={offset}
            onPageChange={setOffset}
            pageSize={pageSize}
          />
        </div>
        {/* Drag handle + Detail panel */}
        {selectedId && (
          <>
            <DragHandle onDrag={handleDrag} />
            <EvalSampleDetail
              sampleId={selectedId}
              onClose={() => setSelectedId(null)}
            />
          </>
        )}
      </div>
    </div>
  );
}
