import { EditorView, basicSetup } from 'codemirror';
import { markdown } from '@codemirror/lang-markdown';
import { keymap } from '@codemirror/view';
import { HighlightStyle, syntaxHighlighting } from '@codemirror/language';
import { tags } from '@lezer/highlight';

type Handlers = {
  onChange: (value: string) => void;
  onSave: () => void;
  onFiles: (files: File[]) => void;
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
  '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection': {
    backgroundColor: 'var(--kw-cm-selection)',
  },
  '.cm-activeLine': { backgroundColor: 'var(--kw-cm-active)' },
});

// Editor Markdown con CodeMirror 6 (SPEC §4.2).
export function createEditor(parent: HTMLElement, doc: string, label: string, h: Handlers): EditorView {
  return new EditorView({
    doc,
    parent,
    extensions: [
      EditorView.cspNonce.of(nonce),
      keymap.of([{ key: 'Mod-s', preventDefault: true, run: () => (h.onSave(), true) }]),
      basicSetup,
      markdown(),
      theme,
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
