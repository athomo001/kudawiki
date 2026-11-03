<script lang="ts">
  import { api, canEdit, canWrite, codeOf, type Area, type PageRef, type Proposal, type SealedRef, type TagCount } from '../lib/api';
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
  import Icon, { type IconName } from '../components/Icon.svelte';
  import Settings from './Settings.svelte';
  import Health from './Health.svelte';
  import Proposals from './Proposals.svelte';
  import Shared from './Shared.svelte';
  import Shares from './Shares.svelte';
  import Sealed from './Sealed.svelte';
  import SealedNew from './SealedNew.svelte';
  import NotFound from './NotFound.svelte';
  import SiteFooter from '../components/SiteFooter.svelte';

  // Layout de área (SPEC §15): árbol a la izquierda y la vista según la ruta.
  let { area, sub, selfId, onareachanged }: { area: Area; sub: string; selfId: string; onareachanged: () => void } = $props();

  let refs = $state<PageRef[]>([]);
  let tags = $state<TagCount[]>([]);
  let misses = $state<{ query: string; count: number }[]>([]);
  let error = $state('');
  const base = $derived(`/w/${area.slug}`);
  const view = $derived(sub.match(/^(p|edit|history|tags|reads|quiz|quizzes|shared|sealed)\/(.+)$/));
  const kind = $derived(view?.[1]);
  const pageSlug = $derived(view?.[2] ?? '');
  // Bandeja de propuestas: proposals, proposals/{id} y proposals/{id}/edit (SPEC §5.2).
  const proposalView = $derived(sub.match(/^proposals(?:\/([^/]+)(\/edit)?)?$/));
  let pending = $state(0);
  // Cuántos artículos le comparten otras áreas: "Compartido" aparece solo si hay alguno (§4.19).
  let sharedCount = $state(0);
  // Los sellados que quien mira puede ver (§6.1): van en el árbol con su ruta; el título va cifrado.
  let sealed = $state<SealedRef[]>([]);
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
    shared: 'shared.title',
    sealed: 'sealed.badge',
    search: 'search.open',
    new: 'page.new',
    history: 'page.history',
    edit: 'page.edit',
  };
  // Administrar área (diseño "Kudawiki — Administración", §15): la configuración del área se
  // agrupa en su propio menú, que reemplaza al árbol mientras se está ahí. Archivos, papelera,
  // revisión y lecturas son trabajo diario y siguen en la barra del área. El espacio personal
  // no tiene miembros ni configuración (§7).
  type AdminItem = { sub: string; label: Key; icon: IconName };
  const adminGroups = $derived.by((): { label: Key; items: AdminItem[] }[] => {
    if (area.role !== 'admin') return [];
    const templates: AdminItem = { sub: 'templates', label: 'templates.title', icon: 'template' };
    const supervision = {
      label: 'area_admin.group_supervision' as Key,
      items: [
        { sub: 'health', label: 'health.short', icon: 'pulse' },
        { sub: 'audit', label: 'audit.title', icon: 'list' },
      ] satisfies AdminItem[],
    };
    if (area.personal) return [{ label: 'area_admin.group_area', items: [templates] }, supervision];
    return [
      {
        label: 'area_admin.group_area',
        items: [
          { sub: 'settings', label: 'area_admin.general', icon: 'settings' },
          { sub: 'settings/publishing', label: 'area_admin.publishing', icon: 'check' },
          { sub: 'settings/content', label: 'area_admin.content', icon: 'history' },
          { sub: 'shares', label: 'shares.title', icon: 'link' },
          templates,
        ],
      },
      { label: 'area_admin.group_people', items: [{ sub: 'members', label: 'admin.members_link', icon: 'people' }] },
      supervision,
    ];
  });
  const adminHome = $derived(adminGroups[0]?.items[0]?.sub);
  const adminMode = $derived(adminGroups.some((g) => g.items.some((i) => i.sub === sub)));
  const adminContext = $derived(`${area.name} · ${t('area_admin.title')}`);
  const sections = new Map([
    ['settings', 'general'],
    ['settings/publishing', 'publishing'],
    ['settings/content', 'content'],
  ] as const);
  const settingsSection = $derived(sections.get(sub as 'settings'));

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

  async function loadSealed() {
    try {
      sealed = await api<SealedRef[]>('GET', `${base}/sealed`);
    } catch {
      sealed = [];
    }
  }

  async function loadShared() {
    if (area.personal) return;
    try {
      sharedCount = (await api<unknown[]>('GET', `${base}/shared`)).length;
    } catch {
      sharedCount = 0;
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
    sharedCount = 0;
    sealed = [];
    loadTree();
    loadPending();
    loadShared();
    loadSealed();
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
  {#if adminMode}
  <nav class="sidebar admin-nav" aria-label={t('area_admin.title')}>
    <div class="admin-nav-head">
      <span class="sidebar-title">{t('area_admin.title')}</span>
      <a class="admin-nav-back" href={base}><Icon name="back" size={14} /> {t('area_admin.back', { area: area.name })}</a>
    </div>
    {#each adminGroups as g (g.label)}
      <h2 class="admin-nav-group">{t(g.label)}</h2>
      <ul class="admin-nav-list">
        {#each g.items as i (i.sub)}
          <li>
            <a class="nav-item" href={`${base}/${i.sub}`} aria-current={sub === i.sub ? 'page' : undefined}><Icon name={i.icon} size={16} /> {t(i.label)}</a>
          </li>
        {/each}
      </ul>
    {/each}
  </nav>
  {:else}
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
    {#if sharedCount}
      <a class="nav-item" href={`${base}/shared`} aria-current={sub === 'shared' || kind === 'shared' ? 'page' : undefined}>
        <Icon name="link" size={16} /> {t('shared.title')}
      </a>
    {/if}
    <hr />
    <Tree nodes={buildTree(refs)} {base} current={pageSlug} />
    {#if sealed.length}
      <ul class="sealed-list" aria-label={t('sealed.list')}>
        {#each sealed as s (s.id)}
          <li>
            <a class="nav-item" href={`${base}/sealed/${s.id}`} aria-current={kind === 'sealed' && pageSlug === s.id ? 'page' : undefined}>
              <Icon name="lock" size={14} /> <span class="sealed-slug">{s.slug}</span>
            </a>
          </li>
        {/each}
      </ul>
    {/if}
    {#if canEdit(area)}
      <hr />
      <a class="nav-item" href={`${base}/files`} aria-current={sub === 'files' ? 'page' : undefined}><Icon name="files" size={16} /> {t('files.title')}</a>
      <a class="nav-item" href={`${base}/trash`} aria-current={sub === 'trash' ? 'page' : undefined}><Icon name="trash" size={16} /> {t('trash.title')}</a>
      <a class="nav-item" href={`${base}/sealed/new`} aria-current={sub === 'sealed/new' ? 'page' : undefined}><Icon name="lock" size={16} /> {t('sealed.new')}</a>
      <!-- En el espacio personal nada vence, y no hay miembros ni configuración que tocar (§7). -->
      {#if !area.personal}
        <a class="nav-item" href={`${base}/review`} aria-current={sub === 'review' ? 'page' : undefined}><Icon name="check" size={16} /> {t('review.nav')}</a>
        <a class="nav-item" href={`${base}/reads`} aria-current={sub === 'reads' || kind === 'reads' ? 'page' : undefined}><Icon name="list" size={16} /> {t('reading.title')}</a>
      {/if}
      {#if adminHome}
        <a class="nav-item" href={`${base}/${adminHome}`}><Icon name="settings" size={16} /> {t('area_admin.title')}</a>
      {/if}
    {/if}
  </nav>
  {/if}

  <main class="area-main">
    {#if sub !== '' && !adminMode}
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
    {:else if kind === 'sealed' && pageSlug !== 'new'}
      <Sealed id={pageSlug} inArea ondeleted={loadSealed} />
    {:else if kind === 'shared' && !area.personal}
      <Shared {area} id={pageSlug} onchanged={loadShared} />
    {:else if proposalView && canWrite(area) && !area.personal}
      <Proposals {area} id={proposalView[1] ?? ''} {selfId} onchanged={changed} />
    {:else}
      <div class={['view', adminMode && 'admin-view']}>
        {#if sub === ''}
          <AreaHome {area} {tags} {misses} />
        {:else if sub === 'search'}
          <SearchResults {area} />
        {:else if sub === 'shared' && !area.personal}
          <Shared {area} id="" onchanged={loadShared} />
        {:else if sub === 'sealed/new' && canEdit(area) && !area.audit}
          <SealedNew {area} oncreated={loadSealed} />
        {:else if sub === 'shares' && area.role === 'admin' && !area.personal}
          <Shares {area} context={adminContext} />
        {:else if kind === 'tags'}
          <TagPages {area} tag={decodeURIComponent(pageSlug)} />
        {:else if kind === 'history'}
          <History {area} slug={pageSlug} level={refs.find((r) => r.slug === pageSlug)?.level} onreverted={loadTree} />
        {:else if sub === 'templates' && area.role === 'admin'}
          <Templates endpoint={`${base}/templates`} context={adminContext} />
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
          <Audit endpoint={`${base}/audit`} context={adminContext} {base} />
        {:else if sub === 'health' && area.role === 'admin'}
          <Health endpoint={`${base}/health`} context={adminContext} />
        {:else if settingsSection && area.role === 'admin' && !area.personal}
          <Settings {area} section={settingsSection} onchanged={onareachanged} />
        {:else if sub === 'members' && area.role === 'admin' && !area.personal}
          <Members endpoint={`${base}/members`} context={adminContext} canGrantAdmin={false} {selfId} />
        {:else}
          <NotFound />
        {/if}
      </div>
    {/if}
    <SiteFooter />
  </main>
</div>
{/if}
