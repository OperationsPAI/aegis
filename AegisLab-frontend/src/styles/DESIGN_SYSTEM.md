# AegisLab Design System — Rosetta

The canonical source of visual truth for the AegisLab frontend. Read this before writing new CSS or layout code.

## 1. Tokens

All visual values live in `theme.css` as CSS custom properties. **Never hardcode** hex, px, ms, or z-index in component CSS. If a token is missing, add it to `theme.css` first.

| Token family | Variables | Usage |
|-------------|-----------|-------|
| Surface | `--bg-page`, `--bg-panel`, `--bg-inverted`, `--bg-muted` | Page, panel, active, hover backgrounds |
| Ink | `--text-main`, `--text-muted`, `--text-on-inverted` | Text hierarchy |
| Accent | `--accent-warning` | **Anomalies only** — failures, breaches, alarms. Never decorative. |
| Border | `--border-hairline`, `--border-strong` | Dividers, panel edges |
| Spacing | `--space-1` … `--space-12` | 4 px scale |
| Type | `--font-brand`, `--font-ui`, `--font-mono` | Geist / Inter / JetBrains Mono |
| Motion | `--motion-fast`, `--motion-base` | 200 ms / 250 ms transitions |
| Layout | `--header-height`, `--sidebar-width-*` | Shell dimensions |
| Z-index | `--z-dropdown` … `--z-tooltip` | Stacking order |

## 2. Activation Model

Active/selected state is expressed by **surface inversion**, never by accent color.

- Inactive: `--bg-panel` + `--text-main`
- Active: `--bg-inverted` + `--text-on-inverted`

## 3. Layout Architecture

### 3.1 Auto-layout first — no fixed sizing

**Prefer fluid, constraint-based layouts over absolute/fixed dimensions.**

- Use `flex` and `grid` for component-internal layout.
- Let containers grow/shrink via `flex: 1 1 auto`, `min-width: 0`, `min-height: 0`.
- Avoid `width: 100%` on flex items — it often fights the flex algorithm. Use `flex: 1 1 auto` instead.
- Avoid `height: 100%` — use `min-height: 100vh` on the shell and let flex grow fill remaining space.
- Percentage-based widths are acceptable for macro columns (e.g., `grid-template-columns: 2fr 1fr`), but not for individual UI elements.

The **only** allowed fixed dimensions are the shell-level constants in `theme.css` (`--header-height`, `--sidebar-width-*`). Everything inside the content area must be auto-sized.

### 3.2 Shell: fixed sidebar + flex content

The canonical app shell has three layers:

```
app-shell (flex container, min-height: 100vh)
├── aside.app-sidebar  (position: fixed, left: 0, top: 0, width: 240px)
└── div.app-content    (flex: 1 1 auto, margin-left: 240px)
```

**Rules:**

1. **The sidebar is `position: fixed`. It does NOT participate in flex layout.** Flex space is allocated to `.app-content` only.
2. `.app-content` must declare `margin-left` equal to the sidebar's visual width. This is what creates the two-column illusion.
3. **No sibling element of the sidebar may sit in normal flow inside `app-shell`.** Any extra element (buttons, overlays, etc.) must either:
   - Be placed *inside* the sidebar, or
   - Have `position: fixed` / `position: absolute` so it also leaves the flex flow.
4. `.app-content` must declare `min-width: 0` and `overflow-x: hidden` to prevent flex items from blowing out horizontally.

### 3.3 Why rule #3 matters — the 32 px gap incident

A toggle button was rendered as a sibling of `<aside>` inside `app-shell`, but without `position: fixed`. It participated in flex layout and occupied 32 px of width. Meanwhile `.app-content` still had `margin-left: 240px` (matching the sidebar width). The content therefore started at `32 + 240 = 272 px`, leaving a 32 px grey strip between the sidebar edge (240 px) and the content start (272 px). The strip showed `var(--bg-page)` because nothing covered it.

**Fix:** remove the stray toggle button from flex flow; the sidebar already contains one.

### 3.4 Borders and visual continuity

The 1 px hairline separating sidebar from content lives on `.app-content` (`border-left`), not on `.app-sidebar` (`border-right`). This ensures the border lines up with the sticky header's bottom border, creating a continuous corner even during scroll.

## 4. Component Rules

- One component per file in `src/components/ui/`.
- Each ships with a `.css` file using only tokens.
- Presentational only — no API calls, no state stores.
- Every new primitive must have a **Specimen** in the gallery (`src/App.tsx`).

## 5. State Mapping

AegisLab has many discrete states across projects, executions, tasks, and injections. **Never invent per-page color schemes.** Use this canonical mapping.

| State family | Values | Dot | Chip tone | Pulse? | Notes |
|---|---|---|---|---|---|
| **Active / Running** | `active`, `running`, `injecting`, `collecting`, `analyzing` | `ink` | `ink` | Yes | Work in progress |
| **Succeeded / Complete** | `succeeded`, `completed` | `ink` | `default` | No | Terminal state, no celebration green |
| **Failed / Error** | `failed`, `error`, `breach`, `alarm` | `warning` | `warning` | No | Anomaly red — reserved for these only |
| **Pending / Draft** | `pending`, `draft`, `queued` | `muted` | `ghost` | No | Not yet started |
| **Archived / Cancelled** | `archived`, `cancelled`, `stopped` | `muted` | `ghost` | No | Terminal, inactive |
| **Unknown** | anything else | `muted` | `default` | No | Fallback |

**Rule:** `StatusDot` only carries the semantic colour. `Chip` carries the text label. When both appear together (e.g. in a table cell), the Chip should include the dot as its `leading` prop.

## 6. Log Levels

Used by `Terminal` and any inline log streams.

| Level | CSS class | Token colour | Usage |
|---|---|---|---|
| `debug` | `.aegis-terminal__prefix--debug` | `--text-muted` | Verbose diagnostics |
| `info` | `.aegis-terminal__prefix--info` | `--text-main` | Normal operation |
| `warn` | `.aegis-terminal__prefix--warn` | `--ink-warn` | Caution, not an anomaly |
| `error` | `.aegis-terminal__prefix--error` | `--accent-warning` | Failures and anomalies |

## 7. Time Display

All timestamps in the UI follow these rules:

- **Absolute:** `YYYY-MM-DD HH:mm:ss` (24 h, zero-padded). Omit date if the event is on the current calendar day.
- **Relative:** use `TimeDisplay` component. Shows "2 m ago", "1 h ago", "yesterday". Hover tooltip reveals full absolute timestamp.
- **Duration:**
  - `< 1 000 ms` → show as `840 ms`
  - `≥ 1 000 ms` → show as `2.84 s` (two decimal places, space before unit)
  - `≥ 60 s` → show as `1 m 23 s`
  - `≥ 60 m` → show as `1 h 12 m`
- **Time zone:** always show in local time; append `(UTC+8)` etc. only in detailed views (tooltip or expanded panel).

## 8. Data Tables

Tables are the primary interaction surface for list views (Projects, Injections, Executions, Tasks, Traces). Follow `DataTable` primitive conventions:

- **Alignment:** text left, numbers / status / time right-aligned or centred.
- **Row height:** fixed at `var(--space-10)` (40 px). No multi-line cells — truncate with ellipsis.
- **Hover:** `background: var(--bg-muted)`.
- **Selection:** surface inversion (`--bg-inverted` + `--text-on-inverted`) on the active row.
- **Empty state:** always render `EmptyState` inside the table body when row count is zero.
- **Loading:** render `DataTableSkeleton` (shimmer rows) instead of a spinner.
- **Borders:** only horizontal hairlines between rows (`border-bottom`). No vertical borders.

## 9. Code / JSON Display

Structured data (trace spans, injection configs, metric queries) is shown with `CodeBlock`:

- Font: `var(--font-mono)`, `var(--fs-11)`.
- Background: `var(--bg-page)`.
- Border: `var(--border-hairline)`.
- Copy button appears on hover in the top-right corner.
- JSON is pretty-printed with 2-space indent.
- Line numbers are optional (off by default for short snippets, on for >10 lines).

## 10. Search & Filter Toolbar

List pages use `Toolbar` above the table:

- **Search input:** left-aligned, width `280 px` (max), placeholder in sentence case.
- **Filter chips:** immediately to the right of search, horizontally scrollable on narrow viewports.
- **Primary action:** right-aligned (e.g. "New Project"). Uses a `Button` primitive (if available) or a styled `<button>`.
- **Clear all:** appears only when at least one filter is active, placed between filters and primary action.

## 11. Responsive

Sidebar collapses to 64 px below 768 px. Header hides user name. Content margin adjusts accordingly. Primitives must not overflow at ≤ 420 px.
