// Modo de color (SPEC §15, B4): claro, oscuro o rosa, como data-scheme en <html>, que es lo que
// lee app.css. Se recuerda en el navegador: depende del equipo y de la luz donde se lee, y no es
// contenido (los sellados no pasan por aquí, §6). Sin elección guardada, sigue al sistema.
export type Scheme = 'light' | 'dark' | 'rose';

const KEY = 'kw-scheme';
const order = ['light', 'dark', 'rose'] as const satisfies readonly Scheme[];
const media = matchMedia('(prefers-color-scheme: dark)');

// localStorage puede no estar (navegación privada, sitio bloqueado): entonces manda el sistema.
function stored(): Scheme | null {
  try {
    const v = localStorage.getItem(KEY);
    return order.includes(v as Scheme) ? (v as Scheme) : null;
  } catch {
    return null;
  }
}

const system = (): Scheme => (media.matches ? 'dark' : 'light');

export const scheme = $state({ current: stored() ?? system() });

function apply() {
  document.documentElement.dataset.scheme = scheme.current;
}
apply();

media.addEventListener('change', () => {
  if (stored()) return;
  scheme.current = system();
  apply();
});

export const nextScheme = (s: Scheme): Scheme => order[(order.indexOf(s) + 1) % order.length] ?? 'light';

// Pasa al modo siguiente con un círculo que nace en (x, y) y cubre la pantalla. Con "reducir
// movimiento" o un navegador sin View Transitions, el cambio es instantáneo.
export function cycleScheme(x: number, y: number) {
  const change = () => {
    scheme.current = nextScheme(scheme.current);
    try {
      localStorage.setItem(KEY, scheme.current);
    } catch {
      // Sin almacenamiento el modo vale hasta recargar.
    }
    apply();
  };
  if (!document.startViewTransition || matchMedia('(prefers-reduced-motion: reduce)').matches) {
    change();
    return;
  }
  const r = Math.hypot(Math.max(x, innerWidth - x), Math.max(y, innerHeight - y));
  document.startViewTransition(change).ready.then(() => {
    document.documentElement.animate(
      { clipPath: [`circle(0px at ${x}px ${y}px)`, `circle(${r}px at ${x}px ${y}px)`] },
      { duration: 550, easing: 'cubic-bezier(.4, 0, .2, 1)', pseudoElement: '::view-transition-new(root)' },
    );
  });
}
