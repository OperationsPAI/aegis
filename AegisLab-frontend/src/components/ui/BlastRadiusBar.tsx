import './BlastRadiusBar.css';

interface BlastRadiusBarProps {
  /** 0 – 100 */
  value: number;
  /** Optional descriptive label for the current value (right-aligned, mid). */
  centerLabel?: string;
  /** Hide the 0 / 100 endpoints. */
  hideTicks?: boolean;
  className?: string;
}

export function BlastRadiusBar({
  value,
  centerLabel,
  hideTicks = false,
  className,
}: BlastRadiusBarProps) {
  const clamped = Math.max(0, Math.min(100, value));
  const cls = ['aegis-blast-radius', className ?? ''].filter(Boolean).join(' ');

  return (
    <div className={cls}>
      <div
        className="aegis-blast-radius__track"
        role="progressbar"
        aria-valuenow={clamped}
        aria-valuemin={0}
        aria-valuemax={100}
      >
        <div
          className="aegis-blast-radius__fill"
          style={{ width: `${clamped}%` }}
        />
      </div>
      {!hideTicks && (
        <div className="aegis-blast-radius__ticks">
          <span className="aegis-blast-radius__tick">0%</span>
          {centerLabel && (
            <span className="aegis-blast-radius__tick aegis-blast-radius__tick--center">
              {centerLabel}
            </span>
          )}
          <span className="aegis-blast-radius__tick">100%</span>
        </div>
      )}
    </div>
  );
}

export default BlastRadiusBar;
