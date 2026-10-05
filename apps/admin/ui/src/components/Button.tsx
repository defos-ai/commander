import type { ComponentProps } from 'react';

export function Button({ variant = 'primary', className = '', type = 'button', ...props }: ComponentProps<'button'> & {
  variant?: 'primary' | 'secondary';
}) {
  return <button {...props} type={type} className={`button button--${variant} ${className}`} />;
}
