# AegisLab Frontend 重构方案

> 基于对后端 90+ API 端点、前端 36,000 行 TS/TSX 代码、64 个 CSS 文件的深度审计编写。
> 无需后向兼容，可大力重构。

---

## 一、���心问题诊断

### 1.1 信息架构分裂：双轨路由

当前前端��在两套完全独立的页面体系：

| 路由族              | Layout          | 典型页面                                     | 面向角色 |
| ------------------- | --------------- | -------------------------------------------- | -------- |
| `/:team/:project/*` | WorkspaceLayout | ProjectInjectionList, ProjectExecutionDetail | 普通用户 |
| `/admin/*`          | MainLayout      | InjectionList, ExecutionDetail, Dashboard    | 管理员   |

**同一个实体（Injection/Execution/Evaluation）有两套列表页和详情页**，代码量对比：

```
InjectionList.tsx        679 行  vs  ProjectInjectionList.tsx    584 行
ExecutionList.tsx        625 行  vs  ProjectExecutionList.tsx    694 行
ExecutionDetail.tsx      704 行  vs  ProjectExecutionDetail.tsx  329 行
InjectionDetail.tsx      214 行  vs  ProjectInjectionDetail.tsx  327 行
                        ───��─       ──────
总计                    2,222 行     1,934 行 = 4,156 行重复代码
```

这两套页面功能高度重叠，但 admin 版使用��统 Table + Card，project 版使用 DetailView + WorkspaceTable，数据来源不同（admin 用全局 API，project 用 project-scoped API），维护成本翻倍。

**结论**：删除 admin 下的业务数据页面，统一走 project-scoped 路由。Admin 只保留���统管理功能。

### 1.2 Mock/Stub 泛滥

| API 模块             | Mock 程度      | 影响范围                                             |
| -------------------- | -------------- | ---------------------------------------------------- |
| `api/workspace.ts`   | 100% mock      | ProjectWorkspace 页面完全假数据                      |
| `api/profile.ts`     | 70% mock       | Activity graph 全假，starred 存 localStorage         |
| `api/teams.ts`       | 6/10 方法 stub | invite/remove/updateRole/settings/delete 全空        |
| `api/evaluations.ts` | 仅调用2个API   | 后端只有2个evaluation endpoint，但前端有完整CRUD页面 |

Settings tab 中调用了 `teamApi.addSecret` 和 `teamApi.deleteSecret`，这两个方法在 `teams.ts` 中根本不存在。

### 1.3 类型系统混乱

三重定���冲突：

```typescript
// types/api.ts line 22-28: enum 定义
export enum InjectionState { PENDING = '0', RUNNING = '1', ... }

// types/api.ts line 10: 同时从 SDK re-export
export { ExecutionState, FaultType } from '@rcabench/client';

// types/workspace.ts line 217-230: 又定义一遍，值完全不同！
export type InjectionState = 'pending' | 'running' | 'success' | 'failed' | 'stopped';
export type FaultType = 'network' | 'cpu' | 'memory' | 'disk' | ...;
export type ExecutionState = 'initial' | 'pending' | 'running' | 'success' | 'failed';
```

`api.ts` 里 `InjectionState.RUNNING = '1'`，`workspace.ts` 里 `InjectionState = 'running'`。同名不同值，导入时极易混淆。

### 1.4 Workspace Store ��度膨胀

`store/workspace.ts` 有 **600 行**，管理了：

- 运行列��状���（runs, selectedRuns, visibleRuns, runColors）
- 表格配置（injectionsTableSettings, executionsTableSettings, columns, sort, filter）
- 图表状态（charts, chartGroups）
- UI 状态（panel collapsed, search, pagination）
- 显示设置（cropMode, sortOrder）

这个 store 是 W&B Workspace 功能的核心，但整个 Workspace 功能完全是 mock（`api/workspace.ts` 全假）��意味着这 600 行 store + 559 行 workspace types 全在为一个不存在的功能服务。

### 1.5 样式��乱

- **64 个 CSS 文件**，其中��有 29 个包含 `@media` 响���式查询
- **78 个文件** 使用 `style={{}}` 行内样式，共计 **542 处** 行内样式使用
- 没有使用 CSS Modules（无作���域隔离），所有 CSS 是全局的
- Teams SettingsTab、UsersTab、OverviewTab 完全使用行内样式，无 CSS 文件
- CSS 变量定义在 `styles/theme.css`���但大量组件硬编码颜色值（`#52c41a`, `#ff4d4f`, `#1890ff`）

### 1.6 SSE 基础设施半成品

后��提供 3 个 SSE 流 + 1 个 WebSocket：

| 端点                    | 前��使用情况                          |
| ----------------------- | ------------------------------------- |
| `/traces/{id}/stream`   | `useTraceSSE` hook 已实现（质量较高） |
| `/groups/{id}/stream`   | 未使用                                |
| `/notifications/stream` | 未使用                                |
| `/tasks/{id}/logs/ws`   | `useTaskLogs` hook 已实现             |

`useSSE` 通用 hook 存在但几乎未被使用，`useTraceSSE` 自己用 fetch + ReadableStream 重新实现了 SSE 解析（更健壮）。两套 SSE 方���并存。

### 1.7 未使用的后端能力

后端��� 90+ 端点，前��缺失覆盖：

**完全缺失的 API 域**（前端没有任何 UI 对应）：

- Notifications SSE（实时通知）
- Metrics（injection/execution/algorithm 聚合统计）
- Traces 列表（只有单个查询，没有列表页）
- Group stream（批量任务进度追踪）
- User RBAC 管理（11 个 role/permission 分配端点）
- Container Build + Helm ���理（3 个端点）
- Dataset 搜索 + 版本注入管理（2 个端点）

**前端�� UI 但调用假数据的**：

- Team CRUD（create/delete/update）
- Team member management（invite/remove/role change）
- Profile activity / starred projects
- Workspace 全部功能

---

## 二、重构方案

### 2.1 新信息架构

```
/login                              # 登录页

# ── 主布局（MainLayout + Sidebar）────────────────────────
/home                               # 首页 Dashboard（合并原 HomePage + Dashboard）
/projects                           # 全局 Project 列表
/projects/new                       # 创建 Project

/:teamName                          # Team 概览（tabs: overview/projects/members/settings）
/:teamName/settings                 # Team 设置

/profile                            # 个人 Profile 页
/settings                           # 个人设置（合并原3个settings页面为1个）

/tasks                              # 全局 Task 列表（从 admin 提升）
/notifications                      # 通知中心（新增）

# ── Admin 布局（仅系统管理员可见）─────────────────────────
/admin/users                        # 用户管理 + RBAC
/admin/roles                        # 角色管理
/admin/containers                   # Container 管理
/admin/datasets                     # Dataset 管理
/admin/system                       # 系统监控

# ── Workspace 布局（WorkspaceLayout）──────────────────────
/:teamName/:projectName             # Project 概览
/:teamName/:projectName/injections  # Injection 列表（唯一版本）
/:teamName/:projectName/injections/:id
/:teamName/:projectName/injections/create
/:teamName/:projectName/executions  # Execution 列表（唯一版本）
/:teamName/:projectName/executions/:id
/:teamName/:projectName/executions/new
/:teamName/:projectName/evaluations # Evaluation 列表
/:teamName/:projectName/evaluations/:id
/:teamName/:projectName/traces      # Trace 列表（新增）
/:teamName/:projectName/traces/:id
/:teamName/:projectName/settings    # Project 设置
```

**关键变化**：

1. **删除 `/admin/injections`、`/admin/executions`、`/admin/evaluations`、`/admin/datapacks`、`/admin/tasks`** — 业务数据只通过 project 访问
2. **删除 `/admin/dashboard`** — 合��到 `/home`
3. **提升 Tasks 到顶级** — 任务是全局概念，不需要 admin 权限
4. **新增 `/notifications`** — 消费后端 SSE 通知流
5. **新增 Traces 页面** — 每个 project 下查看 trace 列表和详情
6. **删除所有 placeholder 路由**（reports, artifacts, charts）— 未实现就不暴露

### 2.2 API 层重构

#### 原则：统一为直接 apiClient 调用，放弃 SDK 类实例化

��前 SDK（`@rcabench/client`）有以下问题：

- SDK 不完整，部分 API 需要手动用 apiClient 补充
- `new ProjectsApi(createApiConfig())` 每次调用都创建新实例
- SDK 的 `Configuration` 对象需要 `createApiConfig()` / `createFileApiConfig()` / `createArrowApiConfig()` 三种工厂
- 混合使用导致两种错误���理路径（SDK 自带 vs axios interceptor）

**方案**：全部使用 `apiClient`（axios instance），SDK 仅用于类型导入。

```typescript
// 新的统一 API 调用模式
// api/projects.ts
import type {
  CreateProjectReq,
  ListProjectResp,
  ProjectDetailResp,
} from '@rcabench/client';

import { apiClient } from './client';

export const projectApi = {
  list: (params?: { page?: number; size?: number }) =>
    apiClient
      .get<{ data: ListProjectResp }>('/projects', { params })
      .then((r) => r.data.data),

  get: (id: number) =>
    apiClient
      .get<{ data: ProjectDetailResp }>(`/projects/${id}`)
      .then((r) => r.data.data),

  create: (data: CreateProjectReq) =>
    apiClient
      .post<{ data: ProjectDetailResp }>('/projects', data)
      .then((r) => r.data.data),

  update: (id: number, data: Partial<CreateProjectReq>) =>
    apiClient
      .patch<{ data: ProjectDetailResp }>(`/projects/${id}`, data)
      .then((r) => r.data.data),

  delete: (id: number) => apiClient.delete(`/projects/${id}`),

  updateLabels: (id: number, labels: LabelItem[]) =>
    apiClient.patch(`/projects/${id}/labels`, { labels }),

  // Project-scoped injections
  listInjections: (projectId: number, params?: PaginationParams) =>
    apiClient
      .get<{
        data: ListInjectionResp;
      }>(`/projects/${projectId}/injections`, { params })
      .then((r) => r.data.data),

  searchInjections: (projectId: number, body: SearchBody) =>
    apiClient
      .post<{
        data: ListInjectionResp;
      }>(`/projects/${projectId}/injections/search`, body)
      .then((r) => r.data.data),

  // ... 每个方法都是一行定义，清晰明了
};
```

#### API 文件清单（重构后）

| 文件                   | 覆盖端点                                                               | 当前状态                |
| ---------------------- | ---------------------------------------------------------------------- | ----------------------- |
| `api/client.ts`        | axios instance + interceptors                                          | 保留���合并 `config.ts` |
| `api/auth.ts`          | login/register/refresh/logout/profile/changePassword                   | 重写                    |
| `api/projects.ts`      | CRUD + labels + injections + executions scoped                         | 重写                    |
| `api/teams.ts`         | CRUD + members + projects                                              | 重写（接真实API）       |
| `api/containers.ts`    | CRUD + versions + labels + build + helm                                | 补充 build/helm         |
| `api/datasets.ts`      | CRUD + versions + search + labels + download                           | 补充 search             |
| `api/injections.ts`    | get/metadata/labels/batchLabels/clone/files/download/query/batchDelete | 重写                    |
| `api/executions.ts`    | get/labels/uploadResults/batchDelete                                   | 重写                    |
| `api/evaluations.ts`   | datapacks/datasets                                                     | 保留简化                |
| `api/tasks.ts`         | list/get/batchDelete                                                   | 保留简化                |
| `api/traces.ts`        | list/get                                                               | 补充 list               |
| `api/labels.ts`        | CRUD + batchDelete                                                     | 保留                    |
| `api/users.ts`         | CRUD + role/permission/container/dataset/project assignment            | 补充 RBAC               |
| `api/roles.ts`         | CRUD + permission assign/remove + listUsers                            | 保留                    |
| `api/permissions.ts`   | list/get/listRoles                                                     | 保留                    |
| `api/resources.ts`     | list/get/listPermissions                                               | 保留                    |
| `api/metrics.ts`       | injections/executions/algorithms                                       | **新增**                |
| `api/notifications.ts` | SSE stream helper                                                      | **新增**                |
| `api/system.ts`        | metrics/history                                                        | 保留简化                |

**删除**：`api/workspace.ts`（全 mock），`api/profile.ts`（大部分 mock），`api/datapacks.ts`（datapacks 就是 injections，不需要独立 API 模块）

### 2.3 类��系统重构

#### 原则：SDK 类型为唯一来源，不再手动重定义

```
types/
├── api.ts          # 仅 re-export SDK 类型 + 少量前端独有的扩展类型
└── workspace.ts    # 删除全部 InjectionState/FaultType/ExecutionState 重定义
                    # 仅保留纯前端 UI 类型（ColumnConfig, SortField, RunVisibility 等）
```

**具体行动**：

1. 删除 `types/api.ts` 中的 `enum InjectionState`、`enum InjectionType`、`enum ProjectState` — 使用 SDK 的定义
2. 删除 `types/workspace.ts` 中的 `type InjectionState`、`type FaultType`、`type ExecutionState` — 直接从 SDK 导入
3. 删除 `types/workspace.ts` 中的 `InjectionTableRow`、`ExecutionTableRow`、`EvaluationTableRow` — 直接使用 `InjectionResp`、`ExecutionResp`
4. 删除 `types/api.ts` 中手动定义的 `Team`、`TeamMember`、`TeamSecret`、`TeamLink` — 使用 SDK 的 `TeamResp`、`TeamDetailResp`、`TeamMemberResp`
5. 如果 SDK 类型不完整，重新生成 SDK：`just generate-typescript-client <version>`

### 2.4 状态管理重构

#### store/workspace.ts（600行 → 拆分 + 瘦身）

当前 workspace store 混合了 4 种关注点：

| 关注点                          | 行数   | 方案                                      |
| ------------------------------- | ------ | ----------------------------------------- |
| Run 可见性/颜色                 | ~200行 | 保留为 `store/visibility.ts`              |
| 表格配置（columns/sort/filter） | ~200行 | 移入 `useTablePersistence` hook（已存在） |
| 图表状态                        | ~50行  | 移入 Workspace 页面的 local state         |
| UI 状态（collapse/search/page） | ~150行 | 移入各页面的 local state                  |

#### store/profile.ts（starred projects → server state）

Starred projects 不应该存 localStorage。后端尚无 star API，两个选择：

1. 后端加 star API → 用 TanStack Query
2. 暂不实现 star 功能 → 删除 `store/profile.ts`

**建议**：选项 2��star 是低优先级功能。

#### store/auth.ts

保留，这是正确的 Zustand 使用场景（全局���户端状态）。

#### store/theme.ts

保留。

### 2.5 页面重构���单

#### 删除（-13 个页面/组件文件）

| 文件                                       | 原因                                 |
| ------------------------------------------ | ------------------------------------ |
| `pages/injections/InjectionList.tsx`       | 合并到 ProjectInjectionList          |
| `pages/injections/InjectionDetail.tsx`     | 合并到 ProjectInjectionDetail        |
| `pages/executions/ExecutionList.tsx`       | 合并到 ProjectExecutionList          |
| `pages/executions/ExecutionDetail.tsx`     | 合并到 ProjectExecutionDetail        |
| `pages/datapacks/DatapackList.tsx`         | Datapack = Injection，不需要独立页面 |
| `pages/datapacks/DatapackDetail.tsx`       | 同上                                 |
| `pages/dashboard/Dashboard.tsx`            | 合并到 HomePage                      |
| `pages/settings/UserProfile.tsx`           | 合并到 Settings.tsx                  |
| `pages/projects/ProjectEvaluationList.tsx` | 全 mock，删除                        |
| `api/workspace.ts`                         | 全 mock                              |
| `api/profile.ts`                           | 大部分 mock                          |
| `api/datapacks.ts`                         | 合并到 injections                    |
| `pages/UtilityTest.tsx`                    | 测试页面                             |

#### 重写（7 个页面）

| 页面                     | 重写内容                                                                                                         |
| ------------------------ | ---------------------------------------------------------------------------------------------------------------- |
| **HomePage**             | 合并 Dashboard 统计数据 + 活跃 tasks + 最近 injections/executions + ���知 feed。使用���端 `/metrics/*` API。     |
| **TeamDetailPage**       | 所有 tab 接真实 API（create team, invite/remove member, update role, delete team, update settings）              |
| **ProfilePage**          | 删除 mock activity graph，���真实 execution 数据做活跃度。profile editing 接 `/auth/profile` + user update API。 |
| **Settings**             | 合并为一个页面（Profile + Security），删除 Notification tab（后端无对应 API）。                                  |
| **ProjectOverview**      | 添加真实统计数据（调用 `/metrics/*`），实现 Project Roles tab（调用 user-project RBAC API）。                    |
| **ProjectInjectionList** | 作为唯一的 injection 列表页，支持 project-scoped 和全局两种模式。                                                |
| **ProjectExecutionList** | 同上，作为唯一的 execution 列表页。                                                                              |

#### ���增（4 个页面）

| 页面                  | 功能                                                                                                                     |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| **TracesPage**        | Trace 列表 + 详情。列表展示所有 traces，详情展示 pipeline 阶段进度（利用 `useTraceSSE`）。路由：`/:team/:project/traces` |
| **NotificationsPage** | 消费 `/notifications/stream` SSE，展示��时 + 历史通知。同时在 Header 添加通知��铛 badge���                               |
| **TaskListPage**      | 从 `/admin/tasks` 提升到 `/tasks`，任何已登录用户可见。                                                                  |
| **AdminUsersPage**    | RBAC 管理界面：用���列表 + 角色分配 + 权限分配 + 资源级权��（container/dataset/project）。                               |

### 2.6 组件架构重构

#### Layout 简化

```
components/layout/
├── AppHeader.tsx        # 保���，添加通知铃铛
├── MainLayout.tsx       # 保留
├── MainSidebar.tsx      # 重写（添加 Tasks、Admin ���口）
├── WorkspaceLayout.tsx  # 保留
├── WorkspaceSidebar.tsx # 重写（删除 Reports/Artifacts/Charts placeholder）
```

删除未使用的：`Breadcrumb.tsx`（AppHeader 内置了），`ProjectLayout.tsx`，`ProjectSidebar.tsx`

#### Sidebar 导航���构

```
Main Sidebar (MainLayout):
├���─ Home
├── ─── divider ───
├── Projects (heading)
│   ├── project-1
│   ├── project-2
│   └── View all →
├── ─── divider ───
├── Teams (heading)
│   ├── team-1
│   ├── team-2
│   └── Create team  (不再 disabled)
├── ─── divider ───
├── Tasks             ← 新增
├── ─── divider ───
├── Admin             ← 仅 admin 可见，展开子菜单
│   ├── Users
│   ├── Roles
│   ├── Containers
│   ├── Datasets
│   └── System
└── footer: System Online indicator

Workspace Sidebar (WorkspaceLayout):
├── Project (overview)
├── Injections
├── Executions
├── Evaluations
├── Traces            ← 新增
└── Settings
```

#### 共享组件优化

当前 `components/workspace/` 有 20+ 组件，很���是 W&B 表格���细粒度组件（ColumnHeaderDropdown, ColumnManager, ColumnsDropdown, SortDropdown, GroupDropdown, NameColumnDropdown, RunListDropdown, RunListItem...）。

**建议**：保留这些组件，但确保它们被 ProjectInjectionList 和 ProjectExecutionList 统一��用（删除 admin 版的独立实现）。

### 2.7 实时功能接入

#### Notification System

```typescript
// hooks/useNotifications.ts
export const useNotifications = () => {
  const [notifications, setNotifications] = useState<NotificationEvent[]>([]);
  const [unreadCount, setUnreadCount] = useState(0);

  // 使用 useTraceSSE 同样的 fetch + ReadableStream 模式
  useEffect(() => {
    const token = localStorage.getItem('access_token');
    if (!token) return;

    const controller = new AbortController();
    const connect = async () => {
      const response = await fetch('/api/v2/notifications/stream', {
        headers: {
          Authorization: `Bearer ${token}`,
          Accept: 'text/event-stream',
        },
        signal: controller.signal,
      });
      // ... 类似 useTraceSSE 的流处理
    };
    connect();
    return () => controller.abort();
  }, []);

  return { notifications, unreadCount, markAsRead };
};
```

在 AppHeader 中添加：

```tsx
<Badge count={unreadCount} size='small'>
  <BellOutlined onClick={() => navigate('/notifications')} />
</Badge>
```

#### Group Progress Tracking

在 injection batch 操作后���展示进度条：

```typescript
// 在 InjectionCreate ��交后，返回 group_id
// 自动跳转到 injection list，显示 group progress bar
// 使用 /groups/{group_id}/stream SSE 追踪完���进度
```

### 2.8 CSS 重构

#### 原则：Ant Design token + CSS variables，消除行内样式

1. **统一颜色 token**：���前 542 处行内样式中大量硬编码颜色。改为使用 `styles/theme.css` 中的 CSS variables：

   ```css
   --color-success: #52c41a; /* 替代所有硬编码的绿色 */
   --color-error: #ff4d4f; /* 替代所有硬编码的红色 */
   --color-warning: #faad14; /* 替代所有硬编码的黄色 */
   --color-primary: #2563eb; /* 替代所有硬编码的蓝色 */
   ```

2. **行内样式迁移**：Teams SettingsTab、UsersTab、OverviewTab 等完全用行内样式的组件，迁移到 CSS 文件。

3. **考虑 CSS Modules**：���组件使用 `*.module.css` 避免全局样式污染。

4. **响应式补齐**：35 个 CSS 文件没有 `@media` 查询。至少在 Layout、List、Detail ��面确保 tablet 适配。

---

## 三、实施计划

### Phase 1：地基清理（预计 3-5 天）

**目标**：删除��代码、消除重复��统一 API 调用方式

- [ ] 重新生成 TypeScript SDK（`just generate-typescript-client`）
- [ ] 重写 API 层：所有文件统一用 apiClient 模式，删除 SDK 类实例化
- [ ] 删除 `api/workspace.ts`、`api/profile.ts`、`api/datapacks.ts`
- [ ] 合并 `api/client.ts` 和 `api/config.ts` 为单一 `api/client.ts`
- [ ] 清���类型系统：删除 `types/workspace.ts` 和 `types/api.ts` 中的重复定义
- [ ] 删除 admin 下的业务数据页面（6 个页面）
- [ ] 删除所有 placeholder 路由（reports, artifacts, charts）
- [ ] 删除所有 "coming soon" 的按钮/菜单项

### Phase 2：核心功能接真实 API（预计 3-5 天）

**目标**：所有页面连接真实后端

- [ ] Teams：��� create/delete/update/invite/remove/updateRole API
- [ ] Homepage：使用 `/metrics/*` API 展示真实统计数据
- [ ] ProjectOverview：使用 metrics API，实现 project roles tab
- [ ] Settings：��并为单页，profile update 接后端
- [ ] Sidebar：添加 Tasks、Admin 入口
- [ ] Container 页面：添加 Build、Helm chart/values 上传 UI

### Phase 3：新增��能��面（预计 3-5 天）

**目标**：补齐后���已有但前端缺失的功能

- [ ] Traces 页面（列表 + 详情 + SSE 实时状态）
- [ ] Notifications ��统（SSE 流 + 铃铛 + 通知列表页）
- [ ] Admin RBAC 管理（用户角色/权限分配 UI）
- [ ] Group progress tracking（批���注入进度条）
- [ ] Dataset search + version injection management

### Phase 4：样式���体验优化（预计 2-3 天）

**目标**：统一视觉语言，响应式适配

- [ ] 消���硬编码颜色值，统一使用 CSS variables
- [ ] 迁移行内样式到 CSS 文件
- [ ] 新增组件使用 CSS Modules
- [ ] 补齐关��页面的响应式适配
- [ ] 拆分 workspace store，清理状态管理

---

## 四、技术决���记录

| 决策               | 选择                   | 原因                                            |
| ------------------ | ---------------------- | ----------------------------------------------- |
| API 调用方式       | 统一 apiClient         | SDK 不完���，混用增加复杂度                     |
| SDK ��途           | 仅导入类型             | 避���运行时依赖 SDK 的 Configuration            |
| Workspace/W&B 功能 | 暂时搁置               | 后端无 workspace API，纯前端 mock 无意义        |
| Star 功能          | 暂时移除               | 后端无 star API，localStorage 方案不可靠        |
| 路由架构           | Project-scoped 为主    | 符合后端 RBAC 模型（project-level permissions） |
| Admin 页面         | 仅系统管理             | 业务数据通过 project 访问，减少��复             |
| CSS 方案           | 渐进迁移到 CSS Modules | 不值得���次性重写，新代码用 modules             |

---

## 五、风险和注意事项

1. **SDK 重新生成可能���入 breaking changes**：后端 API 如果有改动，SDK 类型可能���当前前端代码不匹配。需要在 Phase 1 首先验证。

2. **删除 admin ��面后，系统管��员如何查看全局数据**：在后端���`/injections`（全局）需要 `RequireSystemAdmin`，而 `/projects/:id/injections` 需要 `RequireProjectRead`。确保 admin 用户��授权到所有 project，或者保留一个全局 "All Projects" 视图。

3. **Team name 和 Project name 作为 URL segment ���冲突**：当前 `/:teamName` 和一些顶级路由（home, projects, profile, admin）冲突由 `knownRootRoutes` 列表避免。如果用户创建��为 "admin" 的 team 会出问题。需要在后端/前端都做保留字检查。

4. **Workspace 功能��留**：当前 ProjectWorkspace 页面 + workspace store + workspace types 合计约 2,000 行代码全在为 mock 功能服务。建议 Phase 1 整体移除 workspace 相关代码，等后端 workspace API 就绪后再开发。但这意味着 `WorkspaceTable`、`RunsPanel`、`ChartsPanel` 等已做好的组件暂时没有使用场景。保留这些组件代码但不路由到 workspace 页面是一个折中方案。
