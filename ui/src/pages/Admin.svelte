<script lang="ts">
  import { api, codeOf } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';

  type User = { id: string; email: string; full_name: string; is_global_admin: boolean; invited: boolean };
  type Area = { id: string; slug: string; name: string };

  let users = $state<User[]>([]);
  let areas = $state<Area[]>([]);
  let busy = $state(false);
  let error = $state('');

  let invite = $state({ email: '', full_name: '' });
  let link = $state<{ email: string; url: string } | null>(null);
  let copied = $state(false);
  let area = $state({ name: '', slug: '' });

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

  function sendInvite(e: SubmitEvent) {
    e.preventDefault();
    run(async () => {
      const res = await api<{ invite_url: string }>('POST', '/admin/users', invite);
      link = { email: invite.email, url: res.invite_url };
      copied = false;
      invite = { email: '', full_name: '' };
    });
  }

  function createArea(e: SubmitEvent) {
    e.preventDefault();
    run(async () => {
      await api('POST', '/admin/workspaces', area);
      area = { name: '', slug: '' };
    });
  }

  // Una invitación pendiente (vencida o no) se vuelve a generar: el enlace anterior deja de servir (SPEC §8.5.3).
  function resend(u: User) {
    run(async () => {
      const res = await api<{ invite_url: string }>('POST', `/admin/users/${u.id}/invite`);
      link = { email: u.email, url: res.invite_url };
      copied = false;
    });
  }

  async function copy() {
    if (!link) return;
    await navigator.clipboard.writeText(link.url);
    copied = true;
  }

  run();
</script>

<h1>{t('nav.admin')}</h1>
{#if error}<p class="error" role="alert">{error}</p>{/if}

<section aria-labelledby="people">
  <h2 id="people">{t('admin.people')}</h2>
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
            {u.invited ? t('admin.status_invited') : t('admin.status_active')}
            {#if u.is_global_admin}· {t('admin.superadmin')}{/if}
          </td>
          <td>
            {#if u.invited}
              <button type="button" class="link" disabled={busy} onclick={() => resend(u)}>{t('admin.invite_resend')}</button>
            {/if}
          </td>
        </tr>
      {/each}
    </tbody>
  </table>

  <h3>{t('admin.invite')}</h3>
  <form class="inline" onsubmit={sendInvite}>
    <label>{t('auth.email')} <input type="email" required bind:value={invite.email} /></label>
    <label>{t('common.name')} <input required bind:value={invite.full_name} /></label>
    <button type="submit" class="primary" disabled={busy}>{t('admin.invite_send')}</button>
  </form>
  {#if link}
    <div class="notice" role="status">
      <p>{t('admin.invite_link', { email: link.email })}</p>
      <code>{link.url}</code>
      <button type="button" class="link" onclick={copy}>{copied ? t('common.copied') : t('common.copy')}</button>
    </div>
  {/if}
</section>

<section aria-labelledby="areas">
  <h2 id="areas">{t('admin.areas')}</h2>
  <ul class="plain">
    {#each areas as a (a.id)}
      <li><strong>{a.name}</strong> <span class="muted">/{a.slug}</span> · <a href={`/admin/areas/${a.id}`}>{t('admin.members_link')}</a></li>
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
