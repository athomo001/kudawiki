<script lang="ts">
  import type { Snippet } from 'svelte';

  // Cabecera fija de las pantallas de configuración (SPEC §15, diseño "Kudawiki —
  // Administración"): dónde está, el título, para qué sirve y la acción principal, siempre a la
  // vista aunque la página sea larga. tabs: las pestañas de una página larga, fijas con ella.
  let {
    context,
    title,
    description = '',
    status,
    actions,
    tabs,
  }: { context: string; title: string; description?: string; status?: Snippet; actions?: Snippet; tabs?: Snippet } = $props();
</script>

<header class="page-head">
  <div class="page-head-row">
    <div class="page-head-titles">
      <p class="page-head-context">{context}</p>
      <h1>{title}{#if status}{@render status()}{/if}</h1>
      {#if description}<p class="page-head-desc">{description}</p>{/if}
    </div>
    {#if actions}<div class="page-head-actions">{@render actions()}</div>{/if}
  </div>
  {#if tabs}{@render tabs()}{/if}
</header>
