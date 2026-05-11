import { useState } from 'react';

import { Chip } from './Chip';
import { MetricLabel } from './MetricLabel';
import './ToolCallCard.css';

export interface ToolCallData {
  name: string;
  arguments: string;
  result?: string;
}

interface ToolCallCardProps {
  data: ToolCallData;
  className?: string;
}

function CodeBlock({ label, code }: { label: string; code: string }) {
  const [folded, setFolded] = useState(true);
  const lines = code.split('\n');
  const preview = lines.slice(0, 3).join('\n');
  const showExpand = lines.length > 3;

  return (
    <div className="aegis-tool-call__code">
      <button
        type="button"
        className="aegis-tool-call__code-label"
        onClick={() => setFolded((f) => !f)}
        aria-expanded={!folded}
      >
        <MetricLabel size="xs">{label}</MetricLabel>
        {showExpand && (
          <span className="aegis-tool-call__fold-indicator">
            {folded ? '▸' : '▾'}
          </span>
        )}
      </button>
      <pre className="aegis-tool-call__code-body">
        <code>{folded && showExpand ? preview : code}</code>
      </pre>
    </div>
  );
}

export function ToolCallCard({ data, className }: ToolCallCardProps) {
  const cls = ['aegis-tool-call', className ?? ''].filter(Boolean).join(' ');

  return (
    <div className={cls}>
      <div className="aegis-tool-call__head">
        <Chip tone="ink">{data.name}</Chip>
        <MetricLabel size="xs">tool_call</MetricLabel>
      </div>
      <CodeBlock label="arguments" code={data.arguments} />
      {data.result && <CodeBlock label="result" code={data.result} />}
    </div>
  );
}

export default ToolCallCard;
