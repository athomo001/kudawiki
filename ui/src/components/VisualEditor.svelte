<script lang="ts">
  import { onMount } from 'svelte';
  import { Editor, rootCtx, defaultValueCtx, remarkStringifyOptionsCtx } from '@milkdown/kit/core';
  import {
    commonmark,
    toggleStrongCommand,
    toggleEmphasisCommand,
    wrapInHeadingCommand,
    wrapInBulletListCommand,
    wrapInOrderedListCommand,
    wrapInBlockquoteCommand,
    createCodeBlockCommand,
    insertHrCommand,
    toggleInlineCodeCommand,
  } from '@milkdown/kit/preset/commonmark';
  import { gfm, toggleStrikethroughCommand, insertTableCommand } from '@milkdown/kit/preset/gfm';
  import { history } from '@milkdown/kit/plugin/history';
  import { listener, listenerCtx } from '@milkdown/kit/plugin/listener';
  import { clipboard } from '@milkdown/kit/plugin/clipboard';
  import { callCommand, getMarkdown, insert } from '@milkdown/kit/utils';
  import { toVisual, fromVisual } from '../lib/visual';
  import { t, type Key } from '../lib/i18n/index.svelte';
  import Icon, { type IconName } from './Icon.svelte';

  // Modo visual del editor (SPEC §4.2): Milkdown, que lee y escribe Markdown. Lo que escribe
  // vuelve a pasar por fromVisual, así [[enlaces]], avisos y directivas quedan como el render los
  // entiende. Pegar o arrastrar archivos los sube como media, igual que en el modo Markdown (§4.4).
  let {
    markdown,
    label,
    onchange,
    onfiles,
    onimage,
  }: {
    markdown: string;
    label: string;
    onchange: (md: string) => void;
    onfiles: (files: File[]) => void;
    onimage: () => void;
  } = $props();

  let host: HTMLDivElement;
  let editor: Editor | undefined;

  // El Markdown que el modo visual tenía al abrirse, ya normalizado por remark.
  let baseline = '';
  const current = (e: Editor) => fromVisual(e.action(getMarkdown())).replace(/\n+$/, '\n');

  // flush entrega ya lo último escrito: el listener de Milkdown avisa con retardo, y cambiar de
  // modo o guardar justo después de teclear lo perdía. Si nada cambió no entrega nada: una página
  // que no se tocó no pasa por la normalización de remark.
  export function flush() {
    if (!editor) return;
    const md = current(editor);
    if (md !== baseline) onchange(md);
  }

  // insertMarkdown escribe en el cursor: imágenes subidas, archivos elegidos y bloques de la guía.
  export function insertMarkdown(md: string) {
    editor?.action(insert(toVisual(md)));
  }

  onMount(() => {
    let alive = true;
    Editor.make()
      .config((ctx) => {
        ctx.set(rootCtx, host);
        ctx.set(defaultValueCtx, toVisual(markdown));
        // Las mismas marcas que escribe la barra del modo Markdown: el diff de la revisión no se
        // llena de cambios de "*" por "-".
        ctx.update(remarkStringifyOptionsCtx, (o) => ({ ...o, bullet: '-' as const, emphasis: '*' as const, strong: '*' as const, rule: '-' as const }));
        ctx.get(listenerCtx).markdownUpdated((_, md) => onchange(fromVisual(md).replace(/\n+$/, '\n')));
      })
      .use(commonmark)
      .use(gfm)
      .use(history)
      .use(clipboard)
      .use(listener)
      .create()
      .then((e) => {
        if (!alive) return e.destroy();
        editor = e;
        baseline = current(e);
      });
    return () => {
      alive = false;
      editor?.destroy();
    };
  });

  type Tool = { key: Key; icon?: IconName; text?: string; cls?: string; run: () => void };
  const tools: Tool[] = [
    { key: 'toolbar.heading', text: 'H', cls: 'tb-strong', run: () => editor?.action(callCommand(wrapInHeadingCommand.key, 2)) },
    { key: 'toolbar.bold', text: 'B', cls: 'tb-strong', run: () => editor?.action(callCommand(toggleStrongCommand.key)) },
    { key: 'toolbar.italic', text: 'I', cls: 'tb-italic', run: () => editor?.action(callCommand(toggleEmphasisCommand.key)) },
    { key: 'toolbar.strike', text: 'S', cls: 'tb-strike', run: () => editor?.action(callCommand(toggleStrikethroughCommand.key)) },
    { key: 'toolbar.wikilink', icon: 'link', run: () => insertMarkdown('[[ruta/de/la-pagina]]') },
    { key: 'toolbar.bullets', icon: 'bullets', run: () => editor?.action(callCommand(wrapInBulletListCommand.key)) },
    { key: 'toolbar.numbers', icon: 'numbers', run: () => editor?.action(callCommand(wrapInOrderedListCommand.key)) },
    { key: 'toolbar.quote', icon: 'quote', run: () => editor?.action(callCommand(wrapInBlockquoteCommand.key)) },
    { key: 'toolbar.code', text: '`x`', cls: 'tb-mono', run: () => editor?.action(callCommand(toggleInlineCodeCommand.key)) },
    { key: 'toolbar.codeblock', icon: 'code', run: () => editor?.action(callCommand(createCodeBlockCommand.key)) },
    { key: 'toolbar.rule', icon: 'rule', run: () => editor?.action(callCommand(insertHrCommand.key)) },
    { key: 'toolbar.table', icon: 'grid', run: () => editor?.action(callCommand(insertTableCommand.key)) },
  ];

  function files(e: ClipboardEvent | DragEvent) {
    const list = 'clipboardData' in e ? e.clipboardData?.files : e.dataTransfer?.files;
    if (!list?.length) return;
    e.preventDefault();
    e.stopPropagation();
    onfiles([...list]);
  }
</script>

<div class="visual">
  <div class="toolbar" role="group" aria-label={t('toolbar.label')}>
    {#each tools as tool (tool.key)}
      <button type="button" class={`tb ${tool.cls ?? ''}`} title={t(tool.key)} aria-label={t(tool.key)} onmousedown={(e) => e.preventDefault()} onclick={tool.run}>
        {#if tool.icon}<Icon name={tool.icon} size={16} />{:else}{tool.text}{/if}
      </button>
    {/each}
    <button type="button" class="tb" title={t('toolbar.image')} aria-label={t('toolbar.image')} onclick={onimage}><Icon name="image" size={16} /></button>
  </div>
  <!-- Milkdown pone el contenteditable adentro; el rótulo lo da el contenedor. -->
  <div class="visual-host prose" role="group" aria-label={label} bind:this={host} onpastecapture={files} ondropcapture={files}></div>
</div>
