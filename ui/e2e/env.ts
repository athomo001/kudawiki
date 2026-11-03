// Lo que comparten el servidor de prueba, el setup y las pruebas E2E.
export const PORT = process.env.E2E_PORT ?? '18080';
export const BASE_URL = `http://localhost:${PORT}`;
// localhost relaja la política de contraseñas (§8.1), pero esta igual la cumpliría.
export const PASSWORD = 'Prueba-E2E-larga-2026';

export type Person = { key: string; email: string; name: string };
export const SUPER: Person = { key: 'super', email: 'super@e2e.test', name: 'Super E2E' };
// editor y lector en TI; finanzas, editora en Finanzas y sin nada en TI.
export const PEOPLE: Person[] = [
  { key: 'editor', email: 'editor@e2e.test', name: 'Eva Editora' },
  { key: 'lector', email: 'lector@e2e.test', name: 'Luis Lector' },
  { key: 'finanzas', email: 'finanzas@e2e.test', name: 'Fernanda Finanzas' },
];

export const authFile = (key: string) => `e2e/.auth/${key}.json`;
