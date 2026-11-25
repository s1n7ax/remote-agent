import { writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { defineConfig, type Plugin } from 'vite';

/** Keeps dist/.gitkeep after Vite empties the output dir, so `go build` works from a clean checkout. */
function keepGitkeep(): Plugin {
  return {
    name: 'keep-gitkeep',
    apply: 'build',
    closeBundle() {
      writeFileSync(resolve(__dirname, 'dist/.gitkeep'), '');
    },
  };
}

export default defineConfig({
  plugins: [svelte(), keepGitkeep()],
  build: { outDir: 'dist', emptyOutDir: true, target: 'es2022' },
  server: {
    proxy: { '/api': { target: 'http://127.0.0.1:7700', changeOrigin: false } },
  },
});
