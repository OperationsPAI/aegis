import { viteCommonjs } from '@originjs/vite-plugin-commonjs';
import react from '@vitejs/plugin-react';
import path from 'path';
import { defineConfig, loadEnv } from 'vite';
import topLevelAwait from 'vite-plugin-top-level-await';
import wasm from 'vite-plugin-wasm';

// https://vitejs.dev/config/
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '');

  return {
    plugins: [react(), wasm(), viteCommonjs(), topLevelAwait()],
    // Ensure WASM files are handled correctly
    assetsInclude: ['**/*.wasm'],
    worker: {
      format: 'es',
    },
    resolve: {
      alias: {
        '@': path.resolve(__dirname, './src'),
        '@/components': path.resolve(__dirname, './src/components'),
        '@/pages': path.resolve(__dirname, './src/pages'),
        '@/hooks': path.resolve(__dirname, './src/hooks'),
        '@/utils': path.resolve(__dirname, './src/utils'),
        '@/api': path.resolve(__dirname, './src/api'),
        '@/types': path.resolve(__dirname, './src/types'),
        '@/store': path.resolve(__dirname, './src/store'),
        '@/assets': path.resolve(__dirname, './src/assets'),
      },
    },
    server: {
      port: Number(env.VITE_PORT) || 3000,
      proxy: {
        '/api': {
          target: env.VITE_API_TARGET || 'http://127.0.0.1:8083',
          changeOrigin: true,
          ws: true,
        },
      },
    },
    optimizeDeps: {
      exclude: [
        '@finos/perspective',
        '@finos/perspective-viewer',
        '@finos/perspective-viewer-datagrid',
        '@finos/perspective-viewer-d3fc', // 暂时禁用，chroma-js 依赖有 ESM 问题
      ],
      esbuildOptions: {
        target: 'esnext',
      },
    },
    build: {
      target: 'esnext',
    },
  };
});
