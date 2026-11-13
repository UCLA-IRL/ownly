import { fileURLToPath, URL } from 'node:url';

import { defineConfig } from 'vite';

import defines from './defines';

// https://vite.dev/config/
export default defineConfig({
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  define: defines,
  build: {
    outDir: 'dist/headless',
    lib: {
      name: 'headless',
      entry: {
        headless: 'src/node/headless.ts',
        'email-agent': 'src/node/email-agent.ts',
      },
      fileName: (_format, entryName) => `${entryName}.js`,
      formats: ['es'],
    },
    minify: false,
    rollupOptions: {
      output: {
        preserveModules: true,
        preserveModulesRoot: 'src/node',
      },
      external: [
        /node:.*/,
        'nodemailer',
        'marked',
        'sanitize-html',
        'fs',
        'fs/promises',
        'worker_threads',
        'buffer',
        'util',
        'crypto',
        'path',
      ],
    },
  },
});
