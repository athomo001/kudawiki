<script lang="ts">
  import { site } from '../lib/site.svelte';
  import { t } from '../lib/i18n/index.svelte';

  // Pie discreto con la empresa y "Con Kudawiki" (§15.1.1). También sale al imprimir, con el
  // título de la wiki. "Con Kudawiki" no enlaza todavía: el dominio del producto sigue abierto
  // (SPEC §19.9).
</script>

{#if site.company || site.show_powered_by}
  <footer class="site-footer">
    <span class="print-only">{site.title}</span>
    {#if site.company}<span>{site.company}</span>{/if}
    {#if site.show_powered_by}<span>{t('site.powered_by')}</span>{/if}
  </footer>
{/if}
