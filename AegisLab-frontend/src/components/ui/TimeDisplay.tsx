import { useMemo } from 'react';

import './TimeDisplay.css';

interface TimeDisplayProps {
  /** ISO string, timestamp ms, or Date object. */
  value: string | number | Date;
  /** 'absolute' = full timestamp, 'relative' = "2 m ago", 'duration' = elapsed time. */
  mode?: 'absolute' | 'relative' | 'duration';
  /** For relative mode: refresh interval in ms (default 60 000). */
  refreshMs?: number;
  className?: string;
}

function formatAbsolute(d: Date): string {
  const now = new Date();
  const sameDay =
    d.getFullYear() === now.getFullYear() &&
    d.getMonth() === now.getMonth() &&
    d.getDate() === now.getDate();

  const pad = (n: number) => String(n).padStart(2, '0');
  const time = `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;

  if (sameDay) return time;
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${time}`;
}

function formatRelative(d: Date): string {
  const now = new Date();
  const diffMs = now.getTime() - d.getTime();
  const diffSec = Math.floor(diffMs / 1000);
  if (diffSec < 10) return 'just now';
  if (diffSec < 60) return `${diffSec} s ago`;
  const diffMin = Math.floor(diffSec / 60);
  if (diffMin < 60) return `${diffMin} m ago`;
  const diffHour = Math.floor(diffMin / 60);
  if (diffHour < 24) return `${diffHour} h ago`;
  const diffDay = Math.floor(diffHour / 24);
  if (diffDay < 30) return `${diffDay} d ago`;
  return formatAbsolute(d);
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)} ms`;
  const sec = ms / 1000;
  if (sec < 60) return `${sec.toFixed(2)} s`.replace(/\.00$/, ' s');
  const min = Math.floor(sec / 60);
  const remSec = Math.round(sec % 60);
  if (min < 60) return `${min} m ${remSec} s`;
  const hour = Math.floor(min / 60);
  const remMin = min % 60;
  return `${hour} h ${remMin} m`;
}

export function TimeDisplay({
  value,
  mode = 'absolute',
  className,
}: TimeDisplayProps) {
  const d = useMemo(() => {
    if (typeof value === 'string') return new Date(value);
    if (typeof value === 'number') {
      // Heuristic: if number is small, treat as ms duration; else as epoch ms
      return value < 1e10 ? new Date(value) : new Date(value);
    }
    return value;
  }, [value]);

  const display = useMemo(() => {
    if (mode === 'duration') {
      const ms = typeof value === 'number' ? value : d.getTime();
      return formatDuration(ms);
    }
    if (mode === 'relative') return formatRelative(d);
    return formatAbsolute(d);
  }, [d, mode, value]);

  const tooltip = useMemo(() => {
    if (mode === 'absolute') return undefined;
    return formatAbsolute(d);
  }, [d, mode]);

  const cls = ['aegis-time-display', className ?? ''].filter(Boolean).join(' ');

  return (
    <span className={cls} title={tooltip}>
      {display}
    </span>
  );
}

export default TimeDisplay;
