<script lang="ts">
  import { site, logoURL } from '../lib/site.svelte';
  import { scheme } from '../lib/scheme.svelte';
  import Logo from './Logo.svelte';

  // Logo y título de la wiki (§15.1): los de la empresa si los puso; si no, los de Kudawiki.
  // El logo es decorativo: el título va al lado y es el nombre accesible. En la barra manda su
  // color; fuera de ella, el modo de color elegido (§15, B4).
  let { onBar = false, size = 26 }: { onBar?: boolean; size?: number } = $props();
</script>

{#if onBar && logoURL(site.header_dark)}
  <img class="brand-logo" src={logoURL(site.header_dark)} alt="" />
{:else if !onBar && logoURL(false)}
  <img class="brand-logo" src={logoURL(scheme.current === 'dark')} alt="" />
{:else}
  <Logo {size} />
{/if}
{site.title}
