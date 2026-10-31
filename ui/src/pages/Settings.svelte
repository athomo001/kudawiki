<script lang="ts">
  import { api, codeOf, type ApprovalOverride, type Area } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';

  // Configuración del área para su admin (SPEC §3.1, §14.3): nombre y descripción, que ven todos
  // los miembros, y desde F4 la aprobación de cambios (§5.1, §5.2.1, §19.5) y el historial (§4.3).
  let { area, onchanged }: { area: Area; onchanged: () => void } = $props();

  type Mode = ApprovalOverride['approval_mode'];
  let name = $state('');
  let description = $state('');
  let approvalMode = $state<Mode>('direct');
  let viewersCanPropose = $state(false);
  // Historial: resumen obligatorio y cuánto se guarda (todas, las últimas N o las de N días).
  let requireSummary = $state(false);
  let keep = $state<'all' | 'last' | 'days'>('all');
  let keepLast = $state(50);
  let keepDays = $state(365);
  let overrides = $state<ApprovalOverride[]>([]);
  let prefix = $state('');
  let prefixMode = $state<Mode>('required');
  let busy = $state(false);
  let saved = $state(false);
  let error = $state('');
  let overrideError = $state('');

  const base = $derived(`/w/${area.slug}`);
  const loadOverrides = async () => (overrides = await api<ApprovalOverride[]>('GET', `${base}/approval-overrides`));

  $effect(() => {
    type Settings = {
      name: string;
      description: string;
      approval_mode: Mode;
      viewers_can_propose: boolean;
      require_change_summary: boolean;
      keep_last: number;
      keep_days: number;
    };
    api<Settings>('GET', `${base}/settings`)
      .then((s) => {
        ({ name, description } = s);
        approvalMode = s.approval_mode;
        viewersCanPropose = s.viewers_can_propose;
        requireSummary = s.require_change_summary;
        keep = s.keep_last ? 'last' : s.keep_days ? 'days' : 'all';
        keepLast = s.keep_last || keepLast;
        keepDays = s.keep_days || keepDays;
      })
      .catch((e) => (error = errorText(codeOf(e))));
    loadOverrides().catch((e) => (overrideError = errorText(codeOf(e))));
  });

  async function save(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    saved = false;
    error = '';
    try {
      await api('PATCH', `${base}/settings`, {
        name,
        description,
        approval_mode: approvalMode,
        viewers_can_propose: viewersCanPropose,
        require_change_summary: requireSummary,
        keep_last: keep === 'last' ? keepLast : 0,
        keep_days: keep === 'days' ? keepDays : 0,
      });
      saved = true;
      onchanged();
    } catch (err) {
      error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }

  // Las excepciones se guardan una por una, aparte del formulario de arriba.
  async function changeOverride(action: () => Promise<unknown>) {
    overrideError = '';
    try {
      await action();
      await loadOverrides();
    } catch (err) {
      overrideError = errorText(codeOf(err));
    }
  }

  const addOverride = (e: SubmitEvent) => {
    e.preventDefault();
    const path = prefix.trim().replace(/^\/+|\/+$/g, '');
    return changeOverride(async () => {
      await api('PUT', `${base}/approval-overrides/${path}`, { approval_mode: prefixMode });
      prefix = '';
    });
  };
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

  <fieldset class="stack">
    <legend>{t('settings.approval')}</legend>
    <label><input type="radio" name="approval" value="direct" bind:group={approvalMode} /> {t('settings.approval_direct')}</label>
    <label><input type="radio" name="approval" value="required" bind:group={approvalMode} /> {t('settings.approval_required')}</label>
    <label><input type="checkbox" aria-describedby="viewers-hint" bind:checked={viewersCanPropose} /> {t('settings.viewers_can_propose')}</label>
    <small id="viewers-hint" class="muted">{t('settings.viewers_hint')}</small>
  </fieldset>

  <fieldset class="stack">
    <legend>{t('settings.history')}</legend>
    <label><input type="checkbox" aria-describedby="summary-hint" bind:checked={requireSummary} /> {t('settings.require_summary')}</label>
    <small id="summary-hint" class="muted">{t('settings.require_summary_hint')}</small>
    <label><input type="radio" name="keep" value="all" bind:group={keep} /> {t('settings.keep_all')}</label>
    <label class="keep-row">
      <input type="radio" name="keep" value="last" bind:group={keep} />
      {t('settings.keep_last_before')}
      <input type="number" min="1" max="10000" required={keep === 'last'} disabled={keep !== 'last'} aria-label={t('settings.keep_last_n')} bind:value={keepLast} />
      {t('settings.keep_last_after')}
    </label>
    <label class="keep-row">
      <input type="radio" name="keep" value="days" bind:group={keep} />
      {t('settings.keep_days_before')}
      <input type="number" min="1" max="36500" required={keep === 'days'} disabled={keep !== 'days'} aria-label={t('settings.keep_days_n')} bind:value={keepDays} />
      {t('settings.keep_days_after')}
    </label>
    <small class="muted">{t('settings.keep_hint')}</small>
  </fieldset>

  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#if saved}<p role="status" class="muted">{t('settings.saved')}</p>{/if}
  <button type="submit" class="primary" disabled={busy}>{busy ? t('common.saving') : t('common.save')}</button>
</form>

<section class="settings" aria-labelledby="overrides-title">
  <h2 id="overrides-title">{t('settings.overrides')}</h2>
  <p class="muted">{t('settings.overrides_hint')}</p>
  {#if overrides.length}
    <ul class="override-list">
      {#each overrides as o (o.prefix)}
        <li>
          <code>{area.slug}/{o.prefix}</code>
          <span>{t(`mode.${o.approval_mode}`)}</span>
          <button
            type="button"
            class="link"
            aria-label={t('settings.override_remove', { path: o.prefix })}
            onclick={() => changeOverride(() => api('DELETE', `${base}/approval-overrides/${o.prefix}`))}
          >
            {t('appearance.remove')}
          </button>
        </li>
      {/each}
    </ul>
  {:else}
    <p class="muted">{t('settings.override_none')}</p>
  {/if}
  <form class="inline" onsubmit={addOverride}>
    <label>
      {t('settings.override_path')}
      <input required pattern="/?[a-z0-9]+(-[a-z0-9]+)*(/[a-z0-9]+(-[a-z0-9]+)*)*/?" placeholder="seguridad/politicas" bind:value={prefix} />
    </label>
    <label>
      {t('settings.override_mode')}
      <select bind:value={prefixMode}>
        <option value="required">{t('mode.required')}</option>
        <option value="direct">{t('mode.direct')}</option>
      </select>
    </label>
    <button type="submit" class="primary">{t('settings.override_add')}</button>
  </form>
  {#if overrideError}<p class="error" role="alert">{overrideError}</p>{/if}
</section>
