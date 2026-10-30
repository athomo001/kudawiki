<script lang="ts">
  import type { Snippet } from 'svelte';
  import { t } from '../lib/i18n/index.svelte';
  import Icon from './Icon.svelte';

  // Diálogo modal nativo: el navegador atrapa el foco, Esc lo cierra y el foco vuelve a quien lo abrió.
  // wide: para contenido de dos o tres columnas, como la guía de Markdown.
  let {
    title,
    open = $bindable(false),
    wide = false,
    children,
  }: { title: string; open?: boolean; wide?: boolean; children: Snippet } = $props();
  let dialog: HTMLDialogElement;

  $effect(() => {
    if (open && !dialog.open) dialog.showModal();
    else if (!open && dialog.open) dialog.close();
  });
</script>

<dialog bind:this={dialog} class={['modal', wide && 'wide']} aria-label={title} onclose={() => (open = false)}>
  <header class="modal-head">
    <h2>{title}</h2>
    <button type="button" class="icon-btn" aria-label={t('common.close')} title={t('common.close')} onclick={() => (open = false)}>
      <Icon name="close" />
    </button>
  </header>
  {#if open}{@render children()}{/if}
</dialog>
