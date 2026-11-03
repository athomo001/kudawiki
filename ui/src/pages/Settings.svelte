<script lang="ts">
  import { api, codeOf, type ApprovalOverride, type Area } from '../lib/api';
  import { t, errorText, type Key } from '../lib/i18n/index.svelte';
  import { countChanges } from '../lib/changes';
  import PageHead from '../components/PageHead.svelte';
  import Setting from '../components/Setting.svelte';
  import SaveBar from '../components/SaveBar.svelte';

  // Configuración del área para su admin (SPEC §3.1, §14.3): nombre y descripción, que ven todos
  // los miembros, y desde F4 la aprobación de cambios (§5.1, §5.2.1, §19.5) y el historial (§4.3).
  // Diseño "Kudawiki — Administración" (§15): tres pantallas del menú Administrar área, cada una
  // con sus secciones en dos columnas. Todas guardan la configuración entera, así que lo que no se
  // ve en una queda como estaba.
  type Section = 'general' | 'publishing' | 'content';
  let { area, section, onchanged }: { area: Area; section: Section; onchanged: () => void } = $props();

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
  // Vigencia (§4.8): apagada por defecto (§19.11); al encenderla se propone un año.
  let review = $state(false);
  let reviewDays = $state(365);
  // Cuestionarios (§4.16.7): encendidos por defecto.
  let quizzes = $state(true);
  // Marca de agua en N2 (§4.15): en N3 y sellados va siempre.
  let watermarkN2 = $state(false);
  let overrides = $state<ApprovalOverride[]>([]);
  let prefix = $state('');
  let prefixMode = $state<Mode>('required');
  let busy = $state(false);
  let saved = $state(false);
  let error = $state('');
  let overrideError = $state('');

  type Settings = {
    name: string;
    description: string;
    approval_mode: Mode;
    viewers_can_propose: boolean;
    require_change_summary: boolean;
    keep_last: number;
    keep_days: number;
    review_days: number;
    quizzes_enabled: boolean;
    watermark_n2: boolean;
  };
  // last: lo guardado, para contar los cambios y para Descartar.
  let last = $state<Settings | null>(null);

  const base = $derived(`/w/${area.slug}`);
  const context = $derived(`${area.name} · ${t('area_admin.title')}`);
  const heads: Record<Section, { title: Key; desc: Key }> = {
    general: { title: 'area_admin.general', desc: 'area_admin.general_desc' },
    publishing: { title: 'area_admin.publishing', desc: 'area_admin.publishing_desc' },
    content: { title: 'area_admin.content', desc: 'area_admin.content_desc' },
  };
  const loadOverrides = async () => (overrides = await api<ApprovalOverride[]>('GET', `${base}/approval-overrides`));

  function apply(s: Settings) {
    ({ name, description } = s);
    approvalMode = s.approval_mode;
    viewersCanPropose = s.viewers_can_propose;
    requireSummary = s.require_change_summary;
    keep = s.keep_last ? 'last' : s.keep_days ? 'days' : 'all';
    keepLast = s.keep_last || keepLast;
    keepDays = s.keep_days || keepDays;
    review = s.review_days > 0;
    reviewDays = s.review_days || reviewDays;
    quizzes = s.quizzes_enabled;
    watermarkN2 = s.watermark_n2;
  }

  const current = (): Settings => ({
    name,
    description,
    approval_mode: approvalMode,
    viewers_can_propose: viewersCanPropose,
    require_change_summary: requireSummary,
    keep_last: keep === 'last' ? keepLast : 0,
    keep_days: keep === 'days' ? keepDays : 0,
    review_days: review ? reviewDays : 0,
    quizzes_enabled: quizzes,
    watermark_n2: watermarkN2,
  });
  const changes = $derived(last ? countChanges(last, current()) : 0);

  $effect(() => {
    api<Settings>('GET', `${base}/settings`)
      .then((s) => {
        apply(s);
        last = current();
      })
      .catch((e) => (error = errorText(codeOf(e))));
    loadOverrides().catch((e) => (overrideError = errorText(codeOf(e))));
  });

  async function save() {
    busy = true;
    saved = false;
    error = '';
    try {
      const body = current();
      await api('PATCH', `${base}/settings`, body);
      last = body;
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

<PageHead {context} title={t(heads[section].title)} description={t(heads[section].desc)} />

<form
  onsubmit={(e) => {
    e.preventDefault();
    save();
  }}
>
  {#if section === 'general'}
    <Setting title={t('area_admin.name_section')} description={t('area_admin.name_section_desc')}>
      <label class="field">
        {t('common.name')}
        <input required maxlength="80" bind:value={name} />
      </label>
      <label class="field">
        {t('settings.description')}
        <textarea rows="3" maxlength="500" bind:value={description}></textarea>
        <small class="muted">{t('settings.description_hint')}</small>
      </label>
      <label class="field">
        {t('area_admin.address')}
        <input class="mono" readonly value={`/w/${area.slug}`} />
        <small class="muted">{t('admin.area_slug_fixed')}</small>
      </label>
    </Setting>
    <Setting title={t('settings.quizzes')} description={t('settings.quizzes_hint')}>
      <label class="check">
        <input type="checkbox" bind:checked={quizzes} />
        {t('settings.quizzes_on')}
      </label>
    </Setting>
    <Setting title={t('settings.watermark')} description={t('settings.watermark_hint')}>
      <label class="check">
        <input type="checkbox" bind:checked={watermarkN2} />
        {t('settings.watermark_n2')}
      </label>
    </Setting>
  {:else if section === 'publishing'}
    <Setting title={t('settings.approval')} description={t('area_admin.approval_desc')}>
      <div class="choices" role="radiogroup" aria-label={t('settings.approval')}>
        <label class="choice">
          <input type="radio" name="approval" value="direct" bind:group={approvalMode} />
          <span><strong>{t('mode.direct')}</strong><small>{t('settings.approval_direct')}</small></span>
        </label>
        <label class="choice">
          <input type="radio" name="approval" value="required" bind:group={approvalMode} />
          <span><strong>{t('mode.required')}</strong><small>{t('settings.approval_required')}</small></span>
        </label>
      </div>
      <label class="check check-hint">
        <input type="checkbox" bind:checked={viewersCanPropose} />
        <span><strong>{t('settings.viewers_can_propose')}</strong><small>{t('settings.viewers_hint')}</small></span>
      </label>
    </Setting>
  {:else}
    <Setting title={t('settings.history')} description={t('settings.keep_hint')}>
      <label class="check check-hint">
        <input type="checkbox" bind:checked={requireSummary} />
        <span><strong>{t('settings.require_summary')}</strong><small>{t('settings.require_summary_hint')}</small></span>
      </label>
      <fieldset class="keep">
        <legend>{t('area_admin.keep')}</legend>
        <label class="keep-row"><input type="radio" name="keep" value="all" bind:group={keep} /> {t('settings.keep_all')}</label>
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
      </fieldset>
    </Setting>
    <Setting title={t('settings.review')} description={t('settings.review_hint')}>
      <label class="keep-row">
        <input type="checkbox" bind:checked={review} />
        {t('settings.review_before')}
        <input type="number" min="1" max="3650" required={review} disabled={!review} aria-label={t('settings.review_n')} bind:value={reviewDays} />
        {t('settings.review_after')}
      </label>
    </Setting>
  {/if}
</form>

{#if section === 'publishing'}
  <Setting title={t('settings.overrides')} description={t('settings.overrides_hint')}>
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
        <input required class="mono" pattern="/?[a-z0-9]+(-[a-z0-9]+)*(/[a-z0-9]+(-[a-z0-9]+)*)*/?" placeholder="seguridad/politicas" bind:value={prefix} />
      </label>
      <label>
        {t('settings.override_mode')}
        <select bind:value={prefixMode}>
          <option value="required">{t('mode.required')}</option>
          <option value="direct">{t('mode.direct')}</option>
        </select>
      </label>
      <button type="submit" class="secondary">{t('settings.override_add')}</button>
    </form>
    {#if overrideError}<p class="error" role="alert">{overrideError}</p>{/if}
  </Setting>
{/if}

{#if error}<p class="error" role="alert">{error}</p>{/if}
{#if saved && !changes}<p role="status" class="notice">{t('settings.saved')}</p>{/if}
<SaveBar count={changes} {busy} onsave={save} ondiscard={() => last && apply(last)} />
