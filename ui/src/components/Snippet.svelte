<script lang="ts">
  // El fragmento llega en partes de texto: se pinta sin {@html}, así el contenido de una página
  // nunca se interpreta como marcado (SPEC §9.4).
  let { parts }: { parts: { text: string; hit?: boolean }[] } = $props();
</script>

{#each parts as p, i (i)}{#if p.hit}<mark>{p.text}</mark>{:else}{p.text}{/if}{/each}
