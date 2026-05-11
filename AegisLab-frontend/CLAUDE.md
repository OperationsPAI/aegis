# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Frontend Overview

RCABench frontend is a React 18 + TypeScript application using Ant Design 5, built with Vite. It serves as the web interface for the AegisLab RCA benchmarking platform.

## Current Phase: Design Language First (priority)

The frontend is currently in **design-language-first** mode. Before building
any scenario UI (experiment-observation page, dashboards, etc.), the goal is
to land a small, opinionated set of primitives that everything composes from.

**Top-down hierarchy — maintain in this order, never skip or invert:**

1. **Design tokens** (color, type, spacing, motion, radius, shadow) in
   `src/styles/theme.css`. Tokens are the only source of visual truth.
2. **Layout system** — the page shell (`src/pages/pages.css`) defines the
   single root container (`.page-wrapper`) that every page must inherit.
   Page-level CSS must **not** redefine `max-width`, `margin`, or `padding`;
   those belong to the shared layout system only.
3. **Primitives** in `src/components/ui/` — one component per file, plain
   CSS, no business logic. These are the building blocks that pages compose
   from; never write bespoke one-off markup inside a page when a primitive
   already exists.
4. **Gallery** (`src/App.tsx` + `src/App.css`). The gallery **is** the live
   spec — if it isn't in the gallery, the team doesn't know it exists.
5. **Only after** steps 1–4 are solid, compose scenario pages from the
   existing primitives and layout system. Resist the urge to one-off bespoke
   UI inside a page.

### Layout system rules

- Every page root element must carry `className="page-wrapper"` (plus an
  optional page-specific class for internal content layout only).
- `src/pages/pages.css` owns `.page-wrapper`:
  - `max-width: 1200px`
  - `margin: 0 auto`
  - `padding: var(--space-6)` (mobile: `var(--space-4)`)
- Page-specific CSS files (e.g. `Dashboard.css`, `Gallery.css`) must **not**
  declare `max-width`, `margin`, or `padding` on their root class. They may
  only define internal layout (`display: flex`, `gap`, `grid`, etc.).
- Before adding a new page, check whether the existing layout and primitives
  already satisfy the design. If not, extend the layout system or add a
  primitive first — don't embed a custom layout inside a single page.

**Design conventions** (enforced):

- **No hardcoded values** in component CSS (no raw hex, px, ms). Reference a
  CSS custom property from `theme.css`. If a token is missing, add it there
  first, then use it.
- **Activation = surface inversion** (`--bg-inverted` background +
  `--text-on-inverted` text), never accent color.
- **Anomaly red** (`--accent-warning`, `#E11D48`) is reserved for *actual*
  anomalies — failures, breaches, alarms. Never decorative.
- **Type stack**: brand (Geist) for titles, UI (Inter) for body, data
  (JetBrains Mono) for numbers / IDs / parameters. Use the `--font-*` tokens.
- **Spacing**: use `--space-*` tokens (4 px scale). No raw padding/margin.
- **Components are presentational** — no API calls, no Zustand reads, no
  business state. Hosts pass props in. Story-tested in the gallery.
- **Every primitive ships with a Specimen** in the gallery. PRs that add a
  primitive without a gallery entry are incomplete.

**Where things live**:

| Path | Purpose |
|------|---------|
| `src/styles/theme.css` | Design tokens (CSS custom properties + keyframes) |
| `src/styles/fonts.ts` | Font-asset imports (Geist / Inter / JetBrains Mono) |
| `src/components/ui/` | Primitives + their CSS, one component per file |
| `src/components/ui/index.ts` | Primitive barrel — public API of the UI kit |
| `src/App.tsx` + `src/App.css` | Gallery — the live spec & visual review surface |
| `src/theme/antdTheme.ts` | Ant Design `ConfigProvider` mapped to our tokens |

**Roadmap**: the "Roadmap · planned components" panel at the bottom of the
gallery enumerates every planned wrapper / composition (TabbedWorkbench,
TraceWaterfall, MetricChart, …) with its reference implementation library.
Build top-down from that list — *don't* invent new components that aren't on
it without first adding them there.

**Validation gates** for design-system PRs:

```bash
pnpm type-check
pnpm lint            # --max-warnings 0
pnpm build
pnpm dev             # eyeball the gallery in a browser
```

A primitive is "done" only after the gallery renders cleanly in the browser
at desktop AND ≤768 px width.

## Environment Setup

1. **Install Nix** (devbox 的前置依赖):

   ```bash
   curl --proto '=https' --tlsv1.2 -sSf -L https://install.determinate.systems/nix | sh -s -- install
   ```

2. **Install devbox**:

   ```bash
   curl -fsSL https://get.jetify.com/devbox | bash
   ```

3. **安装 devbox 包** (提供 pnpm 8):

   ```bash
   devbox install
   ```

4. **激活 devbox 环境** (每次新开终端都需要):

   ```bash
   eval "$(devbox shellenv)"
   ```

5. **安装前端依赖**:
   ```bash
   NPM_TOKEN=<your_github_token> pnpm install
   ```

> **重要**: 项目依赖私有包 `@OperationsPAI/portal`（托管在 GitHub Packages）。安装时必须设置 `NPM_TOKEN` 环境变量为有 `read:packages` 权限的 GitHub Personal Access Token。Token 配置在 `.npmrc` 中通过 `${NPM_TOKEN}` 引用。

## Essential Commands

```bash
# Development
pnpm dev             # Start dev server on http://localhost:3000

# Code Quality
pnpm lint            # Run ESLint checks
pnpm lint:fix        # Auto-fix ESLint issues
pnpm format          # Format code with Prettier
pnpm type-check      # Run TypeScript type checking

# Build
pnpm build           # Build for production (vite build)
pnpm preview         # Preview production build
```

## Architecture

### Technology Stack

- **Framework**: React 18.3.1 with TypeScript (strict mode)
- **Build Tool**: Vite 5 with React plugin
- **UI Library**: Ant Design 5.x with custom theme
- **State Management**: Zustand (client state) + TanStack Query (server state)
- **HTTP Client**: Axios with interceptors for auth
- **Routing**: React Router v6
- **Charts**: ECharts, D3.js, Cytoscape.js
- **Code Editor**: Monaco Editor

### Project Structure

```
src/
├── api/           # API clients (modular by domain)
├── components/    # Reusable components
│   ├── charts/    # Chart components
│   ├── dashboard/ # Dashboard-specific components
│   ├── layout/    # Layout components (MainLayout)
│   └── ui/        # Base UI components
├── hooks/         # Custom React hooks
├── pages/         # Page components (route-based)
├── store/         # Zustand stores (auth, theme)
├── types/         # TypeScript type definitions
├── utils/         # Utility functions
└── styles/        # Global styles and CSS variables
```

### Key Patterns

1. **API Integration**: All API calls go through `/api/v2` (proxied to backend)
2. **Authentication**: JWT-based with automatic token refresh
3. **State Management**:
   - Server state via TanStack Query (caching, refetching)
   - Client state via Zustand (auth, theme)
4. **Error Handling**: Centralized in Axios interceptors with Ant Design message notifications
5. **Type Safety**: Strict TypeScript with comprehensive type definitions matching backend

## Development Guidelines

### API Integration (CRITICAL)

- **NEVER modify backend field names** - Use exact field names from API
- **Backend uses snake_case** - Keep it in frontend types
- **All API types in `src/types/api.ts`** - Must match backend exactly
- **Use provided API clients** in `src/api/` directory
- **Handle errors consistently** - 401 triggers auto-refresh, others show message

### Component Development

- **Functional components only** with hooks
- **Use Ant Design components** as base building blocks
- **Follow existing patterns** in similar components
- **Extract reusable logic** into custom hooks
- **Keep components focused** - one component per file
- **Primitives go in `src/components/ui/`** — one `.tsx` + one `.css` per
  primitive, exported through `src/components/ui/index.ts`
- **New primitive ⇒ new gallery Specimen** (`src/App.tsx`). The gallery is
  the spec; an unshown primitive is invisible to the team.

### State Management

- **Server state**: Use TanStack Query for API data
  ```typescript
  const { data, isLoading, error } = useQuery({
    queryKey: ['projects', { page, size }],
    queryFn: () => projectApi.getProjects({ page, size }),
  });
  ```
- **Client state**: Use Zustand stores
  ```typescript
  const { user, login, logout } = useAuthStore();
  ```

### Styling

**See "Current Phase: Design Language First" above for the canonical rules.**
Quick reference:

- All visual values come from tokens in `src/styles/theme.css` — no raw hex,
  px, or ms in component CSS.
- Ant Design themed via `src/theme/antdTheme.ts` (mapped to our tokens) —
  configured in `src/main.tsx`.
- Anomaly red (`--accent-warning`) is reserved for actual anomalies, never
  decorative.
- Activation is expressed by surface inversion (`--bg-inverted`), not by
  accent color.
- Responsive breakpoints handled in component CSS; primitives must work at
  ≥768 px and ≤420 px without overflow.

### Code Quality

- **ESLint rules enforced** - no unused vars, explicit types preferred
- **Prettier formatting** - 80 char width, single quotes, semicolons
- **Import organization** - external libs first, then internal modules
- **Naming conventions** - camelCase for variables, PascalCase for components

## Backend Integration Notes

### Current API Proxy

Vite dev server proxies `/api` to `http://10.10.10.220:32080` (change in `vite.config.ts` if needed)

### Authentication Flow

1. Login stores JWT in localStorage
2. Axios interceptor adds Authorization header
3. 401 responses trigger token refresh
4. Refresh failure redirects to login

### Key Backend Concepts

- **Projects**: Container for experiments
- **Containers**: Pedestal/Benchmark/Algorithm types
- **Injections**: Fault injection configurations
- **Executions**: Algorithm execution instances
- **Tasks**: Background job tracking
- **Datapacks**: Collected data from injections

## Common Tasks

### Adding a New Page

1. **Check the layout system first** — does the page need a new layout
   pattern, or can it reuse `.page-wrapper` + existing primitives?
2. Create component in `src/pages/` with `className="page-wrapper"` as the
   root element. Do not redefine `max-width`, `margin`, or `padding`.
3. Compose the page from existing primitives (`PageHeader`, `Panel`, `Tabs`,
   `DataTable`, etc.). Only add new primitives if the gallery genuinely lacks
   the needed pattern.
4. Add route in `src/App.tsx` and navigation item in the sidebar nav arrays.
5. Create API client if needed in `src/api/`

### Creating API Integration

1. Define types in `src/types/api.ts` (match backend exactly)
2. Create API client in `src/api/` using `apiClient`
3. Use TanStack Query for data fetching
4. Handle loading/error states

### Working with Forms

- Use Ant Design Form component
- Define form types based on API requirements
- Handle validation before submission
- Show success/error feedback

### Adding Charts

- Use ECharts for standard charts
- Use D3.js for custom visualizations
- Use Cytoscape.js for network graphs
- Follow existing chart component patterns in `src/components/charts/`

## Important Considerations

1. **Backend-First Development**: Always check backend API before implementing frontend features
2. **Type Safety**: All API responses must have corresponding TypeScript types
3. **Error Handling**: Use consistent error messages and handling patterns
4. **Performance**: Use React.memo, useMemo, useCallback where appropriate
5. **Accessibility**: Follow Ant Design accessibility guidelines
6. **Mobile Support**: Ensure responsive design for all screen sizes

<!-- auto-harness:begin -->

## Unified Spec (source of truth: AegisLab 后端仓库)

- `project-index.yaml` → symlink 到 `../AegisLab/project-index.yaml`
- Skills → symlink 到 `../AegisLab/.claude/skills/`
- **所有 requirement 变更在后端仓库的 `project-index.yaml` 中修改**

## North-Star Targets

1. **Full-Stack Spec Alignment** — 每条 requirement 在后端+前端+文档三处都有实现
2. **Zero Mock Code** — 不使用 mock 数据替代真实 API 调用
3. **End-to-End Acceptance** — UI requirement 必须经用户浏览器验收

Secondary: 合约优先于实现细节

## Active Skills

- dev-loop, north-star, long-horizon, existing-project — 均 symlink 自后端仓库
- aegislab-dev-loop-profile — 全栈 dev-loop (项目具体命令和门禁)
- aegislab-north-star — 全栈 north-star (3 个核心目标和观测优先级)

## Frontend-Specific Gates

```bash
pnpm type-check    # 类型安全 (catches SDK misalignment)
pnpm lint          # ESLint
pnpm build         # 生产构建

# Zero mock audit
grep -rn "mock\|Mock\|MOCK\|hardcoded\|TODO.*api\|fake.*data\|stub\|Stub" \
  src/ --include="*.ts" --include="*.tsx" | grep -v node_modules
```

## Cross-Repo Rules

- **零 Mock**: 必须调用真实后端 API，不允许 hardcoded data
- **SDK 同步**: 后端 API 变更后，更新 `@OperationsPAI/portal` 并 `pnpm install`
- **Type 对齐**: `src/types/api.ts` 手写类型不能与 SDK 生成类型矛盾
- **用户验收**: UI 变更标记 tested 前请求用户在浏览器验证
<!-- auto-harness:end -->
