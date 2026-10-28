<script lang="ts">
  import type { Me } from '../lib/api';
  import { t } from '../lib/i18n/index.svelte';

  let { me }: { me: Me } = $props();
</script>

<h1>{t('home.greeting', { name: me.full_name })}</h1>
<h2>{t('home.areas')}</h2>
{#if me.workspaces.length}
  <ul class="plain">
    {#each me.workspaces as w (w.slug)}
      <li>
        <a href={`/w/${w.slug}`}><strong>{w.name}</strong></a>
        <span class="muted">· {t(`role.${w.role}`)}</span>
        {#if w.role === 'admin'}· <a href={`/w/${w.slug}/members`}>{t('admin.members_link')}</a>{/if}
      </li>
    {/each}
  </ul>
{:else}
  <p class="muted">{t('home.no_areas')}</p>
{/if}
