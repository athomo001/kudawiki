<script lang="ts">
  import type { Level } from '../lib/api';
  import { t } from '../lib/i18n/index.svelte';

  // Solo N2 y N3 llevan insignia: N1 es el nivel normal del área (SPEC §15).
  let { level }: { level?: Level } = $props();
</script>

{#if level && level > 1}
  <span class={`level level-${level}`} title={t(`level.${level}`)}>🔒 N{level}<span class="sr-only"> · {t(`level.${level}`)}</span></span>
{/if}
