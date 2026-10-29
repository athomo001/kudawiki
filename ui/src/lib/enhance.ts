// Mejora en el navegador el HTML ya saneado por el servidor (SPEC §4.10). Nada de esto
// agrega contenido del autor: solo da comportamiento a pestañas, fórmulas y diagramas.

import { t } from './i18n/index.svelte';

let tabGroup = 0;

// Avisos: el título sale de i18n, así el HTML del servidor no depende del idioma de quien lee.
// Se vuelve a escribir en cada llamada: al cambiar el idioma se traduce sin recargar (SPEC §15).
function callouts(root: HTMLElement) {
  for (const box of root.querySelectorAll<HTMLElement>('.callout')) {
    const kind = (['note', 'tip', 'warning', 'danger'] as const).find((k) => box.classList.contains(`callout-${k}`));
    if (!kind) continue;
    let title = box.querySelector<HTMLElement>(':scope > .callout-title[data-ui]');
    if (!title) {
      title = document.createElement('p');
      title.className = 'callout-title';
      title.dataset.ui = '';
      box.prepend(title);
    }
    title.textContent = t(`callout.${kind}`);
  }
}

// Enlace permanente por encabezado (SPEC §15): un "#" al lado, que aparece al pasar el mouse o
// al llegar con el teclado. Como los avisos, el texto accesible se vuelve a escribir en cada
// llamada para que siga al idioma.
function anchors(root: HTMLElement) {
  for (const h of root.querySelectorAll<HTMLElement>('h2[id], h3[id], h4[id]')) {
    let a = h.querySelector<HTMLAnchorElement>(':scope > a.heading-anchor');
    if (!a) {
      a = document.createElement('a');
      a.className = 'heading-anchor';
      a.href = `#${h.id}`;
      a.textContent = '#';
      h.append(a);
    }
    a.setAttribute('aria-label', t('page.permalink', { heading: h.firstChild?.textContent ?? '' }));
  }
}

// Pestañas: sin JS las secciones se leen una tras otra; con JS, patrón ARIA de tabs.
function tabs(root: HTMLElement) {
  for (const box of root.querySelectorAll<HTMLElement>('.tabs:not([data-ready])')) {
    box.dataset.ready = '';
    const panels = [...box.querySelectorAll<HTMLElement>(':scope > .tab')];
    const list = document.createElement('div');
    list.setAttribute('role', 'tablist');
    const id = `tabs-${++tabGroup}`;
    const buttons = panels.map((panel, i) => {
      const title = panel.querySelector(':scope > .tab-title');
      const b = document.createElement('button');
      b.type = 'button';
      b.id = `${id}-t${i}`;
      b.textContent = title?.textContent ?? String(i + 1);
      b.setAttribute('role', 'tab');
      b.setAttribute('aria-controls', `${id}-p${i}`);
      title?.remove();
      panel.id = `${id}-p${i}`;
      panel.setAttribute('role', 'tabpanel');
      panel.setAttribute('aria-labelledby', b.id);
      list.append(b);
      return b;
    });
    const select = (i: number) =>
      buttons.forEach((b, j) => {
        b.setAttribute('aria-selected', String(i === j));
        b.tabIndex = i === j ? 0 : -1;
        panels[j]!.hidden = i !== j;
      });
    buttons.forEach((b, i) => {
      b.addEventListener('click', () => select(i));
      b.addEventListener('keydown', (e) => {
        const step = { ArrowRight: 1, ArrowLeft: -1 }[e.key];
        if (!step) return;
        const next = (i + step + buttons.length) % buttons.length;
        select(next);
        buttons[next]!.focus();
      });
    });
    box.prepend(list);
    select(0);
  }
}

// Fórmulas con KaTeX como MathML: sin estilos en línea, compatible con la CSP (SPEC §14.5).
async function math(root: HTMLElement) {
  const nodes = root.querySelectorAll<HTMLElement>('.math-inline:not([data-ready]), .math-display:not([data-ready])');
  if (!nodes.length) return;
  const { default: katex } = await import('katex');
  for (const el of nodes) {
    el.dataset.ready = '';
    katex.render(el.textContent ?? '', el, {
      output: 'mathml',
      displayMode: el.classList.contains('math-display'),
      throwOnError: false,
    });
  }
}

// Diagramas: Mermaid necesita estilos en línea, así que dibuja dentro de un iframe aislado
// (sandbox sin allow-same-origin: no ve la sesión ni la API) con su propia CSP.
function mermaid(root: HTMLElement) {
  for (const pre of root.querySelectorAll<HTMLElement>('pre.mermaid:not([data-ready])')) {
    pre.dataset.ready = '';
    const frame = document.createElement('iframe');
    frame.className = 'mermaid-frame';
    frame.setAttribute('sandbox', 'allow-scripts');
    frame.setAttribute('title', t('page.diagram'));
    frame.src = '/sandbox/mermaid.html';
    const source = pre.textContent ?? '';
    const onMessage = (e: MessageEvent) => {
      if (!frame.isConnected) return removeEventListener('message', onMessage); // la vista se rehízo
      if (e.source !== frame.contentWindow) return;
      if (e.data?.type === 'ready') {
        const dark = matchMedia('(prefers-color-scheme: dark)').matches;
        frame.contentWindow?.postMessage({ type: 'render', source, dark }, '*'); // origen opaco: no hay otro destino
      } else if (e.data?.type === 'size' && typeof e.data.height === 'number') {
        frame.height = String(Math.min(Math.ceil(e.data.height), 4000));
      }
    };
    addEventListener('message', onMessage);
    pre.replaceWith(frame);
  }
}

export function enhance(root: HTMLElement | undefined) {
  if (!root) return;
  callouts(root);
  anchors(root);
  tabs(root);
  mermaid(root);
  void math(root);
}
