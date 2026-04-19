/**
 * useTraceSSE — React hook for consuming trace SSE events
 *
 * Connects to the SSE endpoint at /api/v2/traces/{traceId}/stream,
 * processes StreamEvent objects, and tracks per-phase task IDs
 * and statuses for the pipeline phases.
 *
 * The SSE endpoint replays historical events first, then switches
 * to live monitoring — so connecting mid-pipeline still yields the
 * full current state.
 *
 * Usage:
 *   const { phases, isConnected } = useTraceSSE(traceId);
 *   // phases.datapack_building.taskId → pass to LogViewer
 */
import { useEffect, useRef, useState } from 'react';

import {
  type ContainerVersionItem,
  EventType,
  type ExecutionInfo,
  type ExecutionResult,
  type TraceStreamEvent,
} from '@rcabench/client';

import { useAuthStore } from '@/store/auth';

/** Alias for the SDK's TraceStreamEvent (previously exported as StreamEvent) */
type StreamEvent = TraceStreamEvent;

// ─── Types ──────────────────────────────────────────────────────────────────

export type StepPhase = 'fault_injection' | 'datapack_building' | 'detector';

export interface PhaseInfo {
  /** Task ID for this phase (used to open WebSocket log streams) */
  taskId?: string;
  /** Current status derived from the latest SSE event for this phase */
  status: 'wait' | 'process' | 'finish' | 'error';
  /** Unix timestamp (milliseconds) when this phase started */
  startTime?: number;
  /** Unix timestamp (milliseconds) when this phase finished or failed */
  endTime?: number;
}

export type PipelinePhaseMap = Readonly<Record<StepPhase, PhaseInfo>>;

export interface UseTraceSSEResult {
  /** Per-phase task IDs and statuses, updated in real-time from SSE */
  phases: PipelinePhaseMap;
  /** Whether the SSE connection is currently active */
  isConnected: boolean;
  /** Most recent StreamEvent received (useful for debugging) */
  lastEvent?: StreamEvent;
}

// ─── Constants ──────────────────────────────────────────────────────────────

const INITIAL_PHASE_MAP: PipelinePhaseMap = {
  fault_injection: { status: 'wait' },
  datapack_building: { status: 'wait' },
  detector: { status: 'wait' },
};

/**
 * Maps SSE event names to pipeline phase transitions.
 *
 * Handles both the canonical EventType enum format (dotted: `fault.injection.started`)
 * and the Redis stream raw format (underscore: `fault_injection.started`)
 * to be resilient against minor backend inconsistencies.
 */
const EVENT_PHASE_MAP: Readonly<
  Record<string, { phase: StepPhase; status: PhaseInfo['status'] }>
> = {
  // ── Fault injection phase ────────────────────────────────────────────────
  [EventType.FaultInjectionStarted]: {
    phase: 'fault_injection',
    status: 'process',
  },
  [EventType.FaultInjectionCompleted]: {
    phase: 'fault_injection',
    status: 'finish',
  },
  [EventType.FaultInjectionFailed]: {
    phase: 'fault_injection',
    status: 'error',
  },
  // Underscore variants (Redis stream raw format)
  'fault_injection.started': { phase: 'fault_injection', status: 'process' },
  'fault_injection.completed': { phase: 'fault_injection', status: 'finish' },
  'fault_injection.failed': { phase: 'fault_injection', status: 'error' },

  // ── Datapack building phase ──────────────────────────────────────────────
  [EventType.DatapackBuildStarted]: {
    phase: 'datapack_building',
    status: 'process',
  },
  [EventType.DatapackBuildSucceed]: {
    phase: 'datapack_building',
    status: 'finish',
  },
  [EventType.DatapackBuildFailed]: {
    phase: 'datapack_building',
    status: 'error',
  },

  // ── Detector (algorithm run) phase ───────────────────────────────────────
  [EventType.AlgoRunStarted]: { phase: 'detector', status: 'process' },
  [EventType.AlgoRunSucceed]: { phase: 'detector', status: 'finish' },
  [EventType.AlgoRunFailed]: { phase: 'detector', status: 'error' },
};

/** Maximum reconnection attempts when EventSource does not auto-recover */
const MAX_RECONNECT = 5;
const BASE_RECONNECT_DELAY = 2_000;

// ─── Hook ───────────────────────────────────────────────────────────────────

export const useTraceSSE = (traceId?: string): UseTraceSSEResult => {
  const [phases, setPhases] = useState<PipelinePhaseMap>(INITIAL_PHASE_MAP);
  const [isConnected, setIsConnected] = useState(false);
  const [lastEvent, setLastEvent] = useState<StreamEvent | undefined>();

  // Track reconnection state across renders without causing re-renders
  const reconnectRef = useRef(0);
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (!traceId) return;

    // Reset state on traceId change
    setPhases(INITIAL_PHASE_MAP);
    setIsConnected(false);
    setLastEvent(undefined);
    reconnectRef.current = 0;

    let abortController: AbortController | null = null;
    let intentionalClose = false;

    const processMessage = (data: string) => {
      try {
        const streamEvent = JSON.parse(data) as StreamEvent;
        setLastEvent(streamEvent);

        // Debug: log timestamp info
        if (
          import.meta.env.DEV &&
          streamEvent.event_name &&
          streamEvent.timestamp === undefined
        ) {
          console.warn(
            `[SSE] Missing timestamp for event: ${streamEvent.event_name}`,
            streamEvent
          );
        }

        const eventName = streamEvent.event_name;
        if (!eventName) return;

        // SSE event received: eventName

        if (eventName === EventType.AlgoRunStarted) {
          // Special case: filter non-detector algorithm runs
          const executionInfo = streamEvent.payload as ExecutionInfo;
          const containerItem = executionInfo.algorithm as ContainerVersionItem;
          if (containerItem?.container_name !== 'detector') {
            return;
          }
        }

        if (
          eventName === EventType.AlgoRunSucceed ||
          eventName === EventType.AlgoRunFailed
        ) {
          const executionResult = streamEvent.payload as ExecutionResult;
          if (executionResult?.algorithm !== 'detector') {
            return;
          }
        }

        const mapping = EVENT_PHASE_MAP[eventName as string];
        if (!mapping) return;

        const taskId = streamEvent.task_id;
        // Use StreamEvent timestamp (in milliseconds), fallback to current time
        const timestamp = streamEvent.timestamp ?? Date.now();

        setPhases((prev) => {
          const current = prev[mapping.phase];

          // Skip no-op updates (preserves referential equality)
          if (current.status === mapping.status && current.taskId === taskId) {
            return prev;
          }

          const isStarting = mapping.status === 'process';
          const isTerminal =
            mapping.status === 'finish' || mapping.status === 'error';

          return {
            ...prev,
            [mapping.phase]: {
              taskId: taskId ?? current.taskId,
              status: mapping.status,
              startTime: isStarting ? timestamp : current.startTime,
              endTime: isTerminal ? timestamp : current.endTime,
            },
          };
        });
      } catch {
        // Silently drop unparseable messages — the stream may
        // send heartbeat or comment lines that are not JSON.
      }
    };

    const connect = async () => {
      const token = localStorage.getItem('access_token');
      if (!token) return;

      abortController = new AbortController();
      const url = `/api/v2/traces/${traceId}/stream`;

      try {
        const response = await fetch(url, {
          headers: {
            Authorization: `Bearer ${token}`,
            Accept: 'text/event-stream',
          },
          signal: abortController.signal,
        });

        if (!response.ok) {
          if (response.status === 401) {
            await useAuthStore.getState().refreshAccessToken();
            throw new Error('TOKEN_REFRESHED');
          }
          throw new Error(`SSE connection failed: ${response.status}`);
        }

        reconnectRef.current = 0;
        setIsConnected(true);

        const reader = response.body?.getReader();
        const decoder = new TextDecoder();

        if (!reader) {
          throw new Error('Response body is not readable');
        }

        // Buffer for incomplete lines
        let buffer = '';
        let reading = true;

        while (reading) {
          const { done, value } = await reader.read();

          if (done || intentionalClose) {
            reading = false;
            break;
          }

          // Decode chunk and append to buffer
          buffer += decoder.decode(value, { stream: true });

          // Process complete lines
          const lines = buffer.split('\n');
          buffer = lines.pop() ?? ''; // Keep the last incomplete line

          for (let i = 0; i < lines.length; i++) {
            const line = lines[i].trim();
            if (line.startsWith('data:')) {
              const data = line.slice(5).trim();
              processMessage(data);
            }
          }
        }

        reader.releaseLock();
      } catch (error) {
        setIsConnected(false);

        if (intentionalClose) return;

        // Auto-reconnect with exponential backoff
        if (reconnectRef.current < MAX_RECONNECT) {
          reconnectRef.current += 1;
          const delay =
            BASE_RECONNECT_DELAY * Math.pow(2, reconnectRef.current - 1);

          reconnectTimerRef.current = setTimeout(() => {
            if (!intentionalClose) {
              connect();
            }
          }, delay);
        }
      }
    };

    // Defer connect to the next tick so React StrictMode's immediate
    // cleanup can cancel before the fetch is actually initiated.
    const connectTimer = setTimeout(() => {
      connect();
    }, 0);

    return () => {
      intentionalClose = true;
      clearTimeout(connectTimer);

      if (reconnectTimerRef.current) {
        clearTimeout(reconnectTimerRef.current);
        reconnectTimerRef.current = null;
      }

      if (abortController) {
        abortController.abort();
        abortController = null;
      }

      setIsConnected(false);
    };
  }, [traceId]);

  return { phases, isConnected, lastEvent };
};
