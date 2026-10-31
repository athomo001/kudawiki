<script lang="ts">
  import type { EditorView } from '@codemirror/view';
  import { cycleHeading, insertBlock, prefixLines, wrap, wrapLines } from '../lib/editor';
  import { t, type Key } from '../lib/i18n/index.svelte';
  import Icon, { type IconName } from './Icon.svelte';
  import Dropdown from './Dropdown.svelte';
  import ImageMenu from './ImageMenu.svelte';

  // Barra de formato del modo Markdown (SPEC §4.2, diseño aprobado "Editor"): encabezado,
  // negrita, cursiva, enlace a otra página, lista, bloque de código, imagen e "Insertar bloque",
  // más lo que el dueño pidió de la barra de Wiki.js y el texto oculto (§4.10). Solo escribe
  // sintaxis que el render del servidor entiende: Kudawiki no tiene subíndice ni superíndice.
  let { view, onimage, onfiles }: { view: () => EditorView | undefined; onimage: () => void; onfiles: () => void } = $props();

  type Tool = { key: Key; icon?: IconName; text?: string; cls?: string; keys?: string; run: (v: EditorView) => void };
  const tools: Tool[] = [
    { key: 'toolbar.heading', text: 'H', cls: 'tb-strong', run: cycleHeading },
    { key: 'toolbar.bold', text: 'B', cls: 'tb-strong', keys: 'Ctrl+B', run: (v) => wrap(v, '**') },
    { key: 'toolbar.italic', text: 'I', cls: 'tb-italic', keys: 'Ctrl+I', run: (v) => wrap(v, '*') },
    { key: 'toolbar.strike', text: 'S', cls: 'tb-strike', run: (v) => wrap(v, '~~') },
    { key: 'toolbar.wikilink', icon: 'link', run: (v) => wrap(v, '[[', ']]', 'ruta/de/la-pagina') },
    { key: 'toolbar.bullets', icon: 'bullets', run: (v) => prefixLines(v, () => '- ', /^- /) },
    { key: 'toolbar.numbers', icon: 'numbers', run: (v) => prefixLines(v, (i) => `${i + 1}. `, /^\d+\. /) },
    { key: 'toolbar.quote', icon: 'quote', run: (v) => prefixLines(v, () => '> ', /^> ?/) },
    { key: 'toolbar.code', text: '`x`', cls: 'tb-mono', run: (v) => wrap(v, '`') },
    { key: 'toolbar.codeblock', icon: 'code', run: (v) => wrap(v, '```\n', '\n```') },
    { key: 'toolbar.kbd', icon: 'keyboard', run: (v) => wrap(v, '[[', ']]', 'Ctrl+C') },
    { key: 'toolbar.rule', icon: 'rule', run: (v) => insertBlock(v, '---') },
    { key: 'toolbar.secret', icon: 'lock', run: secret },
  ];

  // "Insertar bloque": los mismos ejemplos de la guía de Markdown, en su propia línea.
  const blocks = ['callout', 'table', 'tabs', 'steps', 'details', 'toc', 'children', 'footnote', 'math', 'mermaid'] as const;
  let menu = $state<Dropdown>();

  // Aviso del candado (pedido del dueño): dejar contraseñas en la documentación va contra
  // ISO/IEC 27002:2022, control 5.17, y el texto oculto solo las tapa en pantalla (§4.10). No
  // bloquea: el texto se escribe igual y se sigue tecleando; el globo se cierra con "Entendido",
  // con Esc o con un clic en otro lado.
  let warning = $state(false);
  let bar: HTMLElement;
  let tip = $state<HTMLElement>();

  // [[secret:…]] es de una línea; con varias seleccionadas va el bloque :::secret (§4.10).
  function secret(v: EditorView) {
    const { from, to } = v.state.selection.main;
    if (v.state.sliceDoc(from, to).includes('\n')) wrapLines(v, ':::secret', ':::');
    else wrap(v, '[[secret:', ']]');
    warning = true;
  }

  // El globo arranca cerca del candado sin salirse de la barra, y la flecha apunta a su centro.
  // Se mide aquí y se pasa por CSSOM: la CSP no deja style= (§14.5).
  $effect(() => {
    if (!warning || !tip) return;
    const lock = bar.querySelector<HTMLElement>('[data-tool="toolbar.secret"]');
    if (lock) {
      const x = lock.offsetLeft + lock.offsetWidth / 2;
      const left = Math.max(0, Math.min(x - 32, bar.clientWidth - tip.offsetWidth));
      tip.style.setProperty('--tip-left', `${left}px`);
      tip.style.setProperty('--tip-x', `${x - left}px`);
    }
    const onDown = (e: PointerEvent) => !tip?.contains(e.target as Node) && !lock?.contains(e.target as Node) && (warning = false);
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && (warning = false);
    addEventListener('pointerdown', onDown);
    addEventListener('keydown', onKey);
    return () => {
      removeEventListener('pointerdown', onDown);
      removeEventListener('keydown', onKey);
    };
  });

  function use(run: (v: EditorView) => void) {
    const v = view();
    if (v) run(v);
  }

  function block(name: (typeof blocks)[number]) {
    menu?.close();
    use((v) => insertBlock(v, t(`mdguide.${name}.code`)));
  }
</script>

<div class="toolbar" role="group" aria-label={t('toolbar.label')} bind:this={bar}>
  {#each tools as tool (tool.key)}
    {@const label = tool.keys ? `${t(tool.key)} (${tool.keys})` : t(tool.key)}
    <button type="button" class={`tb ${tool.cls ?? ''}`} data-tool={tool.key} aria-label={label} title={label} onclick={() => use(tool.run)}>
      {#if tool.icon}<Icon name={tool.icon} size={16} />{:else}{tool.text}{/if}
    </button>
  {/each}
  <button type="button" class="tb" aria-label={t('toolbar.image')} title={t('toolbar.image')} onclick={onimage}><Icon name="image" size={16} /></button>
  <button type="button" class="tb" aria-label={t('toolbar.files')} title={t('toolbar.files')} onclick={onfiles}><Icon name="files" size={16} /></button>
  <ImageMenu {view} />
  <Dropdown bind:this={menu} label={t('toolbar.insert')} class="tb tb-text" align="start">
    {#snippet trigger()}{t('toolbar.insert')}{/snippet}
    <ul class="menu">
      {#each blocks as name (name)}
        <li><button type="button" class="menu-item" onclick={() => block(name)}>{t(`mdguide.${name}.desc`)}</button></li>
      {/each}
    </ul>
  </Dropdown>
  <!-- Siempre en el DOM, para que el lector de pantalla anuncie el aviso cuando aparece. -->
  <div class="tb-tip" role="status" bind:this={tip} hidden={!warning}>
    {#if warning}
      <p><strong>{t('secret.warn_title')}</strong> {t('secret.warn_body')}</p>
      <button type="button" class="link" onclick={() => (warning = false)}>{t('secret.warn_ok')}</button>
    {/if}
  </div>
</div>
