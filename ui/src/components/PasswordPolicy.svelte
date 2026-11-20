<script lang="ts">
  import { api, codeOf } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';
  import { site } from '../lib/site.svelte';
  import Setting from './Setting.svelte';

  // Mínimo de las contraseñas locales (SPEC §8.1, QA-06): lo fija el superadmin y nunca baja de
  // 10; el servidor lo valida igual. Las contraseñas que ya existen siguen valiendo: el mínimo se
  // pide en la próxima que alguien elija.
  const floor = 10;
  const ceiling = 64;

  let saved = $state(site.password_min);
  let value = $state(site.password_min);
  let busy = $state(false);
  let error = $state('');
  let notice = $state('');

  async function save(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = '';
    notice = '';
    try {
      const res = await api<{ min_length: number }>('PUT', '/admin/password-policy', { min_length: value });
      saved = value = site.password_min = res.min_length;
      notice = t('admin.password_min_saved', { n: String(saved) });
    } catch (err) {
      error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }
</script>

<Setting title={t('admin.password_policy')} description={t('admin.password_policy_desc')}>
  <form class="stack" onsubmit={save}>
    <label class="field">
      {t('admin.password_min')}
      <input type="number" class="port" min={floor} max={ceiling} required aria-describedby="password-min-hint" bind:value />
      <small id="password-min-hint" class="muted">{t('admin.password_min_hint', { floor: String(floor), ceiling: String(ceiling) })}</small>
    </label>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    {#if notice}<p class="notice" role="status">{notice}</p>{/if}
    <div><button type="submit" class="primary" disabled={busy || value === saved}>{busy ? t('common.saving') : t('common.save')}</button></div>
  </form>
</Setting>
