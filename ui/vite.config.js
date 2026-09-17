import { defineConfig } from 'vite';
import solidPlugin from 'vite-plugin-solid';

export default defineConfig({
  plugins: [solidPlugin()],
  server: {
    port: 3000,
    host: true, // Allow access from network
    proxy: {
      // Latency dashboard (its own service, port 8023) must be matched
      // BEFORE the general /api rule below, since /api/latency/* would
      // otherwise be caught by the broader /api prefix and sent to
      // execution-gateway (8005) instead.
      '/api/latency': {
        target: 'http://127.0.0.1:8023',
        changeOrigin: true,
      },
      '/api': {
        target: 'http://127.0.0.1:8005',
        changeOrigin: true,
      },
    },
  },
  build: {
    target: 'esnext',
  },
});