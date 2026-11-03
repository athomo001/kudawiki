<script lang="ts">
  import { api, codeOf, formatDate, timeAgo, type SessionInfo } from '../lib/api';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';
  import PageHead from '../components/PageHead.svelte';

  // Mi perfil → Sesiones abiertas (SPEC §8.2, §15): dónde está abierta la cuenta, para cerrar lo
  // que no se reconoce. La sesión actual no se cierra aquí: para eso está "Cerrar sesión".
  let sessions = $state<SessionInfo[]>([]);
  let busy = $state(false);
  let error = $state('');
  let closed = $state(0);

  async function run(action?: () => Promise<void>) {
    busy = true;
    error = '';
    try {
      await action?.();
      sessions = await api<SessionInfo[]>('GET', '/me/sessions');
    } catch (e) {
      error = errorText(codeOf(e));
    } finally {
      busy = false;
    }
  }

  // "Chrome en Windows"; lo que no se reconoce, sin inventar.
  function device(s: SessionInfo): string {
    const { browser, os } = s.device;
    if (browser && os) return t('sessions.device', { browser, os });
    return browser || os || t('sessions.unknown_device');
  }

  const revokeOthers = () =>
    run(async () => {
      closed = (await api<{ sessions_closed: number }>('POST', '/me/sessions/revoke-others')).sessions_closed;
    });

  run();
</script>

<PageHead context={`${t('profile.title')} · ${t('profile.group_security')}`} title={t('sessions.title')} description={t('sessions.hint')}>
  {#snippet actions()}
    <button type="button" class="secondary" disabled={busy || sessions.length < 2} onclick={revokeOthers}>{t('sessions.close_others')}</button>
  {/snippet}
</PageHead>

{#if closed}<p role="status" class="notice">{t('sessions.closed', { n: String(closed) })}</p>{/if}
{#if error}<p class="error" role="alert">{error}</p>{/if}

<div class="table-scroll">
  <table class="admin-table">
    <thead>
      <tr>
        <th scope="col">{t('profile.col_device')}</th>
        <th scope="col">IP</th>
        <th scope="col">{t('profile.col_opened')}</th>
        <th scope="col">{t('profile.col_last_seen')}</th>
        <th scope="col"><span class="sr-only">{t('common.actions')}</span></th>
      </tr>
    </thead>
    <tbody>
      {#each sessions as s (s.id)}
        <tr>
          <td><strong>{device(s)}</strong> {#if s.current}<span class="pill plain ok">{t('profile.this_session')}</span>{/if}</td>
          <td class="mono">{s.ip || t('sessions.no_ip')}</td>
          <td>{formatDate(s.created_at, locale())}</td>
          <td>{timeAgo(s.last_seen_at, locale())}</td>
          <td class="row-actions">
            {#if !s.current}
              <button
                type="button"
                class="link danger"
                disabled={busy}
                aria-label={t('sessions.close_label', { device: device(s) })}
                onclick={() => run(() => api('DELETE', `/me/sessions/${s.id}`))}>{t('sessions.close')}</button
              >
            {/if}
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>
