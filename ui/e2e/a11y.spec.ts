// Accesibilidad básica de las pantallas principales (SPEC §15, F8.15): todo control y toda
// imagen tienen nombre en el árbol de accesibilidad de Chrome, y la página declara su idioma.
// No reemplaza una revisión con lector de pantalla; atrapa el botón de ícono sin etiqueta.
import { test, expect, request } from '@playwright/test';
import { BASE_URL, authFile } from './env.ts';

// Roles que sin nombre no se pueden usar con un lector de pantalla.
const NAMED = /^\s*- (button|link|textbox|searchbox|checkbox|radio|switch|combobox|listbox|slider|spinbutton|menuitem|tab|img)(\s*\[[^\]]*\])*:?\s*$/;

async function unnamed(page: import('@playwright/test').Page) {
  const snapshot = await page.locator('body').ariaSnapshot();
  return snapshot.split('\n').filter((l) => NAMED.test(l));
}

const screens: Record<string, string[]> = {
  super: [
    '/w/ti',
    '/w/ti/p/a11y',
    '/w/ti/search?q=prueba',
    '/w/ti/history/a11y',
    '/me',
    '/me/profile',
    '/me/profile/preferences',
    '/me/profile/notifications',
    '/me/profile/security',
    '/me/profile/sessions',
    '/me/profile/vault',
    '/admin/users',
    '/admin/areas',
    '/admin/transfer',
    '/admin/access-review',
    '/admin/appearance',
    '/admin/email',
    '/admin/backups',
    '/admin/health',
    '/admin/audit',
  ],
  editor: ['/w/ti', '/w/ti/p/a11y', '/w/ti/files', '/w/ti/review'],
};

test.beforeAll(async () => {
  const api = await request.newContext({ baseURL: BASE_URL, storageState: authFile('editor'), extraHTTPHeaders: { Origin: BASE_URL } });
  const r = await api.put('/api/w/ti/pages/a11y', {
    data: { title: 'Accesibilidad', markdown: '## Sección\n\nTexto con [[a11y|enlace]].\n\n- [ ] tarea\n\n| a | b |\n|---|---|\n| 1 | 2 |', base_revision: 0 },
  });
  expect([204, 409]).toContain(r.status());
  await api.dispose();
});

for (const [who, paths] of Object.entries(screens)) {
  test.describe(who, () => {
    test.use({ storageState: authFile(who) });
    for (const path of paths) {
      test(`sin controles sin nombre: ${path}`, async ({ page }) => {
        await page.goto(path);
        await page.waitForLoadState('networkidle');
        expect(await page.evaluate(() => document.documentElement.lang)).toMatch(/^(es|en)/);
        expect(await unnamed(page)).toEqual([]);
      });
    }
  });
}

test('el inicio de sesión, sin sesión', async ({ browser }) => {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await page.goto('/');
  await page.waitForLoadState('networkidle');
  expect(await unnamed(page)).toEqual([]);
  await ctx.close();
});
