import type { OverviewData } from '../api/client';
import { syncProblems } from './syncStatus';
import classes from './SyncProblems.module.css';

/** Lists every problem of the latest sync, including those (such as invalid
 * labels) that created no resource and so are missing from the resource list. */
export function SyncProblems({ sync }: { sync: OverviewData['sync'] }) {
  const { items, more } = syncProblems(sync);
  if (items.length === 0) return null;

  return (
    <section className="lg-panel" aria-label="Sync problems">
      <div className="lg-panel-head">
        <h2>Sync problems</h2>
        <span className={classes.badge}>{items.length + more}</span>
      </div>
      <ul className={classes.list}>
        {items.map((p, i) => (
          <li key={`${i}-${p.subject}`} className={classes.item}>
            <span className={classes.subject}>{p.subject}</span>
            <p className={classes.text}>{p.text}</p>
          </li>
        ))}
      </ul>
      {more > 0 && <div className={classes.more}>+{more} more in the labelgate log</div>}
    </section>
  );
}
