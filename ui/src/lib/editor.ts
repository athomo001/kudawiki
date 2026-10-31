import { EditorView, basicSetup } from 'codemirror';
import { markdown } from '@codemirror/lang-markdown';
import { keymap } from '@codemirror/view';
import { EditorSelection, EditorState } from '@codemirror/state';
import type { CompletionContext, CompletionResult } from '@codemirror/autocomplete';
import { HighlightStyle, syntaxHighlighting } from '@codemirror/language';
import { tags } from '@lezer/highlight';

export type PageOption = { slug: string; title: string };

type Handlers = {
  onChange: (value: string) => void;
  onSave: () => void;
  onFiles: (files: File[]) => void;
  // Las páginas del área que quien edita ve, para el autocompletado de [[enlaces]].
  pages: () => PageOption[];
};

const filesOf = (list?: FileList | null) => [...(list ?? [])];

// CodeMirror inserta sus estilos en un <style>: la CSP estricta solo lo deja pasar con el nonce
// que el servidor pone en cada index.html (SPEC §14.5). Sin él, el editor queda sin estilos y
// no se puede usar.
const nonce = document.querySelector<HTMLMetaElement>('meta[name="csp-nonce"]')?.content ?? '';

// Colores del Markdown del mockup "Editor" (SPEC §15): encabezados en azul, marcas en ámbar.
// Son variables CSS (app.css): el mismo tema sirve en claro y en oscuro.
const highlight = HighlightStyle.define([
  { tag: tags.heading, color: 'var(--kw-md-heading)', fontWeight: '600' },
  { tag: [tags.processingInstruction, tags.quote, tags.contentSeparator], color: 'var(--kw-md-mark)' },
  { tag: [tags.link, tags.url], color: 'var(--kw-link)' },
  { tag: [tags.comment, tags.meta, tags.labelName], color: 'var(--kw-md-meta)' },
  { tag: tags.monospace, color: 'var(--kw-md-code)' },
  { tag: tags.emphasis, fontStyle: 'italic' },
  { tag: tags.strong, fontWeight: '600' },
  { tag: tags.strikethrough, textDecoration: 'line-through' },
]);

// El tema base de CodeMirror es claro: cursor, selección y línea activa siguen los tokens de la wiki.
const theme = EditorView.theme({
  '&': { color: 'var(--kw-text-body)', backgroundColor: 'transparent' },
  '.cm-content': { caretColor: 'var(--kw-text)' },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--kw-text)' },
  // Mismo selector que el tema base: con uno más corto gana su lavanda, ilegible en oscuro.
  '&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-selectionBackground': {
    backgroundColor: 'var(--kw-cm-selection)',
  },
  '.cm-activeLine': { backgroundColor: 'var(--kw-cm-active)' },
  // La selección se dibuja debajo del texto y la línea activa, opaca, la taparía: mientras hay
  // algo seleccionado no se marca (como VS Code).
  '&.kw-selecting .cm-activeLine': { backgroundColor: 'transparent' },
  // La lista del autocompletado de [[enlaces]]: el gris claro del tema base no se lee en oscuro.
  '.cm-tooltip': { backgroundColor: 'var(--kw-bg)', color: 'var(--kw-text)', border: '1px solid var(--kw-border)', borderRadius: '6px' },
  '.cm-tooltip-autocomplete ul li[aria-selected]': { backgroundColor: 'var(--kw-primary)', color: 'var(--kw-on-primary)' },
  '.cm-tooltip-autocomplete ul li': { padding: '0.25rem 0.5rem' },
  '.cm-completionDetail': { marginLeft: '0.75rem', fontStyle: 'normal', color: 'inherit', opacity: '0.75', fontFamily: 'var(--kw-font-body)' },
});

const selecting = EditorView.editorAttributes.compute(['selection'], (s) => ({
  class: s.selection.ranges.some((r) => !r.empty) ? 'kw-selecting' : '',
}));

// Envuelve cada selección entre before y after (Ctrl+B, Ctrl+I y la barra de formato, §4.2).
// Sin selección escribe placeholder seleccionado, para tipear encima, o deja el cursor entre
// las marcas.
export function wrap(view: EditorView, before: string, after = before, placeholder = '') {
  view.dispatch(
    view.state.changeByRange((r) => {
      const inner = r.empty ? placeholder : view.state.sliceDoc(r.from, r.to);
      return {
        changes: { from: r.from, to: r.to, insert: before + inner + after },
        range: EditorSelection.range(r.from + before.length, r.from + before.length + inner.length),
      };
    }),
  );
  view.focus();
}

// Pone prefix al comienzo de cada línea tocada por la selección (listas, citas); si todas ya lo
// tienen, lo saca. prefix recibe el número de línea dentro de la selección, para "1. ", "2. "…
export function prefixLines(view: EditorView, prefix: (i: number) => string, has: RegExp) {
  const { state } = view;
  const { from, to } = state.selection.main;
  const first = state.doc.lineAt(from).number;
  const last = state.doc.lineAt(to).number;
  const lines = Array.from({ length: last - first + 1 }, (_, i) => state.doc.line(first + i));
  const remove = lines.every((l) => has.test(l.text));
  view.dispatch({
    changes: lines.map((l, i) => {
      const m = l.text.match(has);
      return remove && m ? { from: l.from, to: l.from + m[0].length } : { from: l.from, insert: prefix(i) };
    }),
  });
  view.focus();
}

// Encabezado: alterna la línea entre sección (##), subsección (###) y texto.
export function cycleHeading(view: EditorView) {
  const line = view.state.doc.lineAt(view.state.selection.main.head);
  const m = line.text.match(/^(#{1,6}) /);
  const next = !m ? '## ' : m[1] === '##' ? '### ' : '';
  view.dispatch({ changes: { from: line.from, to: line.from + (m ? m[0].length : 0), insert: next } });
  view.focus();
}

// Tamaño y posición de imagen como en Wiki.js (§4.4): ![texto](url =50%){.align-center}. El
// menú de la barra los escribe para que nadie tenga que recordarlos. Se trabaja sobre la imagen
// del cursor; si la línea tiene varias y el cursor no está sobre ninguna, la primera.
const IMAGE = /(!\[[^\]\n]*\]\([^\s()]+)(?: +=([^)\s]*))?\)(?:\{\.align-(left|center|right)\})?/g;

export type ImageAlign = '' | 'left' | 'center' | 'right';
export type ImageInfo = { size: string; align: ImageAlign; below: boolean };

const imageText = (head: string, size: string, align: string) => `${head}${size ? ` =${size}` : ''})${align ? `{.align-${align}}` : ''}`;

function imageAt(view: EditorView) {
  const { head } = view.state.selection.main;
  const line = view.state.doc.lineAt(head);
  const all = [...line.text.matchAll(IMAGE)];
  const m = all.find((x) => head >= line.from + x.index && head <= line.from + x.index + x[0].length) ?? all[0];
  return m && { m, line, from: line.from + m.index, to: line.from + m.index + m[0].length };
}

// La siguiente línea con texto, si tiene solo imágenes: la que "Lado a lado" sube a esta.
function imageLineBelow(view: EditorView, n: number) {
  const { doc } = view.state;
  for (let i = n + 1; i <= Math.min(n + 2, doc.lines); i++) {
    const l = doc.line(i);
    if (!l.text.trim()) continue;
    return l.text.replace(IMAGE, '').trim() === '' ? l : undefined;
  }
}

export function imageInfo(view: EditorView): ImageInfo | null {
  const at = imageAt(view);
  if (!at) return null;
  return { size: at.m[2] ?? '', align: (at.m[3] ?? '') as ImageAlign, below: !!imageLineBelow(view, at.line.number) };
}

export function setImage(view: EditorView, change: { size?: string; align?: ImageAlign }) {
  const at = imageAt(view);
  if (!at) return;
  const insert = imageText(at.m[1] ?? '', change.size ?? at.m[2] ?? '', change.align ?? at.m[3] ?? '');
  view.dispatch({ changes: { from: at.from, to: at.to, insert }, selection: { anchor: at.from + insert.length } });
  view.focus();
}

// Sube la imagen de abajo a la línea de esta, y las deja de 49 % cada una para que quepan
// juntas (con el espacio entre las dos); sin posición, que las separaría.
export function imagesBeside(view: EditorView) {
  const at = imageAt(view);
  const below = at && imageLineBelow(view, at.line.number);
  if (!at || !below) return;
  const joined = `${at.line.text} ${below.text.trim()}`.replace(IMAGE, (_, head) => imageText(head, '49%', ''));
  view.dispatch({ changes: { from: at.line.from, to: below.to, insert: joined } });
  view.focus();
}

// Envuelve las líneas enteras que toca la selección entre una línea before y una línea after
// (:::secret … :::), y deja seleccionado lo de adentro.
export function wrapLines(view: EditorView, before: string, after: string) {
  const { state } = view;
  const from = state.doc.lineAt(state.selection.main.from).from;
  const to = state.doc.lineAt(state.selection.main.to).to;
  const inner = state.sliceDoc(from, to);
  view.dispatch({
    changes: { from, to, insert: `${before}\n${inner}\n${after}` },
    selection: EditorSelection.range(from + before.length + 1, from + before.length + 1 + inner.length),
  });
  view.focus();
}

// Inserta un bloque en su propia línea: abre una si el cursor está a mitad de otra.
export function insertBlock(view: EditorView, text: string) {
  const { from, to } = view.state.selection.main;
  const before = from > view.state.doc.lineAt(from).from ? '\n' : '';
  const after = view.state.doc.lineAt(to).to > to ? '\n' : '';
  view.dispatch(view.state.replaceSelection(before + text + after));
  view.focus();
}

const mark = (m: string) => (view: EditorView) => (wrap(view, m), true);

// Sin tildes ni mayúsculas: "configuracion" encuentra "Configuración".
const fold = (s: string) => s.normalize('NFD').replace(/\p{M}/gu, '').toLowerCase();

// Autocompletado de [[enlaces]] (SPEC §15): al escribir [[ sugiere las páginas del área, por ruta
// o por título. Salen del árbol que el editor ya tiene, filtrado por el nivel de quien edita: no
// hay consulta nueva que pueda delatar otras (§17). Elegir una escribe la ruta y cierra los
// corchetes. El texto oculto ([[secret:) y el texto de un enlace (después de |) no se tocan.
function wikiLinks(pages: () => PageOption[]) {
  return (ctx: CompletionContext): CompletionResult | null => {
    const m = ctx.matchBefore(/\[\[[^\]\n|]*$/);
    if (!m || m.text.startsWith('[[secret:')) return null;
    const q = fold(m.text.slice(2).trim());
    const options = pages()
      .filter((p) => !q || fold(p.slug).includes(q) || fold(p.title).includes(q))
      .slice(0, 50)
      .map((p) => ({
        label: p.slug,
        detail: p.title,
        apply: (view: EditorView, _c: unknown, from: number, to: number) => {
          const close = view.state.sliceDoc(to, to + 2) === ']]' ? '' : ']]';
          view.dispatch({ changes: { from, to, insert: p.slug + close }, selection: { anchor: from + p.slug.length + 2 } });
        },
      }));
    return { from: m.from + 2, options, filter: false };
  };
}

// Editor Markdown con CodeMirror 6 (SPEC §4.2).
export function createEditor(parent: HTMLElement, doc: string, label: string, h: Handlers): EditorView {
  // Una sola fuente por editor: el autocompletado reconoce sus resultados por identidad de la
  // función, y una nueva en cada consulta lo deja pidiendo sin mostrar nunca la lista.
  const links = [{ autocomplete: wikiLinks(h.pages) }];
  return new EditorView({
    doc,
    parent,
    extensions: [
      EditorView.cspNonce.of(nonce),
      // Antes que basicSetup: Mod-i allí selecciona el nodo padre.
      keymap.of([
        { key: 'Mod-s', preventDefault: true, run: () => (h.onSave(), true) },
        { key: 'Mod-b', run: mark('**') },
        { key: 'Mod-i', run: mark('*') },
      ]),
      basicSetup,
      markdown(),
      theme,
      selecting,
      EditorState.languageData.of(() => links),
      syntaxHighlighting(highlight),
      EditorView.lineWrapping,
      EditorView.contentAttributes.of({ 'aria-label': label }),
      EditorView.updateListener.of((u) => u.docChanged && h.onChange(u.state.doc.toString())),
      EditorView.domEventHandlers({
        paste: (e) => {
          const files = filesOf(e.clipboardData?.files);
          if (!files.length) return false;
          e.preventDefault();
          h.onFiles(files);
          return true;
        },
        drop: (e) => {
          const files = filesOf(e.dataTransfer?.files);
          if (!files.length) return false;
          e.preventDefault();
          h.onFiles(files);
          return true;
        },
      }),
    ],
  });
}
