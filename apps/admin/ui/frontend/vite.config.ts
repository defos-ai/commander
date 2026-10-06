import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [react()],
  build: { outDir: 'dist', sourcemap: false },
  server: { port: 4275, strictPort: true, proxy: { '/ui': { target: 'http://127.0.0.1:4276', changeOrigin: false } } },
  preview: { port: 4275, strictPort: true, proxy: { '/ui': { target: 'http://127.0.0.1:4276', changeOrigin: false } } },
});
