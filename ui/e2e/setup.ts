// Siembra la instalación de prueba por la API, como lo haría el superadmin: dos áreas, tres
// personas invitadas y sus membresías, y una sesión guardada por persona (así cada prueba no
// vuelve a iniciar sesión y no choca con el límite de intentos, §8.2).
import { request, type APIRequestContext, type FullConfig } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { BASE_URL, PASSWORD, PEOPLE, SUPER, authFile } from './env.ts';

// La protección CSRF pide el Origin del sitio (§14.5).
const newContext = () => request.newContext({ baseURL: BASE_URL, extraHTTPHeaders: { Origin: BASE_URL } });

async function ok(res: Promise<import('@playwright/test').APIResponse>) {
  const r = await res;
  if (!r.ok()) throw new Error(`${r.url()}: ${r.status()} ${await r.text()}`);
  return r.status() === 204 ? null : r.json();
}

async function login(email: string): Promise<APIRequestContext> {
  const ctx = await newContext();
  await ok(ctx.post('/api/auth/login', { data: { email, password: PASSWORD } }));
  return ctx;
}

export default async function setup(_: FullConfig) {
  mkdirSync('e2e/.auth', { recursive: true });
  const admin = await login(SUPER.email);
  await admin.storageState({ path: authFile(SUPER.key) });
  // Agregar miembros pide confirmar la identidad (§8.2): sin 2FA, basta la contraseña.
  await ok(admin.post('/api/auth/reauth', { data: { password: PASSWORD } }));

  const areas: Record<string, string> = {};
  for (const [slug, name] of [['ti', 'TI'], ['finanzas', 'Finanzas']]) {
    areas[slug] = (await ok(admin.post('/api/admin/workspaces', { data: { slug, name } }))).id;
  }

  const anon = await newContext();
  for (const p of PEOPLE) {
    const { invite_url } = await ok(admin.post('/api/admin/users', { data: { email: p.email, full_name: p.name } }));
    const token = new URL(invite_url).hash.slice(1);
    await ok(anon.post('/api/auth/invite/accept', { data: { token, new_password: PASSWORD } }));
  }
  const member = (area: string, email: string, role: string) =>
    ok(admin.post(`/api/admin/workspaces/${areas[area]}/members`, { data: { email, role } }));
  await member('ti', 'editor@e2e.test', 'editor');
  await member('ti', 'lector@e2e.test', 'viewer');
  await member('finanzas', 'finanzas@e2e.test', 'editor');

  for (const p of PEOPLE) {
    const ctx = await login(p.email);
    await ctx.storageState({ path: authFile(p.key) });
    await ctx.dispose();
  }
  await anon.dispose();
  await admin.dispose();
}
