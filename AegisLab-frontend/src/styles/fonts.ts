/**
 * Self-hosted variable fonts (no external CDN, deploy-stable).
 *
 * @fontsource-variable ships woff2 with full weight axis. We import the
 * variable build only — far smaller than 6 fixed-weight files per family.
 *
 * Imported once at app boot via main.tsx.
 */

import '@fontsource-variable/geist';
import '@fontsource-variable/inter';
import '@fontsource-variable/jetbrains-mono';
