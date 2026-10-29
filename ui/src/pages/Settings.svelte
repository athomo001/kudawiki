<script lang="ts">
  import { api, codeOf, type Area } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';

  // Configuración del área para su admin (SPEC §3.1, §14.3). En F3: nombre y descripción, que
  // ven todos los miembros. El modo de aprobación y lo demás llegan con F4.
  let { area, onchanged }: { area: Area; onchanged: () => void } = $props();

  let name = $state('');
  let description = $state('');
  let busy = $state(false);
  let saved = $state(false);
  let error = $state('');

  $effect(() => {
    api<{ name: string; description: string }>('GET', `/w/${area.slug}/settings`)
      .then((s) => ((name = s.name), (description = s.description)))
      .catch((e) => (error = errorText(codeOf(e))));
  });

  async function save(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    saved = false;
    error = '';
    try {
      await api('PATCH', `/w/${area.slug}/settings`, { name, description });
      saved = true;
      onchanged();
    } catch (err) {
      error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }
</script>

<h1>{t('settings.title')}</h1>
<form class="stack settings" onsubmit={save}>
  <label class="field">
    {t('common.name')}
    <input required maxlength="80" bind:value={name} />
  </label>
  <label class="field">
    {t('settings.description')}
    <textarea rows="3" maxlength="500" aria-describedby="desc-hint" bind:value={description}></textarea>
  </label>
  <small id="desc-hint" class="muted">{t('settings.description_hint')}</small>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#if saved}<p role="status" class="muted">{t('settings.saved')}</p>{/if}
  <button type="submit" class="primary" disabled={busy}>{busy ? t('common.saving') : t('common.save')}</button>
</form>
