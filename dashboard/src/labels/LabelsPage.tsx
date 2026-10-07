import type { CSSProperties } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Tabs } from '@mantine/core';
import { IconTag } from '@tabler/icons-react';
import { Chip } from '../components/Badges';
import { PageHeader } from '../components/PageHeader';
import { useLabelPrefix } from '../hooks/useAPI';
import { KIND_META } from '../resources/meta';
import { LABEL_PROPERTIES, PROPERTY_VALUES, recipesFor } from './catalog';
import { FormatSwitch } from './FormatSwitch';
import type { LabelType } from './format';
import { LabelRules } from './LabelRules';
import { RecipeCard } from './RecipeCard';
import classes from './LabelsPage.module.css';

type LabelsTab = LabelType | 'rules';

const TABS: readonly { value: LabelsTab; label: string }[] = [
  { value: 'dns', label: 'DNS' },
  { value: 'tunnel', label: 'Tunnel' },
  { value: 'access', label: 'Access' },
  { value: 'rules', label: 'Rules' },
];
const DEFAULT_TAB: LabelsTab = 'tunnel';
const TAB_PARAM = 'tab';

const isTab = (value: string | null): value is LabelsTab => TABS.some((t) => t.value === value);

function Segment({ code, caption, tone }: { code: string; caption?: string; tone?: string }) {
  return (
    <div className={classes.segment} style={tone ? ({ '--tone': tone } as CSSProperties) : undefined}>
      <code>{code}</code>
      {caption && <small>{caption}</small>}
    </div>
  );
}

const Separator = ({ char }: { char: string }) => (
  <div className={`${classes.segment} ${classes.separator}`} aria-hidden="true">
    <code>{char}</code>
  </div>
);

// prefix.type.service.property=value, each part colored like the code blocks.
function Anatomy({ prefix }: { prefix: string }) {
  return (
    <div className={`lg-panel ${classes.anatomy}`} aria-label="Label format">
      <Segment code={prefix} caption="Prefix, from label_prefix" tone="var(--lg-muted)" />
      <Separator char="." />
      <Segment code="tunnel" caption="dns, tunnel or access" tone="var(--lg-tunnel)" />
      <Separator char="." />
      <Segment code="web" caption="Service name you pick" tone="var(--lg-fg)" />
      <Separator char="." />
      <Segment code="hostname" caption="Property" tone="var(--lg-t-key)" />
      <Separator char="=" />
      <Segment code='"app.example.com"' caption="Value, always a string" tone="var(--lg-t-str)" />
    </div>
  );
}

function PropertyReference({ kind, prefix }: { kind: LabelType; prefix: string }) {
  const values = PROPERTY_VALUES[kind];
  return (
    <aside className={`lg-panel ${classes.props}`} style={{ '--kind': KIND_META[kind].color } as CSSProperties}>
      <div className="lg-panel-head">
        <span className={classes.propsTitle}>{`${prefix}.${kind}.<name>.*`}</span>
      </div>
      <table className={classes.propsTable}>
        <tbody>
          {LABEL_PROPERTIES[kind].map((p) => (
            <tr key={p.name}>
              <td>
                <span className={classes.propName}>{p.name}</span>
                {p.isRequired && <span className={classes.required}>REQUIRED</span>}
                {p.defaultValue && <div className={classes.default}>default {p.defaultValue}</div>}
              </td>
              <td className="lg-dim">{p.description}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <div className={classes.propsFoot}>
        <h4 className={classes.footTitle}>{values.title}</h4>
        <div className={classes.chips}>
          {values.values.map((v) => (
            <Chip key={v}>{v}</Chip>
          ))}
        </div>
      </div>
    </aside>
  );
}

function KindReference({ kind, prefix }: { kind: LabelType; prefix: string }) {
  return (
    <div className={classes.body}>
      <div className={classes.recipes}>
        {recipesFor(kind).map((recipe, i) => (
          <RecipeCard key={recipe.id} recipe={recipe} order={i} />
        ))}
      </div>
      <PropertyReference kind={kind} prefix={prefix} />
    </div>
  );
}

export function LabelsPage() {
  const prefix = useLabelPrefix();
  const [params, setParams] = useSearchParams();
  const requested = params.get(TAB_PARAM);
  const tab: LabelsTab = isTab(requested) ? requested : DEFAULT_TAB;

  return (
    <>
      <PageHeader
        title="Labels"
        description="Copy-ready labels for DNS, Tunnel and Access. Keys use this server's label prefix, so what you copy works as is."
        eyebrow={{ label: 'Reference', icon: IconTag, color: 'var(--lg-accent)' }}
      />
      <Anatomy prefix={prefix} />

      <Tabs value={tab} onChange={(v) => setParams(v ? { [TAB_PARAM]: v } : {}, { replace: true })}>
        <div className={classes.controls}>
          <Tabs.List>
            {TABS.map((t) => (
              <Tabs.Tab key={t.value} value={t.value}>
                {t.label}
              </Tabs.Tab>
            ))}
          </Tabs.List>
          {tab !== 'rules' && <FormatSwitch />}
        </div>

        {TABS.map((t) => (
          <Tabs.Panel key={t.value} value={t.value}>
            {t.value === 'rules' ? <LabelRules /> : <KindReference kind={t.value} prefix={prefix} />}
          </Tabs.Panel>
        ))}
      </Tabs>
    </>
  );
}
