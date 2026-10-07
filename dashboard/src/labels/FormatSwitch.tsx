import { SegmentedControl } from '@mantine/core';
import { LABEL_FORMATS, type LabelFormat } from './format';
import { setLabelFormat, useLabelFormat } from './useLabelFormat';

export function FormatSwitch() {
  const format = useLabelFormat();

  return (
    <SegmentedControl
      size="xs"
      aria-label="Label output format"
      value={format}
      onChange={(value) => setLabelFormat(value as LabelFormat)}
      data={LABEL_FORMATS.map((f) => ({ value: f.value, label: f.label }))}
    />
  );
}
