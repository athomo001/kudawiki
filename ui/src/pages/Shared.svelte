<script lang="ts">
  import { api, ApiError, codeOf, formatDate, timeAgo, type Area, type SharedArticle, type SharedItem } from '../lib/api';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';
  import { navigate } from '../lib/router.svelte';
  import { enhance } from '../lib/enhance';
  import { setDocumentTitle } from '../lib/site.svelte';
  import NotFound from './NotFound.svelte';

  // Compartido (§4.19): lo que otras áreas le comparten a esta, por área de origen, y cada
  // artículo en solo lectura y en su versión vigente. No hay ruta del origen ni enlaces a sus
  // otras páginas: el servidor ya los deja como texto. El admin del área puede quitarlo.
  let { area, id, onchanged }: { area: Area; id: string; onchanged: () => void } = $props();

  let items = $state<SharedItem[]>([]);
  let article = $state<SharedArticle | null>(null);
  let missing = $state(false);
  let error = $state('');
  let busy = $state(false);
  let prose = $state<HTMLElement>();
  const base = $derived(`/w/${area.slug}`);
  const isAdmin = $derived(area.role === 'admin');
  // Agrupado por área de origen, en el orden en que llegan (ya vienen ordenados por área).
  const groups = $derived.by(() => {
    const m = new Map<string, SharedItem[]>();
    for (const it of items) m.set(it.area.name, [...(m.get(it.area.name) ?? []), it]);
    return [...m];
  });

  $effect(() => {
    article = null;
    missing = false;
    error = '';
    if (id) {
      api<SharedArticle>('GET', `${base}/shared/${id}`)
        .then((a) => (article = a))
        .catch((e) => (e instanceof ApiError && e.status === 404 ? (missing = true) : (error = errorText(codeOf(e)))));
    } else {
      api<SharedItem[]>('GET', `${base}/shared`)
        .then((l) => (items = l))
        .catch((e) => (error = errorText(codeOf(e))));
    }
  });

  // Pestañas, fórmulas y diagramas, como en el lector (§4.10).
  $effect(() => {
    void locale();
    if (article && prose) enhance(prose);
  });

  $effect(() => {
    if (article) setDocumentTitle(article.title);
    return () => setDocumentTitle();
  });

  async function remove(target: string) {
    busy = true;
    error = '';
    try {
      await api('DELETE', `${base}/shared/${target}`);
      onchanged();
      if (id) navigate(`${base}/shared`);
      else items = items.filter((i) => i.id !== target);
    } catch (e) {
      error = errorText(codeOf(e));
    } finally {
      busy = false;
    }
  }
</script>

{#if missing}
  <NotFound />
{:else if id && article}
  <div class="reader">
    <article class="article" lang={article.lang}>
      <p class="article-badges" lang={locale()}>{t('shared.from', { area: article.area.name })} · {t('shared.read_only')}</p>
      <h1>{#if article.icon}<span class="page-icon" aria-hidden="true">{article.icon}</span>{/if}{article.title}</h1>
      {#if article.description}<p class="lead">{article.description}</p>{/if}
      <hr />
      {#if error}<p class="error" role="alert" lang={locale()}>{error}</p>{/if}
      <!-- HTML saneado por bluemonday en el servidor (SPEC §4.1), como en el lector. -->
      <div class="prose" bind:this={prose}>{@html article.html}</div>
    </article>
    <aside class="page-info" aria-label={t('page.info')}>
      {#if article.toc?.length}
        <nav aria-label={t('page.toc')}>
          <h2 class="side-title">{t('page.toc')}</h2>
          <ul lang={article.lang}>
            {#each article.toc as h (h.id)}
              <li class:sub={h.level === 3}><a href={`#${h.id}`}>{h.text}</a></li>
            {/each}
          </ul>
        </nav>
      {/if}
      <p class="page-meta">
        {#if article.owner}<span>{t('review.owner_line', { name: article.owner })}</span>{/if}
        <span>
          {t('page.edited_by', { name: article.updated_by })}
          <time datetime={article.updated_at} title={formatDate(article.updated_at, locale())}>{timeAgo(article.updated_at, locale())}</time>
        </span>
      </p>
      {#if isAdmin}
        <button type="button" class="link danger" disabled={busy} onclick={() => remove(article?.id ?? '')}>{t('shared.remove')}</button>
      {/if}
    </aside>
  </div>
{:else if !id}
  <h1>{t('shared.title')}</h1>
  <p class="muted">{t('shared.intro')}</p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#each groups as [from, list] (from)}
    <section aria-label={from}>
      <h2 class="side-title">{t('shared.from', { area: from })}</h2>
      <ul class="results">
        {#each list as it (it.id)}
          <li>
            <a href={`${base}/shared/${it.id}`}><strong>{#if it.icon}{it.icon} {/if}{it.title}</strong></a>
            {#if it.description}<p class="result-desc">{it.description}</p>{/if}
            <small class="muted">{timeAgo(it.updated_at, locale())}</small>
            {#if isAdmin}
              · <button type="button" class="link" disabled={busy} onclick={() => remove(it.id)}>{t('shared.remove')}</button>
            {/if}
          </li>
        {/each}
      </ul>
    </section>
  {:else}
    {#if !error}<p class="muted">{t('shared.empty')}</p>{/if}
  {/each}
{:else if error}
  <p class="error" role="alert">{error}</p>
{/if}
