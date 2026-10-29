<script lang="ts">
  import { api, codeOf, type Area, type PageRef } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';

  let { area, tag }: { area: Area; tag: string } = $props();

  let pages = $state<PageRef[] | null>(null);
  let error = $state('');

  $effect(() => {
    pages = null;
    api<PageRef[]>('GET', `/w/${area.slug}/tags/${encodeURIComponent(tag)}`)
      .then((p) => (pages = p))
      .catch((e) => (error = errorText(codeOf(e))));
  });
</script>

<h1>{t('tag.title', { tag })}</h1>
{#if error}<p class="error" role="alert">{error}</p>{/if}
{#if pages?.length}
  <ul class="plain">
    {#each pages as p (p.slug)}
      <li>
        <a href={`/w/${area.slug}/p/${p.slug}`}>{#if p.icon}{p.icon} {/if}{p.title}</a> <span class="muted">/{p.slug}</span>
        {#if p.description}<br /><span class="muted small">{p.description}</span>{/if}
      </li>
    {/each}
  </ul>
{:else if pages}
  <p class="muted">{t('tag.empty')}</p>
{/if}
