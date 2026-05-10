import { ConfigProvider } from 'antd';
import React from 'react';
import ReactDOM from 'react-dom/client';

import App from './App';
import './styles/fonts';
import './index.css';
import { aegisTheme } from './theme/antdTheme';

const rootElement = document.getElementById('root');
if (!rootElement) {
  throw new Error('Failed to find the root element');
}

ReactDOM.createRoot(rootElement).render(
  <React.StrictMode>
    <ConfigProvider theme={aegisTheme}>
      <App />
    </ConfigProvider>
  </React.StrictMode>
);
