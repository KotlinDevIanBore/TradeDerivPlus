/**
 * The chart's colours, mirrored from the Tailwind theme in tailwind.config.js.
 *
 * Recharts styles its SVG through props, not classes, so these values cannot be
 * expressed as utilities. The tokens are not exposed as CSS custom properties,
 * so this module is the one place the hexes are repeated: change a colour here
 * and in tailwind.config.js together.
 */
export const chartTheme = {
  /** `profit` — a window that closed higher than it opened. */
  up: '#22C55E',
  /** `loss` / `accent` — a window that closed lower. */
  down: '#E24B4A',
  /** `elevated` — tooltip surface. */
  tooltipBg: '#1A1A1D',
  /** `border` — tooltip border, crosshair cursor, the current-price line. */
  border: '#232326',
  /** `muted` — axis labels. */
  axis: '#8A8A93',
} as const
