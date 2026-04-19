import { lazy, Suspense, useEffect } from 'react';
import { Navigate, Route, Routes } from 'react-router-dom';

import MainLayout from '@/components/layout/MainLayout';
import LoadingFallback from '@/components/ui/LoadingFallback';
import { useAuthStore } from '@/store/auth';

// Lazy load all page components
const Login = lazy(() => import('@/pages/auth/Login'));

// User pages
const HomePage = lazy(() => import('@/pages/home/HomePage'));
const ProjectList = lazy(() => import('@/pages/projects/ProjectList'));
const ProjectDetail = lazy(() => import('@/pages/projects/ProjectDetail'));
const ProjectDatapacks = lazy(
  () => import('@/pages/projects/ProjectDatapacks')
);
const ProjectExecutions = lazy(
  () => import('@/pages/projects/ProjectExecutions')
);
const ProjectEvaluations = lazy(
  () => import('@/pages/projects/ProjectEvaluations')
);
const ProjectSettings = lazy(() => import('@/pages/projects/ProjectSettings'));

// Project action pages
const InjectionWizard = lazy(
  () => import('@/pages/injections/InjectionWizard')
);
const CreateExecutionForm = lazy(
  () => import('@/pages/executions/CreateExecutionForm')
);

// Detail pages
const DatapackDetail = lazy(() => import('@/pages/datapacks/DatapackDetail'));
const ExecutionDetail = lazy(
  () => import('@/pages/executions/ExecutionDetail')
);

// Tasks
const TaskList = lazy(() => import('@/pages/tasks/TaskList'));
const TaskDetail = lazy(() => import('@/pages/tasks/TaskDetail'));

// User settings
const ProfilePage = lazy(() => import('@/pages/profile/ProfilePage'));
const Settings = lazy(() => import('@/pages/settings/Settings'));

// Admin pages
const AdminUsersPage = lazy(() => import('@/pages/admin/AdminUsersPage'));
const ContainerList = lazy(() => import('@/pages/containers/ContainerList'));
const ContainerForm = lazy(() => import('@/pages/containers/ContainerForm'));
const ContainerDetail = lazy(
  () => import('@/pages/containers/ContainerDetail')
);
const ContainerVersions = lazy(
  () => import('@/pages/containers/ContainerVersions')
);
const DatasetList = lazy(() => import('@/pages/datasets/DatasetList'));
const DatasetForm = lazy(() => import('@/pages/datasets/DatasetForm'));
const DatasetDetail = lazy(() => import('@/pages/datasets/DatasetDetail'));
const SystemSettings = lazy(() => import('@/pages/system/SystemSettings'));

function App() {
  const { isAuthenticated, loadUser } = useAuthStore();

  useEffect(() => {
    if (isAuthenticated) {
      loadUser();
    }
  }, [isAuthenticated, loadUser]);

  return (
    <Routes>
      {/* Public routes */}
      <Route
        path='/login'
        element={
          <Suspense fallback={<LoadingFallback />}>
            <Login />
          </Suspense>
        }
      />

      {/* Protected routes - ALL under MainLayout */}
      <Route
        element={
          isAuthenticated ? <MainLayout /> : <Navigate to='/login' replace />
        }
      >
        {/* Default redirect */}
        <Route index element={<Navigate to='/home' replace />} />

        {/* ==================== User Routes ==================== */}

        {/* Home */}
        <Route path='home' element={<HomePage />} />

        {/* Projects */}
        <Route path='projects' element={<ProjectList />} />
        <Route path='projects/:id' element={<ProjectDetail />} />
        <Route path='projects/:id/datapacks' element={<ProjectDatapacks />} />
        <Route path='projects/:id/executions' element={<ProjectExecutions />} />
        <Route
          path='projects/:id/evaluations'
          element={<ProjectEvaluations />}
        />
        <Route path='projects/:id/settings' element={<ProjectSettings />} />
        <Route path='projects/:id/inject' element={<InjectionWizard />} />
        <Route path='projects/:id/execute' element={<CreateExecutionForm />} />

        {/* Datapacks */}
        <Route path='datapacks/:id' element={<DatapackDetail />} />

        {/* Executions */}
        <Route path='executions/:id' element={<ExecutionDetail />} />

        {/* Tasks */}
        <Route path='tasks' element={<TaskList />} />
        <Route path='tasks/:id' element={<TaskDetail />} />

        {/* Profile */}
        <Route path='profile' element={<ProfilePage />} />

        {/* Settings */}
        <Route path='settings' element={<Settings />} />

        {/* ==================== Admin Routes ==================== */}

        {/* Admin Users */}
        <Route path='admin/users' element={<AdminUsersPage />} />

        {/* Admin Containers */}
        <Route path='admin/containers' element={<ContainerList />} />
        <Route path='admin/containers/new' element={<ContainerForm />} />
        <Route path='admin/containers/:id' element={<ContainerDetail />} />
        <Route path='admin/containers/:id/edit' element={<ContainerForm />} />
        <Route
          path='admin/containers/:id/versions'
          element={<ContainerVersions />}
        />

        {/* Admin Datasets */}
        <Route path='admin/datasets' element={<DatasetList />} />
        <Route path='admin/datasets/new' element={<DatasetForm />} />
        <Route path='admin/datasets/:id' element={<DatasetDetail />} />
        <Route path='admin/datasets/:id/edit' element={<DatasetForm />} />

        {/* Admin System */}
        <Route path='admin/system' element={<SystemSettings />} />
      </Route>

      {/* Fallback - redirect unknown routes */}
      <Route
        path='*'
        element={
          isAuthenticated ? (
            <Navigate to='/home' replace />
          ) : (
            <Navigate to='/login' replace />
          )
        }
      />
    </Routes>
  );
}

export default App;
