function useWebSocket(onEvent) {
  const [status, setStatus] = useState('disconnected');
  const wsRef = useRef(null);
  const retryRef = useRef(1000);
  const onEventRef = useRef(onEvent);
  onEventRef.current = onEvent;

  useEffect(() => {
    let mounted = true;
    let timer = null;

    function connect() {
      if (!mounted) return;
      const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
      const ws = new WebSocket(`${proto}//${location.host}/ws`);
      wsRef.current = ws;

      ws.onopen = () => {
        if (!mounted) return;
        setStatus('connected');
        retryRef.current = 1000;
      };
      ws.onmessage = (e) => {
        try {
          const event = JSON.parse(e.data);
          onEventRef.current(event);
        } catch {}
      };
      ws.onclose = () => {
        if (!mounted) return;
        setStatus('disconnected');
        timer = setTimeout(() => {
          retryRef.current = Math.min(retryRef.current * 1.5, 10000);
          connect();
        }, retryRef.current);
      };
      ws.onerror = () => ws.close();
    }
    connect();

    return () => {
      mounted = false;
      if (timer) clearTimeout(timer);
      if (wsRef.current) wsRef.current.close();
    };
  }, []);

  return status;
}
