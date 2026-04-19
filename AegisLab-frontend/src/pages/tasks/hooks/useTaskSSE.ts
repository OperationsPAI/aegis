import { useEffect } from 'react';

import { type TaskDetailResp, TaskState } from '@rcabench/client';
import dayjs from 'dayjs';

import { createTraceStream } from '@/api/traces';

/**
 * SSE log streaming for a single task.
 * Appends log messages and triggers refetch on task_update events.
 */
export function useTaskSSE(
  task: TaskDetailResp | undefined,
  refetch: () => void,
  setLogs: React.Dispatch<React.SetStateAction<string[]>>
) {
  useEffect(() => {
    if (!task || String(task.state) !== String(TaskState.Running)) return;
    if (!task.trace_id) return;

    const eventSource = createTraceStream(task.trace_id);

    eventSource.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data);
        if (data.type === 'log') {
          setLogs((prev) => [
            ...prev,
            `[${dayjs().format('HH:mm:ss')}] ${data.message}`,
          ]);
        } else if (data.type === 'task_update') {
          refetch();
        }
      } catch (error) {
        if (import.meta.env.DEV) {
          console.error('Error parsing SSE data:', error);
        }
      }
    };

    eventSource.onerror = (error) => {
      if (import.meta.env.DEV) {
        console.error('SSE error:', error);
      }
      eventSource.close();
    };

    return () => {
      eventSource.close();
    };
  }, [task, refetch, setLogs]);
}
