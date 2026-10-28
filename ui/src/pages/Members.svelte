<script lang="ts">
  import { untrack } from 'svelte';
  import { api, codeOf, type Member, type Role } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';

  // La misma pantalla sirve al admin de área y al panel del superadmin; el servidor decide
  // los permisos, aquí solo se ocultan las acciones que igual rechazaría (§3.1).
  let {
    endpoint,
    areaName,
    canGrantAdmin,
    selfId,
  }: { endpoint: string; areaName?: string; canGrantAdmin: boolean; selfId: string } = $props();

  let members = $state<Member[]>([]);
  let email = $state('');
  let role = $state<Role>('viewer');
  let busy = $state(false);
  let error = $state('');

  const roles = $derived<Role[]>(canGrantAdmin ? ['viewer', 'editor', 'admin'] : ['viewer', 'editor']);
  const editable = (m: Member) => canGrantAdmin || (m.user_id !== selfId && m.role !== 'admin');

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
    });
  }

  // Recarga al cambiar de área: endpoint se lee aquí porque dentro de run() va después de un await.
  $effect(() => {
    void endpoint;
    untrack(() => run());
  });
</script>

<h1>{areaName ? t('members.title', { area: areaName }) : t('admin.members_link')}</h1>

{#if members.length}
  <table>
    <thead>
      <tr>
        <th scope="col">{t('common.name')}</th>
        <th scope="col">{t('auth.email')}</th>
        <th scope="col">{t('members.role')}</th>
        <th scope="col"><span class="sr-only">{t('common.actions')}</span></th>
      </tr>
    </thead>
    <tbody>
      {#each members as m (m.user_id)}
        <tr>
          <td>{m.full_name}</td>
          <td>{m.email}</td>
          <td>{t(`role.${m.role}`)}</td>
          <td>
            {#if editable(m)}
              <button
                type="button"
                class="link"
                disabled={busy}
                aria-label={t('members.remove', { name: m.full_name })}
                onclick={() => run(() => api('DELETE', `${endpoint}/${m.user_id}`))}>×</button
              >
            {/if}
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
{:else if !busy}
  <p class="muted">{t('members.empty')}</p>
{/if}

<h2>{t('members.add')}</h2>
<form class="inline" onsubmit={add}>
  <label>
    {t('auth.email')}
    <input type="email" required bind:value={email} />
  </label>
  <label>
    {t('members.role')}
    <select bind:value={role}>
      {#each roles as r (r)}<option value={r}>{t(`role.${r}`)}</option>{/each}
    </select>
  </label>
  <button type="submit" class="primary" disabled={busy}>{t('common.save')}</button>
</form>
{#if error}<p class="error" role="alert">{error}</p>{/if}
