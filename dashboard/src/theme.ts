import { createTheme, type CSSVariablesResolver, type MantineColorsTuple } from '@mantine/core';

// Cloudflare orange. Light scheme uses shade 7, dark scheme shade 6 (see primaryShade).
const brand: MantineColorsTuple = [
  '#fff4e6', '#ffe4c4', '#ffc98e', '#ffab57', '#fb942e',
  '#f88a24', '#f6821f', '#e0690a', '#c55c06', '#a84d03',
];

// Hue-free zinc scales, matching src/styles/tokens.css.
const dark: MantineColorsTuple = [
  '#ededed', '#c2c2c2', '#8a8a8a', '#5a5a5a', '#343434',
  '#262626', '#1f1f1f', '#121212', '#0e0e0e', '#0a0a0a',
];
const gray: MantineColorsTuple = [
  '#fafafa', '#f2f2f2', '#e5e5e5', '#d4d4d4', '#a3a3a3',
  '#8a8a8a', '#6b6b6b', '#4a4a4a', '#2e2e2e', '#171717',
];

export const theme = createTheme({
  colors: { brand, dark, gray },
  primaryColor: 'brand',
  primaryShade: { light: 7, dark: 6 },
  fontFamily: 'var(--lg-font-ui)',
  fontFamilyMonospace: 'var(--lg-font-mono)',
  headings: { fontFamily: 'var(--lg-font-ui)', fontWeight: '700' },
  defaultRadius: 'md',
  radius: { sm: '6px', md: '10px' },
  cursorType: 'pointer',
});

// Point Mantine's surface variables at our tokens so Mantine components
// (inputs, drawers, tooltips) sit on the same palette as custom components.
export const cssVariablesResolver: CSSVariablesResolver = () => {
  const shared = {
    '--mantine-color-body': 'var(--lg-bg)',
    '--mantine-color-text': 'var(--lg-fg)',
    '--mantine-color-dimmed': 'var(--lg-muted)',
    '--mantine-color-default': 'var(--lg-raised)',
    '--mantine-color-default-hover': 'var(--lg-hover)',
    '--mantine-color-default-color': 'var(--lg-fg)',
    '--mantine-color-default-border': 'var(--lg-border-strong)',
    '--mantine-color-placeholder': 'var(--lg-faint)',
  };
  return { variables: {}, light: shared, dark: shared };
};
