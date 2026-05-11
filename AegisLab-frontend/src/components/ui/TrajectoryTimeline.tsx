import type { ReactNode } from 'react';

import { Chip } from './Chip';
import { MetricLabel } from './MetricLabel';
import { MonoValue } from './MonoValue';
import { StatusDot } from './StatusDot';
import { TrajectoryStep, type TrajectoryStepData } from './TrajectoryStep';
import './TrajectoryTimeline.css';

export interface TrajectoryTimelineProps {
  steps: TrajectoryStepData[];
  agentName?: string;
  status?: 'running' | 'completed' | 'failed';
  totalDurationMs?: number;
  extra?: ReactNode;
  className?: string;
}

export function TrajectoryTimeline({
  steps,
  agentName,
  status = 'running',
  totalDurationMs,
  extra,
  className,
}: TrajectoryTimelineProps) {
  const cls = ['aegis-timeline', className ?? ''].filter(Boolean).join(' ');

  const statusTone =
    status === 'failed' ? 'warning' : status === 'completed' ? 'ink' : 'ink';
  const pulse = status === 'running';

  return (
    <div className={cls}>
      <header className="aegis-timeline__header">
        <div className="aegis-timeline__header-left">
          <StatusDot tone={statusTone} pulse={pulse} />
          {agentName ? (
            <span className="aegis-timeline__agent-name">{agentName}</span>
          ) : (
            <MetricLabel size="sm">agent trajectory</MetricLabel>
          )}
          <Chip tone={status === 'failed' ? 'warning' : 'default'}>
            {status}
          </Chip>
        </div>
        <div className="aegis-timeline__header-right">
          {totalDurationMs !== undefined && (
            <MetricLabel size="sm">
              <MonoValue size="sm" weight="regular">
                {totalDurationMs}
              </MonoValue>{' '}
              ms
            </MetricLabel>
          )}
          {extra}
        </div>
      </header>

      <div className="aegis-timeline__track">
        {steps.map((step, i) => (
          <TrajectoryStep
            key={step.step}
            data={step}
            defaultExpanded={i === steps.length - 1}
          />
        ))}
      </div>
    </div>
  );
}

export default TrajectoryTimeline;
