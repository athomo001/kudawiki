<script lang="ts">
  import { api, codeOf, formatDate, timeAgo, type Area, type ReviewItem } from '../lib/api';
  import LevelBadge from '../components/LevelBadge.svelte';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';

  // Páginas por revisar (§4.8): las vencidas de quien pregunta o, para el admin, las de toda el área.
  let { area }: { area: Area } = $props();

  let items = $state<ReviewItem[]>([]);
  let loaded = $state(false);
  let error = $state('');
  const base = $derived(`/w/${area.slug}`);
  const isAdmin = $derived(area.role === 'admin');

  $effect(() => {
    items = [];
    loaded = false;
    api<ReviewItem[]>('GET', `${base}/review-queue`)
      .then((list) => (items = list))
      .catch((e) => (error = errorText(codeOf(e))))
      .finally(() => (loaded = true));
  });
</script>

<h1>{t(isAdmin ? 'review.queue_area' : 'review.queue_mine')}</h1>
<p class="muted">{t(isAdmin ? 'review.queue_area_intro' : 'review.queue_mine_intro')}</p>
{#if error}<p class="error" role="alert">{error}</p>{/if}

{#if items.length}
  <table>
    <thead>
      <tr>
        <th scope="col">{t('page.title')}</th>
        {#if isAdmin}<th scope="col">{t('review.owner')}</th>{/if}
        <th scope="col">{t('review.due')}</th>
        <th scope="col">{t('review.last_verified')}</th>
      </tr>
    </thead>
    <tbody>
      {#each items as it (it.slug)}
        <tr>
          <td>
            <a href={`${base}/p/${it.slug}`}>{it.title}</a> <LevelBadge level={it.level} />
            <br /><small class="muted">{it.slug}</small>
          </td>
          {#if isAdmin}<td>{it.owner || '—'}</td>{/if}
          <td><time datetime={it.due_at} title={formatDate(it.due_at, locale())}>{timeAgo(it.due_at, locale())}</time></td>
          <td>{formatDate(it.verified_at, locale())}</td>
        </tr>
      {/each}
    </tbody>
  </table>
{:else if loaded && !error}
  <p class="muted">{t('review.queue_empty')}</p>
{/if}
