import type { CSSProperties, ReactNode } from 'react';
import type { Icon } from '@tabler/icons-react';
import classes from './PageHeader.module.css';

interface PageHeaderProps {
  title: string;
  description?: string;
  eyebrow?: { label: string; icon: Icon; color?: string };
  actions?: ReactNode;
}

export function PageHeader({ title, description, eyebrow, actions }: PageHeaderProps) {
  return (
    <header className={classes.header}>
      <div>
        {eyebrow && (
          <div
            className={classes.eyebrow}
            style={eyebrow.color ? ({ '--kind': eyebrow.color } as CSSProperties) : undefined}
          >
            <eyebrow.icon size={14} stroke={1.75} />
            {eyebrow.label}
          </div>
        )}
        <h1 className={classes.title}>{title}</h1>
        {description && <p className={classes.description}>{description}</p>}
      </div>
      {actions}
    </header>
  );
}
