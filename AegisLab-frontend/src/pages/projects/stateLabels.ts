export const injectionStateMap: Record<
  number,
  { label: string; color: string }
> = {
  0: { label: 'Initial', color: 'default' },
  1: { label: 'Inject Failed', color: 'red' },
  2: { label: 'Inject Success', color: 'blue' },
  3: { label: 'Build Failed', color: 'red' },
  4: { label: 'Build Success', color: 'green' },
  5: { label: 'Detector Failed', color: 'red' },
  6: { label: 'Detector Success', color: 'green' },
};

export const executionStateMap: Record<
  number,
  { label: string; color: string }
> = {
  0: { label: 'Initial', color: 'default' },
  1: { label: 'Failed', color: 'red' },
  2: { label: 'Success', color: 'green' },
};
