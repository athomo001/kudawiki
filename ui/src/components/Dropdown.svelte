<script lang="ts">
  import type { Snippet } from 'svelte';
  import { route } from '../lib/router.svelte';

  // Menú desplegable de la barra superior y de la columna de acciones: se cierra con Esc
  // (devolviendo el foco al botón), con un clic afuera o al navegar.
  let {
    label,
    trigger,
    children,
    class: cls = '',
    align = 'end',
  }: { label: string; trigger: Snippet; children: Snippet; class?: string; align?: 'start' | 'end' } = $props();

  let open = $state(false);
  let root: HTMLElement;
  let button: HTMLButtonElement;

  export function close(focus = false) {
    open = false;
    if (focus) button.focus();
  }

  $effect(() => {
    void route.path;
    open = false;
  });

  $effect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => !root.contains(e.target as Node) && close();
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && close(true);
    addEventListener('pointerdown', onDown);
    addEventListener('keydown', onKey);
    return () => {
      removeEventListener('pointerdown', onDown);
      removeEventListener('keydown', onKey);
    };
  });
</script>

<div class="dropdown" bind:this={root}>
  <button bind:this={button} type="button" class={cls} aria-label={label} title={label} aria-expanded={open} onclick={() => (open = !open)}>
    {@render trigger()}
  </button>
  {#if open}
    <div class={`dropdown-panel align-${align}`}>{@render children()}</div>
  {/if}
</div>
