import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

export default defineConfig({
  plugins: [react()],
  build: { outDir: 'dist', sourcemap: false },
  server: { port: 4275, strictPort: true },
  preview: { port: 4275, strictPort: true },
});
