import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, test } from 'vitest';
import type { OverviewData } from '../api/client';
import { SyncProblems } from './SyncProblems';

type Sync = OverviewData['sync'];

const failed = (patch: Partial<Sync>): Sync => ({ last_sync: '', status: 'error', error: '2 problems', ...patch });

describe('SyncProblems', () => {
  test('lists each subject with its message', () => {
    const html = renderToStaticMarkup(
      <SyncProblems
        sync={failed({
          problems: [
            { subject: 'local container bad label labelgate.dns.web.hostname', text: 'label hostname: is required' },
            { subject: 'local container bad label labelgate.dns.web.type', text: 'label type: unknown type <AAAA>' },
          ],
          problems_total: 2,
        })}
      />,
    );
    expect(html).toContain('Sync problems');
    expect(html).toContain('local container bad label labelgate.dns.web.hostname');
    expect(html).toContain('label hostname: is required');
    expect(html).toContain('local container bad label labelgate.dns.web.type');
    expect(html).toContain('unknown type &lt;AAAA&gt;');
    expect(html).not.toContain('more');
  });

  test('shows how many problems are not listed', () => {
    const html = renderToStaticMarkup(
      <SyncProblems sync={failed({ problems: [{ subject: 's', text: 't' }], problems_total: 251 })} />,
    );
    expect(html).toContain('+250 more');
    expect(html).toContain('labelgate log');
    expect(html).toContain('>251<');
  });

  test('renders nothing without problems', () => {
    expect(renderToStaticMarkup(<SyncProblems sync={failed({})} />)).toBe('');
    expect(
      renderToStaticMarkup(<SyncProblems sync={failed({ status: 'success', problems: [], problems_total: 0 })} />),
    ).toBe('');
  });
});
