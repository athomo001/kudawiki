<script lang="ts">
  import { api, codeOf, formatDate } from '../lib/api';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';
  import Dialog from '../components/Dialog.svelte';
  import Dropdown from '../components/Dropdown.svelte';
  import Icon from '../components/Icon.svelte';
  import PageHead from '../components/PageHead.svelte';

  // Administración → Áreas (SPEC §14.2, §15, diseño "Kudawiki — Administración"): todas las áreas
  // con sus miembros, admins, páginas y última edición. Crear un área abre un diálogo; el nombre
  // de cada una lleva a su ficha (nombre, descripción y miembros), sin ser miembro.
  type Area = {
    id: string;
    slug: string;
    name: string;
    description: string;
    members: number;
    admins: string[];
    pages: number;
    last_edit?: string;
    // Ciclo de vida (§3.5): lo que no está activa se marca junto al nombre.
    status: 'active' | 'archived' | 'deactivated';
    delete_at?: string;
  };

  // onchanged recarga la sesión: un área nueva puede aparecer en el selector.
  let { onchanged }: { onchanged: () => void } = $props();

  let areas = $state<Area[]>([]);
  let busy = $state(false);
  let error = $state('');
  let creating = $state(false);
  let draft = $state({ name: '', slug: '' });

  async function run(action?: () => Promise<void>) {
    busy = true;
    error = '';
    try {
      await action?.();
      areas = await api<Area[]>('GET', '/admin/workspaces');
    } catch (e) {
      error = errorText(codeOf(e));
    } finally {
      busy = false;
    }
  }

  function create(e: SubmitEvent) {
    e.preventDefault();
    run(async () => {
      await api('POST', '/admin/workspaces', draft);
      draft = { name: '', slug: '' };
      creating = false;
      onchanged();
    });
  }

  run();
</script>

<PageHead context={`${t('nav.admin')} · ${t('admin.group_access')}`} title={t('admin.areas')} description={t('admin.areas_desc')}>
  {#snippet actions()}
    <button type="button" class="primary" onclick={() => ((creating = true), (error = ''))}><Icon name="plus" size={16} /> {t('admin.create_area')}</button>
  {/snippet}
</PageHead>

{#if error && !creating}<p class="error" role="alert">{error}</p>{/if}

<div class="table-scroll wide-table">
  <table class="admin-table">
    <thead>
      <tr>
        <th scope="col">{t('admin.col_area')}</th>
        <th scope="col">{t('settings.description')}</th>
        <th scope="col" class="num">{t('admin.members_link')}</th>
        <th scope="col">{t('admin.col_admins')}</th>
        <th scope="col" class="num">{t('admin.col_pages')}</th>
        <th scope="col">{t('admin.col_last_edit')}</th>
        <th scope="col"><span class="sr-only">{t('common.actions')}</span></th>
      </tr>
    </thead>
    <tbody>
      {#each areas as a (a.id)}
        <tr>
          <td><a href={`/admin/areas/${a.id}`}><strong>{a.name}</strong></a> <span class="muted mono">/{a.slug}</span>
            {#if a.delete_at}<br /><small class="sev-error">{t('lifecycle.delete_pending_short', { date: formatDate(a.delete_at, locale()) })}</small>
            {:else if a.status !== 'active'}<br /><small class="muted">{t(`lifecycle.${a.status}`)}</small>{/if}</td>
          <td class="muted">{a.description || '—'}</td>
          <td class="num">{a.members}</td>
          <td>{a.admins.join(', ') || '—'}</td>
          <td class="num">{a.pages}</td>
          <td>{a.last_edit ? formatDate(a.last_edit, locale()) : '—'}</td>
          <td class="row-actions">
            <Dropdown label={t('admin.actions_for', { name: a.name })} class="icon-btn" align="end" closeOnSelect>
              {#snippet trigger()}<Icon name="more" />{/snippet}
              <ul class="menu" role="menu">
                <li><a role="menuitem" href={`/admin/areas/${a.id}`}>{t('admin.area_edit_full')}</a></li>
                <li><a role="menuitem" href={`/admin/areas/${a.id}/members`}>{t('admin.members_link')}</a></li>
              </ul>
            </Dropdown>
          </td>
        </tr>
      {:else}
        {#if !busy}<tr><td colspan="7" class="muted">{t('admin.areas_none')}</td></tr>{/if}
      {/each}
    </tbody>
  </table>
</div>

<Dialog title={t('admin.create_area')} bind:open={creating}>
  <form class="stack" onsubmit={create}>
    <label class="field">{t('common.name')} <input required maxlength="80" bind:value={draft.name} /></label>
    <label class="field">
      {t('admin.slug')}
      <input required class="mono" pattern="[a-z0-9][a-z0-9\-]*[a-z0-9]" maxlength="40" aria-describedby="slug-hint" bind:value={draft.slug} />
      <small id="slug-hint" class="muted">{t('admin.slug_hint')}</small>
    </label>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <button type="submit" class="primary" disabled={busy}>{t('admin.create_area')}</button>
  </form>
</Dialog>
