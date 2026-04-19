/**
 * Color utilities for assigning consistent colors to injections/execuions
 */

// 50 vibrant, distinguishable colors
const COLOR_PALETTE = [
  '#3b82f6', // blue
  '#ef4444', // red
  '#10b981', // green
  '#f59e0b', // amber
  '#8b5cf6', // purple
  '#ec4899', // pink
  '#06b6d4', // cyan
  '#f97316', // orange
  '#84cc16', // lime
  '#6366f1', // indigo
  '#14b8a6', // teal
  '#f43f5e', // rose
  '#a855f7', // violet
  '#eab308', // yellow
  '#22d3ee', // cyan-400
  '#fb923c', // orange-400
  '#4ade80', // green-400
  '#c084fc', // purple-400
  '#fb7185', // rose-400
  '#fbbf24', // amber-400
  '#60a5fa', // blue-400
  '#f87171', // red-400
  '#34d399', // emerald-400
  '#facc15', // yellow-400
  '#a78bfa', // violet-400
  '#f472b6', // pink-400
  '#2dd4bf', // teal-400
  '#fdba74', // orange-300
  '#bef264', // lime-400
  '#818cf8', // indigo-400
  '#5eead4', // teal-300
  '#fda4af', // rose-300
  '#c4b5fd', // violet-300
  '#fde047', // yellow-300
  '#67e8f9', // cyan-300
  '#fed7aa', // orange-200
  '#86efac', // green-300
  '#ddd6fe', // violet-200
  '#fbbf24', // amber-400
  '#fcd34d', // amber-300
  '#93c5fd', // blue-300
  '#fca5a5', // red-300
  '#6ee7b7', // emerald-300
  '#fde68a', // yellow-200
  '#d8b4fe', // purple-300
  '#f9a8d4', // pink-300
  '#a5f3fc', // cyan-200
  '#fdba74', // orange-300
  '#d9f99d', // lime-300
  '#c7d2fe', // indigo-300
];

/**
 * Assign a consistent color to an item based on its ID
 * Uses a fixed color palette and cycles through it
 *
 * @param id - The item ID
 * @returns A hex color string
 */
export const getColor = (id: number): string => {
  const index = id % COLOR_PALETTE.length;
  return COLOR_PALETTE[index];
};

/**
 * Get a lighter version of the item color (for backgrounds)
 *
 * @param id - The item ID
 * @param opacity - Opacity value (0-1)
 * @returns A rgba color string
 */
export const getColorLight = (id: number, opacity: number = 0.1): string => {
  const color = getColor(id);
  // Convert hex to rgba
  const r = parseInt(color.slice(1, 3), 16);
  const g = parseInt(color.slice(3, 5), 16);
  const b = parseInt(color.slice(5, 7), 16);
  return `rgba(${r}, ${g}, ${b}, ${opacity})`;
};

/**
 * Generate a random color from the palette
 * Useful for new items that don't have an ID yet
 */
export const getRandomColor = (): string => {
  const index = Math.floor(Math.random() * COLOR_PALETTE.length);
  return COLOR_PALETTE[index];
};

/**
 * Get all available colors in the palette
 */
export const getAllColors = (): string[] => {
  return [...COLOR_PALETTE];
};

/**
 * Convert hex color to RGB object
 */
export const hexToRgb = (
  hex: string
): { r: number; g: number; b: number } | null => {
  const result = /^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(hex);
  return result
    ? {
        r: parseInt(result[1], 16),
        g: parseInt(result[2], 16),
        b: parseInt(result[3], 16),
      }
    : null;
};

/**
 * Check if a color is light or dark (for determining text color)
 */
export const isLightColor = (hex: string): boolean => {
  const rgb = hexToRgb(hex);
  if (!rgb) return false;
  // Using relative luminance formula
  const luminance = (0.299 * rgb.r + 0.587 * rgb.g + 0.114 * rgb.b) / 255;
  return luminance > 0.5;
};
