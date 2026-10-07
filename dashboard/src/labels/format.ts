// Renders container labels as copy-ready text in three formats, plus tokens
// for syntax highlighting. Keys are prefix-less ("tunnel.web.hostname"); the
// server's label_prefix is added at render time.

export type LabelType = 'dns' | 'tunnel' | 'access';
export type LabelFormat = 'map' | 'list' | 'docker';

export type LabelLine = { key: string; value: string } | { comment: string };

export type TokenKind =
  | 'plain'
  | 'punct'
  | 'prefix'
  | 'type'
  | 'service'
  | 'property'
  | 'string'
  | 'comment'
  | 'command';

export interface Token {
  kind: TokenKind;
  text: string;
  labelType?: LabelType;
}

export interface FormattedLabels {
  lines: Token[][];
  text: string;
  labelCount: number;
}

export interface FormatOptions {
  format: LabelFormat;
  prefix: string;
  image?: string;
}

export const LABEL_FORMATS: readonly { value: LabelFormat; label: string }[] = [
  { value: 'map', label: 'Compose map' },
  { value: 'list', label: 'Compose list' },
  { value: 'docker', label: 'docker run' },
];

const INDENT = '  ';
const PLACEHOLDER_IMAGE = 'your-image';
const SHELL_SAFE = /^[\w@%+=:,./-]+$/;

const tok = (kind: TokenKind, text: string): Token => ({ kind, text });

function escapeYamlDoubleQuoted(value: string): string {
  return value.replace(/\\/g, '\\\\').replace(/"/g, '\\"');
}

function quoteShell(value: string): string {
  return SHELL_SAFE.test(value) ? value : `'${value.replace(/'/g, `'\\''`)}'`;
}

function keyTokens(key: string, prefix: string): Token[] {
  const [type, service, ...property] = key.split('.');
  return [
    tok('prefix', `${prefix}.`),
    { kind: 'type', text: type, labelType: type as LabelType },
    tok('punct', '.'),
    tok('service', service),
    tok('punct', '.'),
    tok('property', property.join('.')),
  ];
}

function composeLine(line: LabelLine, opts: FormatOptions): Token[] {
  if ('comment' in line) return [tok('plain', INDENT), tok('comment', line.comment)];
  const value = escapeYamlDoubleQuoted(line.value);
  const key = keyTokens(line.key, opts.prefix);
  if (opts.format === 'list') {
    return [tok('plain', INDENT), tok('punct', '- "'), ...key, tok('punct', '='), tok('string', value), tok('punct', '"')];
  }
  return [tok('plain', INDENT), ...key, tok('punct', ': '), tok('string', `"${value}"`)];
}

function dockerLines(lines: LabelLine[], opts: FormatOptions): Token[][] {
  const comments = lines.flatMap((l) => ('comment' in l ? [[tok('comment', l.comment)]] : []));
  const flags = lines.flatMap((l) =>
    'key' in l
      ? [[
          tok('plain', INDENT),
          tok('punct', '--label '),
          ...keyTokens(l.key, opts.prefix),
          tok('punct', '='),
          tok('string', quoteShell(l.value)),
          tok('punct', ' \\'),
        ]]
      : [],
  );
  return [
    ...comments,
    [tok('command', 'docker run'), tok('plain', ' -d '), tok('punct', '\\')],
    ...flags,
    [tok('plain', `${INDENT}${opts.image || PLACEHOLDER_IMAGE}`)],
  ];
}

export function formatLabels(lines: LabelLine[], opts: FormatOptions): FormattedLabels {
  const rendered =
    opts.format === 'docker'
      ? dockerLines(lines, opts)
      : [[tok('punct', 'labels:')], ...lines.map((l) => composeLine(l, opts))];

  return {
    lines: rendered,
    text: rendered.map((line) => line.map((t) => t.text).join('')).join('\n'),
    labelCount: lines.filter((l) => 'key' in l).length,
  };
}
