import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// En desarrollo la API la sirve `kudawiki serve` en :8080; en producción todo sale del binario.
export default defineConfig({
  plugins: [svelte()],
  // Logo y favicon del producto (SPEC §15): se copian tal cual a dist/.
  publicDir: 'static',
  server: { proxy: { '/api': 'http://localhost:8080', '/_site': 'http://localhost:8080' } },
  build: {
    // El editor (CodeMirror) y Mermaid se cargan aparte, solo cuando hacen falta.
    chunkSizeWarningLimit: 1500,
    rolldownOptions: {
      // La página aislada de Mermaid es una entrada propia (SPEC §4.10).
      input: { main: 'index.html', mermaid: 'sandbox/mermaid.html' },
    },
  },
});
