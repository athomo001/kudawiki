<script lang="ts">
  import { api, codeOf, ApiError, type Role } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';
  import Dialog from './Dialog.svelte';

  // Vincular una cuenta local con el directorio (SPEC §8.6.3): primero se busca a la persona con
  // la configuración guardada, para ver qué cuenta es y a qué áreas entraría; recién después se
  // vincula. Vincular le quita la contraseña local y cierra sus sesiones: se dice antes.

  type Found = {
    status: 'found' | 'not_found' | 'ambiguous';
    login: string;
    dn?: string;
    enabled: boolean;
    email: string;
    name: string;
    access: { workspace_id: string; workspace_name: string; role: Role }[];
  };

  let {
    user,
    open = $bindable(false),
    ondone,
  }: { user: { id: string; email: string; full_name: string } | null; open: boolean; ondone: (message: string) => void } = $props();

  let login = $state('');
  let found = $state<Found | null>(null);
  let busy = $state(false);
  let error = $state('');

  // Cada vez que se abre, desde cero; la búsqueda arranca con el correo de la cuenta local.
  $effect(() => {
    if (open && user) {
      login = user.email;
      found = null;
      error = '';
    }
  });

  const linkable = $derived(found?.status === 'found' && found.enabled && found.access.length > 0);

  async function lookup(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = '';
    found = null;
    try {
      found = await api<Found>('POST', '/admin/ldap/test', { kind: 'user', login, config: {} });
    } catch (err) {
      error = errorText(codeOf(err));
      if (err instanceof ApiError && typeof err.detail.detail === 'string') error = `${error} ${err.detail.detail}`;
    } finally {
      busy = false;
    }
  }

  async function link() {
    if (!user) return;
    busy = true;
    error = '';
    try {
      const res = await api<{ email: string; full_name: string }>('POST', `/admin/users/${user.id}/link-ldap`, { login });
      open = false;
      ondone(t('admin.link_done', { name: res.full_name, email: res.email }));
    } catch (err) {
      error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }
</script>

<Dialog title={t('admin.link_title')} bind:open>
  <form class="stack" onsubmit={lookup}>
    <p>{t('admin.link_intro', { name: user?.full_name ?? '' })}</p>
    <label class="field">
      {t('ldap.login')}
      <input required maxlength="256" spellcheck="false" autocomplete="off" bind:value={login} oninput={() => (found = null)} />
    </label>
    <button type="submit" class="secondary" disabled={busy || !login.trim()}>{t('admin.link_lookup')}</button>
  </form>
  {#if found}
    <div class="stack" role="status">
      {#if found.status === 'not_found'}
        <p class="error">{t('ldap.user_not_found', { login: found.login })}</p>
      {:else if found.status === 'ambiguous'}
        <p class="error">{t('ldap.user_ambiguous')}</p>
      {:else if !found.enabled}
        <p class="error">{t('ldap.user_disabled')}</p>
      {:else}
        <dl class="ldap-facts">
          <dt>DN</dt>
          <dd><code>{found.dn}</code></dd>
          <dt>{t('ldap.attrs_email')}</dt>
          <dd>{found.email || '—'}</dd>
          <dt>{t('ldap.attrs_name')}</dt>
          <dd>{found.name || '—'}</dd>
          <dt>{t('ldap.user_access')}</dt>
          <dd>{found.access.map((a) => `${a.workspace_name} (${t(`role.${a.role}`)})`).join(', ') || '—'}</dd>
        </dl>
        {#if found.access.length}
          <p>{t('admin.link_effect')}</p>
        {:else}
          <p class="error">{t('error.ldap_no_mapped_group')}</p>
        {/if}
      {/if}
    </div>
  {/if}
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#if linkable}
    <button type="button" class="primary danger-btn" disabled={busy} onclick={link}>{t('admin.link_confirm')}</button>
  {/if}
</Dialog>
