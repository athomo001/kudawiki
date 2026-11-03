<script lang="ts">
  import type { Snippet } from 'svelte';

  // Una sección de ajustes en dos columnas (SPEC §15, diseño "Kudawiki — Administración"): a la
  // izquierda qué es y para qué sirve, a la derecha los campos. En una pantalla de PC aprovecha
  // el ancho; en una angosta se apila. wide: la columna de los campos más ancha, para una tabla.
  let { title, description = '', wide = false, children }: { title: string; description?: string; wide?: boolean; children: Snippet } = $props();
  const id = $props.id();
</script>

<section class={["setting", wide && "setting-wide"]} aria-labelledby={id}>
  <header>
    <h2 {id}>{title}</h2>
    {#if description}<p>{description}</p>{/if}
  </header>
  <div class="setting-fields">{@render children()}</div>
</section>
