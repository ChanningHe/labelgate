import { useSyncExternalStore } from 'react';
import { LABEL_FORMATS, type LabelFormat } from './format';

// The chosen output format is shared by every code block on the page and
// remembered per browser. Storage failures only lose the preference.

const STORAGE_KEY = 'labelgate.labelFormat';
const DEFAULT_FORMAT: LabelFormat = 'map';
const listeners = new Set<() => void>();

const isFormat = (value: unknown): value is LabelFormat => LABEL_FORMATS.some((f) => f.value === value);

function readStored(): LabelFormat {
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY);
    return isFormat(stored) ? stored : DEFAULT_FORMAT;
  } catch {
    return DEFAULT_FORMAT;
  }
}

let current: LabelFormat = typeof window === 'undefined' ? DEFAULT_FORMAT : readStored();

export function setLabelFormat(format: LabelFormat) {
  current = format;
  try {
    window.localStorage.setItem(STORAGE_KEY, format);
  } catch {
    // Preference stays in memory for this page load.
  }
  listeners.forEach((l) => l());
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function useLabelFormat(): LabelFormat {
  return useSyncExternalStore(subscribe, () => current, () => DEFAULT_FORMAT);
}
