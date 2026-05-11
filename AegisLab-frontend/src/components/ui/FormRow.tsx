import type { ReactNode } from 'react';

import './FormRow.css';

interface FormRowProps {
  label: string;
  description?: string;
  children: ReactNode;
  className?: string;
}

export function FormRow({ label, description, children, className }: FormRowProps) {
  const cls = ['aegis-form-row', className ?? ''].filter(Boolean).join(' ');

  return (
    <div className={cls}>
      <div className="aegis-form-row__label-group">
        <label className="aegis-form-row__label">{label}</label>
        {description && (
          <span className="aegis-form-row__description">{description}</span>
        )}
      </div>
      <div className="aegis-form-row__control">
        {children}
      </div>
    </div>
  );
}

export default FormRow;
