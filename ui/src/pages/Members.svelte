<script lang="ts">
  import { untrack } from 'svelte';
  import { api, codeOf, type Member, type Role } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';
  import Dialog from '../components/Dialog.svelte';
  import Dropdown from '../components/Dropdown.svelte';
  import Icon from '../components/Icon.svelte';
  import PageHead from '../components/PageHead.svelte';

  // La misma pantalla sirve al admin de área y al panel del superadmin; el servidor decide
  // los permisos, aquí solo se ocultan las acciones que igual rechazaría (§3.1). Diseño
  // "Kudawiki — Administración" (§15): tabla con búsqueda y filtro por rol, el rol se cambia en
  // la misma fila y agregar abre un diálogo. embedded: dentro de la ficha de un área del
  // superadmin, que ya tiene su cabecera.
  let {
    endpoint,
    context = '',
    canGrantAdmin,
    selfId,
    embedded = false,
  }: { endpoint: string; context?: string; canGrantAdmin: boolean; selfId: string; embedded?: boolean } = $props();

  type Filter = 'all' | Role;

  let members = $state<Member[]>([]);
  let email = $state('');
  let role = $state<Role>('viewer');
  let adding = $state(false);
  let busy = $state(false);
  let error = $state('');
  let query = $state('');
  let filter = $state<Filter>('all');

  const roles = $derived<Role[]>(canGrantAdmin ? ['viewer', 'editor', 'admin'] : ['viewer', 'editor']);
  const filters: Filter[] = ['all', 'admin', 'editor', 'viewer'];
  // Lo que da un grupo del directorio no se quita desde acá (§8.3.2): se cambia en el directorio.
  // Quitar a quien tiene además un rol manual le quita solo lo manual.
  const editable = (m: Member) => (m.ldap_role ? !!m.manual_role : true) && (canGrantAdmin || (m.user_id !== selfId && m.role !== 'admin'));
  const shown = $derived.by(() => {
    const q = query.trim().toLowerCase();
    return members.filter(
      (m) => (filter === 'all' || m.role === filter) && (!q || m.full_name.toLowerCase().includes(q) || m.email.toLowerCase().includes(q)),
    );
  });

  async function run(action?: () => Promise<void>) {
    busy = true;
    error = '';
    try {
      await action?.();
      members = await api<Member[]>('GET', endpoint);
    } catch (e) {
      error = errorText(codeOf(e));
    } finally {
      busy = false;
    }
  }

  function add(e: SubmitEvent) {
    e.preventDefault();
    run(async () => {
      await api('POST', endpoint, { email, role });
      email = '';
      adding = false;
    });
  }

  // Cambiar el rol en la fila es el mismo pedido que agregar: pide la reautenticación (§8.2).
  function changeRole(m: Member, to: Role) {
    if (to === m.role) return;
    run(() => api('POST', endpoint, { email: m.email, role: to }));
  }

  // Recarga al cambiar de área: endpoint se lee aquí porque dentro de run() va después de un await.
  $effect(() => {
    void endpoint;
    untrack(() => run());
  });
</script>

{#snippet addButton()}
  <button type="button" class="primary" onclick={() => ((adding = true), (error = ''))}><Icon name="plus" size={16} /> {t('members.add_person')}</button>
{/snippet}

{#if !embedded}
  <PageHead {context} title={t('admin.members_link')} description={t('members.desc')}>
    {#snippet actions()}{@render addButton()}{/snippet}
  </PageHead>
{/if}

{#if error && !adding}<p class="error" role="alert">{error}</p>{/if}

<div class="table-toolbar">
  <input type="search" placeholder={t('members.search')} aria-label={t('members.search')} bind:value={query} />
  <div class="chips" role="group" aria-label={t('members.filter')}>
    {#each filters as f (f)}
      <button type="button" class="chip" aria-pressed={filter === f} onclick={() => (filter = f)}>
        {f === 'all' ? t('admin.filter_all') : t(`members.filter_${f}`)}
        <span class="chip-count">{f === 'all' ? members.length : members.filter((m) => m.role === f).length}</span>
      </button>
    {/each}
  </div>
  {#if embedded}<span class="toolbar-end">{@render addButton()}</span>{/if}
</div>

{#if members.length}
  <div class="table-scroll wide-table">
    <table class="admin-table">
      <thead>
        <tr>
          <th scope="col">{t('admin.col_person')}</th>
          <th scope="col">{t('members.role')}</th>
          <th scope="col">{t('members.why')}</th>
          <th scope="col"><span class="sr-only">{t('common.actions')}</span></th>
        </tr>
      </thead>
      <tbody>
        {#each shown as m (m.user_id)}
          <tr>
            <td>
              <span class="who"><span><strong>{m.full_name}</strong><small>{m.email}</small></span></span>
            </td>
            <td>
              {#if editable(m)}
                <select
                  aria-label={t('members.role_of', { name: m.full_name })}
                  disabled={busy}
                  value={m.role}
                  onchange={(e) => changeRole(m, e.currentTarget.value as Role)}
                >
                  {#each roles as r (r)}<option value={r}>{t(`role.${r}`)}</option>{/each}
                </select>
              {:else}
                {t(`role.${m.role}`)}
              {/if}
            </td>
            <td>
              {#if m.ldap_role}<span class="pill plain managed">{t('members.managed', { group: m.ldap_group || '—' })}</span>{/if}
              {#if m.manual_role || !m.ldap_role}<span class="muted">{t('members.manual')}</span>{/if}
            </td>
            <td class="row-actions">
              {#if (canGrantAdmin && m.ldap_role) || editable(m)}
                <Dropdown label={t('admin.actions_for', { name: m.full_name })} class="icon-btn" align="end" closeOnSelect>
                  {#snippet trigger()}<Icon name="more" />{/snippet}
                  <ul class="menu" role="menu">
                    {#if canGrantAdmin && m.ldap_role}
                      <!-- Desanclar: la excepción del superadmin; el rol queda manual y el grupo deja de mandar en esta fila. -->
                      <li>
                        <button
                          type="button"
                          class="menu-item"
                          role="menuitem"
                          disabled={busy}
                          aria-label={t('members.detach_label', { name: m.full_name })}
                          onclick={() => run(() => api('POST', `${endpoint}/${m.user_id}/detach`))}>{t('members.detach')}</button
                        >
                      </li>
                    {/if}
                    {#if editable(m)}
                      <li>
                        <button
                          type="button"
                          class="menu-item danger"
                          role="menuitem"
                          disabled={busy}
                          onclick={() => run(() => api('DELETE', `${endpoint}/${m.user_id}`))}>{t('members.remove', { name: m.full_name })}</button
                        >
                      </li>
                    {/if}
                  </ul>
                </Dropdown>
              {/if}
            </td>
          </tr>
        {:else}
          <tr><td colspan="4" class="muted">{t('admin.people_none')}</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
{:else if !busy}
  <p class="muted">{t('members.empty')}</p>
{/if}

<Dialog title={t('members.add_person')} bind:open={adding}>
  <form class="stack" onsubmit={add}>
    <label class="field">{t('auth.email')} <input type="email" required bind:value={email} /></label>
    <label class="field">
      {t('members.role')}
      <select bind:value={role}>
        {#each roles as r (r)}<option value={r}>{t(`role.${r}`)}</option>{/each}
      </select>
    </label>
    <small class="muted">{t('members.add_hint')}</small>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <button type="submit" class="primary" disabled={busy}>{t('members.add_person')}</button>
  </form>
</Dialog>
