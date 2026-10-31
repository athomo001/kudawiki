<script lang="ts">
  import { tick } from 'svelte';
  import type { EditorView } from '@codemirror/view';
  import { api, codeOf, type Level, type Template } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';
  import { createEditor } from '../lib/editor';
  import LevelBadge from '../components/LevelBadge.svelte';

  // Plantillas (SPEC §4.9): las del área las administra su admin; las globales, el superadmin, y
  // se copian a cada área nueva. Aquí no se suben imágenes: una imagen sin página va a la papelera
  // a los 7 días (§4.4) y la plantilla quedaría con el enlace roto.
  let { endpoint, global = false }: { endpoint: string; global?: boolean } = $props();

  type Form = { id: string; name: string; description: string; title: string; level: Level; tags: string; review: number };
  let list = $state<Template[]>([]);
  let loaded = $state(false);
  let form = $state<Form | null>(null);
  let asking = $state(''); // id de la plantilla cuyo borrado se está confirmando
  let busy = $state(false);
  let error = $state('');
  let host = $state<HTMLDivElement>();
  let view: EditorView | undefined;
  let markdown = '';

  async function load() {
    try {
      list = await api<Template[]>('GET', endpoint);
      loaded = true;
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }

  $effect(() => {
    void endpoint;
    load();
  });
  $effect(() => () => view?.destroy());

  async function open(tpl?: Template) {
    error = '';
    asking = '';
    try {
      const full = tpl ? await api<Template>('GET', `${endpoint}/${tpl.id}`) : undefined;
      form = full
        ? { id: full.id, name: full.name, description: full.description, title: full.title, level: full.level, tags: full.tags.join(', '), review: full.review_interval_days }
        : { id: '', name: '', description: '', title: '', level: 1, tags: '', review: 0 };
      markdown = full?.markdown ?? '';
    } catch (e) {
      error = errorText(codeOf(e));
      return;
    }
    await tick();
    view?.destroy();
    view = createEditor(host!, markdown, t('templates.content'), {
      onChange: (v) => (markdown = v),
      onSave: () => save(),
      onFiles: () => {},
      pages: () => [],
    });
  }

  function close() {
    view?.destroy();
    view = undefined;
    form = null;
  }

  async function save(e?: SubmitEvent) {
    e?.preventDefault();
    if (!form || busy) return;
    busy = true;
    error = '';
    const body = {
      name: form.name,
      description: form.description,
      title: form.title,
      markdown,
      level: form.level,
      tags: form.tags.split(',').map((s) => s.trim()).filter(Boolean),
      review_interval_days: form.review,
    };
    try {
      if (form.id) await api('PUT', `${endpoint}/${form.id}`, body);
      else await api('POST', endpoint, body);
      close();
      await load();
    } catch (err) {
      error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }

  async function remove(tpl: Template) {
    error = '';
    try {
      await api('DELETE', `${endpoint}/${tpl.id}`);
      asking = '';
      list = list.filter((x) => x.id !== tpl.id);
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }
</script>

<h1>{global ? t('templates.global_title') : t('templates.title')}</h1>
<p class="muted">{global ? t('templates.global_intro') : t('templates.intro')}</p>
{#if error}<p class="error" role="alert">{error}</p>{/if}

{#if form}
  <form class="stack template-form" onsubmit={save}>
    <label>{t('templates.name')} <input required maxlength="80" bind:value={form.name} /></label>
    <label>{t('templates.description')} <input maxlength="300" bind:value={form.description} /></label>
    <label>
      {t('templates.page_title')}
      <input maxlength="200" placeholder={`${t('templates.page_title_example')} {{fecha}}`} bind:value={form.title} />
    </label>
    <div class="template-row">
      <label>
        {t('page.level')}
        <select bind:value={form.level}>
          {#each [1, 2, 3] as const as n (n)}<option value={n}>{t(`level.${n}`)}</option>{/each}
        </select>
      </label>
      <label class="template-tags">{t('page.tags')} <input placeholder={t('page.tags_hint')} bind:value={form.tags} /></label>
      <!-- La página que nazca de ella revisa cada tanto (§4.8); "el del área" es no fijar uno. -->
      <label>
        {t('review.interval')}
        <select bind:value={form.review}>
          <option value={0}>{t('review.area_any')}</option>
          {#each [30, 90, 180, 365, 730] as d (d)}<option value={d}>{t('review.every_days', { days: String(d) })}</option>{/each}
          {#if form.review && ![30, 90, 180, 365, 730].includes(form.review)}
            <option value={form.review}>{t('review.every_days', { days: String(form.review) })}</option>
          {/if}
        </select>
      </label>
    </div>
    <div class="template-content">
      <span class="label-text">{t('templates.content')}</span>
      <div class="template-editor" bind:this={host}></div>
      <p class="muted small">
        {t('templates.variables')}
        <code>{'{{fecha}}'}</code> {t('templates.var_fecha')} · <code>{'{{autor}}'}</code> {t('templates.var_autor')} ·
        <code>{'{{titulo}}'}</code> {t('templates.var_titulo')}
      </p>
    </div>
    <div class="form-actions">
      <button type="submit" class="primary" disabled={busy}>{busy ? t('common.saving') : t('common.save')}</button>
      <button type="button" class="link" onclick={close}>{t('common.cancel')}</button>
    </div>
  </form>
{:else}
  <button type="button" class="primary" onclick={() => open()}>{t('templates.new')}</button>
  {#if loaded && !list.length}<p class="muted">{t('templates.none')}</p>{/if}
  <ul class="template-list">
    {#each list as tpl (tpl.id)}
      <li>
        <div class="template-info">
          <strong>{tpl.name}</strong>
          <LevelBadge level={tpl.level} />
          {#if tpl.description}<span class="muted small">{tpl.description}</span>{/if}
        </div>
        <div class="file-actions">
          {#if asking === tpl.id}
            <span class="small">{t('templates.confirm', { name: tpl.name })}</span>
            <button type="button" class="danger-btn" onclick={() => remove(tpl)}>{t('templates.delete_yes')}</button>
            <button type="button" class="link" onclick={() => (asking = '')}>{t('common.cancel')}</button>
          {:else}
            <button type="button" class="link" onclick={() => open(tpl)}>{t('templates.edit')}</button>
            <button type="button" class="link" onclick={() => (asking = tpl.id)}>{t('templates.delete')}</button>
          {/if}
        </div>
      </li>
    {/each}
  </ul>
{/if}
