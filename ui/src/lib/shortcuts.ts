// Atajos de teclado de una pantalla (SPEC §15): teclas sueltas ("e") y secuencias de dos ("g h",
// la segunda dentro de un segundo). No se disparan mientras se escribe en un campo, con un
// diálogo abierto, ni con Ctrl/Cmd/Alt: esos atajos son del navegador o de Ctrl+K.
const typing = 'input, textarea, select, [contenteditable=""], [contenteditable="true"], .cm-editor, dialog[open]';

export function shortcuts(map: Record<string, () => void>): () => void {
  let prev = '';
  let timer: ReturnType<typeof setTimeout> | undefined;
  const onKey = (e: KeyboardEvent) => {
    if (e.ctrlKey || e.metaKey || e.altKey || e.defaultPrevented) return;
    if ((e.target as Element | null)?.closest?.(typing)) return;
    const key = e.key.toLowerCase();
    const run = (prev && map[`${prev} ${key}`]) || map[key];
    clearTimeout(timer);
    if (run) {
      e.preventDefault();
      prev = '';
      run();
      return;
    }
    prev = key;
    timer = setTimeout(() => (prev = ''), 1000);
  };
  addEventListener('keydown', onKey);
  return () => {
    clearTimeout(timer);
    removeEventListener('keydown', onKey);
  };
}
