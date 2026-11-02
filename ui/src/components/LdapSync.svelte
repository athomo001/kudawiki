<script lang="ts">
  import { api, codeOf, formatDate } from '../lib/api';
  import { t, has, errorText, locale, type Key } from '../lib/i18n/index.svelte';
  import Dialog from './Dialog.svelte';

  // Estado del directorio y sincronización (SPEC §8.3.3, §8.3.4, §8.6.5). "Ver qué haría" no
  // cambia nada; "Sincronizar ahora" aplica, y si una salvaguarda la detiene se muestra por qué y
  // se ofrece forzarla, con una confirmación aparte.

  type Status = {
    enabled: boolean;
    last_login?: string;
    last_sync?: { at: string; aborted?: string };
    last_error?: { at: string; code: string };
    cert_days?: number;
    debug_until?: string;
  };
  type Leaver = { user_id: string; name: string; email: string; reason: 'disabled' | 'not_found' | 'outside_base' | 'no_mapped_group' };
  type Report = {
    dry_run: boolean;
    forced: boolean;
    aborted?: 'removal_ratio' | 'empty_group' | 'truncated';
    group?: string;
    groups: number;
    people: number;
    created: number;
    updated: number;
    added: number;
    changed: number;
    removed: number;
    managed: number;
    ratio: number;
    max_ratio: number;
    deactivated: Leaver[];
    conflicts: string[];
  };

  let status = $state<Status | null>(null);
  let report = $state<Report | null>(null);
  let busy = $state(false);
  let error = $state('');
  let confirmForce = $state(false);

  const when = (s: string) => formatDate(s, locale());
  const pct = (n: number) => `${Math.round(n * 100)}%`;
  const codeText = (code: string) => (has(`ldap.status_code.${code}`) ? t(`ldap.status_code.${code}` as Key) : code);

  async function load() {
    try {
      status = await api<Status>('GET', '/admin/ldap/status');
    } catch (err) {
      error = errorText(codeOf(err));
    }
  }

  async function sync(mode: 'preview' | 'apply' | 'force') {
    busy = true;
    error = '';
    confirmForce = false;
    try {
      report =
        mode === 'preview'
          ? await api<Report>('POST', '/admin/ldap/sync/preview')
          : await api<Report>('POST', '/admin/ldap/sync', { force: mode === 'force' });
      await load();
    } catch (err) {
      error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }

  async function setDebug(on: boolean) {
    busy = true;
    error = '';
    try {
      await api('PUT', '/admin/ldap/debug', { on });
      await load();
    } catch (err) {
      error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }

  load();
</script>

{#if status?.enabled}
  <section aria-labelledby="ldap-sync-title" class="ldap-sync">
    <h2 id="ldap-sync-title">{t('ldap.sync_title')}</h2>
    <dl class="ldap-facts">
      <dt>{t('ldap.status_last_sync')}</dt>
      <dd>
        {#if status.last_sync}
          {when(status.last_sync.at)}
          {#if status.last_sync.aborted}<span class="error">· {t(`ldap.sync_aborted_short.${status.last_sync.aborted}` as Key)}</span>{/if}
        {:else}{t('ldap.status_never')}{/if}
      </dd>
      <dt>{t('ldap.status_last_login')}</dt>
      <dd>{status.last_login ? when(status.last_login) : t('ldap.status_never')}</dd>
      {#if status.last_error}
        <dt>{t('ldap.status_last_error')}</dt>
        <dd class="error">{when(status.last_error.at)} · {codeText(status.last_error.code)}</dd>
      {/if}
      {#if status.cert_days !== undefined}
        <dt>{t('ldap.status_cert')}</dt>
        <dd class={status.cert_days < 30 ? 'error' : ''}>{t('ldap.status_cert_days', { days: String(status.cert_days) })}</dd>
      {/if}
    </dl>

    <div class="conflict-actions">
      <button type="button" class="secondary" disabled={busy} onclick={() => sync('preview')}>{t('ldap.sync_preview')}</button>
      <button type="button" class="primary" disabled={busy} onclick={() => sync('apply')}>{busy ? t('ldap.sync_running') : t('ldap.sync_now')}</button>
    </div>
    {#if error}<p class="error" role="alert">{error}</p>{/if}

    {#if report}
      <div class="ldap-report" role="status">
        {#if report.aborted}
          <p class="error">{t(`ldap.sync_aborted.${report.aborted}`, { group: report.group ?? '' })}</p>
        {:else if report.groups === 0}
          <p class="muted">{t('ldap.sync_no_rules')}</p>
        {:else}
          <p class="notice">{report.dry_run ? t('ldap.sync_would') : t('ldap.sync_done')}</p>
        {/if}
        {#if report.groups > 0}
          <ul>
            <li>{t('ldap.sync_people', { groups: String(report.groups), people: String(report.people) })}</li>
            <li>{t('ldap.sync_accounts', { created: String(report.created), updated: String(report.updated) })}</li>
            <li>
              {t('ldap.sync_memberships', {
                added: String(report.added),
                changed: String(report.changed),
                removed: String(report.removed),
                managed: String(report.managed),
                ratio: pct(report.ratio),
                max: pct(report.max_ratio),
              })}
            </li>
          </ul>
        {/if}
        {#if report.deactivated.length}
          <h3>{t('ldap.sync_deactivated')}</h3>
          <ul>
            {#each report.deactivated as l (l.user_id)}
              <li>{l.name} · {l.email} — {t(`ldap.sync_reason.${l.reason}`)}</li>
            {/each}
          </ul>
        {/if}
        {#if report.conflicts.length}
          <p class="error">{t('ldap.sync_conflicts', { emails: report.conflicts.join(', ') })}</p>
        {/if}
        <!-- Una lista cortada no se puede forzar: daría de baja a quien quedó afuera (§8.3.4). -->
        {#if report.aborted && report.aborted !== 'truncated'}
          <button type="button" class="link danger" disabled={busy} onclick={() => (confirmForce = true)}>{t('ldap.sync_force')}</button>
        {/if}
      </div>
    {/if}

    <h3>{t('ldap.debug_title')}</h3>
    {#if status.debug_until}
      <p>
        {t('ldap.debug_on', { until: when(status.debug_until) })}
        <button type="button" class="link" disabled={busy} onclick={() => setDebug(false)}>{t('ldap.debug_stop')}</button>
      </p>
    {:else}
      <p class="muted">
        {t('ldap.debug_hint')}
        <button type="button" class="link" disabled={busy} onclick={() => setDebug(true)}>{t('ldap.debug_start')}</button>
      </p>
    {/if}
  </section>
{/if}

<Dialog title={t('ldap.sync_force_title')} bind:open={confirmForce}>
  <form
    class="stack"
    onsubmit={(e) => {
      e.preventDefault();
      sync('force');
    }}
  >
    <p>{t('ldap.sync_force_body', { removed: String(report?.removed ?? 0), deactivated: String(report?.deactivated.length ?? 0) })}</p>
    <button type="submit" class="primary danger-btn" disabled={busy}>{t('ldap.sync_force')}</button>
  </form>
</Dialog>
