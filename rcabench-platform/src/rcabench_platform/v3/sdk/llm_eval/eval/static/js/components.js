function Tag({ text, color }) {
  return (
    <span style={{
      display: 'inline-block',
      padding: '1px 8px',
      borderRadius: 3,
      fontSize: 11,
      fontWeight: 600,
      color: color || C.teal,
      background: (color || C.teal) + '20',
      border: `1px solid ${(color || C.teal)}40`,
    }}>{text}</span>
  );
}

function CollapsibleContent({ title, children, defaultOpen = false }) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <div style={{ borderBottom: `1px solid ${C.line}` }}>
      <div
        onClick={() => setOpen(!open)}
        style={{
          padding: '6px 10px',
          cursor: 'pointer',
          display: 'flex',
          alignItems: 'center',
          gap: 6,
          color: C.muted,
          userSelect: 'none',
        }}
      >
        <span style={{ fontSize: 10, transition: 'transform 0.2s', transform: open ? 'rotate(90deg)' : 'rotate(0)' }}>&#9654;</span>
        <span style={{ color: C.text, fontSize: 12 }}>{title}</span>
      </div>
      {open && <div style={{ padding: '0 10px 8px 24px' }}>{children}</div>}
    </div>
  );
}

function CopyButton({ text }) {
  const [copied, setCopied] = useState(false);
  const handleCopy = () => {
    navigator.clipboard.writeText(typeof text === 'string' ? text : JSON.stringify(text, null, 2));
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };
  return (
    <button
      onClick={handleCopy}
      style={{
        background: 'none',
        border: `1px solid ${C.line}`,
        color: copied ? C.green : C.muted,
        padding: '2px 8px',
        borderRadius: 3,
        cursor: 'pointer',
        fontSize: 11,
        fontFamily: 'inherit',
      }}
    >{copied ? 'Copied!' : 'Copy'}</button>
  );
}

function JsonHighlighter({ data, depth = 0, maxStringLength = 0 }) {
  if (data === null || data === undefined) return <span style={{ color: C.muted }}>null</span>;
  if (typeof data === 'boolean') return <span style={{ color: C.purple }}>{data.toString()}</span>;
  if (typeof data === 'number') return <span style={{ color: C.yellow }}>{data}</span>;
  if (typeof data === 'string') {
    const display = maxStringLength > 0 && data.length > maxStringLength ? data.slice(0, maxStringLength) + '...' : data;
    return <span style={{ color: C.green }}>"{display}"</span>;
  }
  if (Array.isArray(data)) {
    if (data.length === 0) return <span style={{ color: C.muted }}>[]</span>;
    return (
      <span>
        <span style={{ color: C.muted }}>[</span>
        {data.map((item, i) => (
          <div key={i} style={{ paddingLeft: 16 }}>
            <JsonHighlighter data={item} depth={depth + 1} maxStringLength={maxStringLength} />
            {i < data.length - 1 && <span style={{ color: C.muted }}>,</span>}
          </div>
        ))}
        <span style={{ color: C.muted }}>]</span>
      </span>
    );
  }
  if (typeof data === 'object') {
    const keys = Object.keys(data);
    if (keys.length === 0) return <span style={{ color: C.muted }}>{'{}'}</span>;
    return (
      <span>
        <span style={{ color: C.muted }}>{'{'}</span>
        {keys.map((key, i) => (
          <div key={key} style={{ paddingLeft: 16 }}>
            <span style={{ color: C.teal }}>"{key}"</span>
            <span style={{ color: C.muted }}>: </span>
            <JsonHighlighter data={data[key]} depth={depth + 1} maxStringLength={maxStringLength} />
            {i < keys.length - 1 && <span style={{ color: C.muted }}>,</span>}
          </div>
        ))}
        <span style={{ color: C.muted }}>{'}'}</span>
      </span>
    );
  }
  return <span style={{ color: C.text }}>{String(data)}</span>;
}

function JsonCard({ data, title }) {
  return (
    <div style={{
      background: C.bg,
      border: `1px solid ${C.line}`,
      borderRadius: 6,
      overflow: 'hidden',
    }}>
      <div style={{
        padding: '4px 10px',
        borderBottom: `1px solid ${C.line}`,
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
        background: C.line + '30',
      }}>
        <span style={{ color: C.muted, fontSize: 10, fontWeight: 600 }}>{title || 'JSON'}</span>
        <CopyButton text={data} />
      </div>
      <div style={{ padding: '8px 10px', fontSize: 11, overflowX: 'auto' }}>
        <JsonHighlighter data={data} />
      </div>
    </div>
  );
}

function StatusDot({ status, size = 8 }) {
  const color = STATUS_COLORS[status] || C.muted;
  const isRunning = status === 'running';
  return (
    <span style={{
      display: 'inline-block',
      width: size,
      height: size,
      borderRadius: '50%',
      background: color,
      animation: isRunning ? 'pulse 1.5s infinite' : 'none',
    }} />
  );
}

function DragHandle({ onDrag }) {
  const handleMouseDown = (e) => {
    e.preventDefault();
    const move = (e2) => onDrag(e2.clientX);
    const up = () => {
      document.removeEventListener('mousemove', move);
      document.removeEventListener('mouseup', up);
    };
    document.addEventListener('mousemove', move);
    document.addEventListener('mouseup', up);
  };
  return (
    <div
      onMouseDown={handleMouseDown}
      style={{
        width: 6,
        cursor: 'col-resize',
        background: C.line,
        flexShrink: 0,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
      }}
    >
      <div style={{ width: 2, height: 30, background: C.muted, borderRadius: 1 }} />
    </div>
  );
}
