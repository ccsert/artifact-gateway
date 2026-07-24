import { defineConfig, loadEnv } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '');

  return {
    plugins: [react()],
    server: {
      port: 4173,
      proxy: {
        '/api': {
          changeOrigin: true,
          target: env.VITE_GATEWAY_PROXY_TARGET ?? 'http://127.0.0.1:8080',
        },
      },
    },
  };
});
