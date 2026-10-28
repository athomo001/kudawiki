// Router mínimo por URL (SPEC §15): sin dependencias, con el historial del navegador.
let path = $state(location.pathname);
let query = $state(location.search);
let hash = $state(location.hash);

function sync() {
  path = location.pathname;
  query = location.search;
  hash = location.hash;
}

addEventListener('popstate', sync);

// Los enlaces internos (<a href="/…">) navegan sin recargar.
addEventListener('click', (e) => {
  const a = (e.target as Element).closest('a');
  if (!a || a.origin !== location.origin || a.target || e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
  if (a.hash && a.pathname === location.pathname) return; // ancla en la misma página: la resuelve el navegador
  e.preventDefault();
  navigate(a.pathname + a.search + a.hash);
});

export const route = {
  get path() {
    return path;
  },
  get query() {
    return query;
  },
  // El ancla: p. ej. la sección de un resultado de búsqueda (SPEC §9.4).
  get hash() {
    return hash;
  },
};

// replace: para corregir la URL sin agregar una entrada al historial (p. ej. tras un alias).
export function navigate(to: string, { replace = false } = {}) {
  if (to === location.pathname + location.search + location.hash) return;
  history[replace ? 'replaceState' : 'pushState'](null, '', to);
  sync();
}
