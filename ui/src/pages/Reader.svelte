<script lang="ts">
  import { api, ApiError, canEdit, canWrite, codeOf, formatDate, timeAgo, type Access, type Area, type Page, type PageRef } from '../lib/api';
  import Icon from '../components/Icon.svelte';
  import Dialog from '../components/Dialog.svelte';
  import Dropdown from '../components/Dropdown.svelte';
  import ReadAloud from '../components/ReadAloud.svelte';
  import FolderPicker from '../components/FolderPicker.svelte';
  import WatchMenu from '../components/WatchMenu.svelte';
  import ReviewDialog from '../components/ReviewDialog.svelte';
  import PublishDialog from '../components/PublishDialog.svelte';
  import ReadRequestDialog from '../components/ReadRequestDialog.svelte';
  import QuizSection from '../components/QuizSection.svelte';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';
  import { navigate, route } from '../lib/router.svelte';
  import { tick } from 'svelte';
  import { enhance } from '../lib/enhance';
  import { remember } from '../components/SearchBox.svelte';
  import { shortcuts } from '../lib/shortcuts';
  import NotFound from './NotFound.svelte';
  import { setDocumentTitle } from '../lib/site.svelte';

  // refs: el árbol del área, para anterior/siguiente entre hermanas (§15).
  let { area, slug, refs, onmoved }: { area: Area; slug: string; refs: PageRef[]; onmoved: () => void } = $props();
  let confirmDelete = $state(false);

  let page = $state<Page | null>(null);
  let backlinks = $state<PageRef[]>([]);
  let missing = $state(false);
  let error = $state('');
  let moveTo = $state('');
  let access = $state<Access | null>(null);
  let simulate = $state(0);

  // Lo que se abre desde la columna de acciones (SPEC §15).
  let showSource = $state(false);
  let showAccess = $state(false);
  let showMove = $state(false);
  let showDelete = $state(false);
  let showReview = $state(false);
  let showPublish = $state(false);
  let showAsk = $state(false);
  let showNewQuiz = $state(false);
  // Lo que falta confirmar de las solicitudes de lectura que quien lee tiene sobre la página (§4.14).
  const toConfirm = $derived(page?.reading.filter((r) => !r.confirmed_at) ?? []);
  // En el espacio personal (§7) no hay vigencia ni "quién puede ver esto": lo ve solo su dueño.
  const inArea = $derived(!area.personal);
  let more = $state<Dropdown>();

  function fromMenu(open: () => void) {
    more?.close();
    error = '';
    open();
  }

  // "¿Quién puede ver esto?" (SPEC §3.4), solo para admins del área: con simular, quién la leería con otro nivel.
  function openAccess() {
    simulate = 0;
    access = null;
    showAccess = true;
    loadAccess();
  }

  async function loadAccess() {
    try {
      access = await api<Access>('GET', `${base}/access/${slug}${simulate ? `?simulate=${simulate}` : ''}`);
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }
  let prose = $state<HTMLElement>();
  const base = $derived(`/w/${area.slug}`);

  // Pestañas, fórmulas y diagramas, cada vez que cambia el HTML o el idioma (SPEC §4.10).
  $effect(() => {
    void locale();
    if (page && prose) enhance(prose);
  });

  // Es común repetir el título como "# Título" al comienzo del Markdown: el lector ya lo muestra
  // arriba, así que ese primer encabezado se oculta cuando dice lo mismo (§15).
  const plain = (s: string) => s.normalize('NFD').replace(/[^\p{L}\p{N}]/gu, '').toLowerCase();
  $effect(() => {
    const first = page && prose?.firstElementChild;
    if (page && first?.tagName === 'H1') first.classList.toggle('dup-title', plain(first.textContent ?? '') === plain(page.title));
  });

  // Atajos (§15): E edita, G H abre el historial. Ctrl+K busca y Ctrl+S guarda en el editor.
  $effect(() =>
    shortcuts({
      e: () => page && canWrite(area) && navigate(`${base}/edit/${page.slug}`),
      'g h': () => page && navigate(`${base}/history/${page.slug}`),
    }),
  );

  // El índice resalta la sección que se está leyendo: la del último encabezado que ya pasó
  // por arriba de la pantalla (§15).
  let activeId = $state('');
  $effect(() => {
    if (!page || !prose) return;
    const heads = [...prose.querySelectorAll<HTMLElement>('h2[id], h3[id]')];
    const update = () => {
      let id = heads[0]?.id ?? '';
      // Al final de la página ya no se puede bajar más: las últimas secciones no llegarían
      // nunca arriba, así que se marca la última que se ve.
      const atEnd = innerHeight + scrollY >= document.documentElement.scrollHeight - 2;
      for (const h of heads) {
        const top = h.getBoundingClientRect().top;
        if (atEnd ? top > innerHeight : top > 120) break;
        id = h.id;
      }
      activeId = id;
    };
    update();
    addEventListener('scroll', update, { passive: true });
    return () => removeEventListener('scroll', update);
  });

  // Anterior y siguiente entre hermanas, en el orden del árbol (§15).
  const siblings = $derived.by(() => {
    if (!page) return { prev: undefined, next: undefined };
    const parent = page.slug.includes('/') ? page.slug.slice(0, page.slug.lastIndexOf('/') + 1) : '';
    const list = refs.filter((r) => r.slug.startsWith(parent) && !r.slug.slice(parent.length).includes('/'));
    const i = list.findIndex((r) => r.slug === page?.slug);
    return { prev: i > 0 ? list[i - 1] : undefined, next: i >= 0 ? list[i + 1] : undefined };
  });

  // La pestaña dice qué se lee (§15.1.1); al salir de la página vuelve al título de la wiki.
  $effect(() => {
    if (page) setDocumentTitle(page.title);
    return () => setDocumentTitle();
  });

  // Con #ancla (p. ej. desde la búsqueda, §9.4) se salta a esa sección cuando el contenido ya está.
  $effect(() => {
    const id = decodeURIComponent(route.hash.slice(1));
    if (page && prose && id) tick().then(() => document.getElementById(id)?.scrollIntoView());
  });

  $effect(() => {
    page = null;
    missing = false;
    api<Page>('GET', `${base}/pages/${slug}`)
      .then((p) => {
        page = p;
        moveTo = p.slug;
        remember(area.slug, { slug: p.slug, title: p.title });
        // Una visita para "más vistas" del inicio del área (§4.17); si falla, la página igual se lee.
        // El superadmin en modo auditoría solo lee (§7): no cuenta.
        if (!area.audit) api('POST', `${base}/views/${p.slug}`).catch(() => {});
        // El servidor redirigió desde una ruta vieja (alias): se corrige la URL sin otra entrada.
        if (p.slug !== slug) navigate(`${base}/p/${p.slug}`, { replace: true });
        return api<PageRef[]>('GET', `${base}/backlinks/${p.slug}`);
      })
      .then((refs) => (backlinks = refs ?? []))
      .catch((e) => (e instanceof ApiError && e.status === 404 ? (missing = true) : (error = errorText(codeOf(e)))));
  });

  // Recarga la página sin vaciarla, después de cambiar su vigencia (§4.8).
  async function refresh() {
    try {
      page = await api<Page>('GET', `${base}/pages/${slug}`);
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }

  // "Confirmo que lo leí" vale para la revisión que está en pantalla; si la página cambió entre
  // tanto, se recarga para confirmar lo que hay ahora.
  async function ack(id: string) {
    if (!page) return;
    error = '';
    try {
      await api('POST', `${base}/read-requests/${id}/ack`, { revision: page.revision });
      await refresh();
    } catch (e) {
      error = errorText(codeOf(e));
      if (codeOf(e) === 'page_changed') await refresh();
    }
  }

  async function verify() {
    error = '';
    try {
      await api('POST', `${base}/verify/${slug}`);
      await refresh();
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }

  async function move(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    try {
      await api('POST', `${base}/move/${slug}`, { to: moveTo.trim() });
      showMove = false;
      onmoved();
      navigate(`${base}/p/${moveTo.trim()}`);
    } catch (err) {
      error = errorText(codeOf(err));
    }
  }

  // Borrar manda la página y sus subpáginas a la papelera, de donde se puede restaurar (SPEC §4.5).
  async function remove(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    try {
      await api('DELETE', `${base}/pages/${slug}`);
      showDelete = false;
      onmoved();
      navigate(`${base}/trash`);
    } catch (err) {
      error = errorText(codeOf(err));
    }
  }
</script>

{#if missing}
  <div class="view"><NotFound /></div>
{:else if page}
  <div class="reader">
    <article class="article" lang={page.lang}>
      <p class={`article-badges level-${page.level}`} lang={locale()}>
        {page.level > 1 ? '🔒 ' : ''}{t('page.level_line', { level: t(`level.${page.level}`) })}
        <!-- La vigencia solo se muestra si la página vence: con el área apagada sería ruido (§4.8, §19.11). -->
        {#if page.review.due_at}
          <span class="review-badge" class:overdue={page.review.overdue} title={formatDate(page.review.verified_at, locale())}>
            {page.review.overdue
              ? `⚠️ ${t('review.badge_overdue', { ago: timeAgo(page.review.due_at, locale()) })}`
              : `✅ ${t('review.badge_ok', { ago: timeAgo(page.review.verified_at, locale()) })}`}
          </span>
        {/if}
      </p>
      <h1>{#if page.icon}<span class="page-icon" aria-hidden="true">{page.icon}</span>{/if}{page.title}</h1>
      {#if page.description}<p class="lead">{page.description}</p>{/if}
      <hr />
      {#if page.sent?.length}
        <!-- Lo que se mandó desde este borrador y en qué quedó (§7). -->
        <ul class="sent-list" aria-label={t('publish.sent_title')} lang={locale()}>
          {#each page.sent as s (s.id)}
            <li class={`sent-${s.status}`}>
              {t(`publish.sent_${s.status}`, { area: s.area.name })}
              {#if s.status === 'merged'}
                <a href={`/w/${s.area.slug}/p/${s.slug}`}>{t('publish.sent_open')}</a>
              {:else}
                <a href={`/w/${s.area.slug}/proposals/${s.id}`}>{t('publish.sent_proposal')}</a>
              {/if}
              {#if s.comment}<br /><small class="muted">«{s.comment}»</small>{/if}
            </li>
          {/each}
        </ul>
      {/if}
      {#each toConfirm as r (r.id)}
        <div class="read-notice" role="note" lang={locale()}>
          <p>
            {r.due_at
              ? t('reading.notice_due', { name: r.requested_by, date: formatDate(r.due_at, locale()) })
              : t('reading.notice', { name: r.requested_by })}
            <a href="#read-confirm">{t('reading.go_confirm')}</a>
          </p>
        </div>
      {/each}
      {#if page.review.overdue}
        <!-- Aviso fuerte para el dueño (o un admin), discreto para los demás (§4.8). -->
        <div class="review-notice" class:strong={page.review.can_review} role="note" lang={locale()}>
          {#if page.review.can_review}
            <p>{t('review.notice_owner', { ago: timeAgo(page.review.due_at ?? '', locale()) })}</p>
            <button type="button" class="primary" onclick={verify}>{t('review.verify')}</button>
          {:else}
            <p>{t('review.notice_reader', { ago: timeAgo(page.review.verified_at, locale()) })}</p>
          {/if}
        </div>
      {/if}
      {#if error && !showMove && !showDelete}<p class="error" role="alert" lang={locale()}>{error}</p>{/if}
      <!-- Único {@html} del lector: HTML saneado por bluemonday en el servidor (SPEC §4.1). -->
      <div class="prose" bind:this={prose}>{@html page.html}</div>
      {#if page.reading.length}
        <section id="read-confirm" class="read-confirm" aria-label={t('reading.confirm_title')} lang={locale()}>
          {#each page.reading as r (r.id)}
            {#if r.confirmed_at}
              <p class="muted">✅ {t('reading.confirmed', { ago: timeAgo(r.confirmed_at, locale()), n: String(r.confirmed_revision) })}</p>
            {:else}
              <p>{r.statement || t('reading.statement_default')}</p>
              {#if r.quiz_id}
                <!-- Con cuestionario, la lectura se cumple al aprobarlo (§4.14, §4.16). -->
                <a class="primary" href={`${base}/quiz/${r.quiz_id}`}>{t('quiz.take')}</a>
              {:else}
                <button type="button" class="primary" onclick={() => ack(r.id)}>{t('reading.confirm')}</button>
              {/if}
            {/if}
          {/each}
        </section>
      {/if}
      {#if inArea}<QuizSection {area} {page} bind:creating={showNewQuiz} />{/if}
      {#if siblings.prev || siblings.next}
        <nav class="pager" aria-label={t('page.pager')} lang={locale()}>
          {#if siblings.prev}<a href={`${base}/p/${siblings.prev.slug}`} rel="prev">← {siblings.prev.title}</a>{:else}<span></span>{/if}
          {#if siblings.next}<a href={`${base}/p/${siblings.next.slug}`} rel="next">{siblings.next.title} →</a>{/if}
        </nav>
      {/if}
    </article>

    <aside class="page-info" aria-label={t('page.info')}>
      {#if page.toc?.length}
        <nav aria-label={t('page.toc')}>
          <h2 class="side-title">{t('page.toc')}</h2>
          <ul lang={page.lang}>
            {#each page.toc as h (h.id)}
              <li class:sub={h.level === 3}><a href={`#${h.id}`} aria-current={h.id === activeId ? 'location' : undefined}>{h.text}</a></li>
            {/each}
          </ul>
        </nav>
      {/if}
      {#if page.tags?.length}
        <section aria-label={t('page.tags')}>
          <h2 class="side-title">{t('page.tags')}</h2>
          <ul class="tags">
            {#each page.tags as tag (tag)}<li><a href={`${base}/tags/${encodeURIComponent(tag)}`}>{tag}</a></li>{/each}
          </ul>
        </section>
      {/if}
      {#if backlinks.length}
        <nav aria-label={t('page.backlinks')}>
          <h2 class="side-title">{t('page.backlinks')}</h2>
          <ul>
            {#each backlinks as b (b.slug)}<li><a href={`${base}/p/${b.slug}`}>{b.title}</a></li>{/each}
          </ul>
        </nav>
      {/if}
      <p class="page-meta">
        {#if page.review.owner}<span>{t('review.owner_line', { name: page.review.owner })}</span>{/if}
        <span>
          {t('page.edited_by', { name: page.updated_by })}
          <time datetime={page.updated_at} title={formatDate(page.updated_at, locale())}>{timeAgo(page.updated_at, locale())}</time>
        </span>
        <span>{t('page.revision', { n: String(page.revision) })}</span>
      </p>
    </aside>

    <div class="page-actions" role="group" aria-label={t('page.actions')}>
      <!-- Un lector solo llega aquí si el área le deja proponer (§19.5): el mismo botón, otro nombre. -->
      {#if canWrite(area)}
        {@const label = canEdit(area) ? t('page.edit_label') : t('page.propose')}
        <a class="action action-primary" href={`${base}/edit/${page.slug}`} aria-label={label} aria-keyshortcuts="E" title={`${label} (E)`}>
          <Icon name="edit" />
        </a>
      {/if}
      <a class="action" href={`${base}/history/${page.slug}`} aria-label={t('page.history')} aria-keyshortcuts="G H" title={`${t('page.history')} (G H)`}><Icon name="history" /></a>
      {#if !area.audit}<WatchMenu {area} {page} />{/if}
      <button type="button" class="action" aria-label={t('page.source')} title={t('page.source')} onclick={() => (showSource = true)}>
        <Icon name="code" />
      </button>
      <button type="button" class="action" aria-label={t('page.print')} title={t('page.print')} onclick={() => print()}><Icon name="print" /></button>
      <ReadAloud root={prose} title={page.title} lang={page.lang} />
      {#if area.role === 'admin' && inArea}
        <button type="button" class="action" aria-label={t('access.title')} title={t('access.title')} onclick={openAccess}><Icon name="people" /></button>
      {/if}
      {#if canEdit(area)}
        <Dropdown bind:this={more} label={t('page.more')} class="action">
          {#snippet trigger()}<Icon name="more" />{/snippet}
          <ul class="menu">
            {#if area.personal}
              <li><button type="button" class="menu-item" onclick={() => fromMenu(() => (showPublish = true))}>{t('publish.title')}</button></li>
            {/if}
            {#if inArea}
              <li><button type="button" class="menu-item" onclick={() => fromMenu(() => (showAsk = true))}>{t('reading.ask_title')}</button></li>
              <li><button type="button" class="menu-item" onclick={() => fromMenu(() => (showNewQuiz = true))}>{t('quiz.new')}</button></li>
            {/if}
            {#if page.review.can_review && inArea}
              <li><button type="button" class="menu-item" onclick={() => fromMenu(() => (showReview = true))}>{t('review.title')}</button></li>
            {/if}
            <li><button type="button" class="menu-item" onclick={() => fromMenu(() => (showMove = true))}>{t('page.move')}</button></li>
            <li><button type="button" class="menu-item danger" onclick={() => fromMenu(() => (showDelete = true))}>{t('page.delete')}</button></li>
          </ul>
        </Dropdown>
      {/if}
    </div>
  </div>

  {#if page.review.can_review && inArea}
    <ReviewDialog {area} {page} bind:open={showReview} onchanged={refresh} />
  {/if}
  {#if inArea && canEdit(area)}
    <ReadRequestDialog {area} slug={page.slug} bind:open={showAsk} />
  {/if}
  {#if area.personal && canEdit(area)}
    <PublishDialog {area} {page} bind:open={showPublish} onsent={refresh} {onmoved} />
  {/if}

  <Dialog title={t('page.source')} bind:open={showSource}>
    <pre class="source" lang={page.lang}>{page.markdown}</pre>
  </Dialog>

  {#if area.role === 'admin' && inArea}
    <Dialog title={t('access.title')} bind:open={showAccess}>
      <label class="field">
        {t('access.simulate')}
        <select bind:value={simulate} onchange={loadAccess}>
          <option value={0}>—</option>
          {#each [1, 2, 3] as n (n)}<option value={n}>{t(`level.${n as 1 | 2 | 3}`)}</option>{/each}
        </select>
      </label>
      {#if access}
        <ul class="access">
          {#each access.members as m (m.email)}
            <li class:denied={!(simulate ? m.can_read_simulated : m.can_read)}>
              {m.name} <span class="muted">· {t(`role.${m.role}`)}</span>
              {#if simulate && m.can_read !== m.can_read_simulated}
                <strong>{m.can_read_simulated ? t('access.gains') : t('access.loses')}</strong>
              {:else if !m.can_read}
                <span class="muted">· {t('access.cannot')}</span>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    </Dialog>
  {/if}

  {#if canEdit(area)}
    <Dialog title={t('page.move')} bind:open={showMove}>
      <form class="stack" onsubmit={move}>
        <label class="field">
          {t('page.move_to')}
          <input required pattern="[a-z0-9]+(-[a-z0-9]+)*(/[a-z0-9]+(-[a-z0-9]+)*)*" bind:value={moveTo} />
        </label>
        <small class="muted">{t('page.move_hint')}</small>
        <FolderPicker {refs} rootLabel={area.name} bind:path={moveTo} title={page.title} exclude={page.slug} />
        {#if error}<p class="error" role="alert">{error}</p>{/if}
        <button type="submit" class="primary">{t('page.move_submit')}</button>
      </form>
    </Dialog>
    <Dialog title={t('page.delete')} bind:open={showDelete}>
      <form class="stack" onsubmit={remove}>
        <p class="muted">{t('page.delete_hint')}</p>
        <label><input type="checkbox" bind:checked={confirmDelete} /> {t('page.delete_confirm')}</label>
        {#if error}<p class="error" role="alert">{error}</p>{/if}
        <button type="submit" class="primary danger-btn" disabled={!confirmDelete}>{t('page.delete_submit')}</button>
      </form>
    </Dialog>
  {/if}
{:else if error}
  <div class="view"><p class="error" role="alert">{error}</p></div>
{/if}
