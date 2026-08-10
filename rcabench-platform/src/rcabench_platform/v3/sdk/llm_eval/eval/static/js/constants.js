const { useState, useEffect, useRef, useCallback, useMemo } = React;

// Design tokens
const C = {
  bg:     '#05080a',
  panel:  'rgba(5, 8, 10, 0.85)',
  line:   '#2a3640',
  text:   '#c0cdd6',
  muted:  '#5a6b7c',
  orange: '#ff6b35',
  teal:   '#4db4b9',
  green:  '#22c55e',
  red:    '#ef4444',
  yellow: '#f59e0b',
  purple: '#a855f7',
};

const STATUS_COLORS = {
  pending: C.muted,
  running: C.teal,
  completed: C.green,
  failed: C.red,
  skipped: C.yellow,
};

const AGENT_COLORS = [C.teal, C.purple, C.orange, C.yellow, C.green, C.red];
