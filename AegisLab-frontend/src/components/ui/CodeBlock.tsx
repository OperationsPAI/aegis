import { useCallback, useState } from 'react';

import './CodeBlock.css';

interface CodeBlockProps {
  code: string;
  language?: 'json' | 'yaml' | 'sql' | 'bash' | 'text';
  showLineNumbers?: boolean;
  className?: string;
}

export function CodeBlock({
  code,
  language = 'text',
  showLineNumbers = false,
  className,
}: CodeBlockProps) {
  const [copied, setCopied] = useState(false);

  const handleCopy = useCallback(() => {
    navigator.clipboard.writeText(code).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  }, [code]);

  const lines = code.split('\n');
  const padLen = String(lines.length).length;

  const cls = ['aegis-code-block', className ?? ''].filter(Boolean).join(' ');

  return (
    <div className={cls}>
      <div className="aegis-code-block__header">
        <span className="aegis-code-block__lang">{language}</span>
        <button
          type="button"
          className="aegis-code-block__copy"
          onClick={handleCopy}
          aria-label="Copy to clipboard"
        >
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
      <pre className="aegis-code-block__pre">
        <code>
          {lines.map((line, i) => (
            <div key={i} className="aegis-code-block__line">
              {showLineNumbers && (
                <span className="aegis-code-block__ln">
                  {String(i + 1).padStart(padLen, ' ')}
                </span>
              )}
              <span className="aegis-code-block__body">{line || ' '}</span>
            </div>
          ))}
        </code>
      </pre>
    </div>
  );
}

export default CodeBlock;
