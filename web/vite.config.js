import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [svelte()],
  build: {
    // Output directly into the Go embed package so `go build` picks it up.
    outDir: '../internal/webstatic/dist',
    emptyOutDir: true,
  },
  server: {
    // Proxy API and WebSocket calls to the Go proxy during development.
    proxy: {
      '/auth':   'http://localhost:7888',
      '/state':  'http://localhost:7888',
      '/events': 'http://localhost:7888',
      '/seen':   'http://localhost:7888',
      '/expand': 'http://localhost:7888',
      '/fetch':  'http://localhost:7888',
      '/store':  'http://localhost:7888',
      '/ws': { target: 'ws://localhost:7888', ws: true },
    },
  },
});
