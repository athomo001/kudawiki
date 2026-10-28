import type { ReadingPrefs } from './api';

// Las preferencias de lectura (SPEC §15) van como atributos de <html>: alcanzan a toda la
// interfaz, al editor y a la impresión, y no son estilos en línea (la CSP no los deja).
export function applyPrefs(p: ReadingPrefs | undefined) {
  const root = document.documentElement;
  if (p && p.font !== 'default') root.dataset.font = p.font;
  else delete root.dataset.font;
  root.toggleAttribute('data-wide', !!p?.wide);
}
