// i18n propia y mínima (SPEC §15, §19.34): dos diccionarios y una función.
import es from './es.json';
import en from './en.json';

export type Key = keyof typeof es;
type Locale = 'es' | 'en';

// El tipo obliga a que en.json tenga todas las claves de es.json al compilar.
const messages: Record<Locale, Record<Key, string>> = { es, en };

// Antes del login, el idioma del navegador; después, el del perfil (SPEC §15).
let current = $state<Locale>(navigator.language.toLowerCase().startsWith('en') ? 'en' : 'es');
document.documentElement.lang = current;

export const locale = () => current;

export function setLocale(l: string) {
  if (l !== 'es' && l !== 'en') return;
  current = l;
  document.documentElement.lang = l;
}

export function t(key: Key, params: Record<string, string> = {}): string {
  return messages[current][key].replace(/\{(\w+)\}/g, (_, p: string) => params[p] ?? '');
}

// has dice si hay texto para una clave armada en tiempo de ejecución (p. ej. una acción de la auditoría).
export const has = (key: string): key is Key => key in messages[current];

// Los códigos de error del servidor son estables (SPEC §14); uno desconocido cae en el genérico.
export function errorText(code: string): string {
  const key = `error.${code}`;
  return t(key in messages[current] ? (key as Key) : 'error.internal');
}
