<script lang="ts" module>
  import type { PageRef } from '../lib/api';

  // Visitadas hace poco, por área. Solo en memoria: se pierden al recargar o al cerrar sesión.
  const recent = new Map<string, PageRef[]>();

  export function remember(area: string, page: PageRef) {
    const list = (recent.get(area) ?? []).filter((p) => p.slug !== page.slug);
    recent.set(area, [page, ...list].slice(0, 5));
  }
</script>

<script lang="ts">
  import { onMount } from 'svelte';
  import { api, codeOf, type Area, type SearchHit, type SearchResult } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';
  import { navigate } from '../lib/router.svelte';
  import Snippet from './Snippet.svelte';
  import Icon from './Icon.svelte';

  // Búsqueda rápida con Ctrl/Cmd+K (SPEC §9.4): resultados en vivo con debounce de 150 ms.
  let { area }: { area: Area } = $props();

  let dialog: HTMLDialogElement;
  let input: HTMLInputElement;
  let q = $state('');
  let hits = $state<SearchHit[]>([]);
  let suggestion = $state('');
  let active = $state(0);
  let error = $state('');
  let timer: ReturnType<typeof setTimeout>;
  let seq = 0;

  const base = $derived(`/w/${area.slug}`);
  const items = $derived(q.trim().length < 2 ? (recent.get(area.slug) ?? []).map((p) => ({ ...p, snippet: [] })) : hits);

  export function open() {
    q = '';
    hits = [];
    active = 0;
    dialog.showModal();
    input.focus();
  }

  function search() {
    clearTimeout(timer);
    const query = q.trim();
    if (query.length < 2) return;
    timer = setTimeout(async () => {
      const mine = ++seq; // descarta respuestas viejas si se sigue escribiendo
      try {
        const res = await api<SearchResult>('GET', `${base}/search?q=${encodeURIComponent(query)}`);
        if (mine !== seq) return;
        hits = res.results.slice(0, 8);
        suggestion = res.suggestion ?? '';
        active = 0;
        error = '';
      } catch (e) {
        error = errorText(codeOf(e));
      }
    }, 150);
  }

  // Con sección, el enlace salta a ella (SPEC §9.4).
  const href = (item: { slug: string; section?: SearchHit['section'] }) =>
    `${base}/p/${item.slug}${item.section ? `#${encodeURIComponent(item.section.id)}` : ''}`;

  function go(to: string) {
    dialog.close();
    navigate(to);
  }

  function onkeydown(e: KeyboardEvent) {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      const n = items.length + 1; // la última opción es "ver todos los resultados"
      active = (active + (e.key === 'ArrowDown' ? 1 : n - 1)) % n;
    } else if (e.key === 'Enter') {
      e.preventDefault();
      const item = items[active];
      go(item ? href(item) : `${base}/search?q=${encodeURIComponent(q.trim())}`);
    }
  }

  onMount(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault();
        open();
      }
    };
    addEventListener('keydown', onKey);
    return () => removeEventListener('keydown', onKey);
  });
</script>

<button type="button" class="search-trigger" onclick={open}>
  <Icon name="search" size={16} />
  <span>{t('search.in_area', { area: area.name })}</span>
  <kbd>{t('search.shortcut')}</kbd>
</button>

<dialog bind:this={dialog} class="search-dialog" aria-label={t('search.label', { area: area.name })}>
  <input
    bind:this={input}
    bind:value={q}
    oninput={search}
    {onkeydown}
    type="search"
    placeholder={t('search.placeholder')}
    aria-label={t('search.label', { area: area.name })}
    aria-controls="search-results"
    aria-activedescendant={`search-opt-${active}`}
    role="combobox"
    aria-expanded="true"
  />
  <small class="muted">{t('search.help')}</small>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#if q.trim().length < 2 && items.length}<p class="side-title">{t('search.recent')}</p>{/if}
  <ul id="search-results" role="listbox">
    {#each items as item, i (item.slug)}
      <li id={`search-opt-${i}`} role="option" aria-selected={i === active}>
        <a href={href(item)} onclick={() => dialog.close()}>
          <strong>{item.title}</strong>
          {#if 'section' in item && item.section}<span class="search-section">› {item.section.text}</span>{/if}
          <span class="muted">/{item.slug}</span>
          {#if item.snippet.length}<span class="snippet"><Snippet parts={item.snippet} /></span>{/if}
        </a>
      </li>
    {/each}
    {#if q.trim().length >= 2}
      <li id={`search-opt-${items.length}`} role="option" aria-selected={active === items.length}>
        <a href={`${base}/search?q=${encodeURIComponent(q.trim())}`} onclick={() => dialog.close()}>{t('search.all_results')}</a>
      </li>
    {/if}
  </ul>
  {#if q.trim().length >= 2 && !hits.length && !suggestion}<p class="muted">{t('search.no_results')}</p>{/if}
  {#if suggestion}
    <p>{t('search.did_you_mean')} <button type="button" class="link" onclick={() => ((q = suggestion), search())}>{suggestion}</button>?</p>
  {/if}
</dialog>
