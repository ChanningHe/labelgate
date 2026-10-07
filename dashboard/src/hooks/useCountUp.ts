import { useEffect, useRef, useState } from 'react';

const DEFAULT_DURATION_MS = 650;
const REDUCED_MOTION_QUERY = '(prefers-reduced-motion: reduce)';

const prefersReducedMotion = () =>
  typeof window !== 'undefined' && window.matchMedia(REDUCED_MOTION_QUERY).matches;

const easeOutCubic = (p: number) => 1 - (1 - p) ** 3;

/**
 * Animates a displayed number toward target. Starts from 0 on mount and from
 * the last shown value when target changes, so polling with unchanged data
 * never replays the animation.
 */
export function useCountUp(target: number, durationMs: number = DEFAULT_DURATION_MS): number {
  const [value, setValue] = useState(0);
  const shownRef = useRef(0);

  useEffect(() => {
    const from = shownRef.current;
    const duration = prefersReducedMotion() ? 0 : durationMs;
    const start = performance.now();
    let frame = 0;

    const step = (now: number) => {
      const progress = duration === 0 ? 1 : Math.min(1, (now - start) / duration);
      const next = Math.round(from + (target - from) * easeOutCubic(progress));
      shownRef.current = next;
      setValue(next);
      if (progress < 1) frame = requestAnimationFrame(step);
    };

    frame = requestAnimationFrame(step);
    return () => cancelAnimationFrame(frame);
  }, [target, durationMs]);

  return value;
}
