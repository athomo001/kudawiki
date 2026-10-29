<script lang="ts">
  import { untrack } from 'svelte';
  import { api, codeOf, type Area, type Facets, type SearchHit, type SearchResult } from '../lib/api';
  import { t, errorText, type Key } from '../lib/i18n/index.svelte';
  import { navigate, route } from '../lib/router.svelte';
  import Snippet from '../components/Snippet.svelte';

  // Página de resultados (SPEC §9.4): filtros con conteos a la izquierda, orden por relevancia o
  // por fecha, 20 por página. Los conteos solo cuentan lo que quien busca puede ver.
  let { area }: { area: Area } = $props();

  let q = $state(''); // lo que está en el cuadro, mientras se escribe
  let current = $state(''); // la consulta de la URL, la que se buscó
  let sort = $state<'relevance' | 'date'>('relevance');
  let hits = $state<SearchHit[]>([]);
  let facets = $state<Facets | null>(null);
  let more = $state(false);
  let page = $state(1);
  let suggestion = $state('');
  let loading = $state(false);
  let error = $state('');
  const base = $derived(`/w/${area.slug}`);

  const url = (query: string, by = sort) => `${base}/search?q=${encodeURIComponent(query.trim())}${by === 'date' ? '&sort=date' : ''}`;

  async function load(reset: boolean) {
    loading = true;
    error = '';
    try {
      page = reset ? 1 : page + 1;
      const res = await api<SearchResult>('GET', `${url(current)}&page=${page}`);
      hits = reset ? res.results : [...hits, ...res.results];
      if (reset) facets = res.facets ?? null;
      more = !!res.more;
      suggestion = res.suggestion ?? '';
    } catch (e) {
      error = errorText(codeOf(e));
    } finally {
      loading = false;
    }
  }

  // Enviar o elegir un filtro cambia la URL, y la URL dispara la búsqueda; si no cambia, se repite a mano.
  function go(query: string, by = sort) {
    const to = url(query, by);
    if (to === location.pathname + location.search) load(true);
    else navigate(to, { replace: true });
  }

  function submit(e: SubmitEvent) {
    e.preventDefault();
    go(q);
  }

  // Solo la URL dispara la búsqueda: lo demás que lee load() (página, resultados) no debe re-ejecutarla.
  $effect(() => {
    const params = new URLSearchParams(route.query);
    const query = params.get('q') ?? '';
    const by = params.get('sort') === 'date' ? 'date' : 'relevance';
    untrack(() => {
      q = current = query;
      sort = by;
      if (current.trim()) load(true);
    });
  });

  // ----- Filtros: fichas de la misma consulta (§9.3) -----
  const tokenRe = /\S+:"[^"]*"?|"[^"]*"?|\S+/g;
  const tokens = (s: string): string[] => [...(s.match(tokenRe) ?? [])];
  const quote = (v: string) => (/\s/.test(v) ? `"${v}"` : v);
  const token = (key: string, value: string) => `${key}:${quote(value)}`;
  const isActive = (key: string, value: string) => tokens(current).includes(token(key, value));

  // single: ese filtro admite un solo valor (carpeta, autor, fecha); las etiquetas se suman.
  function toggle(key: string, value: string, single = true) {
    const tok = token(key, value);
    let list = tokens(current);
    if (list.includes(tok)) list = list.filter((x) => x !== tok);
    else {
      if (single) list = list.filter((x) => !x.startsWith(`${key}:`));
      list.push(tok);
    }
    go(list.join(' '));
  }

  const sinceDate = (days: number) => new Date(Date.now() - days * 86_400_000).toISOString().slice(0, 10);
  const dateKeys: Record<number, Key> = { 7: 'search.date_7', 30: 'search.date_30', 365: 'search.date_365' };
  const pageHref = (h: SearchHit) => `${base}/p/${h.slug}${h.section ? `#${encodeURIComponent(h.section.id)}` : ''}`;
  const hasFacets = $derived(!!facets && (facets.tags.length + facets.folders.length + facets.authors.length > 0 || facets.dates.some((d) => d.count)));
</script>

<form class="inline search-form" onsubmit={submit} role="search">
  <input type="search" bind:value={q} aria-label={t('search.label', { area: area.name })} aria-describedby="search-help" />
  <button type="submit" class="primary">{t('search.open')}</button>
</form>
<small id="search-help" class="muted">{t('search.help')}</small>

{#if current.trim()}
  <div class="results-head">
    <h1>{t('search.title', { q: current.trim() })}</h1>
    <label class="results-sort">
      {t('search.sort')}
      <select value={sort} onchange={(e) => go(current, e.currentTarget.value as 'relevance' | 'date')}>
        <option value="relevance">{t('search.sort_relevance')}</option>
        <option value="date">{t('search.sort_date')}</option>
      </select>
    </label>
  </div>
{/if}
{#if error}<p class="error" role="alert">{error}</p>{/if}
{#if suggestion}
  <p>{t('search.did_you_mean')} <a href={url(suggestion)}>{suggestion}</a>?</p>
{/if}

<div class="results-layout">
  {#if hasFacets && facets}
    <aside class="facets" aria-label={t('search.filters')}>
      {#snippet group(title: string, key: string, values: { value: string; count: number }[], single: boolean, label: (v: string) => string)}
        {#if values.length}
          <section>
            <h2 class="side-title">{title}</h2>
            <ul>
              {#each values as f (f.value)}
                <li>
                  <button type="button" class="facet" aria-pressed={isActive(key, f.value)} onclick={() => toggle(key, f.value, single)}>
                    <span class="facet-label">{label(f.value)}</span><span class="facet-count">{f.count}</span>
                  </button>
                </li>
              {/each}
            </ul>
          </section>
        {/if}
      {/snippet}
      {@render group(t('search.facet_folders'), 'path', facets.folders, true, (v) => v)}
      {@render group(t('search.facet_tags'), 'tag', facets.tags, false, (v) => `#${v}`)}
      {@render group(t('search.facet_authors'), 'autor', facets.authors, true, (v) => v)}
      {#if facets.dates.some((d) => d.count)}
        <section>
          <h2 class="side-title">{t('search.facet_dates')}</h2>
          <ul>
            {#each facets.dates.filter((d) => d.count) as d (d.days)}
              <li>
                <button type="button" class="facet" aria-pressed={isActive('desde', sinceDate(d.days))} onclick={() => toggle('desde', sinceDate(d.days))}>
                  <span class="facet-label">{t(dateKeys[d.days] ?? 'search.date_365')}</span><span class="facet-count">{d.count}</span>
                </button>
              </li>
            {/each}
          </ul>
        </section>
      {/if}
    </aside>
  {/if}

  <div class="results-main">
    {#if hits.length}
      <ol class="results">
        {#each hits as h (h.slug)}
          <li>
            <a href={pageHref(h)}><strong>{#if h.icon}{h.icon} {/if}{h.title}</strong></a>
            {#if h.section}<span class="search-section">› {h.section.text}</span>{/if}
            <span class="muted">/{h.slug}</span>
            {#if h.description}<p class="result-desc">{h.description}</p>{/if}
            {#if h.snippet.length}<p class="snippet"><Snippet parts={h.snippet} /></p>{/if}
          </li>
        {/each}
      </ol>
      {#if more}<button type="button" class="link" disabled={loading} onclick={() => load(false)}>{t('search.more')}</button>{/if}
    {:else if current.trim() && !loading}
      <p class="muted">{t('search.no_results')}</p>
    {/if}
  </div>
</div>
