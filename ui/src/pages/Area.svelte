<script lang="ts">
  import { api, canEdit, canWrite, codeOf, type Area, type PageRef, type Proposal, type TagCount } from '../lib/api';
  import { t, errorText, type Key } from '../lib/i18n/index.svelte';
  import Tree, { buildTree } from '../components/Tree.svelte';
  import Reader from './Reader.svelte';
  import AreaHome from './AreaHome.svelte';
  import History from './History.svelte';
  import Members from './Members.svelte';
  import TagPages from './TagPages.svelte';
  import SearchResults from './SearchResults.svelte';
  import Trash from './Trash.svelte';
  import Files from './Files.svelte';
  import Templates from './Templates.svelte';
  import Review from './Review.svelte';
  import ReadRequests from './ReadRequests.svelte';
  import QuizTake from './QuizTake.svelte';
  import QuizEditor from './QuizEditor.svelte';
  import Audit from './Audit.svelte';
  import Icon from '../components/Icon.svelte';
  import Settings from './Settings.svelte';
  import Health from './Health.svelte';
  import Proposals from './Proposals.svelte';
  import NotFound from './NotFound.svelte';
  import SiteFooter from '../components/SiteFooter.svelte';

  // Layout de área (SPEC §15): árbol a la izquierda y la vista según la ruta.
  let { area, sub, selfId, onareachanged }: { area: Area; sub: string; selfId: string; onareachanged: () => void } = $props();

  let refs = $state<PageRef[]>([]);
  let tags = $state<TagCount[]>([]);
  let misses = $state<{ query: string; count: number }[]>([]);
  let error = $state('');
  const base = $derived(`/w/${area.slug}`);
  const view = $derived(sub.match(/^(p|edit|history|tags|reads|quiz|quizzes)\/(.+)$/));
  const kind = $derived(view?.[1]);
  const pageSlug = $derived(view?.[2] ?? '');
  // Bandeja de propuestas: proposals, proposals/{id} y proposals/{id}/edit (SPEC §5.2).
  const proposalView = $derived(sub.match(/^proposals(?:\/([^/]+)(\/edit)?)?$/));
  let pending = $state(0);
  const titles = $derived(new Map(refs.map((r) => [r.slug, r.title])));

  // Migas de pan (SPEC §15): área / carpetas / página. Una carpeta que no es página va sin enlace.
  const crumbs = $derived.by(() => {
    if (kind !== 'p' && kind !== 'history' && kind !== 'edit') return [];
    const parts = pageSlug.split('/');
    return parts.map((seg, i) => {
      const slug = parts.slice(0, i + 1).join('/');
      return { slug, label: titles.get(slug) ?? seg };
    });
  });
  // Lo que se ve, si no es una página: va al final de las migas.
  const viewKeys: Record<string, Key | undefined> = {
    trash: 'trash.title',
    files: 'files.title',
    templates: 'templates.title',
    review: 'review.nav',
    reads: 'reading.title',
    quiz: 'quiz.title',
    quizzes: 'quiz.title',
    audit: 'audit.title',
    members: 'admin.members_link',
    settings: 'settings.title',
    health: 'health.short',
    search: 'search.open',
    new: 'page.new',
    history: 'page.history',
    edit: 'page.edit',
  };
  const viewKey = $derived(proposalView ? 'proposals.title' : viewKeys[kind ?? sub]);
  const viewLabel = $derived(kind === 'tags' ? `#${decodeURIComponent(pageSlug)}` : viewKey ? t(viewKey) : '');

  // Un lector entra al editor solo para proponer (§19.5); una página nueva no la propone.
  const editing = $derived((sub === 'new' && canEdit(area)) || (kind === 'edit' && canWrite(area)) || !!proposalView?.[2]);
  // CodeMirror pesa: se descarga solo al abrir el editor, no para leer.
  const loadEditor = () => import('./Editor.svelte');

  async function loadTree() {
    try {
      [refs, tags] = await Promise.all([api<PageRef[]>('GET', `${base}/tree`), api<TagCount[]>('GET', `${base}/tags`)]);
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }

  // El número junto a "Propuestas" es lo que espera al admin (diseño aprobado).
  async function loadPending() {
    if (area.role !== 'admin' || area.personal) return;
    try {
      pending = (await api<Proposal[]>('GET', `${base}/proposals?view=pending`)).length;
    } catch {
      pending = 0;
    }
  }

  function changed() {
    loadTree();
    loadPending();
  }

  // Al cambiar de área se recarga todo: nada del área anterior queda en pantalla (SPEC §15).
  $effect(() => {
    refs = [];
    misses = [];
    pending = 0;
    loadTree();
    loadPending();
    // "Lo que buscan y no encuentran" es solo para el admin del área (SPEC §9.6).
    if (area.role === 'admin') api<typeof misses>('GET', `${base}/search-misses`).then((m) => (misses = m)).catch(() => {});
  });
</script>

{#if editing}
  {#await loadEditor() then { default: Editor }}
    {#key sub}<Editor {area} slug={kind === 'edit' ? pageSlug : undefined} proposal={proposalView?.[1]} {selfId} {refs} onsaved={changed} />{/key}
  {/await}
{:else}
<div class="area">
  <nav class="sidebar" aria-label={t('nav.tree')}>
    <div class="sidebar-head">
      <span class="sidebar-title">{area.name}</span>
      {#if canEdit(area)}
        <a class="sidebar-icon" href={`${base}/new`} aria-label={t('page.new')} title={t('page.new')}><Icon name="plus" size={16} /></a>
      {/if}
    </div>
    <a class="nav-item" href={base} aria-current={sub === '' ? 'page' : undefined}><Icon name="home" size={16} /> {t(area.personal ? 'nav.home' : 'nav.area_home')}</a>
    {#if canWrite(area) && !area.personal}
      <a class="nav-item" href={`${base}/proposals`} aria-current={proposalView ? 'page' : undefined}>
        <Icon name="list" size={16} /> {t('proposals.title')}{#if pending}<span class="nav-count">{pending}</span>{/if}
      </a>
    {/if}
    <hr />
    <Tree nodes={buildTree(refs)} {base} current={pageSlug} />
    {#if canEdit(area)}
      <hr />
      <a class="nav-item" href={`${base}/files`} aria-current={sub === 'files' ? 'page' : undefined}><Icon name="files" size={16} /> {t('files.title')}</a>
      <a class="nav-item" href={`${base}/trash`} aria-current={sub === 'trash' ? 'page' : undefined}><Icon name="trash" size={16} /> {t('trash.title')}</a>
      <!-- En el espacio personal nada vence, y no hay miembros ni configuración que tocar (§7). -->
      {#if !area.personal}
        <a class="nav-item" href={`${base}/review`} aria-current={sub === 'review' ? 'page' : undefined}><Icon name="check" size={16} /> {t('review.nav')}</a>
        <a class="nav-item" href={`${base}/reads`} aria-current={sub === 'reads' || kind === 'reads' ? 'page' : undefined}><Icon name="list" size={16} /> {t('reading.title')}</a>
      {/if}
      {#if area.role === 'admin'}
        <a class="nav-item" href={`${base}/templates`} aria-current={sub === 'templates' ? 'page' : undefined}><Icon name="template" size={16} /> {t('templates.title')}</a>
        {#if !area.personal}
          <a class="nav-item" href={`${base}/members`} aria-current={sub === 'members' ? 'page' : undefined}><Icon name="people" size={16} /> {t('admin.members_link')}</a>
          <a class="nav-item" href={`${base}/settings`} aria-current={sub === 'settings' ? 'page' : undefined}><Icon name="settings" size={16} /> {t('settings.title')}</a>
        {/if}
        <a class="nav-item" href={`${base}/health`} aria-current={sub === 'health' ? 'page' : undefined}><Icon name="pulse" size={16} /> {t('health.short')}</a>
        <a class="nav-item" href={`${base}/audit`} aria-current={sub === 'audit' ? 'page' : undefined}><Icon name="list" size={16} /> {t('audit.title')}</a>
      {/if}
    {/if}
  </nav>

  <main class="area-main">
    {#if sub !== ''}
      <nav class="crumbs" aria-label={t('nav.breadcrumbs')}>
        <ol>
          <li><a href={base}>{area.name}</a></li>
          {#each crumbs as c, i (c.slug)}
            {@const current = kind === 'p' && i === crumbs.length - 1}
            <li>
              {#if titles.has(c.slug) && !current}<a href={`${base}/p/${c.slug}`}>{c.label}</a>
              {:else}<span aria-current={current ? 'page' : undefined}>{c.label}</span>{/if}
            </li>
          {/each}
          {#if viewLabel}<li><span aria-current="page">{viewLabel}</span></li>{/if}
        </ol>
      </nav>
    {/if}
    {#if area.audit}<p class="audit-banner" role="note">{t('personal.audit_banner')}</p>{/if}
    {#if error}<p class="error banner" role="alert">{error}</p>{/if}
    {#if kind === 'p'}
      <Reader {area} slug={pageSlug} {refs} onmoved={loadTree} />
    {:else if proposalView && canWrite(area) && !area.personal}
      <Proposals {area} id={proposalView[1] ?? ''} {selfId} onchanged={changed} />
    {:else}
      <div class="view">
        {#if sub === ''}
          <AreaHome {area} {tags} {misses} />
        {:else if sub === 'search'}
          <SearchResults {area} />
        {:else if kind === 'tags'}
          <TagPages {area} tag={decodeURIComponent(pageSlug)} />
        {:else if kind === 'history'}
          <History {area} slug={pageSlug} onreverted={loadTree} />
        {:else if sub === 'templates' && area.role === 'admin'}
          <Templates endpoint={`${base}/templates`} />
        {:else if sub === 'files' && canEdit(area)}
          <Files {area} />
        {:else if kind === 'quiz' && !area.personal}
          <QuizTake {area} id={pageSlug} />
        {:else if kind === 'quizzes' && canEdit(area) && !area.personal}
          <QuizEditor {area} id={pageSlug} />
        {:else if (sub === 'reads' || kind === 'reads') && canEdit(area) && !area.personal}
          <ReadRequests {area} id={kind === 'reads' ? pageSlug : ''} />
        {:else if sub === 'review' && canEdit(area) && !area.personal}
          <Review {area} />
        {:else if sub === 'trash' && canEdit(area)}
          <Trash {area} onrestored={loadTree} />
        {:else if sub === 'audit' && area.role === 'admin'}
          <Audit endpoint={`${base}/audit`} {base} />
        {:else if sub === 'health' && area.role === 'admin'}
          <Health endpoint={`${base}/health`} />
        {:else if sub === 'settings' && area.role === 'admin' && !area.personal}
          <Settings {area} onchanged={onareachanged} />
        {:else if sub === 'members' && area.role === 'admin' && !area.personal}
          <Members endpoint={`${base}/members`} areaName={area.name} canGrantAdmin={false} {selfId} />
        {:else}
          <NotFound />
        {/if}
      </div>
    {/if}
    <SiteFooter />
  </main>
</div>
{/if}
