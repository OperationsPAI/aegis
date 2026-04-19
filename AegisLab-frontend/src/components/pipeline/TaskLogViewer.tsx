import { useCallback, useEffect, useRef, useState } from 'react';

import { Badge, Space, Typography } from 'antd';

import { createTaskLogWebSocket } from '@/api/tasks';

const { Text } = Typography;

type ConnectionStatus = 'connecting' | 'connected' | 'disconnected';

interface TaskLogViewerProps {
  /** Task ID to stream logs for. If undefined, the component renders nothing. */
  taskId: string | undefined;
}

const STATUS_COLOR: Record<ConnectionStatus, string> = {
  connecting: 'orange',
  connected: 'green',
  disconnected: 'red',
};

const STATUS_LABEL: Record<ConnectionStatus, string> = {
  connecting: 'Connecting',
  connected: 'Connected',
  disconnected: 'Disconnected',
};

const RECONNECT_DELAY_MS = 3000;

const TaskLogViewer: React.FC<TaskLogViewerProps> = ({ taskId }) => {
  const [logs, setLogs] = useState<string[]>([]);
  const [status, setStatus] = useState<ConnectionStatus>('disconnected');
  const containerRef = useRef<HTMLDivElement>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const mountedRef = useRef(true);

  const scrollToBottom = useCallback(() => {
    if (containerRef.current) {
      containerRef.current.scrollTop = containerRef.current.scrollHeight;
    }
  }, []);

  const connect = useCallback(() => {
    if (!taskId || !mountedRef.current) return;

    // Clean up any existing connection
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }

    setStatus('connecting');
    const ws = createTaskLogWebSocket(taskId);
    wsRef.current = ws;

    ws.onopen = () => {
      if (mountedRef.current) setStatus('connected');
    };

    ws.onmessage = (event) => {
      if (!mountedRef.current) return;
      try {
        const data = JSON.parse(event.data) as {
          content?: string;
          timestamp?: string;
        };
        if (data.content) {
          setLogs((prev) => [...prev, data.content as string]);
          // Defer scroll so the DOM has updated
          requestAnimationFrame(scrollToBottom);
        }
      } catch {
        // If not JSON, treat as plain text
        setLogs((prev) => [...prev, event.data]);
        requestAnimationFrame(scrollToBottom);
      }
    };

    ws.onerror = () => {
      if (mountedRef.current) setStatus('disconnected');
    };

    ws.onclose = () => {
      if (!mountedRef.current) return;
      setStatus('disconnected');
      // Auto-reconnect after delay
      reconnectTimerRef.current = setTimeout(() => {
        if (mountedRef.current) connect();
      }, RECONNECT_DELAY_MS);
    };
  }, [taskId, scrollToBottom]);

  useEffect(() => {
    mountedRef.current = true;
    setLogs([]);
    connect();

    return () => {
      mountedRef.current = false;
      if (reconnectTimerRef.current) {
        clearTimeout(reconnectTimerRef.current);
        reconnectTimerRef.current = null;
      }
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
      }
    };
  }, [connect]);

  if (!taskId) return null;

  return (
    <div>
      <div style={{ marginBottom: 8 }}>
        <Space>
          <Text strong>Task Logs</Text>
          <Badge color={STATUS_COLOR[status]} text={STATUS_LABEL[status]} />
        </Space>
      </div>
      <div
        ref={containerRef}
        style={{
          background: '#1e1e1e',
          color: '#d4d4d4',
          fontFamily: "'Menlo', 'Monaco', 'Courier New', monospace",
          fontSize: 13,
          lineHeight: 1.6,
          padding: 16,
          borderRadius: 8,
          overflowY: 'auto',
          maxHeight: 400,
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-all',
        }}
      >
        {logs.length === 0 ? (
          <span style={{ color: '#888' }}>Waiting for logs...</span>
        ) : (
          logs.map((line, idx) => <div key={idx}>{line}</div>)
        )}
      </div>
    </div>
  );
};

export default TaskLogViewer;
