import { useEffect } from 'react';

import { type TaskResp, TaskState } from '@rcabench/client';
import { message } from 'antd';

import { createTraceStream } from '@/api/traces';

/**
 * SSE connections for running tasks in a task list.
 * Shows info messages on task_update events and triggers refetch.
 */
export function useTasksSSE(
  autoRefresh: boolean,
  tasks: TaskResp[] | undefined,
  refetch: () => void
) {
  useEffect(() => {
    if (!autoRefresh) return;

    const runningTasks = tasks?.filter(
      (t: TaskResp) =>
        String(t.state) === String(TaskState.Running) ||
        t.state === '2' ||
        t.state === 'RUNNING'
    );
    if (!runningTasks?.length) return;

    const eventSources: EventSource[] = [];

    runningTasks.forEach((task) => {
      if (!task.trace_id) return;
      const eventSource = createTraceStream(task.trace_id);

      eventSource.onmessage = (event) => {
        try {
          const data = JSON.parse(event.data);
          if (data.type === 'task_update') {
            message.info(`Task ${task.id} update: ${data.message}`);
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

      eventSources.push(eventSource);
    });

    return () => {
      eventSources.forEach((es) => es.close());
    };
  }, [autoRefresh, tasks, refetch]);
}
