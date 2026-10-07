import { CopyButton } from '@mantine/core';
import { IconCheck, IconCopy } from '@tabler/icons-react';
import { useLabelPrefix } from '../hooks/useAPI';
import { formatLabels, type LabelFormat, type LabelLine, type Token } from './format';
import { useLabelFormat } from './useLabelFormat';
import classes from './CodeBlock.module.css';

const COPIED_RESET_MS = 1400;

interface CodeBlockProps {
  lines: LabelLine[];
  image?: string;
  // Forces a format; otherwise the shared user preference is used.
  format?: LabelFormat;
  variant?: 'default' | 'bad' | 'good';
}

function TokenSpan({ token }: { token: Token }) {
  if (token.kind === 'plain') return token.text;
  return (
    <span className={classes[token.kind]} data-type={token.labelType}>
      {token.text}
    </span>
  );
}

export function CodeBlock({ lines, image, format, variant = 'default' }: CodeBlockProps) {
  const preferred = useLabelFormat();
  const prefix = useLabelPrefix();
  const active = format ?? preferred;
  const out = formatLabels(lines, { format: active, prefix, image });
  const noun = out.labelCount === 1 ? 'label' : 'labels';

  return (
    <div className={classes.block} data-variant={variant}>
      <div className={classes.bar}>
        <span>{active === 'docker' ? 'shell' : 'compose.yaml'}</span>
        <CopyButton value={out.text} timeout={COPIED_RESET_MS}>
          {({ copied, copy }) => (
            <button
              type="button"
              className={classes.copy}
              data-copied={copied}
              onClick={copy}
              aria-label={copied ? `Copied ${out.labelCount} ${noun}` : `Copy ${out.labelCount} ${noun}`}
            >
              {copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
              {copied ? 'Copied' : 'Copy'}
            </button>
          )}
        </CopyButton>
      </div>
      <pre className={classes.pre}>
        {out.lines.map((line, i) => (
          <div key={i}>
            {line.map((token, j) => (
              <TokenSpan key={j} token={token} />
            ))}
          </div>
        ))}
      </pre>
    </div>
  );
}
