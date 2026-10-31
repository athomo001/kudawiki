// Mejora en el navegador el HTML ya saneado por el servidor (SPEC §4.10). Nada de esto
// agrega contenido del autor: solo da comportamiento a pestañas, fórmulas y diagramas.

import { t } from './i18n/index.svelte';
import { iconSvg } from '../components/Icon.svelte';

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

// Copiar un bloque de código con un clic (§4.10). El botón va en un envoltorio y no dentro del
// <pre>: no se corre con el código ancho ni se copia a sí mismo. El {@html} es el único hijo de
// .prose, así que Svelte lo reemplaza entero y el envoltorio no le estorba. Como los avisos, el
// texto accesible se vuelve a escribir en cada llamada para que siga al idioma.
function copyButtons(root: HTMLElement) {
  for (const pre of root.querySelectorAll<HTMLElement>('pre:not([data-ready])')) {
    pre.dataset.ready = '';
    const box = document.createElement('div');
    box.className = 'code-block';
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'code-copy';
    button.append(iconSvg('copy'), iconSvg('check'));
    const status = document.createElement('span');
    status.className = 'sr-only';
    status.setAttribute('role', 'status');
    let timer = 0;
    button.addEventListener('click', async () => {
      try {
        await navigator.clipboard.writeText((pre.textContent ?? '').replace(/\n$/, ''));
      } catch {
        // Sin contexto seguro (HTTP fuera de localhost) no hay portapapeles: queda seleccionado
        // para copiarlo con Ctrl+C.
        getSelection()?.selectAllChildren(pre);
        return;
      }
      box.classList.add('copied');
      status.textContent = t('code.copied');
      clearTimeout(timer);
      timer = setTimeout(() => {
        box.classList.remove('copied');
        status.textContent = '';
      }, 2000);
    });
    pre.replaceWith(box);
    box.append(button, pre, status);
  }
  for (const button of root.querySelectorAll<HTMLButtonElement>('.code-copy')) {
    button.setAttribute('aria-label', t('code.copy'));
    button.title = t('code.copy');
  }
}

// Tablas: en una columna angosta el código de una celda quedaba cortado en el guion
// ("{.is-" arriba y "success}" abajo). Ahora se corta solo donde se lee bien: en los espacios
// y después de cada "/", así una ruta larga baja por carpeta sin ensanchar la tabla. Cada
// tramo va en un span que no se parte; el texto que se copia o se busca es el mismo. Si aun así
// la tabla no cabe (un celular), se desplaza dentro de su caja en vez de correr la página.
function codeInCell(code: HTMLElement) {
  const nodes: Node[] = [];
  for (const word of (code.textContent ?? '').split(/(\s+)/)) {
    if (/^\s+$/.test(word)) nodes.push(document.createTextNode(word));
    else if (word)
      word.split(/(?<=\/)/).forEach((part, i) => {
        if (i) nodes.push(document.createElement('wbr'));
        const span = document.createElement('span');
        span.className = 'nobr';
        span.textContent = part;
        nodes.push(span);
      });
  }
  code.replaceChildren(...nodes);
}

function tables(root: HTMLElement) {
  for (const table of root.querySelectorAll<HTMLElement>('table:not([data-ready])')) {
    table.dataset.ready = '';
    for (const code of table.querySelectorAll<HTMLElement>('td code, th code')) codeInCell(code);
    const box = document.createElement('div');
    box.className = 'table-scroll';
    table.replaceWith(box);
    box.append(table);
  }
}

// Texto oculto [[secret:…]] (SPEC §4.10): se ve "[contenido censurado]" hasta un clic, y al
// copiar se copia eso en vez del valor, aunque esté a la vista. El CSS lo esconde desde el
// primer cuadro, antes de que llegue este botón. Ocultar en pantalla, no proteger: el valor
// viene en la página.
function secretLabel(button: HTMLButtonElement, shown: boolean) {
  button.setAttribute('aria-expanded', String(shown));
  button.setAttribute('aria-label', t(shown ? 'secret.hide' : 'secret.show'));
  button.title = t(shown ? 'secret.hide' : 'secret.show');
  const mask = button.querySelector('.secret-mask');
  if (mask) mask.textContent = t('secret.censored');
}

function secrets(root: HTMLElement) {
  for (const box of root.querySelectorAll<HTMLElement>('.secret:not([data-ready])')) {
    box.dataset.ready = '';
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'secret-toggle';
    const mask = document.createElement('span');
    mask.className = 'secret-mask';
    button.append(iconSvg('lock'), mask);
    button.addEventListener('click', () => secretLabel(button, box.toggleAttribute('data-shown')));
    box.prepend(button);
  }
  for (const button of root.querySelectorAll<HTMLButtonElement>('.secret-toggle')) {
    secretLabel(button, button.parentElement?.hasAttribute('data-shown') ?? false);
  }
  censorCopies();
}

let censoring = false;

// Al copiar una selección que toca un texto oculto, cada uno sale como "[contenido censurado]".
// Escucha en todo el documento: una selección que empieza fuera de la página (la barra, el
// menú) dispara el evento afuera y aun así puede llevar el valor a la vista. El texto se arma
// en el documento, fuera de la vista, para conservar los saltos de párrafo.
function censorCopies() {
  if (censoring) return;
  censoring = true;
  document.addEventListener('copy', (e) => {
    const range = getSelection()?.rangeCount ? getSelection()?.getRangeAt(0) : undefined;
    if (!range || !e.clipboardData) return;
    const common = range.commonAncestorContainer;
    const inside = (common instanceof Element ? common : common.parentElement)?.closest('.secret');
    if (!inside && ![...document.querySelectorAll('.secret')].some((s) => range.intersectsNode(s))) return;
    const box = document.createElement('div');
    box.className = 'sr-only';
    if (inside) box.textContent = t('secret.censored');
    else {
      box.append(range.cloneContents());
      for (const s of box.querySelectorAll('.secret')) s.replaceWith(t('secret.censored'));
    }
    document.body.append(box);
    e.clipboardData.setData('text/plain', box.innerText);
    e.clipboardData.setData('text/html', box.innerHTML);
    box.remove();
    e.preventDefault();
  });
}

export function enhance(root: HTMLElement | undefined) {
  if (!root) return;
  secrets(root);
  callouts(root);
  anchors(root);
  tabs(root);
  mermaid(root);
  copyButtons(root);
  tables(root);
  void math(root);
}
