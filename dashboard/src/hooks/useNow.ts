import { useEffect, useState } from 'react';

const DEFAULT_TICK_MS = 1000;

/** Current time in ms, re-rendering the caller every tick for relative timestamps. */
export function useNow(intervalMs: number = DEFAULT_TICK_MS): number {
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);

  return now;
}
