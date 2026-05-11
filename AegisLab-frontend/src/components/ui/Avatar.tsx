import { type ReactNode } from 'react';

import './Avatar.css';

interface AvatarProps {
  src?: string;
  name?: string;
  icon?: ReactNode;
  size?: 'sm' | 'base' | 'lg';
  className?: string;
}

export function Avatar({
  src,
  name,
  icon,
  size = 'base',
  className,
}: AvatarProps) {
  const cls = [
    'aegis-avatar',
    `aegis-avatar--${size}`,
    className ?? '',
  ].filter(Boolean).join(' ');

  if (src) {
    return (
      <img
        src={src}
        alt={name ?? 'Avatar'}
        className={cls}
      />
    );
  }

  const fallback = name
    ? name
        .split(' ')
        .map((w) => w[0])
        .join('')
        .slice(0, 2)
        .toUpperCase()
    : icon;

  return <span className={cls}>{fallback}</span>;
}

export default Avatar;
