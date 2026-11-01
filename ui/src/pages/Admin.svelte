<script lang="ts">
  import { api, codeOf, formatDate } from '../lib/api';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';
  import Dialog from '../components/Dialog.svelte';
  import OffboardDialog from '../components/OffboardDialog.svelte';

  // offboarding_at: baja en curso, que se completa ese día; offboarded_at: baja completa (§8.4).
  type User = {
    id: string;
    email: string;
    full_name: string;
    is_global_admin: boolean;
    is_active: boolean;
    invited: boolean;
    totp_enabled: boolean;
    offboarding_at?: string;
    offboarded_at?: string;
  };
  type Area = { id: string; slug: string; name: string; description: string };

  // onchanged recarga la sesión: el nombre nuevo de un área sale también en el selector y el menú.
  // selfId: a sí mismo no se ofrece cerrar todas las sesiones; para eso está "Mis sesiones".
  let { onchanged, selfId }: { onchanged: () => void; selfId: string } = $props();

  let users = $state<User[]>([]);
  let areas = $state<Area[]>([]);
  let busy = $state(false);
  let error = $state('');

  let invite = $state({ email: '', full_name: '' });
  // Lo que pasó con la última invitación o el último enlace para restablecer: salió por correo,
  // o (sin correo) el enlace para entregar a mano, que se muestra una sola vez (SPEC §8.5.3).
  type Sent = { delivery: 'email' | 'manual'; invite_url?: string; reset_url?: string };
  let result = $state<{ kind: 'invite' | 'reset'; email: string; url: string } | null>(null);
  let copied = $state(false);
  let mailOn = $state(false);
  let area = $state({ name: '', slug: '' });
  // El área que se está renombrando desde la lista (pedido del dueño, 2026-10-30): nombre y
  // descripción, sin ser miembro. La dirección (/slug) no cambia aquí.
  let editing = $state<Area | null>(null);
  // Restablecer el 2FA de quien perdió el celular (§14.2): se confirma en un diálogo.
  let resetting = $state<User | null>(null);
  let confirmingReset = $state(false);
  // Cerrar todas las sesiones de alguien (§8.2, B16), por ejemplo si perdió el notebook.
  let revoking = $state<User | null>(null);
  let confirmingRevoke = $state(false);
  // Lo que pasó con la última acción sobre una persona: sesiones cerradas, baja, reactivación.
  let notice = $state('');
  // La baja (§8.4) y el superadmin (B15), cada uno con su confirmación.
  let offboarding = $state<User | null>(null);
  let confirmingOffboard = $state(false);
  let promoting = $state<User | null>(null);
  let confirmingSuper = $state(false);

  async function run(action?: () => Promise<void>) {
    busy = true;
    error = '';
    try {
      await action?.();
      [users, areas] = await Promise.all([api<User[]>('GET', '/admin/users'), api<Area[]>('GET', '/admin/workspaces')]);
    } catch (e) {
      error = errorText(codeOf(e));
    } finally {
      busy = false;
    }
  }

  function show(kind: 'invite' | 'reset', email: string, res: Sent) {
    result = { kind, email, url: res.delivery === 'email' ? '' : (res.invite_url ?? res.reset_url ?? '') };
    copied = false;
  }

  function sendInvite(e: SubmitEvent) {
    e.preventDefault();
    run(async () => {
      show('invite', invite.email, await api<Sent>('POST', '/admin/users', invite));
      invite = { email: '', full_name: '' };
    });
  }

  // "Enviar enlace para restablecer" (§8.5.3): por correo si lo hay; si no, el enlace a mano.
  function sendReset(u: User) {
    run(async () => {
      show('reset', u.email, await api<Sent>('POST', `/admin/users/${u.id}/password-reset`, { delivery: mailOn ? 'email' : 'manual' }));
    });
  }

  function createArea(e: SubmitEvent) {
    e.preventDefault();
    run(async () => {
      await api('POST', '/admin/workspaces', area);
      area = { name: '', slug: '' };
    });
  }

  function renameArea(e: SubmitEvent) {
    e.preventDefault();
    const a = editing;
    if (!a) return;
    run(async () => {
      await api('PATCH', `/admin/workspaces/${a.id}`, { name: a.name, description: a.description });
      editing = null;
      onchanged();
    });
  }

  // Una invitación pendiente (vencida o no) se vuelve a generar: el enlace anterior deja de servir (SPEC §8.5.3).
  function resend(u: User) {
    run(async () => {
      show('invite', u.email, await api<Sent>('POST', `/admin/users/${u.id}/invite`));
    });
  }

  function resetTotp(e: SubmitEvent) {
    e.preventDefault();
    const u = resetting;
    if (!u) return;
    run(async () => {
      await api('PATCH', `/admin/users/${u.id}`, { reset_totp: true });
      confirmingReset = false;
    });
  }

  function revokeSessions(e: SubmitEvent) {
    e.preventDefault();
    const u = revoking;
    if (!u) return;
    run(async () => {
      const res = await api<{ sessions_closed: number }>('PATCH', `/admin/users/${u.id}`, { revoke_sessions: true });
      notice = t('admin.sessions_revoked', { name: u.full_name, n: String(res.sessions_closed) });
      confirmingRevoke = false;
    });
  }

  function reactivate(u: User) {
    run(async () => {
      await api('PATCH', `/admin/users/${u.id}`, { is_active: true });
      notice = t('admin.reactivated', { name: u.full_name });
    });
  }

  function setSuperadmin(e: SubmitEvent) {
    e.preventDefault();
    const u = promoting;
    if (!u) return;
    run(async () => {
      await api('PATCH', `/admin/users/${u.id}`, { is_global_admin: !u.is_global_admin });
      confirmingSuper = false;
    });
  }

  function status(u: User): string {
    if (u.offboarded_at) return t('admin.status_offboarded', { date: formatDate(u.offboarded_at, locale()) });
    if (u.offboarding_at) return t('admin.status_inactive', { date: formatDate(u.offboarding_at, locale()) });
    return u.invited ? t('admin.status_invited') : t('admin.status_active');
  }

  async function copy() {
    if (!result?.url) return;
    await navigator.clipboard.writeText(result.url);
    copied = true;
  }

  run();
  api<{ enabled: boolean }>('GET', '/admin/smtp')
    .then((s) => (mailOn = s.enabled))
    .catch(() => {});
</script>

<h1>{t('nav.admin')}</h1>
{#if error}<p class="error" role="alert">{error}</p>{/if}
{#if notice}<p role="status" class="muted">{notice}</p>{/if}

<section aria-labelledby="people">
  <h2 id="people">{t('admin.people')}</h2>
  <!-- En el teléfono la tabla se desplaza sola, sin correr la página. -->
  <div class="table-scroll">
    <table>
      <thead>
        <tr>
          <th scope="col">{t('common.name')}</th>
          <th scope="col">{t('auth.email')}</th>
          <th scope="col">{t('admin.status')}</th>
          <th scope="col"><span class="sr-only">{t('common.actions')}</span></th>
        </tr>
      </thead>
      <tbody>
        {#each users as u (u.id)}
          <tr>
            <td>{u.full_name}</td>
            <td>{u.email}</td>
            <td>
              {status(u)}
              {#if u.is_global_admin}· {t('admin.superadmin')}{/if}
              {#if u.totp_enabled}· {t('admin.totp_on')}{/if}
            </td>
            <td>
              {#if u.offboarded_at}
                <button
                  type="button"
                  class="link"
                  aria-label={t('admin.reactivate_label', { name: u.full_name })}
                  disabled={busy}
                  onclick={() => reactivate(u)}>{t('admin.reactivate')}</button
                >
              {:else if u.offboarding_at}
                <!-- Durante la gracia nada se perdió todavía: su espacio personal sigue ahí. -->
                <a href={`/w/~${u.id.toLowerCase()}`} aria-label={t('personal.audit_label', { name: u.full_name })}>{t('personal.audit_link')}</a> ·
                <button
                  type="button"
                  class="link"
                  aria-label={t('admin.reactivate_label', { name: u.full_name })}
                  disabled={busy}
                  onclick={() => reactivate(u)}>{t('admin.reactivate')}</button
                >
                ·
                <button
                  type="button"
                  class="link"
                  disabled={busy}
                  onclick={() => ((offboarding = u), (notice = ''), (confirmingOffboard = true))}>{t('admin.offboard_complete_now')}</button
                >
              {:else if u.invited}
                <button type="button" class="link" disabled={busy} onclick={() => resend(u)}>{t('admin.invite_resend')}</button>
              {:else}
                <!-- Modo auditoría (§7): solo lectura, y cada pedido queda en la auditoría. -->
                <a href={`/w/~${u.id.toLowerCase()}`} aria-label={t('personal.audit_label', { name: u.full_name })}>{t('personal.audit_link')}</a> ·
                <button
                  type="button"
                  class="link"
                  aria-label={t('admin.reset_label', { name: u.full_name })}
                  disabled={busy}
                  onclick={() => sendReset(u)}>{t('admin.reset')}</button
                >
                {#if u.totp_enabled}
                  ·
                  <button
                    type="button"
                    class="link"
                    aria-label={t('admin.totp_reset_label', { name: u.full_name })}
                    disabled={busy}
                    onclick={() => ((resetting = u), (confirmingReset = true))}>{t('admin.totp_reset')}</button
                  >
                {/if}
                {#if u.id !== selfId}
                  ·
                  <button
                    type="button"
                    class="link"
                    aria-label={t('admin.sessions_revoke_label', { name: u.full_name })}
                    disabled={busy}
                    onclick={() => ((revoking = u), (notice = ''), (confirmingRevoke = true))}>{t('admin.sessions_revoke')}</button
                  >
                  ·
                  <button
                    type="button"
                    class="link"
                    aria-label={t(u.is_global_admin ? 'admin.superadmin_revoke_label' : 'admin.superadmin_grant_label', { name: u.full_name })}
                    disabled={busy}
                    onclick={() => ((promoting = u), (confirmingSuper = true))}
                    >{t(u.is_global_admin ? 'admin.superadmin_revoke' : 'admin.superadmin_grant')}</button
                  >
                  ·
                  <button
                    type="button"
                    class="link danger"
                    aria-label={t('admin.offboard_label', { name: u.full_name })}
                    disabled={busy}
                    onclick={() => ((offboarding = u), (notice = ''), (confirmingOffboard = true))}>{t('admin.offboard')}</button
                  >
                {/if}
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>

  <h3>{t('admin.invite')}</h3>
  <form class="inline" onsubmit={sendInvite}>
    <label>{t('auth.email')} <input type="email" required bind:value={invite.email} /></label>
    <label>{t('common.name')} <input required bind:value={invite.full_name} /></label>
    <button type="submit" class="primary" disabled={busy}>{t('admin.invite_send')}</button>
  </form>
  {#if result}
    <div class="notice" role="status">
      {#if result.url}
        <p>{t(result.kind === 'invite' ? 'admin.invite_link' : 'admin.reset_link', { email: result.email })}</p>
        <code>{result.url}</code>
        <button type="button" class="link" onclick={copy}>{copied ? t('common.copied') : t('common.copy')}</button>
      {:else}
        <p>{t(result.kind === 'invite' ? 'admin.invite_emailed' : 'admin.reset_emailed', { email: result.email })}</p>
      {/if}
    </div>
  {/if}
</section>

<section aria-labelledby="areas">
  <h2 id="areas">{t('admin.areas')}</h2>
  <ul class="plain">
    {#each areas as a (a.id)}
      <li>
        {#if editing?.id === a.id}
          <form class="stack area-edit" aria-label={t('admin.area_edit_label', { area: a.name })} onsubmit={renameArea}>
            <label class="field">
              {t('common.name')}
              <input required maxlength="80" bind:value={editing.name} />
            </label>
            <label class="field">
              {t('settings.description')}
              <textarea rows="2" maxlength="500" aria-describedby="area-edit-hint" bind:value={editing.description}></textarea>
            </label>
            <small id="area-edit-hint" class="muted">{t('admin.area_edit_hint', { slug: a.slug })}</small>
            <div class="inline">
              <button type="submit" class="primary" disabled={busy}>{busy ? t('common.saving') : t('common.save')}</button>
              <button type="button" class="link" onclick={() => (editing = null)}>{t('common.cancel')}</button>
            </div>
          </form>
        {:else}
          <strong>{a.name}</strong> <span class="muted">/{a.slug}</span> ·
          <button type="button" class="link" aria-label={t('admin.area_edit_label', { area: a.name })} onclick={() => (editing = { ...a })}>
            {t('admin.area_edit')}
          </button>
          · <a href={`/admin/areas/${a.id}`}>{t('admin.members_link')}</a>
        {/if}
      </li>
    {/each}
  </ul>

  <h3>{t('admin.create_area')}</h3>
  <form class="inline" onsubmit={createArea}>
    <label>{t('common.name')} <input required bind:value={area.name} /></label>
    <label>
      {t('admin.slug')}
      <input required pattern="[a-z0-9][a-z0-9\-]*[a-z0-9]" maxlength="40" aria-describedby="slug-hint" bind:value={area.slug} />
    </label>
    <button type="submit" class="primary" disabled={busy}>{t('common.save')}</button>
  </form>
  <small id="slug-hint" class="muted">{t('admin.slug_hint')}</small>
</section>

<Dialog title={t('admin.totp_reset')} bind:open={confirmingReset}>
  <form class="stack" onsubmit={resetTotp}>
    <p>{t('admin.totp_reset_confirm', { name: resetting?.full_name ?? '' })}</p>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <button type="submit" class="primary danger-btn" disabled={busy}>{t('admin.totp_reset')}</button>
  </form>
</Dialog>

<Dialog title={t('admin.sessions_revoke')} bind:open={confirmingRevoke}>
  <form class="stack" onsubmit={revokeSessions}>
    <p>{t('admin.sessions_revoke_confirm', { name: revoking?.full_name ?? '' })}</p>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <button type="submit" class="primary danger-btn" disabled={busy}>{t('admin.sessions_revoke')}</button>
  </form>
</Dialog>

<Dialog title={t(promoting?.is_global_admin ? 'admin.superadmin_revoke' : 'admin.superadmin_grant')} bind:open={confirmingSuper}>
  <form class="stack" onsubmit={setSuperadmin}>
    <p>
      {t(promoting?.is_global_admin ? 'admin.superadmin_revoke_confirm' : 'admin.superadmin_grant_confirm', { name: promoting?.full_name ?? '' })}
    </p>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <button type="submit" class="primary" disabled={busy}>
      {t(promoting?.is_global_admin ? 'admin.superadmin_revoke' : 'admin.superadmin_grant')}
    </button>
  </form>
</Dialog>

<OffboardDialog user={offboarding} bind:open={confirmingOffboard} ondone={(m) => run(async () => void (notice = m))} />
