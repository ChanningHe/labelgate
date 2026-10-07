import type { CSSProperties } from 'react';
import { Chip } from '../components/Badges';
import { RESERVED_SERVICE_NAMES, SERVICE_NAME_PATTERN } from './catalog';
import { CodeBlock } from './CodeBlock';
import classes from './LabelsPage.module.css';

const conflict = [
  { key: 'dns.web.hostname', value: 'app.example.com' },
  { key: 'tunnel.web.hostname', value: 'app.example.com' },
];
const separate = [
  { key: 'dns.legacy.hostname', value: 'old.example.com' },
  { key: 'tunnel.web.hostname', value: 'app.example.com' },
];

const Verdict = ({ label, tone }: { label: string; tone: string }) => (
  <span className={classes.verdict} style={{ '--tone': tone } as CSSProperties}>
    {label}
  </span>
);

export function LabelRules() {
  return (
    <div className={classes.rules}>
      <section className={`lg-panel ${classes.rule}`}>
        <h3>Service names</h3>
        <p>
          The segment after the type names the service. Use lowercase letters, digits and hyphens, starting and ending
          with a letter or digit.
        </p>
        <pre className={classes.pattern}>{SERVICE_NAME_PATTERN.source}</pre>
        <p>These words are reserved and rejected as service names:</p>
        <div className={classes.chips}>
          {RESERVED_SERVICE_NAMES.map((word) => (
            <Chip key={word}>{word}</Chip>
          ))}
        </div>
      </section>

      <section className={`lg-panel ${classes.rule}`}>
        <h3>One hostname, one owner</h3>
        <p>A hostname can be a DNS record or a tunnel route, not both. The tunnel already creates its own CNAME.</p>
        <Verdict label="Conflict" tone="var(--lg-err)" />
        <CodeBlock lines={conflict} format="map" variant="bad" />
        <Verdict label="Works" tone="var(--lg-ok)" />
        <CodeBlock lines={separate} format="map" variant="good" />
      </section>

      <section className={`lg-panel ${classes.rule}`}>
        <h3>First container wins</h3>
        <p>
          When two containers claim the same hostname, the one registered first keeps it. The other is skipped and its
          resource shows the status Error.
        </p>
      </section>

      <section className={`lg-panel ${classes.rule}`}>
        <h3>Cleanup</h3>
        <p>
          cleanup: "true" deletes the resource once its container has been gone for sync.remove_delay. With cleanup off,
          the resource is marked Orphaned and kept on Cloudflare. Access apps follow the cleanup setting of the service
          that references them.
        </p>
      </section>
    </div>
  );
}
