<script lang="ts">
  import { api, ApiError, canEdit, codeOf, formatDate, timeAgo, type Announcement, type Area, type AreaStats, type Page, type TagCount } from '../lib/api';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';
  import { enhance } from '../lib/enhance';
  import Dialog from '../components/Dialog.svelte';
  import Icon from '../components/Icon.svelte';

  // Inicio del área (SPEC §4.17): la portada, los anuncios y la actividad. La portada es la
  // página "portada" del área: se escribe con el editor, tiene historial y nivel como cualquier
  // página. Los números los cuenta el servidor con el nivel de quien mira.
  let { area, tags, misses }: { area: Area; tags: TagCount[]; misses: { query: string; count: number }[] } = $props();

  const COVER = 'portada';
  const base = $derived(`/w/${area.slug}`);
  let cover = $state<Page | null>(null);
  let coverLoaded = $state(false);
  let news = $state<Announcement[]>([]);
  let stats = $state<AreaStats | null>(null);
  let error = $state('');
  let coverEl = $state<HTMLElement>();
  let newsEl = $state<HTMLElement>();

  let writing = $state(false);
  let title = $state('');
  let body = $state('');
  let busy = $state(false);
  let formError = $state('');
  let removing = $state<Announcement | null>(null);
  let confirming = $state(false);

  const fail = (e: unknown) => (error = errorText(codeOf(e)));
  const loadNews = () => api<Announcement[]>('GET', `${base}/announcements`).then((n) => (news = n));

  $effect(() => {
    cover = null;
    coverLoaded = false;
    news = [];
    stats = null;
    error = '';
    // Sin portada (o una que quien mira no puede leer) el servidor responde el 404 de siempre.
    api<Page>('GET', `${base}/pages/${COVER}`)
      .then((p) => (cover = p))
      .catch((e) => !(e instanceof ApiError && e.status === 404) && fail(e))
      .finally(() => (coverLoaded = true));
    loadNews().catch(fail);
    api<AreaStats>('GET', `${base}/stats`)
      .then((s) => (stats = s))
      .catch(fail);
  });

  // Avisos, pestañas, fórmulas y copiar código, como en el lector (SPEC §4.10).
  $effect(() => {
    void locale();
    if (cover && coverEl) enhance(coverEl);
  });
  $effect(() => {
    void locale();
    if (news.length && newsEl) enhance(newsEl);
  });

  function openWriter() {
    title = '';
    body = '';
    formError = '';
    writing = true;
  }

  async function publish(e: SubmitEvent) {
    e.preventDefault();
    if (busy) return;
    busy = true;
    formError = '';
    try {
      await api('POST', `${base}/announcements`, { title, body });
      writing = false;
      await loadNews();
    } catch (err) {
      formError = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }

  async function remove(e: SubmitEvent) {
    e.preventDefault();
    if (!removing) return;
    try {
      await api('DELETE', `${base}/announcements/${removing.id}`);
      confirming = false;
      await loadNews();
    } catch (err) {
      formError = errorText(codeOf(err));
    }
  }
</script>

<h1>{area.name}</h1>
{#if area.description}<p class="lead">{area.description}</p>{/if}
{#if error}<p class="error" role="alert">{error}</p>{/if}

{#if cover}
  <section class="area-cover" aria-label={t('area.cover')}>
    <!-- HTML saneado por bluemonday en el servidor, como en el lector (SPEC §4.1). -->
    <div class="prose" lang={cover.lang} bind:this={coverEl}>{@html cover.html}</div>
    {#if canEdit(area)}
      <a class="area-cover-edit" href={`${base}/edit/${COVER}`}><Icon name="edit" size={14} /> {t('area.cover_edit')}</a>
    {/if}
  </section>
{:else if coverLoaded && canEdit(area)}
  <p class="area-cover-empty">
    <a href={`${base}/new?path=${COVER}`}><Icon name="edit" size={14} /> {t('area.cover_write')}</a>
    <span class="muted">{t('area.cover_hint')}</span>
  </p>
{/if}
{#if stats && !stats.pages}<p class="muted">{t('area.empty')}</p>{/if}

{#if news.length || canEdit(area)}
  <section class="area-section" aria-labelledby="area-news">
    <div class="area-section-head">
      <h2 id="area-news">{t('area.announcements')}</h2>
      {#if canEdit(area)}
        <button type="button" class="secondary" onclick={openWriter}><Icon name="plus" size={14} /> {t('announce.new')}</button>
      {/if}
    </div>
    {#if news.length}
      <ul class="news" bind:this={newsEl}>
        {#each news as n (n.id)}
          <li class="news-item">
            <h3>{n.title}</h3>
            <p class="news-meta">
              <time datetime={n.created_at} title={formatDate(n.created_at, locale())}>{timeAgo(n.created_at, locale())}</time>
              · {n.author}
              {#if n.can_delete}
                · <button type="button" class="link" onclick={() => ((removing = n), (formError = ''), (confirming = true))}>{t('announce.delete')}</button>
              {/if}
            </p>
            <div class="prose">{@html n.html}</div>
          </li>
        {/each}
      </ul>
    {:else}
      <p class="muted">{t('announce.none')}</p>
    {/if}
  </section>
{/if}

{#if stats?.pages}
  <section class="area-section" aria-labelledby="area-activity">
    <h2 id="area-activity">{t('area.activity')}</h2>
    <dl class="stat-row">
      <div><dt>{t('stats.pages')}</dt><dd>{stats.pages}</dd></div>
      <div><dt>{t('stats.changes')}</dt><dd>{stats.changes_30d}</dd></div>
      <div><dt>{t('stats.new')}</dt><dd>{stats.new_30d}</dd></div>
    </dl>
    <div class="stat-lists">
      <section aria-labelledby="area-recent">
        <h3 id="area-recent">{t('stats.recent')}</h3>
        <ol class="plain">
          {#each stats.recent as p (p.slug)}
            <li>
              <a href={`${base}/p/${p.slug}`}>{p.icon ? `${p.icon} ` : ''}{p.title}</a>
              <span class="muted">· <time datetime={p.updated_at} title={formatDate(p.updated_at, locale())}>{timeAgo(p.updated_at, locale())}</time></span>
            </li>
          {/each}
        </ol>
      </section>
      {#if stats.top_viewed.length}
        <section aria-labelledby="area-top">
          <h3 id="area-top">{t('stats.top')}</h3>
          <ol class="plain">
            {#each stats.top_viewed as p (p.slug)}
              <li>
                <a href={`${base}/p/${p.slug}`}>{p.icon ? `${p.icon} ` : ''}{p.title}</a>
                <span class="muted">· {t('stats.views', { n: String(p.views) })}</span>
              </li>
            {/each}
          </ol>
        </section>
      {/if}
    </div>
  </section>
{/if}

{#if tags.length}
  <h2>{t('area.tags')}</h2>
  <ul class="tags">
    {#each tags as tg (tg.tag)}
      <li><a href={`${base}/tags/${encodeURIComponent(tg.tag)}`}>{tg.tag}</a> <span class="muted">{tg.count}</span></li>
    {/each}
  </ul>
{/if}
{#if misses.length}
  <h2>{t('search.misses')}</h2>
  <ul class="plain">
    {#each misses as m (m.query)}<li>{m.query} <span class="muted">· {m.count}</span></li>{/each}
  </ul>
{/if}

{#if canEdit(area)}
  <Dialog title={t('announce.new')} bind:open={writing}>
    <form class="stack" onsubmit={publish}>
      <label class="field">
        {t('announce.title')}
        <input required maxlength="200" bind:value={title} />
      </label>
      <label class="field">
        {t('announce.body')}
        <textarea rows="6" bind:value={body}></textarea>
      </label>
      <small class="muted">{t('announce.audience')}</small>
      {#if formError}<p class="error" role="alert">{formError}</p>{/if}
      <button type="submit" class="primary" disabled={busy}>{t('announce.publish')}</button>
    </form>
  </Dialog>
  <Dialog title={t('announce.delete')} bind:open={confirming}>
    <form class="stack" onsubmit={remove}>
      <p>{t('announce.delete_confirm', { title: removing?.title ?? '' })}</p>
      {#if formError}<p class="error" role="alert">{formError}</p>{/if}
      <button type="submit" class="primary danger-btn">{t('announce.delete')}</button>
    </form>
  </Dialog>
{/if}
