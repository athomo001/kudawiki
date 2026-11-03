// Flujos principales de punta a punta (SPEC §17): iniciar sesión, escribir y leer, el editor
// visual bajo la CSP estricta, mover arrastrando en el árbol, buscar, y que un lector no edite
// ni otra área se vea. Cada prueba siembra lo suyo con rutas propias.
import { test, expect, request, type Page } from '@playwright/test';
import { BASE_URL, PASSWORD, authFile } from './env.ts';

const as = (key: string) => test.use({ storageState: authFile(key) });

// Las violaciones de la CSP salen como errores de consola: ninguna pantalla debe tenerlas (§14.5).
function watchCSP(page: Page) {
  const errors: string[] = [];
  page.on('console', (m) => {
    if (m.type() === 'error' && /Content Security Policy|Refused to/i.test(m.text())) errors.push(m.text());
  });
  return errors;
}

// Una página creada por la API con la sesión de quien la escribe (la CSRF pide el Origin).
async function seed(key: string, slug: string, title: string, markdown: string) {
  const api = await request.newContext({ baseURL: BASE_URL, storageState: authFile(key), extraHTTPHeaders: { Origin: BASE_URL } });
  const r = await api.put(`/api/w/ti/pages/${slug}`, { data: { title, markdown, base_revision: 0 } });
  expect(r.status(), await r.text()).toBe(204);
  await api.dispose();
}

test.describe('inicio de sesión', () => {
  test('una contraseña mala no entra; la buena lleva directo a la única área', async ({ page }) => {
    await page.goto('/');
    await page.getByLabel('Correo').fill('editor@e2e.test');
    await page.getByLabel('Contraseña').fill('no-es-la-clave-123');
    await page.getByRole('button', { name: 'Iniciar sesión' }).click();
    await expect(page.getByRole('alert')).toBeVisible();
    await page.getByLabel('Contraseña').fill(PASSWORD);
    await page.getByRole('button', { name: 'Iniciar sesión' }).click();
    await expect(page).toHaveURL(/\/w\/ti/);
  });
});

test.describe('editora', () => {
  as('editor');

  test('escribe una página en el editor y se lee publicada', async ({ page }) => {
    const csp = watchCSP(page);
    await page.goto('/w/ti/new');
    await page.getByLabel('Título').fill('Guía de VPN');
    await page.getByLabel('Ruta').fill('e2e/vpn');
    await page.locator('.editor-source .cm-content').fill('# Guía\n\nConectarse con **WireGuard**.');
    await page.getByRole('button', { name: 'Guardar', exact: true }).click();
    await expect(page).toHaveURL(/\/w\/ti\/p\/e2e\/vpn$/);
    await expect(page.locator('strong', { hasText: 'WireGuard' })).toBeVisible();
    expect(csp).toEqual([]);
  });

  test('el modo visual funciona bajo la CSP y vuelve a Markdown sin perder nada', async ({ page }) => {
    const csp = watchCSP(page);
    await page.goto('/w/ti/new');
    await page.getByLabel('Título').fill('Visual');
    await page.getByLabel('Ruta').fill('e2e/visual');
    const modes = page.locator('.mode-switch');
    await modes.getByRole('button', { name: 'Visual' }).click();
    const prose = page.locator('.editor-visual .ProseMirror');
    await expect(prose).toBeVisible();
    await prose.click();
    await page.keyboard.type('Hola desde el modo visual');
    // De vuelta a Markdown: queda guardado así en el perfil para las otras pruebas.
    await modes.getByRole('button', { name: 'Markdown' }).click();
    await expect(page.locator('.editor-source .cm-content')).toContainText('Hola desde el modo visual');
    await page.getByRole('button', { name: 'Guardar', exact: true }).click();
    await expect(page).toHaveURL(/\/w\/ti\/p\/e2e\/visual$/);
    await expect(page.getByText('Hola desde el modo visual')).toBeVisible();
    expect(csp).toEqual([]);
  });

  test('arrastrar en el árbol mueve la página después de confirmar, con los enlaces a reescribir', async ({ page }) => {
    await seed('editor', 'redes', 'Redes', 'Carpeta de redes.');
    await seed('editor', 'firewall', 'Firewall', 'Reglas.');
    await seed('editor', 'indice', 'Índice', 'Ver [[firewall]].');
    await page.goto('/w/ti/p/indice');
    const tree = page.locator('.sidebar .tree').first();
    await tree.getByRole('link', { name: 'Firewall' }).dragTo(tree.getByRole('link', { name: 'Redes' }));
    const dialog = page.getByRole('dialog');
    await expect(dialog).toContainText('redes/firewall');
    await expect(dialog.getByRole('listitem').filter({ hasText: 'Índice' })).toBeVisible();
    await dialog.getByRole('button', { name: 'Mover' }).click();
    await expect(tree.locator('a[href="/w/ti/p/redes/firewall"]')).toBeVisible();
    // El enlace de Índice se reescribió: ya no está roto.
    await page.reload();
    await expect(page.locator('a[href="/w/ti/p/redes/firewall"]').last()).toBeVisible();
  });

  test('la búsqueda encuentra lo del área', async ({ page }) => {
    await seed('editor', 'e2e/buscable', 'Página buscable', 'Contiene la palabra zarzamoraunica.');
    await page.goto('/w/ti');
    await page.getByRole('button', { name: /Buscar en TI/ }).click();
    const box = page.getByRole('dialog', { name: 'Buscar en TI' });
    await box.getByPlaceholder('Buscar en el área…').fill('zarzamoraunica');
    // Las sugerencias del diálogo, y Enter abre la primera.
    await expect(box.getByRole('option').filter({ hasText: 'Página buscable' })).toBeVisible();
    await page.keyboard.press('Enter');
    await expect(page).toHaveURL(/\/w\/ti\/p\/e2e\/buscable$/);
  });
});

test.describe('lector', () => {
  as('lector');

  test('lee pero no edita', async ({ page }) => {
    await seed('editor', 'e2e/para-leer', 'Para leer', 'Texto para el lector.');
    await page.goto('/w/ti/p/e2e/para-leer');
    await expect(page.getByText('Texto para el lector.')).toBeVisible();
    await expect(page.locator('a[href="/w/ti/edit/e2e/para-leer"]')).toHaveCount(0);
    const r = await page.request.put('/api/w/ti/pages/e2e/para-leer', {
      headers: { Origin: BASE_URL },
      data: { title: 'x', markdown: 'x', base_revision: 1 },
    });
    expect(r.status()).toBe(404); // ni 403: el 404 uniforme (§3.3)
  });
});

test.describe('otra área', () => {
  as('finanzas');

  test('TI no existe para quien no es miembro: ni la pantalla, ni la API, ni la búsqueda', async ({ page }) => {
    await seed('editor', 'e2e/secreto', 'Plan secreto', 'Contiene la palabra frambuesaoculta.');
    await page.goto('/w/ti');
    await expect(page.getByRole('heading', { name: 'No encontramos esta página' })).toBeVisible();
    await expect(page.getByText('Plan secreto')).toHaveCount(0);
    for (const path of ['/api/w/ti/tree', '/api/w/ti/pages/e2e/secreto', '/api/w/ti/search?q=frambuesaoculta']) {
      expect((await page.request.get(path)).status(), path).toBe(404);
    }
    const mine = await page.request.get('/api/w/finanzas/search?q=frambuesaoculta');
    expect(await mine.text()).not.toContain('secreto');
  });
});
