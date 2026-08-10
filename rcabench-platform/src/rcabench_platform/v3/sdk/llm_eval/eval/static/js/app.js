function App() {
  // WebSocket event handler — forward eval events to EvalPage
  const handleEvent = useCallback((event) => {
    if (event.channel === 'eval') {
      window.dispatchEvent(new CustomEvent('eval_event', { detail: event }));
    }
  }, []);

  const wsStatus = useWebSocket(handleEvent);

  return (
    <>
      {/* Header */}
      <div style={{
        height: 40,
        padding: '0 16px',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        borderBottom: `1px solid ${C.line}`,
        flexShrink: 0,
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <span style={{ fontWeight: 700, color: C.teal, fontSize: 14 }}>RCABench</span>
          <span style={{ color: C.muted, fontSize: 12 }}>Eval Dashboard</span>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 11 }}>
          <span style={{
            width: 8, height: 8, borderRadius: '50%',
            background: wsStatus === 'connected' ? C.green : C.red,
          }} />
          <span style={{ color: wsStatus === 'connected' ? C.green : C.red }}>
            {wsStatus === 'connected' ? 'CONNECTED' : 'DISCONNECTED'}
          </span>
        </div>
      </div>

      {/* Main content */}
      <div style={{ flex: 1, overflow: 'hidden' }}>
        <EvalPage />
      </div>
    </>
  );
}

// Mount
ReactDOM.createRoot(document.getElementById('root')).render(<App />);
