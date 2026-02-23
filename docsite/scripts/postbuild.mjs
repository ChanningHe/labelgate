/**
 * Post-build script for static export with i18n.
 *
 * fumadocs uses hideLocale: 'default-locale', so English links point to
 * /docs/... (no /en prefix). But Next.js static export generates all pages
 * under /en/... This script promotes English content to the root so that
 * URLs match what the HTML links expect.
 *
 * Before: out/en.html, out/en/docs/...
 * After:  out/index.html, out/docs/..., out/zh/...
 */

import { cpSync, renameSync, existsSync } from 'node:fs';
import { join } from 'node:path';

const out = join(import.meta.dirname, '..', 'out');

// Copy en/ subtree into root (merging with existing files like _next/, zh/, etc.)
const enDir = join(out, 'en');
if (existsSync(enDir)) {
  cpSync(enDir, out, { recursive: true });
}

// Promote en.html → index.html (the English home page)
const enHtml = join(out, 'en.html');
if (existsSync(enHtml)) {
  renameSync(enHtml, join(out, 'index.html'));
}

// Promote en.txt → index.txt (RSC payload for client-side navigation)
const enTxt = join(out, 'en.txt');
if (existsSync(enTxt)) {
  renameSync(enTxt, join(out, 'index.txt'));
}

console.log('postbuild: promoted en/ content to root');
