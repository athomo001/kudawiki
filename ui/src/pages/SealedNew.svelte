<script lang="ts">
  import { api, codeOf, type Area } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';
  import { navigate } from '../lib/router.svelte';

  // Sellar un documento (§6.1). Sin autoguardado ni borradores: lo escrito vive solo en este
  // formulario hasta que se sella, nunca en localStorage (§6). Tampoco hay subida de archivos:
  // la media cifrada de los sellados todavía no existe (F7).
  let { area, oncreated }: { area: Area; oncreated: () => void } = $props();

  let slug = $state('');
  let title = $state('');
  let markdown = $state('');
  let password = $state('');
  let repeat = $state('');
  let busy = $state(false);
  let error = $state('');
  const base = $derived(`/w/${area.slug}`);

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    if (password !== repeat) {
      error = t('sealed.password_mismatch');
      return;
    }
    busy = true;
    try {
      const { id } = await api<{ id: string }>('POST', `${base}/sealed`, { slug: slug.trim(), title, markdown, password });
      password = repeat = markdown = '';
      oncreated();
      navigate(`${base}/sealed/${id}`);
    } catch (err) {
      error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }
</script>

<h1>{t('sealed.new')}</h1>
<p class="muted">{t('sealed.new_intro')}</p>
<form class="stack sealed-form" onsubmit={submit} autocomplete="off">
  <label class="field">{t('sealed.slug')} <input required maxlength="200" placeholder="legal/contrato" bind:value={slug} /></label>
  <label class="field">{t('page.title')} <input required maxlength="200" bind:value={title} /></label>
  <label class="field">{t('sealed.content')} <textarea rows="14" bind:value={markdown}></textarea></label>
  <div class="sealed-passwords">
    <label class="field">{t('sealed.password')} <input type="password" required minlength="12" autocomplete="new-password" bind:value={password} /></label>
    <label class="field">{t('sealed.password_repeat')} <input type="password" required minlength="12" autocomplete="new-password" bind:value={repeat} /></label>
  </div>
  <p class="muted small">{t('sealed.password_hint')}</p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  <div class="form-actions">
    <button type="submit" class="primary" disabled={busy}>{t('sealed.create')}</button>
    <a href={base}>{t('common.cancel')}</a>
  </div>
</form>
