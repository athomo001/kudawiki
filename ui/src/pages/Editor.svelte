<script lang="ts">
  import { onMount, tick } from 'svelte';
  import type { EditorView } from '@codemirror/view';
  import {
    api,
    ApiError,
    codeOf,
    mediaMarkdown,
    rankOf,
    timeAgo,
    type Area,
    type Conflict,
    type Draft,
    type DraftRef,
    type Template,
    type MediaFile,
    type Level,
    type Page,
    type PageRef,
    type PresenceEditor,
    type ProposalDetail,
    type Upload,
  } from '../lib/api';
  import { createEditor, insertBlock } from '../lib/editor';
  import { enhance } from '../lib/enhance';
  import { syncScroll } from '../lib/scrollsync';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';
  import { navigate, route } from '../lib/router.svelte';
  import Icon from '../components/Icon.svelte';
  import DiffView from '../components/DiffView.svelte';
  import Dialog from '../components/Dialog.svelte';
  import LevelBadge from '../components/LevelBadge.svelte';
  import FolderPicker from '../components/FolderPicker.svelte';
  import MarkdownGuide from '../components/MarkdownGuide.svelte';
  import EditorToolbar from '../components/EditorToolbar.svelte';
  import FileBrowser from '../components/FileBrowser.svelte';

  // Editor a pantalla completa, con el diseño aprobado (SPEC §15). Sin slug es una página nueva.
  // Guardar envía base_revision: si otra persona guardó antes, el servidor responde 409 con su
  // revisión y el diff, y se resuelve aquí sin perder lo escrito (SPEC §4.1).
  // refs: el árbol del área, para elegir la carpeta sin adivinar la ruta.
  // Con proposal se edita una propuesta (§5.2): su autor la corrige, o un admin la modifica
  // antes de aprobarla. Donde la ruta pide revisión, guardar envía una propuesta (§5.1).
  let {
    area,
    slug,
    proposal,
    selfId,
    refs,
    onsaved,
  }: { area: Area; slug?: string; proposal?: string; selfId: string; refs: PageRef[]; onsaved: () => void } = $props();

  let title = $state('');
  // Ruta de la página: en una nueva es dónde se crea; en una existente, cambiarla la mueve al
  // guardar, con sus subpáginas, igual que "Mover" en el lector (SPEC §4.1).
  let path = $state('');
  let markdown = $state('');
  let summary = $state('');
  let tags = $state('');
  // Descripción corta e ícono (§4.1): metadatos, como las etiquetas; no crean revisión.
  let description = $state('');
  let icon = $state('');
  let lang = $state<'es' | 'en'>('es');
  // null = heredar del padre (página nueva) o no cambiar; solo se ofrecen niveles hasta el propio.
  let level = $state<Level | null>(null);
  const levels = $derived(([1, 2, 3] as Level[]).filter((n) => n <= rankOf(area.role)));
  let base = $state(0);
  let previewHtml = $state('');
  let busy = $state(false);
  let error = $state('');
  let conflict = $state<Conflict | null>(null);
  let picking = $state(false);
  let guide = $state(false);
  // mustPropose: en esta ruta quien escribe no publica directo. draft: la propuesta que se edita.
  let mustPropose = $state(false);
  let draft = $state<ProposalDetail | null>(null);
  // Comentario para el autor cuando un admin aprueba con modificaciones.
  let comment = $state('');
  const approving = $derived(!!draft && draft.author_id !== selfId);
  const proposing = $derived(!draft && mustPropose);
  // Por propuesta solo viajan el título, el contenido y el idioma: la ruta, el nivel y los
  // metadatos los cambia quien publica directo.
  const contentOnly = $derived(!!draft || mustPropose);
  let host: HTMLDivElement;
  let view: EditorView | undefined;
  let timer: ReturnType<typeof setTimeout>;
  let preview = $state<HTMLElement>();
  let previewPane: HTMLElement;
  let unsync: (() => void) | undefined;

  const back = $derived(proposal ? `/w/${area.slug}/proposals/${proposal}` : slug ? `/w/${area.slug}/p/${slug}` : `/w/${area.slug}`);

  // Divisor entre el Markdown y la vista previa: se arrastra o se mueve con las flechas, y el
  // ancho queda en este navegador (es una preferencia de pantalla, no del perfil).
  const SPLIT_KEY = 'kw.editor.split';
  const clamp = (n: number) => Math.min(80, Math.max(20, Math.round(n)));
  let split = $state(clamp(Number(localStorage.getItem(SPLIT_KEY)) || 50));
  let body = $state<HTMLElement>();
  let dragging = false;

  // Por CSSOM y no con un atributo style: la CSP no deja estilos en línea (SPEC §14.5).
  $effect(() => {
    body?.style.setProperty('--kw-split', `${split}%`);
  });

  function setSplit(n: number) {
    split = clamp(n);
    localStorage.setItem(SPLIT_KEY, String(split));
  }

  function onSplitDown(e: PointerEvent) {
    dragging = true;
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
  }

  function onSplitMove(e: PointerEvent) {
    if (!dragging || !body) return;
    const r = body.getBoundingClientRect();
    setSplit(((e.clientX - r.left) / r.width) * 100);
  }

  function onSplitKey(e: KeyboardEvent) {
    const step = { ArrowLeft: -5, ArrowRight: 5 }[e.key];
    if (step) {
      e.preventDefault();
      setSplit(split + step);
    } else if (e.key === 'Home' || e.key === 'End') {
      e.preventDefault();
      setSplit(e.key === 'Home' ? 20 : 80);
    }
  }

  $effect(() => {
    if (previewHtml && preview) enhance(preview);
  });

  // Pegar o arrastrar archivos los sube como media y escribe el enlace; nunca se guardan
  // data: en el Markdown (SPEC §4.4).
  async function attach(files: File[]) {
    error = '';
    for (const file of files) {
      try {
        const body = new FormData();
        body.append('file', file);
        const m = await api<Upload>('POST', `/w/${area.slug}/media`, body);
        view?.dispatch(view.state.replaceSelection(`${mediaMarkdown(m)}\n`));
      } catch (e) {
        error = errorText(codeOf(e));
      }
    }
  }

  const target = $derived(slug ?? path.trim());
  const slugPattern = /^[a-z0-9]+(-[a-z0-9]+)*(\/[a-z0-9]+(-[a-z0-9]+)*)*$/;

  const checkApproval = async (to: string) =>
    (mustPropose = (await api<{ must_propose: boolean }>('GET', `/w/${area.slug}/approval/${to}`)).must_propose);

  // Autoguardado en el servidor (SPEC §4.11): cada 10 s, si el título, el contenido o la ruta
  // cambiaron desde lo último guardado. Solo al editar una página, nueva o existente: una
  // propuesta abierta ya vive en el servidor. Una página nueva se guarda cuando tiene ruta, que
  // es su clave; el servidor borra el borrador al publicar o proponer.
  const AUTOSAVE_MS = 10_000;
  const autosaves = $derived(!proposal);
  let lastKey = ''; // ruta, título y contenido como están en el servidor (la página o el borrador)
  let draftSlug = ''; // ruta del borrador que guardó esta edición; '' si todavía ninguno
  let saving: Promise<void> | undefined;
  let closed = false; // ya se publicó o se descartó: no se autoguarda más
  let autosaveTimer: ReturnType<typeof setInterval>;
  let savedAt = $state('');
  let draftFailed = $state(false);
  // recover: el borrador de esta página que quedó de antes; pending: las páginas nuevas sin
  // publicar. Mientras no se decide qué hacer con un borrador viejo, el autoguardado no lo pisa.
  let recover = $state<Draft | null>(null);
  let pending = $state<DraftRef[]>([]);
  let published = $state(0); // revisión publicada al abrir: avisa si el borrador quedó atrás
  const needsPath = $derived(autosaves && !slug && !slugPattern.test(path.trim()) && !!(title.trim() || markdown.trim()));

  const draftKey = () => `${target}\u0000${title}\u0000${markdown}`;

  function autosave() {
    const to = target;
    if (!autosaves || closed || busy || saving || recover || !view || !slugPattern.test(to)) return;
    if (draftKey() === lastKey || pending.some((d) => d.slug === to) || !(title.trim() || markdown.trim())) return;
    const key = draftKey();
    const prev = draftSlug;
    saving = api('PUT', `/w/${area.slug}/drafts/${to}`, { title, markdown, base_revision: base })
      .then(async () => {
        draftSlug = to;
        lastKey = key;
        savedAt = new Date().toISOString();
        draftFailed = false;
        // En una página nueva la ruta es la clave: si cambió, el borrador de la ruta vieja sobra.
        if (prev && prev !== to) await api('DELETE', `/w/${area.slug}/drafts/${prev}`).catch(() => {});
      })
      .catch(() => {
        draftFailed = true;
      })
      .finally(() => (saving = undefined));
  }

  // Recuperar un borrador: si quedó sobre una revisión vieja, guardar muestra el conflicto con
  // las diferencias, como cuando otra persona guarda antes (§4.1).
  function useDraft(d: Draft) {
    recover = null;
    pending = pending.filter((x) => x.slug !== d.slug);
    title = d.title;
    base = d.base_revision;
    if (!slug) path = d.slug;
    view?.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: d.markdown } });
    draftSlug = target;
    lastKey = draftKey();
    view?.focus();
  }

  async function recoverNew(ref: DraftRef) {
    try {
      useDraft(await api<Draft>('GET', `/w/${area.slug}/drafts/${ref.slug}`));
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }

  async function dropDraft(to: string) {
    try {
      await api('DELETE', `/w/${area.slug}/drafts/${to}`);
      recover = null;
      pending = pending.filter((x) => x.slug !== to);
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }

  // Descartar borra lo que autoguardó esta edición; un borrador viejo que no se recuperó queda.
  async function discard() {
    closed = true;
    await saving;
    if (draftSlug) await api('DELETE', `/w/${area.slug}/drafts/${draftSlug}`).catch(() => {});
    navigate(back);
  }

  const timeOf = (iso: string) => new Intl.DateTimeFormat(locale(), { timeStyle: 'short' }).format(new Date(iso));

  // Aviso de edición concurrente (§4.11): quién más tiene la página abierta en el editor. El
  // navegador reconecta solo si la conexión se corta, y deja de intentar ante un 404.
  let others = $state<string[]>([]);
  let presence: EventSource | undefined;
  const othersText = $derived(
    others.length
      ? t(others.length === 1 ? 'editor.presence_one' : 'editor.presence_many', {
          names: new Intl.ListFormat(locale(), { type: 'conjunction' }).format(others),
        })
      : '',
  );

  function watchPresence(to: string) {
    presence = new EventSource(`/api/w/${area.slug}/presence/${to}`);
    presence.onmessage = (e) => {
      const { editors } = JSON.parse(e.data) as { editors: PresenceEditor[] };
      others = editors.filter((x) => x.id !== selfId).map((x) => x.name);
    };
    presence.onerror = () => (others = []);
  }

  // En una página nueva el modo depende de la carpeta (§5.2.1): se vuelve a preguntar al cambiar la ruta.
  let approvalTimer: ReturnType<typeof setTimeout>;
  $effect(() => {
    const to = path.trim();
    if (slug || proposal || !slugPattern.test(to)) return;
    clearTimeout(approvalTimer);
    approvalTimer = setTimeout(() => checkApproval(to).catch(() => {}), 300);
  });

  // Vista previa con el mismo render del servidor, 300 ms después de dejar de escribir (SPEC §4.2).
  function schedulePreview() {
    clearTimeout(timer);
    timer = setTimeout(async () => {
      try {
        previewHtml = (await api<{ html: string }>('POST', `/w/${area.slug}/render`, { markdown })).html;
      } catch (e) {
        error = errorText(codeOf(e));
      }
    }, 300);
  }

  async function save(e?: SubmitEvent) {
    e?.preventDefault();
    if (busy) return;
    busy = true;
    error = '';
    // Un autoguardado en camino llegaría después y dejaría un borrador de lo ya publicado.
    closed = true;
    await saving;
    try {
      if (draft || proposing) return await sendProposal();
      await api('PUT', `/w/${area.slug}/pages/${target}`, {
        title, markdown, lang, base_revision: base, change_summary: summary, review_interval_days: base ? 0 : reviewDays,
      });
      // Las etiquetas son metadatos, no contenido: no crean revisión (SPEC §4.6).
      const list = tags.split(',').map((s) => s.trim()).filter(Boolean);
      const meta = { tags: list, description, icon: icon.trim() };
      await api('PATCH', `/w/${area.slug}/meta/${target}`, level ? { ...meta, level } : meta);
      const to = path.trim();
      const moving = !!slug && to !== slug;
      if (moving) {
        try {
          await api('POST', `/w/${area.slug}/move/${slug}`, { to });
        } catch (err) {
          // El contenido ya quedó guardado en la ruta vieja: se sigue editando desde esa revisión.
          base = (await api<Page>('GET', `/w/${area.slug}/pages/${slug}`)).revision;
          throw err;
        }
      }
      onsaved();
      navigate(`/w/${area.slug}/p/${moving ? to : target}`);
    } catch (err) {
      closed = false;
      if (err instanceof ApiError && err.code === 'conflict' && err.detail.current) conflict = err.detail.current as Conflict;
      else error = errorText(codeOf(err));
      // El área pasó a pedir revisión mientras se escribía: el botón cambia a "Enviar a revisión".
      if (codeOf(err) === 'approval_required') mustPropose = true;
    } finally {
      busy = false;
    }
  }

  // Propuesta nueva, corrección del autor o aprobación con modificaciones (§5.2).
  async function sendProposal() {
    const body = { slug: target, title, markdown, lang, base_revision: base, change_summary: summary };
    if (!draft) {
      const { id } = await api<{ id: string }>('POST', `/w/${area.slug}/proposals`, body);
      onsaved();
      navigate(`/w/${area.slug}/proposals/${id}`);
    } else if (approving) {
      await api('POST', `/w/${area.slug}/proposals/${draft.id}/approve`, { comment, edit: body });
      onsaved();
      navigate(`/w/${area.slug}/p/${draft.slug}`);
    } else {
      await api('PATCH', `/w/${area.slug}/proposals/${draft.id}`, body);
      navigate(`/w/${area.slug}/proposals/${draft.id}`);
    }
  }

  // Al abrir una propuesta: con conflicto se rehace sobre la versión actual, a la vista de lo que
  // la página cambió. Sin conflicto, el autor sigue sobre su base (se mezcla al aprobar) y el admin
  // parte de lo que se publicaría.
  function openDraft(p: ProposalDetail) {
    draft = p;
    const merged = approving && !p.conflict;
    title = merged ? p.result_title : p.title;
    lang = p.lang;
    path = p.slug;
    summary = p.summary;
    base = p.new_page ? 0 : p.conflict || approving ? p.current_revision : p.base_revision;
    return merged ? p.result : p.markdown;
  }

  // Desde la guía de Markdown: un bloque necesita su propia línea, así que se abre una si el
  // cursor está a mitad de otra. El foco vuelve al texto cuando el diálogo ya se cerró.
  async function insert(text: string, block: boolean) {
    guide = false;
    if (!view) return;
    if (block) insertBlock(view, text);
    else view.dispatch(view.state.replaceSelection(text));
    await tick();
    view.focus();
  }

  // El botón de imagen de la barra: el mismo camino que pegar o arrastrar un archivo (§4.4).
  let picker = $state<HTMLInputElement>();
  function pickImages(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    attach([...(input.files ?? [])]);
    input.value = '';
  }

  // Plantillas (§4.9): una página nueva puede empezar desde una. El servidor llena {{fecha}} con
  // el día de quien la usa, {{autor}} y {{titulo}} (el título escrito, o el de la plantilla).
  let templates = $state<Template[]>([]);
  let choosing = $state(false);
  // El intervalo de revisión de la plantilla (§4.8): la página nace con él.
  let reviewDays = $state(0);
  async function useTemplate(tpl: Template) {
    const d = new Date();
    const today = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
    try {
      const u = await api<Template>('GET', `/w/${area.slug}/templates/${tpl.id}/use?title=${encodeURIComponent(title)}&today=${today}`);
      choosing = false;
      title = u.title;
      view?.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: u.markdown ?? '' } });
      if (levels.includes(u.level)) level = u.level;
      tags = u.tags.join(', ');
      reviewDays = u.review_interval_days;
      await tick();
      view?.focus();
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }

  // "Archivos del área": un archivo ya subido se usa otra vez sin volver a subirlo (§4.4).
  let browsing = $state(false);
  async function pickFile(f: MediaFile) {
    browsing = false;
    if (!view) return;
    view.dispatch(view.state.replaceSelection(mediaMarkdown(f)));
    await tick();
    view.focus();
  }

  // Guardar encima: se toma la revisión que ganó como base, a sabiendas de lo que se pisa.
  function keepMine() {
    if (!conflict) return;
    base = conflict.revision;
    conflict = null;
    save();
  }

  // Descartar lo propio: el editor pasa a la versión guardada y se sigue desde ahí.
  async function loadTheirs() {
    try {
      const p = await api<Page>('GET', `/w/${area.slug}/pages/${target}`);
      title = p.title;
      base = p.revision;
      view?.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: p.markdown } });
      conflict = null;
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }

  onMount(() => {
    const mount = (doc: string) => {
      view = createEditor(host, doc, t('editor.source'), {
        onChange: (v) => ((markdown = v), schedulePreview()),
        onSave: () => save(),
        onFiles: attach,
        pages: () => refs,
      });
      // Las dos columnas se desplazan juntas, y un clic en la vista previa lleva a esa línea (§4.2).
      unsync = syncScroll(view, previewPane);
      schedulePreview();
    };
    if (proposal) {
      api<ProposalDetail>('GET', `/w/${area.slug}/proposals/${proposal}`)
        .then((p) => {
          mount((markdown = openDraft(p)));
          if (!p.new_page) watchPresence(p.slug);
        })
        .catch((e) => (error = errorText(e instanceof ApiError && e.status === 404 ? 'not_found' : codeOf(e))));
    } else if (!slug) {
      // "Escribir portada" en el inicio del área llega con la ruta ya puesta (?path=portada, §4.17).
      path = new URLSearchParams(route.query).get('path') ?? '';
      mount('');
      lastKey = draftKey();
      api<DraftRef[]>('GET', `/w/${area.slug}/drafts`)
        .then((list) => (pending = list))
        .catch(() => {});
      api<Template[]>('GET', `/w/${area.slug}/templates`)
        .then((list) => (templates = list))
        .catch(() => {});
    } else {
      api<Page>('GET', `/w/${area.slug}/pages/${slug}`)
        .then((p) => {
          ({ title, markdown } = p);
          tags = (p.tags ?? []).join(', ');
          description = p.description;
          icon = p.icon;
          lang = p.lang;
          level = p.level;
          base = published = p.revision;
          path = p.slug;
          mount(p.markdown);
          lastKey = draftKey();
          watchPresence(p.slug);
          // Un 404 es lo normal: no hay nada sin publicar.
          api<Draft>('GET', `/w/${area.slug}/drafts/${p.slug}`)
            .then((d) => (d.title !== p.title || d.markdown !== p.markdown) && (recover = d))
            .catch(() => {});
          return checkApproval(p.slug);
        })
        .catch((e) => (error = errorText(e instanceof ApiError && e.status === 404 ? 'not_found' : codeOf(e))));
    }
    autosaveTimer = setInterval(autosave, AUTOSAVE_MS);
    return () => {
      clearTimeout(timer);
      clearTimeout(approvalTimer);
      clearInterval(autosaveTimer);
      // Salir del editor por otro camino (volver, un enlace) guarda lo último escrito.
      autosave();
      presence?.close();
      unsync?.();
      view?.destroy();
    };
  });
</script>

<form class="editor" onsubmit={save}>
  <header class="editor-head">
    <a class="editor-back" href={back} aria-label={t('editor.back')} title={t('editor.back')}><Icon name="back" /></a>
    <div class="editor-title">
      <input class="title-input" required maxlength="200" aria-label={t('page.title')} placeholder={t('page.title')} bind:value={title} />
      <div class="editor-path">
        <label class="path-field">
          {area.slug} /
          <input
            class="slug-input"
            required
            pattern="[a-z0-9]+(-[a-z0-9]+)*(/[a-z0-9]+(-[a-z0-9]+)*)*"
            aria-label={t('page.slug')}
            readonly={contentOnly && !!(slug || draft)}
            title={slug ? t('page.move_hint') : t('page.slug_hint')}
            placeholder="redes/vpn/procedimiento"
            size={Math.max(path.length + 2, 24)}
            bind:value={path}
          />
        </label>
        {#if !(contentOnly && (slug || draft))}
          <button type="button" class="path-pick" onclick={() => (picking = true)}><Icon name="folder" size={14} /> {t('picker.open')}</button>
        {/if}
        {#if draft && base}<span>· {t('editor.proposal_rev', { n: String(base) })}</span>
        {:else if slug}<span>· {t('editor.editing_rev', { n: String(base) })}</span>{/if}
        {#if autosaves}
          <span class="draft-status" role="status">
            {#if draftFailed}· {t('editor.draft_failed')}
            {:else if savedAt}· {t('editor.draft_saved', { time: timeOf(savedAt) })}
            {:else if needsPath}· {t('editor.draft_needs_path')}{/if}
          </span>
        {/if}
      </div>
    </div>
    {#if templates.length}
      <button type="button" class="editor-btn" onclick={() => (choosing = true)}><Icon name="template" size={16} /> {t('templates.use')}</button>
    {/if}
    <button type="button" class="editor-btn" onclick={() => (guide = true)}><Icon name="help" size={16} /> {t('mdguide.open')}</button>
    <button type="button" class="editor-btn" onclick={discard}>{t('editor.discard')}</button>
    <button type="submit" class="editor-btn editor-save" disabled={busy}>
      {#if busy}{proposing ? t('editor.sending') : t('common.saving')}
      {:else if approving}{t('proposals.approve')}
      {:else if draft}{t('editor.save_proposal')}
      {:else if proposing}{t('editor.submit_review')}
      {:else}{t('common.save')}{/if}
    </button>
  </header>

  {#if othersText}<p class="editor-note editor-presence" role="status"><Icon name="people" size={16} /> {othersText}</p>{/if}
  {#if recover}
    {@const d = recover}
    <div class="editor-note draft-note">
      <p>
        {d.base_revision < published
          ? t('editor.draft_found_stale', { when: timeAgo(d.updated_at, locale()), base: String(d.base_revision), n: String(published) })
          : t('editor.draft_found', { when: timeAgo(d.updated_at, locale()) })}
      </p>
      <button type="button" class="secondary" onclick={() => useDraft(d)}>{t('editor.draft_recover')}</button>
      <button type="button" class="link" onclick={() => dropDraft(target)}>{t('editor.draft_discard')}</button>
    </div>
  {/if}
  {#if pending.length}
    <div class="editor-note draft-note">
      <p>{t('editor.drafts_new')}</p>
      <ul class="draft-list">
        {#each pending as d (d.slug)}
          <li>
            <strong>{d.title || t('editor.untitled')}</strong> <code>{area.slug}/{d.slug}</code>
            <span class="muted">· {timeAgo(d.updated_at, locale())}</span>
            <button type="button" class="secondary" onclick={() => recoverNew(d)}>{t('editor.draft_recover')}</button>
            <button type="button" class="link" onclick={() => dropDraft(d.slug)}>{t('editor.draft_discard')}</button>
          </li>
        {/each}
      </ul>
      <button type="button" class="link" onclick={() => (pending = [])}>{t('editor.drafts_hide')}</button>
    </div>
  {/if}
  {#if contentOnly && !approving}<p class="editor-note">{t('editor.propose_note')}</p>{/if}
  <!-- Como en el diseño aprobado: el formato a la izquierda, idioma y nivel a la derecha. -->
  <div class="editor-bar">
    <EditorToolbar view={() => view} onimage={() => picker?.click()} onfiles={() => (browsing = true)} />
    <div class="editor-bar-end">
      <label>
        {t('page.lang')}
        <select bind:value={lang}><option value="es">{t('lang.es')}</option><option value="en">{t('lang.en')}</option></select>
      </label>
      {#if !contentOnly}
        <label>
          {t('page.level')}
          <select bind:value={level}>
            {#if !slug}<option value={null}>{t('level.inherit')}</option>{/if}
            {#each levels as n (n)}<option value={n}>{t(`level.${n}`)}</option>{/each}
          </select>
        </label>
      {/if}
    </div>
  </div>
  {#if !contentOnly}
    <div class="editor-bar editor-meta">
      <label class="editor-icon">
        {t('page.icon')}
        <input maxlength="16" placeholder="📘" aria-describedby="icon-hint" bind:value={icon} />
      </label>
      <small id="icon-hint" class="editor-icon-hint">{t('page.icon_hint')}</small>
      <label class="editor-desc">{t('page.description')} <input maxlength="200" placeholder={t('page.description_hint')} bind:value={description} /></label>
      <label class="editor-tags">{t('page.tags')} <input placeholder={t('page.tags_hint')} bind:value={tags} /></label>
    </div>
  {/if}

  {#if draft?.conflict && draft.page_changes}
    <section class="conflict" aria-labelledby="rebase-title">
      <h2 id="rebase-title">{t('editor.rebase_title', { n: String(draft.current_revision) })}</h2>
      <p class="muted">{t('editor.rebase_body', { n: String(draft.current_revision) })}</p>
      <DiffView lines={draft.page_changes} />
    </section>
  {/if}

  {#if conflict}
    <section class="conflict" role="alert" aria-labelledby="conflict-title">
      <h2 id="conflict-title">{t('editor.conflict_title', { name: conflict.updated_by, n: String(conflict.revision) })}</h2>
      <p class="muted">{t('editor.conflict_body')}</p>
      <div class="conflict-actions">
        <button type="button" class="primary" onclick={keepMine}>{t('editor.conflict_keep_mine')}</button>
        <button type="button" class="secondary" onclick={loadTheirs}>{t('editor.conflict_load_theirs')}</button>
      </div>
      <DiffView lines={conflict.diff} />
    </section>
  {/if}
  {#if error}<p class="error banner" role="alert">{error}</p>{/if}

  <div class="editor-body" bind:this={body}>
    <section class="editor-source" aria-label={t('editor.source')}>
      <div class="cm-host" bind:this={host}></div>
    </section>
    <!-- Un separador con foco y valor es un control (patrón ARIA "window splitter"), aunque Svelte no lo sepa. -->
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_no_noninteractive_tabindex -->
    <div
      class="editor-split"
      role="separator"
      aria-orientation="vertical"
      aria-label={t('editor.split')}
      aria-valuenow={split}
      aria-valuemin={20}
      aria-valuemax={80}
      tabindex="0"
      onpointerdown={onSplitDown}
      onpointermove={onSplitMove}
      onpointerup={() => (dragging = false)}
      onkeydown={onSplitKey}
      ondblclick={() => setSplit(50)}
    ></div>
    <!-- Vista previa: HTML saneado por el servidor, igual al del lector. -->
    <section class="editor-preview" aria-label={t('page.preview')} bind:this={previewPane}>
      <p class="side-title">{t('page.preview')}</p>
      <div class="prose" lang={lang} bind:this={preview}>{@html previewHtml}</div>
    </section>
  </div>

  <footer class="editor-foot">
    {#if approving}
      <label>{t('proposals.comment')} <input maxlength="2000" placeholder={t('editor.comment_placeholder')} bind:value={comment} /></label>
    {:else}
      <!-- El área puede exigirlo al cambiar una página que existe (§4.3); crear una no lo pide. -->
      {@const needSummary = area.require_change_summary && !!base && !approving}
      <label>
        {needSummary ? t('page.summary_required') : t('page.summary')}
        <input maxlength="200" required={needSummary} placeholder={t('editor.summary_placeholder')} bind:value={summary} />
      </label>
    {/if}
  </footer>
</form>

<!-- Fuera del formulario: nada de aquí adentro lo envía. -->
<input class="sr-only" type="file" accept="image/*" multiple tabindex="-1" aria-hidden="true" bind:this={picker} onchange={pickImages} />
<Dialog title={t('picker.title')} bind:open={picking}>
  <FolderPicker {refs} rootLabel={area.name} bind:path {title} exclude={slug} />
  <p class="picker-result">{t('picker.result')} <code>{area.slug}/{path}</code></p>
  <button type="button" class="primary" onclick={() => (picking = false)}>{t('picker.done')}</button>
</Dialog>
<Dialog title={t('templates.choose')} bind:open={choosing}>
  {#if markdown.trim()}<p class="muted small">{t('templates.replace_note')}</p>{/if}
  <ul class="template-list template-pick">
    {#each templates as tpl (tpl.id)}
      <li>
        <button type="button" class="link" onclick={() => useTemplate(tpl)}>{tpl.name}</button>
        <LevelBadge level={tpl.level} />
        {#if tpl.description}<span class="muted small">{tpl.description}</span>{/if}
      </li>
    {/each}
  </ul>
</Dialog>
<Dialog title={t('files.title')} bind:open={browsing} wide>
  <FileBrowser {area} onpick={pickFile} />
</Dialog>
<Dialog title={t('mdguide.title')} bind:open={guide} wide>
  <MarkdownGuide oninsert={insert} />
</Dialog>
