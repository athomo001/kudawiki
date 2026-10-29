<script lang="ts">
  import { onMount } from 'svelte';
  import type { EditorView } from '@codemirror/view';
  import { api, ApiError, codeOf, rankOf, type Area, type Conflict, type Level, type Page, type Upload } from '../lib/api';
  import { createEditor } from '../lib/editor';
  import { enhance } from '../lib/enhance';
  import { t, errorText } from '../lib/i18n/index.svelte';
  import { navigate } from '../lib/router.svelte';
  import Icon from '../components/Icon.svelte';
  import DiffView from '../components/DiffView.svelte';

  // Editor a pantalla completa, con el diseño aprobado (SPEC §15). Sin slug es una página nueva.
  // Guardar envía base_revision: si otra persona guardó antes, el servidor responde 409 con su
  // revisión y el diff, y se resuelve aquí sin perder lo escrito (SPEC §4.1).
  let { area, slug, onsaved }: { area: Area; slug?: string; onsaved: () => void } = $props();

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
  let host: HTMLDivElement;
  let view: EditorView | undefined;
  let timer: ReturnType<typeof setTimeout>;
  let preview = $state<HTMLElement>();

  const back = $derived(slug ? `/w/${area.slug}/p/${slug}` : `/w/${area.slug}`);

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
        const text = m.mime.startsWith('image/') ? `![${m.name}](${m.url})` : `[${m.name}](${m.url})`;
        view?.dispatch(view.state.replaceSelection(`${text}\n`));
      } catch (e) {
        error = errorText(codeOf(e));
      }
    }
  }

  const target = $derived(slug ?? path.trim());

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
    try {
      await api('PUT', `/w/${area.slug}/pages/${target}`, { title, markdown, lang, base_revision: base, change_summary: summary });
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
      if (err instanceof ApiError && err.code === 'conflict' && err.detail.current) conflict = err.detail.current as Conflict;
      else error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
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
      });
      schedulePreview();
    };
    if (!slug) {
      mount('');
    } else {
      api<Page>('GET', `/w/${area.slug}/pages/${slug}`)
        .then((p) => {
          ({ title, markdown } = p);
          tags = (p.tags ?? []).join(', ');
          description = p.description;
          icon = p.icon;
          lang = p.lang;
          level = p.level;
          base = p.revision;
          path = p.slug;
          mount(p.markdown);
        })
        .catch((e) => (error = errorText(e instanceof ApiError && e.status === 404 ? 'not_found' : codeOf(e))));
    }
    return () => {
      clearTimeout(timer);
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
            title={slug ? t('page.move_hint') : t('page.slug_hint')}
            placeholder="redes/vpn/procedimiento"
            size={Math.max(path.length + 2, 24)}
            bind:value={path}
          />
        </label>
        {#if slug}<span>· {t('editor.editing_rev', { n: String(base) })}</span>{/if}
      </div>
    </div>
    <a class="editor-btn" href={back}>{t('editor.discard')}</a>
    <button type="submit" class="editor-btn editor-save" disabled={busy}>{busy ? t('common.saving') : t('common.save')}</button>
  </header>

  <div class="editor-bar">
    <label class="editor-icon">
      {t('page.icon')}
      <input maxlength="16" placeholder="🔐" title={t('page.icon_hint')} bind:value={icon} />
    </label>
    <label class="editor-desc">{t('page.description')} <input maxlength="200" placeholder={t('page.description_hint')} bind:value={description} /></label>
    <label class="editor-tags">{t('page.tags')} <input placeholder={t('page.tags_hint')} bind:value={tags} /></label>
    <label>
      {t('page.lang')}
      <select bind:value={lang}><option value="es">{t('lang.es')}</option><option value="en">{t('lang.en')}</option></select>
    </label>
    <label>
      {t('page.level')}
      <select bind:value={level}>
        {#if !slug}<option value={null}>{t('level.inherit')}</option>{/if}
        {#each levels as n (n)}<option value={n}>{t(`level.${n}`)}</option>{/each}
      </select>
    </label>
  </div>

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
    <section class="editor-preview" aria-label={t('page.preview')}>
      <p class="side-title">{t('page.preview')}</p>
      <div class="prose" lang={lang} bind:this={preview}>{@html previewHtml}</div>
    </section>
  </div>

  <footer class="editor-foot">
    <label>{t('page.summary')} <input maxlength="200" placeholder={t('editor.summary_placeholder')} bind:value={summary} /></label>
  </footer>
</form>
