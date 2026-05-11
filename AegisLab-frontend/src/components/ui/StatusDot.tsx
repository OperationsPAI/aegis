import './StatusDot.css';

interface StatusDotProps {
  pulse?: boolean;
  tone?: 'ink' | 'inverted' | 'warning' | 'muted';
  size?: number;
  className?: string;
}

export function StatusDot({
  pulse = false,
  tone = 'ink',
  size = 6,
  className,
}: StatusDotProps) {
  const cls = [
    'aegis-status-dot',
    `aegis-status-dot--${tone}`,
    pulse ? 'aegis-status-dot--pulse' : '',
    className ?? '',
  ]
    .filter(Boolean)
    .join(' ');
  return (
    <span
      className={cls}
      style={{ width: size, height: size }}
      aria-hidden="true"
    />
  );
}

export default StatusDot;
