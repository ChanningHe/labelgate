import { describe, expect, test } from 'vitest';
import { formatLabels, type LabelLine } from './format';

const tunnel: LabelLine[] = [
  { key: 'tunnel.web.hostname', value: 'app.example.com' },
  { key: 'tunnel.web.service', value: 'http://webapp:80' },
];

describe('formatLabels map format', () => {
  test('renders a compose labels mapping with the default prefix', () => {
    const out = formatLabels(tunnel, { format: 'map', prefix: 'labelgate' });

    expect(out.text).toBe(
      [
        'labels:',
        '  labelgate.tunnel.web.hostname: "app.example.com"',
        '  labelgate.tunnel.web.service: "http://webapp:80"',
      ].join('\n'),
    );
    expect(out.labelCount).toBe(2);
  });

  test('uses the configured prefix', () => {
    const out = formatLabels(tunnel, { format: 'map', prefix: 'lg' });

    expect(out.text).toContain('  lg.tunnel.web.hostname: "app.example.com"');
  });

  test('escapes backslashes and double quotes for YAML double-quoted strings', () => {
    const out = formatLabels([{ key: 'tunnel.static.path', value: '\\.(jpg|png)$ "x"' }], {
      format: 'map',
      prefix: 'labelgate',
    });

    expect(out.text).toContain('labelgate.tunnel.static.path: "\\\\.(jpg|png)$ \\"x\\""');
  });

  test('renders comments in place and leaves them out of the label count', () => {
    const out = formatLabels([{ comment: '# Attach the policy' }, ...tunnel], { format: 'map', prefix: 'labelgate' });

    expect(out.text.split('\n')[1]).toBe('  # Attach the policy');
    expect(out.labelCount).toBe(2);
  });
});

describe('formatLabels list format', () => {
  test('renders key=value list items', () => {
    const out = formatLabels(tunnel, { format: 'list', prefix: 'labelgate' });

    expect(out.text).toBe(
      [
        'labels:',
        '  - "labelgate.tunnel.web.hostname=app.example.com"',
        '  - "labelgate.tunnel.web.service=http://webapp:80"',
      ].join('\n'),
    );
  });
});

describe('formatLabels docker format', () => {
  test('renders docker run flags ending with the image', () => {
    const out = formatLabels(tunnel, { format: 'docker', prefix: 'labelgate', image: 'nginx:alpine' });

    expect(out.text).toBe(
      [
        'docker run -d \\',
        '  --label labelgate.tunnel.web.hostname=app.example.com \\',
        '  --label labelgate.tunnel.web.service=http://webapp:80 \\',
        '  nginx:alpine',
      ].join('\n'),
    );
  });

  test('falls back to a placeholder image', () => {
    const out = formatLabels(tunnel, { format: 'docker', prefix: 'labelgate' });

    expect(out.text.split('\n').at(-1)).toBe('  your-image');
  });

  test.each([
    ['v=spf1 include:_spf.google.com ~all', "'v=spf1 include:_spf.google.com ~all'"],
    ["it's", "'it'\\''s'"],
    ['', "''"],
    ['\\.(jpg|png)$', "'\\.(jpg|png)$'"],
    ['alice@example.com,bob@example.com', 'alice@example.com,bob@example.com'],
  ])('shell-quotes %j as %s', (value, quoted) => {
    const out = formatLabels([{ key: 'dns.spf.target', value }], { format: 'docker', prefix: 'labelgate' });

    expect(out.text.split('\n')[1]).toBe(`  --label labelgate.dns.spf.target=${quoted} \\`);
  });

  test('moves comments above the command', () => {
    const out = formatLabels([...tunnel, { comment: '# note' }], { format: 'docker', prefix: 'labelgate' });

    expect(out.text.split('\n')[0]).toBe('# note');
  });
});

describe('formatLabels tokens', () => {
  test('splits each key into prefix, type, service and property tokens', () => {
    const out = formatLabels([{ key: 'tunnel.web.origin.no_tls_verify', value: 'true' }], {
      format: 'map',
      prefix: 'labelgate',
    });

    const keyTokens = out.lines[1].filter((t) => ['prefix', 'type', 'service', 'property'].includes(t.kind));
    expect(keyTokens).toEqual([
      { kind: 'prefix', text: 'labelgate.' },
      { kind: 'type', text: 'tunnel', labelType: 'tunnel' },
      { kind: 'service', text: 'web' },
      { kind: 'property', text: 'origin.no_tls_verify' },
    ]);
  });

  test('joins tokens back into exactly the copied text', () => {
    const out = formatLabels(tunnel, { format: 'list', prefix: 'labelgate' });

    expect(out.lines.map((line) => line.map((t) => t.text).join('')).join('\n')).toBe(out.text);
  });
});
