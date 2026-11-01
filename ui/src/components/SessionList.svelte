<script lang="ts">
  import { api, codeOf, formatDate, timeAgo, type SessionInfo } from '../lib/api';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';

  // Mis sesiones (SPEC §8.2): dónde está abierta la cuenta, para cerrar lo que no se reconoce. La
  // sesión actual no se cierra aquí: para eso está "Cerrar sesión".
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

<section class="stack" id="sessions" aria-labelledby="sessions-title">
  <h2 id="sessions-title">{t('sessions.title')}</h2>
  <p class="muted small">{t('sessions.hint')}</p>
  <ul class="plain session-list">
    {#each sessions as s (s.id)}
      <li>
        <div>
          <strong>{device(s)}</strong>
          {#if s.current}<span class="muted">· {t('sessions.current')}</span>{/if}
          <br />
          <span class="muted small">
            {s.ip || t('sessions.no_ip')} · {t('sessions.started', { when: formatDate(s.created_at, locale()) })} ·
            {t('sessions.last_seen', { ago: timeAgo(s.last_seen_at, locale()) })}
          </span>
        </div>
        {#if !s.current}
          <button
            type="button"
            class="link"
            disabled={busy}
            aria-label={t('sessions.close_label', { device: device(s) })}
            onclick={() => run(() => api('DELETE', `/me/sessions/${s.id}`))}>{t('sessions.close')}</button
          >
        {/if}
      </li>
    {/each}
  </ul>
  {#if sessions.length > 1}
    <button type="button" disabled={busy} onclick={revokeOthers}>{t('sessions.close_others')}</button>
  {/if}
  {#if closed}<p role="status" class="muted">{t('sessions.closed', { n: String(closed) })}</p>{/if}
  {#if error}<p class="error" role="alert">{error}</p>{/if}
</section>
