# AegisLab Frontend Restructure Plan

> Date: 2026-04-14
> Status: Draft
> Author: Claude (based on backend API review + frontend UX audit)

## 1. Background & Problem Statement

### Current Issues

1. **Conceptual confusion** — Users don't understand what Project, Team, Task mean in the context of RCA benchmarking. No guidance, no onboarding flow.
2. **W&B clone without domain fit** — The UI copies Weights & Biases patterns (visibility toggles, run panels, workspace headers) but AegisLab is an RCA benchmarking platform, not an ML experiment tracker. Comments in code explicitly say "W&B Style".
3. **Broken flows** — `/projects/new` crashes because `ProjectEdit` tries to fetch a non-existent project. `ProjectList` navigates to `/${name}` without team prefix.
4. **Pipeline invisible** — The core workflow (Inject → Collect → Run Algorithm → Evaluate) is hidden behind flat tabs with no ordering or status indication.
5. **Information architecture chaos** — Projects appear in 4 places (Home, /projects, /:teamName/projects, /:teamName overview). Team detail page duplicates project list. Tasks have no link back to their source.

### Goal

Provide a **simple, intuitive** interface that covers the complete AegisLab workflow based on actual backend capabilities. No backward compatibility needed.

---

## 2. Backend Domain Model (Source of Truth)

```
Team (namespace / org unit)
  └── Project (experiment container)
        ├── Datapacks (fault injection results)
        │     States: Initial → InjectFailed/InjectSuccess → BuildFailed/BuildSuccess → DetectorFailed/DetectorSuccess
        └── Executions (algorithm runs on datapacks)
              States: Initial → Failed/Success
              → produces DetectorResults + GranularityResults

Evaluations: compare execution results across algorithms/datapacks

Containers (admin-managed, 3 types):
  - Pedestal: the target microservice system (e.g., train-ticket on k8s)
  - Benchmark: the data collector/detector
  - Algorithm: RCA algorithms to evaluate

Tasks: background jobs (BuildContainer, RestartPedestal, FaultInjection, RunAlgorithm, BuildDatapack, CollectResult, CronJob)
Traces: group related tasks into a pipeline view (FullPipeline, FaultInjection, DatapackBuild, AlgorithmRun)
Datasets: curated collections of datapacks
```

### Core Workflow

```
1. Pick/create a Project (under a Team)
2. Create Datapacks by:
   a. Injecting faults: choose Pedestal + Benchmark + Fault config → system runs pipeline
   b. Or uploading pre-collected data (ZIP)
3. Run Algorithms on Datapacks → creates Executions
4. View Evaluation results comparing algorithms
```

### Key API Endpoints

| Endpoint                                | Purpose                                 |
| --------------------------------------- | --------------------------------------- |
| `POST /projects`                        | Create project                          |
| `GET /projects`                         | List projects                           |
| `GET /projects/:id`                     | Project detail                          |
| `POST /projects/:id/injections/inject`  | Submit fault injection (main action)    |
| `POST /projects/:id/injections/build`   | Build datapack from existing injection  |
| `POST /projects/:id/executions/execute` | Run algorithm on datapacks              |
| `GET /projects/:id/injections`          | List project datapacks                  |
| `GET /projects/:id/executions`          | List project executions                 |
| `POST /injections/upload`               | Upload external datapack (ZIP)          |
| `GET /injections/:id`                   | Datapack detail                         |
| `GET /injections/:id/files`             | Datapack file structure                 |
| `GET /injections/:id/download`          | Download datapack                       |
| `GET /executions/:id`                   | Execution detail with results           |
| `GET /evaluations`                      | List evaluations                        |
| `POST /evaluations/datapacks`           | Evaluation results by datapacks         |
| `GET /tasks`                            | List tasks (supports project_id filter) |
| `GET /tasks/:id`                        | Task detail with logs                   |
| `GET /tasks/:id/logs/ws`                | Task log streaming (WebSocket)          |
| `GET /traces/:id/stream`                | Trace event streaming (SSE)             |
| `GET /containers?type=X`                | List containers by type                 |
| `GET /teams`                            | List teams                              |
| `POST /teams`                           | Create team                             |

---

## 3. Restructured Page Architecture

### Navigation

```
Sidebar (always visible, same layout for all pages):
  ── Home (dashboard)
  ── Projects (list all)
  ── Tasks (global monitor)
  ── Admin (if superuser):
       Containers, Datasets, Users, System
```

No more WorkspaceLayout with separate sidebar. Project detail pages use the same main layout with breadcrumbs.

### Route Structure

```
/login
/home
/projects                          — project list + create modal
/projects/:id                      — project detail (tabs: overview/datapacks/executions/evaluations/settings)
/projects/:id/inject               — injection wizard
/projects/:id/upload               — upload datapack
/projects/:id/execute              — create execution
/datapacks/:id                     — datapack detail (state stepper + task logs + files)
/executions/:id                    — execution detail (results)
/tasks                             — task monitor
/tasks/:id                         — task detail
/profile
/settings
/admin/users
/admin/containers[/new|/:id|/:id/edit|/:id/versions]
/admin/datasets[/new|/:id|/:id/edit]
/admin/system
```

Key change: **Route by project ID** (`/projects/:id`) instead of `/:teamName/:projectName`. This eliminates URL conflicts with team routes and reserved keywords.

---

## 4. Page Design Details

### 4.1 Home (`/home`)

| Element         | Description                                                                                                                     |
| --------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| Welcome         | "Welcome back, {username}"                                                                                                      |
| Quick Stats     | Total projects, running tasks, recent datapacks (3 cards)                                                                       |
| Recent Projects | Last 5 projects as clickable cards                                                                                              |
| Getting Started | **Always visible** (not just for empty state), explains 4-step flow: Create Project → Inject Faults → Run Algorithms → Evaluate |

Remove: system CPU/memory metrics (not useful for regular users)

### 4.2 Projects (`/projects`)

| Element             | Description                                                                            |
| ------------------- | -------------------------------------------------------------------------------------- |
| Header              | "Projects" + "New Project" button                                                      |
| Project Cards/Table | name, team name, datapack count, execution count, last activity, state badge           |
| "New Project"       | **Inline modal** (not a separate page): name, team (dropdown), description, visibility |

Remove: stat cards row, filter/sort buttons (premature), W&B-style table

### 4.3 Project Detail (`/projects/:id`)

**Tab-based layout** with clear pipeline ordering:

| Tab             | Content                                                                                                                                                               |
| --------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Overview**    | Project info (name, team, visibility, description), quick stats (datapack/execution counts), team members                                                             |
| **Datapacks**   | Table with columns: name, fault_type, state (color badge), benchmark, pedestal, created_at. Buttons: "New Injection", "Upload Datapack". Click row → `/datapacks/:id` |
| **Executions**  | Table with columns: algorithm_name, datapack_name, state, duration, created_at. Button: "Run Algorithm". Click row → `/executions/:id`                                |
| **Evaluations** | Comparison table/chart of algorithm results across datapacks                                                                                                          |
| **Settings**    | Edit project info, danger zone (delete)                                                                                                                               |

### 4.4 Injection Wizard (`/projects/:id/inject`)

**Step-by-step** (Ant Design Steps component):

| Step                            | Content                                                                                                                  |
| ------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| 1. Select Pedestal              | Dropdown from `GET /containers?type=2` (pedestal type). Show name + version. Allow env var overrides.                    |
| 2. Select Benchmark             | Dropdown from `GET /containers?type=1` (benchmark type). Show name + version.                                            |
| 3. Configure Faults             | **Reuse existing** `VisualCanvas` + `FaultNode` + `FaultConfigPanel` components — the fault config UI is good            |
| 4. Set Timing                   | `interval` (minutes) + `pre_duration` (minutes). Explain: "pre_duration = normal data collection before fault injection" |
| 5. Select Algorithms (optional) | Multi-select from `GET /containers?type=0` (algorithm type). "Run these algorithms automatically after data collection"  |
| 6. Review & Submit              | Summary of all selections. Submit → redirect to pipeline progress view                                                   |

After submit: show **pipeline progress** via SSE (`GET /traces/:id/stream`), then navigate to datapack detail when complete.

### 4.5 Upload Datapack (`/projects/:id/upload`)

Simple form: name, ZIP file upload (drag & drop), fault_type, category, benchmark_name, pedestal_name.

### 4.6 Datapack Detail (`/datapacks/:id`)

| Section           | Content                                                                                              |
| ----------------- | ---------------------------------------------------------------------------------------------------- |
| Header            | Datapack name + state badge + action buttons (Download, Run Algorithm, Clone)                        |
| **State Stepper** | Visual progression: `Inject → Build → Detect`. Each step shows success/failed/pending with timestamp |
| Task Status       | If associated task exists: show task state, duration, **embedded logs** (WebSocket streaming)        |
| Ground Truth      | Editor for ground truth labels (PUT `/injections/:id/groundtruth`)                                   |
| Files             | Tree view of datapack files (`GET /injections/:id/files`), click to preview/query parquet            |
| Labels            | Tag management for this datapack                                                                     |

### 4.7 Create Execution (`/projects/:id/execute`)

| Field     | Description                                                           |
| --------- | --------------------------------------------------------------------- |
| Datapacks | Multi-select from project's datapacks (filter: state >= BuildSuccess) |
| Algorithm | Dropdown from `GET /containers?type=0`                                |
| Env Vars  | If algorithm has configurable env vars, show overrides                |
| Labels    | Optional labels                                                       |
| Submit    | Creates execution group, shows progress via SSE                       |

### 4.8 Execution Detail (`/executions/:id`)

| Section             | Content                                         |
| ------------------- | ----------------------------------------------- |
| Header              | Algorithm name + version, state badge, duration |
| Datapack Reference  | Link to source datapack                         |
| Detector Results    | Table of detector results (from API)            |
| Granularity Results | Table of granularity/prediction results         |
| Task Logs           | Embedded logs if task exists                    |

### 4.9 Evaluations Tab (`/projects/:id` evaluations tab)

- Call `POST /evaluations/datapacks` with project's datapack IDs
- Show comparison matrix: rows = datapacks, columns = algorithms, cells = metrics (precision, recall, F1)
- Support filtering by labels

### 4.10 Tasks (`/tasks`)

Simplify existing task monitor:

| Change           | Detail                                                                  |
| ---------------- | ----------------------------------------------------------------------- |
| Fix type display | Show "BuildContainer", "FaultInjection" etc. instead of enum numbers    |
| Add project link | Show `project_name` column, clickable → `/projects/:id`                 |
| Add source link  | Link to related injection/execution if available                        |
| Remove           | Retries column (hardcoded N/A), batch selection, redundant progress bar |
| Keep             | Auto-refresh, state filters, type filters, SSE updates                  |

### 4.11 Team Management

**No dedicated team detail page**. Teams are managed via:

- Project creation modal → team selector dropdown
- Profile page → "My Teams" section with create/edit/invite
- Admin → if needed

---

## 5. Files to Create / Modify / Delete

### Delete

| File                                                  | Reason                                                   |
| ----------------------------------------------------- | -------------------------------------------------------- |
| `src/components/layout/WorkspaceLayout.tsx`           | Replaced by tabs in project detail                       |
| `src/components/layout/WorkspaceSidebar.tsx`          | No longer needed                                         |
| `src/components/workspace/*`                          | WorkspaceTable, WorkspacePageHeader, etc. (W&B patterns) |
| `src/pages/projects/ProjectOverview.tsx`              | Replaced by new project detail                           |
| `src/pages/projects/ProjectInjectionList.tsx`         | Replaced by simpler datapacks tab                        |
| `src/pages/projects/ProjectExecutionList.tsx`         | Replaced by simpler executions tab                       |
| `src/pages/projects/ProjectInjectionDetail.tsx`       | Replaced by new datapack detail                          |
| `src/pages/projects/ProjectExecutionDetail.tsx`       | Replaced by new execution detail                         |
| `src/pages/projects/ProjectEdit.tsx`                  | Replaced by create modal + settings tab                  |
| `src/pages/projects/ProjectSettings.tsx`              | Merged into project detail settings tab                  |
| `src/pages/projects/algorithms/AlgorithmListPage.tsx` | Merged into execution creation                           |
| `src/pages/teams/TeamDetailPage.tsx` + tabs/\*        | Teams become lightweight                                 |
| `src/components/teams/TeamSidebar.tsx`                | No longer needed                                         |

### Create

| File                                           | Purpose                                                        |
| ---------------------------------------------- | -------------------------------------------------------------- |
| `src/pages/projects/ProjectDetail.tsx`         | Tab-based project detail page                                  |
| `src/pages/projects/tabs/OverviewTab.tsx`      | Project overview (info + stats)                                |
| `src/pages/projects/tabs/DatapacksTab.tsx`     | Datapacks table with state badges                              |
| `src/pages/projects/tabs/ExecutionsTab.tsx`    | Executions table                                               |
| `src/pages/projects/tabs/EvaluationsTab.tsx`   | Algorithm comparison results                                   |
| `src/pages/projects/tabs/SettingsTab.tsx`      | Project edit + danger zone                                     |
| `src/pages/projects/CreateProjectModal.tsx`    | Modal: team selector + name + desc + visibility                |
| `src/pages/datapacks/DatapackDetail.tsx`       | State stepper + task logs + file browser                       |
| `src/pages/injections/InjectionWizard.tsx`     | Step-by-step injection creation wrapping existing fault config |
| `src/pages/executions/ExecutionDetail.tsx`     | Results view                                                   |
| `src/pages/executions/CreateExecutionForm.tsx` | Select datapacks + algorithm                                   |
| `src/components/pipeline/StateStepper.tsx`     | Reusable state progression component                           |
| `src/components/pipeline/TaskLogViewer.tsx`    | Embedded task log viewer (WebSocket)                           |

### Modify

| File                                           | Changes                                                                                          |
| ---------------------------------------------- | ------------------------------------------------------------------------------------------------ |
| `src/App.tsx`                                  | Flatten routes: `/projects/:id`, `/datapacks/:id`, `/executions/:id`, remove `/:teamName` routes |
| `src/components/layout/MainSidebarContent.tsx` | Simplify: Home, Projects, Tasks, Admin only                                                      |
| `src/components/layout/MainLayout.tsx`         | Always visible (no WorkspaceLayout split)                                                        |
| `src/components/layout/AppHeader.tsx`          | Simplify breadcrumbs                                                                             |
| `src/pages/home/HomePage.tsx`                  | Simplify, always show getting started, remove system metrics                                     |
| `src/pages/projects/ProjectList.tsx`           | Card grid, add create modal, remove stat cards                                                   |
| `src/pages/tasks/TaskList.tsx`                 | Fix type names, add project link, remove retries column                                          |
| `src/api/projects.ts`                          | Ensure createProject works correctly                                                             |

### Keep As-Is

| File                                                    | Reason                                          |
| ------------------------------------------------------- | ----------------------------------------------- |
| `src/pages/injections/InjectionCreate.tsx` + components | Fault config UI is good, wrap in wizard         |
| `src/pages/injections/components/*`                     | FaultNode, VisualCanvas, FaultConfigPanel, etc. |
| `src/pages/auth/Login.tsx`                              | Works fine                                      |
| `src/pages/admin/*`                                     | Admin pages serve their purpose                 |
| `src/pages/containers/*`                                | Container management works                      |
| `src/pages/datasets/*`                                  | Dataset management works                        |
| `src/pages/profile/ProfilePage.tsx`                     | Works fine                                      |
| All API clients (`src/api/*`)                           | Keep, add minor methods as needed               |
| All hooks (`src/hooks/*`)                               | Keep                                            |
| All stores (`src/store/*`)                              | Keep                                            |
| All types (`src/types/*`)                               | Keep                                            |

---

## 6. Implementation Phases

| Phase                          | Scope                                                               | Verification                       |
| ------------------------------ | ------------------------------------------------------------------- | ---------------------------------- |
| **1. Routing & Layout**        | Flatten routes in App.tsx, remove WorkspaceLayout, simplify sidebar | `pnpm type-check && pnpm build`    |
| **2. Project CRUD**            | CreateProjectModal with team selector, fix project creation         | Create a project in browser        |
| **3. Project Detail**          | Tab-based detail page (Overview + Settings first)                   | Navigate to project, see tabs      |
| **4. Datapacks Tab**           | Simple table with state badges, link to detail                      | See datapacks in project           |
| **5. Datapack Detail**         | State stepper, task logs, file browser                              | View datapack detail page          |
| **6. Executions Tab + Detail** | Table + results view                                                | See executions, view results       |
| **7. Injection Wizard**        | Wrap existing fault config in step wizard                           | Complete injection flow in browser |
| **8. Create Execution**        | Select datapacks + algorithm form                                   | Run algorithm from UI              |
| **9. Evaluations**             | Comparison table                                                    | View evaluation results            |
| **10. Task Monitor Cleanup**   | Fix type names, add project links                                   | Tasks show readable info           |

---

## 7. Acceptance Criteria

### Functional

- [ ] User can login and see Home page with getting started guide
- [ ] User can create a project (with team selection) via modal
- [ ] User can navigate to project and see Overview / Datapacks / Executions / Evaluations / Settings tabs
- [ ] User can create a fault injection via step wizard (Pedestal → Benchmark → Faults → Timing → Algorithms)
- [ ] User can upload a datapack (ZIP file)
- [ ] User can view datapack detail with state progression and task logs
- [ ] User can run an algorithm on selected datapacks
- [ ] User can view execution results (detector + granularity)
- [ ] User can view evaluation comparison across algorithms
- [ ] User can monitor tasks with readable type names and project links
- [ ] Admin can manage containers, datasets, users

### Technical

- [ ] `pnpm type-check` passes
- [ ] `pnpm lint` passes
- [ ] `pnpm build` produces clean production build
- [ ] No mock data — all data from real API calls
- [ ] No W&B-specific patterns remain (visibility toggles, run panels, workspace headers)

### UX

- [ ] Navigation is flat and predictable (max 2 clicks to any feature)
- [ ] Core pipeline flow (Inject → Collect → Execute → Evaluate) is visually obvious
- [ ] Every page has context (breadcrumbs, back links)
- [ ] New users can understand the workflow from the Getting Started guide
- [ ] Task progress is visible inline (not hidden in a separate page)
