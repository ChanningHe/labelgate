// Cloudflare tunnel paths are regular expressions. Only a path made of plain
// characters is a literal prefix we can safely link to.
const REGEX_METACHARS = /[\\^$.|?*+()[\]{}]/;
const UNSAFE_URL_CHARS = /[\s#]/;

/**
 * Returns the URL to open for a hostname, or null when none exists
 * (wildcard hostnames have no single address). A tunnel path is appended only
 * when it is an absolute plain prefix; otherwise the hostname root is used.
 */
export function openUrl(hostname: string, path?: string): string | null {
  if (hostname.includes('*')) return null;
  const base = `https://${hostname}`;
  if (!path || !path.startsWith('/') || REGEX_METACHARS.test(path) || UNSAFE_URL_CHARS.test(path)) return base;
  return base + path;
}
