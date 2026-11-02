<script lang="ts">
  import { api, codeOf, formatDate, type Role } from '../lib/api';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';
  import Dialog from '../components/Dialog.svelte';
  import Dropdown from '../components/Dropdown.svelte';
  import Icon from '../components/Icon.svelte';
  import PageHead from '../components/PageHead.svelte';
  import OffboardDialog from '../components/OffboardDialog.svelte';
  import LinkDirectoryDialog from '../components/LinkDirectoryDialog.svelte';

  // Administración → Personas (SPEC §14.2, §15, diseño "Kudawiki — Administración"): una tabla a
  // todo el ancho con cómo entra cada persona, su verificación en dos pasos, sus áreas y su estado.
  // Las acciones van en el menú ⋯ de cada fila, y solo las que aplican a esa persona.

  // offboarding_at: baja en curso, que se completa ese día; offboarded_at: baja completa (§8.4).
  type User = {
    id: string;
    email: string;
    full_name: string;
    is_global_admin: boolean;
    is_active: boolean;
    invited: boolean;
    totp_enabled: boolean;
    passkeys: number;
    auth_source: 'local' | 'ldap'; // ldap: entra con la contraseña del directorio (§8.6)
    areas: { name: string; role: Role }[];
    offboarding_at?: string;
    offboarded_at?: string;
  };
  type Filter = 'all' | 'active' | 'invited' | 'leaving' | 'ldap';

  // selfId: a sí mismo no se ofrece cerrar todas las sesiones; para eso está "Mis sesiones".
  let { selfId }: { selfId: string } = $props();

  let users = $state<User[]>([]);
  let busy = $state(false);
  let error = $state('');
  let query = $state('');
  let filter = $state<Filter>('all');

  let invite = $state({ email: '', full_name: '' });
  let inviting = $state(false);
  // Lo que pasó con la última invitación o el último enlace para restablecer: salió por correo,
  // o (sin correo) el enlace para entregar a mano, que se muestra una sola vez (SPEC §8.5.3).
  type Sent = { delivery: 'email' | 'manual'; invite_url?: string; reset_url?: string };
  let result = $state<{ kind: 'invite' | 'reset'; email: string; url: string } | null>(null);
  let copied = $state(false);
  let mailOn = $state(false);
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
  // Vincular una cuenta local con el directorio (§8.6.3); solo con el directorio activo.
  let linking = $state<User | null>(null);
  let confirmingLink = $state(false);
  let ldapOn = $state(false);

  const leaving = (u: User) => !!(u.offboarding_at || u.offboarded_at);
  const matches: Record<Filter, (u: User) => boolean> = {
    all: () => true,
    active: (u) => !u.invited && !leaving(u),
    invited: (u) => u.invited && !leaving(u),
    leaving,
    ldap: (u) => u.auth_source === 'ldap',
  };
  const filters: Filter[] = ['all', 'active', 'invited', 'leaving', 'ldap'];
  const shown = $derived.by(() => {
    const q = query.trim().toLowerCase();
    return users.filter((u) => matches[filter](u) && (!q || u.full_name.toLowerCase().includes(q) || u.email.toLowerCase().includes(q)));
  });

  async function run(action?: () => Promise<void>) {
    busy = true;
    error = '';
    try {
      await action?.();
      users = await api<User[]>('GET', '/admin/users');
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
      inviting = false;
    });
  }

  // "Enviar enlace para restablecer" (§8.5.3): por correo si lo hay; si no, el enlace a mano.
  function sendReset(u: User) {
    run(async () => {
      show('reset', u.email, await api<Sent>('POST', `/admin/users/${u.id}/password-reset`, { delivery: mailOn ? 'email' : 'manual' }));
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

  function status(u: User): { text: string; tone: 'ok' | 'warn' | 'neutral' } {
    if (u.offboarded_at) return { text: t('admin.status_offboarded', { date: formatDate(u.offboarded_at, locale()) }), tone: 'neutral' };
    if (u.offboarding_at) return { text: t('admin.status_inactive', { date: formatDate(u.offboarding_at, locale()) }), tone: 'warn' };
    return u.invited ? { text: t('admin.status_invited'), tone: 'neutral' } : { text: t('admin.status_active'), tone: 'ok' };
  }

  function secondFactor(u: User): string {
    if (u.totp_enabled && u.passkeys) return t('admin.2fa_both');
    if (u.totp_enabled) return t('admin.2fa_app');
    if (u.passkeys) return t('admin.2fa_passkey');
    return '';
  }

  const initials = (name: string) =>
    name
      .split(/\s+/)
      .filter(Boolean)
      .slice(0, 2)
      .map((w) => w.charAt(0).toUpperCase())
      .join('');

  // Abre la confirmación de una acción desde el menú de la fila.
  function confirm(set: () => void) {
    notice = '';
    set();
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
  api<{ enabled: boolean }>('GET', '/admin/ldap/status')
    .then((s) => (ldapOn = s.enabled))
    .catch(() => {});
</script>

<PageHead context={`${t('nav.admin')} · ${t('admin.group_access')}`} title={t('admin.people')} description={t('admin.people_desc')}>
  {#snippet actions()}
    <button type="button" class="primary" onclick={() => ((inviting = true), (error = ''))}><Icon name="plus" size={16} /> {t('admin.invite')}</button>
  {/snippet}
</PageHead>

{#if error && !inviting}<p class="error" role="alert">{error}</p>{/if}
{#if notice}<p role="status" class="notice">{notice}</p>{/if}
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

<div class="table-toolbar">
  <input type="search" placeholder={t('admin.people_search')} aria-label={t('admin.people_search')} bind:value={query} />
  <div class="chips" role="group" aria-label={t('admin.filter')}>
    {#each filters as f (f)}
      <button type="button" class="chip" aria-pressed={filter === f} onclick={() => (filter = f)}>
        {t(`admin.filter_${f}`)} <span class="chip-count">{users.filter(matches[f]).length}</span>
      </button>
    {/each}
  </div>
</div>

<div class="table-scroll wide-table">
  <table class="admin-table">
    <thead>
      <tr>
        <th scope="col">{t('admin.col_person')}</th>
        <th scope="col">{t('admin.col_source')}</th>
        <th scope="col">{t('admin.col_2fa')}</th>
        <th scope="col">{t('admin.areas')}</th>
        <th scope="col">{t('admin.status')}</th>
        <th scope="col"><span class="sr-only">{t('common.actions')}</span></th>
      </tr>
    </thead>
    <tbody>
      {#each shown as u (u.id)}
        {@const st = status(u)}
        <tr>
          <td>
            <span class="who">
              <span class="who-initials" aria-hidden="true">{initials(u.full_name)}</span>
              <span><strong>{u.full_name}</strong><small>{u.email}</small></span>
            </span>
          </td>
          <td>{u.auth_source === 'ldap' ? t('admin.source_directory') : t('admin.source_local')}</td>
          <td class={secondFactor(u) ? '' : 'muted'}>{secondFactor(u) || t('admin.2fa_none')}</td>
          <td>
            {#if u.is_global_admin}<span class="pill plain">{t('admin.superadmin')}</span>{/if}
            {u.areas.map((a) => `${a.name} · ${t(`role.${a.role}`)}`).join(', ')}
            {#if !u.areas.length && !u.is_global_admin}<span class="muted">—</span>{/if}
          </td>
          <td><span class={`pill ${st.tone}`}>{st.text}</span></td>
          <td class="row-actions">
            <Dropdown label={t('admin.actions_for', { name: u.full_name })} class="icon-btn" align="end" closeOnSelect>
              {#snippet trigger()}<Icon name="more" />{/snippet}
              <ul class="menu" role="menu">
                {#if u.offboarded_at}
                  <li><button type="button" class="menu-item" role="menuitem" disabled={busy} onclick={() => reactivate(u)}>{t('admin.reactivate')}</button></li>
                {:else if u.offboarding_at}
                  <!-- Durante la gracia nada se perdió todavía: su espacio personal sigue ahí. -->
                  <li><a role="menuitem" href={`/w/~${u.id.toLowerCase()}`}>{t('admin.personal_audit')}</a></li>
                  <li><button type="button" class="menu-item" role="menuitem" disabled={busy} onclick={() => reactivate(u)}>{t('admin.reactivate')}</button></li>
                  <li>
                    <button type="button" class="menu-item danger" role="menuitem" disabled={busy} onclick={() => confirm(() => ((offboarding = u), (confirmingOffboard = true)))}
                      >{t('admin.offboard_complete_now')}</button
                    >
                  </li>
                {:else if u.invited}
                  <li><button type="button" class="menu-item" role="menuitem" disabled={busy} onclick={() => resend(u)}>{t('admin.invite_resend')}</button></li>
                {:else}
                  <li class="menu-label">{t('admin.menu_access')}</li>
                  <!-- La contraseña de una cuenta del directorio se cambia en el directorio (§8.6.3). -->
                  {#if u.auth_source !== 'ldap'}
                    <li><button type="button" class="menu-item" role="menuitem" disabled={busy} onclick={() => sendReset(u)}>{t('admin.reset')}</button></li>
                    {#if ldapOn}
                      <li>
                        <button type="button" class="menu-item" role="menuitem" disabled={busy} onclick={() => confirm(() => ((linking = u), (confirmingLink = true)))}
                          >{t('admin.link')}</button
                        >
                      </li>
                    {/if}
                  {/if}
                  {#if u.id !== selfId}
                    <li>
                      <button type="button" class="menu-item" role="menuitem" disabled={busy} onclick={() => confirm(() => ((revoking = u), (confirmingRevoke = true)))}
                        >{t('admin.sessions_revoke')}</button
                      >
                    </li>
                  {/if}
                  {#if u.totp_enabled || u.passkeys}
                    <li>
                      <button type="button" class="menu-item" role="menuitem" disabled={busy} onclick={() => ((resetting = u), (confirmingReset = true))}
                        >{t('admin.totp_reset')}</button
                      >
                    </li>
                  {/if}
                  {#if u.id !== selfId}
                    <li>
                      <button type="button" class="menu-item" role="menuitem" disabled={busy} onclick={() => ((promoting = u), (confirmingSuper = true))}
                        >{t(u.is_global_admin ? 'admin.superadmin_revoke' : 'admin.superadmin_grant')}</button
                      >
                    </li>
                  {/if}
                  <li class="menu-label">{t('admin.menu_view')}</li>
                  <!-- Modo auditoría (§7): solo lectura, y cada pedido queda en la auditoría. -->
                  <li><a role="menuitem" href={`/w/~${u.id.toLowerCase()}`}>{t('admin.personal_audit')}</a></li>
                  {#if u.id !== selfId}
                    <li class="menu-sep" role="separator"></li>
                    <li>
                      <button type="button" class="menu-item danger" role="menuitem" disabled={busy} onclick={() => confirm(() => ((offboarding = u), (confirmingOffboard = true)))}
                        >{t('admin.offboard')}</button
                      >
                    </li>
                  {/if}
                {/if}
              </ul>
            </Dropdown>
          </td>
        </tr>
      {:else}
        {#if users.length}<tr><td colspan="6" class="muted">{t('admin.people_none')}</td></tr>{/if}
      {/each}
    </tbody>
  </table>
</div>

<Dialog title={t('admin.invite')} bind:open={inviting}>
  <form class="stack" onsubmit={sendInvite}>
    <label class="field">{t('auth.email')} <input type="email" required bind:value={invite.email} /></label>
    <label class="field">{t('common.name')} <input required bind:value={invite.full_name} /></label>
    <small class="muted">{mailOn ? t('admin.invite_hint_email') : t('admin.invite_hint_manual')}</small>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <button type="submit" class="primary" disabled={busy}>{t('admin.invite_send')}</button>
  </form>
</Dialog>

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
<LinkDirectoryDialog user={linking} bind:open={confirmingLink} ondone={(m) => run(async () => void (notice = m))} />
