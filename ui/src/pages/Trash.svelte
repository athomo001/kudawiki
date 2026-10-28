<script lang="ts">
  import { api, codeOf, formatDate, type Area, type TrashItem } from '../lib/api';
  import LevelBadge from '../components/LevelBadge.svelte';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';

  // Papelera (SPEC §4.5): el admin ve toda la del área; un editor, lo que él borró (§19.38).
  let { area, onrestored }: { area: Area; onrestored: () => void } = $props();

  let items = $state<TrashItem[]>([]);
  let restored = $state<{ slug: string; title: string } | null>(null);
  let busy = $state(false);
  let error = $state('');
  const base = $derived(`/w/${area.slug}`);

  async function load() {
    try {
      items = await api<TrashItem[]>('GET', `${base}/trash`);
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }

  async function restore(item: TrashItem) {
    busy = true;
    error = '';
    restored = null;
    try {
      await api('POST', `${base}/trash/${item.id}/restore`);
      restored = { slug: item.slug, title: item.title };
      onrestored();
      await load();
    } catch (e) {
      error = errorText(codeOf(e));
    } finally {
      busy = false;
    }
  }

  $effect(() => {
    items = [];
    load();
  });
</script>

<h1>{t('trash.title')}</h1>
<p class="muted">{t('trash.intro')}</p>
{#if error}<p class="error" role="alert">{error}</p>{/if}
{#if restored}
  <p role="status">{t('trash.restored')} <a href={`${base}/p/${restored.slug}`}>{restored.title}</a></p>
{/if}

{#if items.length}
  <table>
    <thead>
      <tr>
        <th scope="col">{t('page.title')}</th>
        <th scope="col">{t('trash.deleted')}</th>
        <th scope="col">{t('trash.purge')}</th>
        <th scope="col"><span class="sr-only">{t('common.actions')}</span></th>
      </tr>
    </thead>
    <tbody>
      {#each items as item (item.id)}
        <tr>
          <td>
            {item.title} <LevelBadge level={item.level} />
            <br /><small class="muted">{item.slug}{#if item.pages > 1} · {t('trash.pages', { n: String(item.pages) })}{/if}</small>
          </td>
          <td>{item.deleted_by} · {formatDate(item.deleted_at, locale())}</td>
          <td>{formatDate(item.purge_at, locale())}</td>
          <td><button type="button" class="link" disabled={busy} onclick={() => restore(item)}>{t('trash.restore')}</button></td>
        </tr>
      {/each}
    </tbody>
  </table>
{:else if !error}
  <p class="muted">{t('trash.empty')}</p>
{/if}
