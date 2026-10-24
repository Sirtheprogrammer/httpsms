import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// In development the Go server runs on :8080; Vite proxies API and WebSocket calls to it.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8080',
      '/ws': { target: 'ws://localhost:8080', ws: true },
    },
  },
  build: {
    outDir: 'dist',
    chunkSizeWarningLimit: 900,
  },
});
