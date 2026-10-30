import { EditorView } from '@codemirror/view';
import { StateEffect } from '@codemirror/state';

// Desplazamiento sincronizado entre el Markdown y la vista previa del editor (SPEC §4.2). La
// vista previa trae cada bloque marcado con la línea donde empieza (data-line, wiki.Options.
// SourceLines); entre dos marcas se interpola, así una tabla larga o un bloque de código sin
// marca también quedan alineados. Manda la columna que la persona está usando: el
// desplazamiento que se hace por código en la otra no vuelve de rebote.

type Anchor = { line: number; top: number };
type Side = 'source' | 'preview';

// Marcas visibles que avanzan en las dos columnas. Lo oculto (una pestaña o un desplegable
// cerrados) no ocupa lugar, y las celdas de una misma fila repiten línea y altura.
function anchors(pane: HTMLElement, lines: number): Anchor[] {
  const base = pane.getBoundingClientRect().top - pane.scrollTop;
  const out: Anchor[] = [{ line: 1, top: 0 }];
  for (const el of pane.querySelectorAll<HTMLElement>('[data-line]')) {
    const rect = el.getBoundingClientRect();
    const line = Number(el.dataset.line);
    const last = out[out.length - 1]!;
    if (rect.height && line > last.line && rect.top - base > last.top) out.push({ line, top: rect.top - base });
  }
  out.push({ line: lines + 1, top: pane.scrollHeight });
  return out;
}

// Interpola entre las dos marcas que rodean value (una línea o una altura).
function interpolate(list: Anchor[], from: keyof Anchor, value: number): number {
  const to: keyof Anchor = from === 'line' ? 'top' : 'line';
  let i = 1;
  while (i < list.length - 1 && list[i]![from] <= value) i++;
  const a = list[i - 1]!;
  const b = list[i]!;
  const span = b[from] - a[from];
  return a[to] + (span > 0 ? ((value - a[from]) / span) * (b[to] - a[to]) : 0);
}

// Sin barra de desplazamiento no hay final: una columna corta no arrastra a la otra al fondo.
const atEnd = (el: HTMLElement) => el.scrollHeight > el.clientHeight + 2 && el.scrollTop + el.clientHeight >= el.scrollHeight - 2;

export function syncScroll(view: EditorView, pane: HTMLElement): () => void {
  const source = view.scrollDOM;
  let active: Side = 'source';
  let frame = 0;

  // Altura del comienzo del documento dentro de la columna del Markdown (su relleno).
  const docTop = () => view.documentTop - source.getBoundingClientRect().top + source.scrollTop;

  // Línea, con fracción, que está arriba de todo en el Markdown; y la altura de una línea así.
  function topLine(): number {
    const h = Math.max(0, source.scrollTop - docTop());
    const block = view.lineBlockAtHeight(h);
    return view.state.doc.lineAt(block.from).number + Math.min(1, (h - block.top) / (block.height || 1));
  }
  function lineTop(line: number): number {
    const doc = view.state.doc;
    const n = Math.min(Math.max(1, Math.floor(line)), doc.lines);
    const block = view.lineBlockAt(doc.line(n).from);
    return docTop() + block.top + (line - n) * block.height;
  }

  function align(from: Side) {
    cancelAnimationFrame(frame);
    frame = requestAnimationFrame(() => {
      const [leader, follower] = from === 'source' ? [source, pane] : [pane, source];
      // Al final de una, también al final de la otra: la última pantalla no siempre llega arriba.
      if (atEnd(leader)) {
        follower.scrollTop = follower.scrollHeight;
        return;
      }
      const list = anchors(pane, view.state.doc.lines);
      if (from === 'source') pane.scrollTop = interpolate(list, 'line', topLine());
      else source.scrollTop = lineTop(interpolate(list, 'top', pane.scrollTop));
    });
  }

  const onScroll = (side: Side) => () => side === active && align(side);
  const onSourceScroll = onScroll('source');
  const onPaneScroll = onScroll('preview');
  const toSource = () => (active = 'source');
  const toPreview = () => (active = 'preview');
  // Manda la columna que se toca, no la que el mouse cruza: escribir con el puntero sobre la
  // vista previa no debe mover el Markdown.
  const intent = ['pointerdown', 'wheel', 'touchstart', 'focusin', 'keydown'] as const;

  // Clic en la vista previa: el cursor va al comienzo de ese bloque en el Markdown, a la misma
  // altura en que estaba el bloque. Los enlaces, pestañas y desplegables siguen funcionando.
  function onClick(e: MouseEvent) {
    const target = e.target as Element;
    const el = target.closest<HTMLElement>('[data-line]');
    if (!el || target.closest('a, button, summary, input, label') || !getSelection()?.isCollapsed) return;
    const line = view.state.doc.line(Math.min(Number(el.dataset.line), view.state.doc.lines));
    const y = el.getBoundingClientRect().top - pane.getBoundingClientRect().top;
    // scrollIntoView de CodeMirror mide la línea antes de mover: una lejana puede no estar dibujada.
    view.dispatch({ selection: { anchor: line.from }, effects: EditorView.scrollIntoView(line.from, { y: 'start', yMargin: y }) });
    view.focus();
  }

  // La vista previa cambia de alto al volver a dibujarse, al cargar una imagen o una fórmula, o
  // al abrir un desplegable: se vuelve a alinear desde la columna que se está usando.
  const resized = new ResizeObserver(() => align(active));
  for (const child of pane.children) resized.observe(child);

  // CodeMirror estima la altura de las líneas que todavía no dibujó y la corrige al medirlas
  // (una fila de tabla larga ocupa dos): al saltar lejos desde la vista previa, el Markdown
  // llega a la línea justa en el cuadro siguiente.
  let live = true;
  view.dispatch({ effects: StateEffect.appendConfig.of(EditorView.updateListener.of((u) => live && u.heightChanged && align(active))) });

  source.addEventListener('scroll', onSourceScroll, { passive: true });
  pane.addEventListener('scroll', onPaneScroll, { passive: true });
  pane.addEventListener('click', onClick);
  for (const type of intent) {
    source.addEventListener(type, toSource, { passive: true });
    pane.addEventListener(type, toPreview, { passive: true });
  }
  return () => {
    live = false;
    cancelAnimationFrame(frame);
    resized.disconnect();
    source.removeEventListener('scroll', onSourceScroll);
    pane.removeEventListener('scroll', onPaneScroll);
    pane.removeEventListener('click', onClick);
    for (const type of intent) {
      source.removeEventListener(type, toSource);
      pane.removeEventListener(type, toPreview);
    }
  };
}
