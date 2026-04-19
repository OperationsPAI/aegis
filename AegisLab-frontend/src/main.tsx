import React from 'react';
import ReactDOM from 'react-dom/client';
import { BrowserRouter } from 'react-router-dom';

import {
  MutationCache,
  QueryClient,
  QueryClientProvider,
} from '@tanstack/react-query';
import { App as AntdApp, ConfigProvider, message } from 'antd';
import enUS from 'antd/locale/en_US';
import type { AxiosError } from 'axios';
import dayjs from 'dayjs';
import 'dayjs/locale/en';
import relativeTime from 'dayjs/plugin/relativeTime';

import ErrorBoundary from './components/ErrorBoundary';
import { initializeTheme } from './store/theme';

import App from './App';

import './index.css';
import './styles/responsive.css';
import './styles/theme.css';

dayjs.locale('en');
dayjs.extend(relativeTime);

const queryClient = new QueryClient({
  mutationCache: new MutationCache({
    onError: (error) => {
      const msg =
        (error as AxiosError<{ message?: string }>)?.response?.data?.message ||
        error.message ||
        'Operation failed';
      message.error(msg);
    },
  }),
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      retry: (failureCount, error) => {
        if ((error as AxiosError)?.response?.status === 401) return false;
        return failureCount < 1;
      },
      staleTime: 5 * 60 * 1000, // 5 minutes
    },
  },
});

// Initialize theme on app load
initializeTheme();

const rootElement = document.getElementById('root');
if (!rootElement) {
  throw new Error('Failed to find the root element');
}

ReactDOM.createRoot(rootElement).render(
  <React.StrictMode>
    <BrowserRouter
      future={{
        v7_startTransition: true,
        v7_relativeSplatPath: true,
      }}
    >
      <ErrorBoundary>
        <QueryClientProvider client={queryClient}>
          <ConfigProvider
            locale={enUS}
            theme={{
              token: {
                colorPrimary: '#0ea5e9',
                colorSuccess: '#10b981',
                colorWarning: '#f59e0b',
                colorError: '#ef4444',
                colorInfo: '#06b6d4',
                borderRadius: 8,
                fontFamily:
                  'Inter, -apple-system, BlinkMacSystemFont, sans-serif',
                fontSize: 14,
                controlHeight: 40,
              },
              components: {
                Layout: {
                  headerBg: 'transparent',
                  headerHeight: 64,
                  siderBg: 'transparent',
                },
                Menu: {
                  itemBg: 'transparent',
                  itemSelectedBg: '#0ea5e920',
                  itemSelectedColor: '#0ea5e9',
                  itemHoverBg: '#f1f5f9',
                },
                Card: {
                  borderRadiusLG: 12,
                },
                Button: {
                  borderRadius: 8,
                  controlHeight: 40,
                  controlHeightLG: 48,
                },
                Input: {
                  borderRadius: 8,
                  controlHeight: 40,
                },
                Table: {
                  borderRadius: 12,
                },
              },
            }}
          >
            <AntdApp>
              <App />
            </AntdApp>
          </ConfigProvider>
        </QueryClientProvider>
      </ErrorBoundary>
    </BrowserRouter>
  </React.StrictMode>
);
