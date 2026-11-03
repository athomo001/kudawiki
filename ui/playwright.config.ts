// Pruebas E2E de los flujos principales (SPEC §17) contra el binario real. Usan el Chrome del
// equipo (channel 'chrome'): ni se descargan navegadores ni se depende de un CDN. Correr con
// `make e2e`, que antes arma la UI que el binario embebe.
import { defineConfig } from '@playwright/test';
import { BASE_URL } from './e2e/env.ts';

export default defineConfig({
  testDir: 'e2e',
  globalSetup: './e2e/setup.ts',
  // Una sola instalación compartida: las pruebas crean rutas propias, pero el árbol y la
  // búsqueda son comunes; en serie no se pisan.
  workers: 1,
  timeout: 30_000,
  reporter: [['list']],
  use: {
    baseURL: BASE_URL,
    channel: 'chrome',
    locale: 'es-CL',
    trace: 'retain-on-failure',
  },
  webServer: {
    command: 'node e2e/server.ts',
    url: `${BASE_URL}/healthz`,
    timeout: 180_000, // compila el binario
    reuseExistingServer: false,
  },
});
