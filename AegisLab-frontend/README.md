# RCABench Frontend

English | [中文](./docs/README.zh-CN.md)

Frontend application for RCABench (AegisLab) - A microservices root cause analysis benchmarking platform.

## Tech Stack

- **Framework**: React 18 + TypeScript
- **Build Tool**: Vite 5
- **UI Library**: Ant Design 5
- **State Management**: Zustand
- **Data Fetching**: TanStack Query (React Query)
- **HTTP Client**: Axios
- **Routing**: React Router v6
- **Charts**: ECharts
- **Code Editor**: Monaco Editor
- **Styling**: CSS + Ant Design Theme Customization

## Design Style

**Academic Research Style**:

- Neutral color scheme with deep blue (#2563eb) as primary color
- Clear information hierarchy and data presentation
- Emphasis on chart and analysis result readability
- Clean and professional interface design

## Project Structure

```plain
frontend/
├── src/
│   ├── api/                  # API clients
│   │   ├── client.ts        # Axios config and interceptors
│   │   ├── auth.ts          # Authentication APIs
│   │   ├── projects.ts      # Project management APIs
│   │   ├── containers.ts    # Container management APIs
│   │   ├── injections.ts    # Injection APIs
│   │   ├── executions.ts    # Execution APIs
│   │   ├── tasks.ts         # Task management APIs
│   │   └── evaluations.ts   # Evaluation APIs
│   ├── assets/              # Static assets
│   ├── components/          # React components
│   │   ├── common/          # Common components
│   │   └── layout/          # Layout components
│   │       └── MainLayout.tsx
│   ├── hooks/               # Custom hooks
│   ├── pages/               # Page components
│   │   ├── auth/            # Authentication pages
│   │   │   └── Login.tsx
│   │   ├── dashboard/       # Dashboard
│   │   │   └── Dashboard.tsx
│   │   ├── projects/        # Project management
│   │   │   └── ProjectList.tsx
│   │   ├── containers/      # Container management
│   │   │   └── ContainerList.tsx
│   │   ├── datasets/        # Dataset management
│   │   ├── injections/      # Fault injection
│   │   │   ├── InjectionList.tsx
│   │   │   └── InjectionCreate.tsx
│   │   ├── executions/      # Algorithm execution
│   │   │   └── ExecutionList.tsx
│   │   ├── evaluations/     # Evaluation
│   │   ├── tasks/           # Task monitoring
│   │   ├── system/          # System management
│   │   └── settings/        # Settings
│   ├── store/               # State management
│   │   └── auth.ts         # Authentication state
│   ├── types/               # TypeScript type definitions
│   │   └── api.ts          # API types
│   ├── utils/               # Utility functions
│   │   └── theme.ts        # Theme configuration
│   ├── App.tsx             # Root component
│   ├── main.tsx            # Application entry
│   └── index.css           # Global styles
├── index.html              # HTML template
├── package.json            # Dependencies
├── tsconfig.json           # TypeScript config
├── vite.config.ts          # Vite config
└── README.md               # Documentation
```

## Quick Start

### Prerequisites

- Node.js >= 18
- pnpm >= 8

### Install Dependencies

```bash
pnpm install
```

### Development Mode

```bash
pnpm dev
# or use just command
just dev
```

The app will start at `http://localhost:3000` with API requests proxied to `http://10.10.10.220:32080`

### Build for Production

```bash
pnpm build
```

Build artifacts will be generated in the `dist` directory.

### Preview Production Build

```bash
pnpm preview
```

### Linting

```bash
pnpm lint
```

### Type Checking

```bash
pnpm type-check
```

## Core Features

### Implemented

✅ **Authentication System**

- Login page
- JWT Token authentication
- Automatic token refresh
- Auth state management

✅ **Main Layout**

- Fixed Header and Sidebar
- Responsive design
- User info dropdown menu

✅ **Dashboard**

- Key metrics display (projects, experiments, tasks, executions)
- Task status distribution pie chart
- Recent activity list

✅ **Project Management**

- Project list (pagination, search, filtering)
- Create/Edit/Delete projects
- Label management

✅ **Container Management**

- Container list (pagination, search, type filtering)
- Support for Pedestal/Benchmark/Algorithm types
- Version management

### To Be Implemented

⏳ **Fault Injection**

- Visual fault orchestrator (batches + parallel nodes)
- Fault configuration forms (dynamic forms based on fault type)
- Injection details page (real-time status, log streaming)

⏳ **Algorithm Execution**

- Execution list and details
- Result visualization (service topology, layered results)
- Accuracy analysis (Top-K, confusion matrix)

⏳ **Evaluation**

- Datapack evaluation
- Dataset evaluation
- Algorithm comparison (radar chart, bar chart)

⏳ **Task Monitoring**

- Task list and details
- Real-time log streaming (SSE)
- Task dependency tree visualization

⏳ **System Management**

- User management
- Role management
- Permission management
- Label management

## API Proxy Configuration

In development mode, Vite proxies `/api` requests to the backend server:

```typescript
// vite.config.ts
server: {
  port: 3000,
  proxy: {
    '/api': {
      target: 'http://10.10.10.220:32080',
      changeOrigin: true,
    },
  },
}
```

## Environment Variables

Create a `.env.local` file to configure environment variables:

```bash
# API Base URL (uses proxy by default)
VITE_API_BASE_URL=/api/v2

# Other configurations...
```

## Theme Customization

Theme configuration is in `src/utils/theme.ts` and `src/main.tsx`:

```typescript
// Primary colors
colorPrimary: '#2563eb'; // Deep blue
colorSuccess: '#10b981'; // Green
colorWarning: '#f59e0b'; // Amber
colorError: '#ef4444'; // Red
colorInfo: '#06b6d4'; // Cyan
```

## State Management

Using Zustand for lightweight state management, primarily for auth state:

```typescript
// Usage example
import { useAuthStore } from '@/store/auth';

const { user, login, logout, isAuthenticated } = useAuthStore();
```

## Data Fetching

Using TanStack Query (React Query) for server state management:

```typescript
// Usage example
import { useQuery } from '@tanstack/react-query';

import { projectApi } from '@/api/projects';

const { data, isLoading } = useQuery({
  queryKey: ['projects', { page: 1, size: 10 }],
  queryFn: () => projectApi.getProjects({ page: 1, size: 10 }),
});
```

## Routing Structure

```plain
/                     # Redirect to /dashboard
/login                # Login page
/dashboard            # Dashboard
/projects             # Project list
/containers           # Container list
/datasets             # Dataset list
/injections           # Injection list
/injections/create    # Create injection
/executions           # Execution list
/evaluations          # Evaluation page
/tasks                # Task monitoring
/system               # System management
/settings             # User settings
```

## Docker Deployment

### Development Environment

Start the dev container with live reload:

```bash
just dev
```

The container will:

- Run in the background
- Auto-restart on system reboot
- Sync local code changes (hot reload)
- Expose port 3000

See [README.Docker.md](README.Docker.md) for more details.

## Next Steps

1. **Implement Fault Injection Visual Orchestrator** (Core feature)
   - Drag-and-drop batch management
   - Parallel fault node configuration
   - Real-time preview and validation

2. **Implement Algorithm Execution Result Visualization**
   - Service topology graph (D3.js or Cytoscape)
   - Layered result display
   - Accuracy analysis charts

3. **Implement SSE Real-time Log Streaming**
   - EventSource integration
   - Auto-reconnection mechanism
   - Log level filtering

4. **Improve Common Component Library**
   - LabelSelector
   - ContainerSelector
   - LogStream
   - TaskStatusBadge

## Contributing

1. Fork the project
2. Create a feature branch (`git checkout -b feature/AmazingFeature`)
3. Commit your changes (`git commit -m 'Add some AmazingFeature'`)
4. Push to the branch (`git push origin feature/AmazingFeature`)
5. Open a Pull Request

## License

MIT License
