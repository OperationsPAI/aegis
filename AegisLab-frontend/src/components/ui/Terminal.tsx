import type { ReactNode } from 'react';

import './Terminal.css';

export type LogLevel = 'debug' | 'info' | 'warn' | 'error';

export interface TerminalLine {
  ts?: string;
  prefix?: string;
  level?: LogLevel;
  body: ReactNode;
}

interface TerminalProps {
  lines: TerminalLine[];
  ariaLabel?: string;
  className?: string;
}

export function Terminal({
  lines,
  ariaLabel = 'Experiment log',
  className,
}: TerminalProps) {
  const cls = ['aegis-terminal', className ?? ''].filter(Boolean).join(' ');
  return (
    <div className={cls} role="log" aria-label={ariaLabel} aria-live="polite">
      {lines.map((line, i) => (
        <div className="aegis-terminal__line" key={i}>
          {line.ts && (
            <span className="aegis-terminal__ts">[{line.ts}]</span>
          )}
          {line.prefix && (
            <span className={`aegis-terminal__prefix${line.level ? ` aegis-terminal__prefix--${line.level}` : ''}`}>
              {line.prefix}
            </span>
          )}
          <span className="aegis-terminal__body">{line.body}</span>
        </div>
      ))}
    </div>
  );
}

export default Terminal;
