<script lang="ts">
  import { api, codeOf } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';
  import { countChanges } from '../lib/changes';
  import PageHead from '../components/PageHead.svelte';
  import Setting from '../components/Setting.svelte';
  import SaveBar from '../components/SaveBar.svelte';
  import Members from './Members.svelte';
  import NotFound from './NotFound.svelte';

  // Ficha de un área en la administración (SPEC §14.2, §15): General (nombre y descripción, que el
  // superadmin cambia sin ser miembro; la dirección y la aprobación son de otro lado) y Miembros,
  // donde además nombra admins y desancla filas del directorio.
  type Area = { id: string; slug: string; name: string; description: string };

  // onchanged recarga la sesión: el nombre nuevo sale también en el selector y el menú.
  let { id, tab, selfId, onchanged }: { id: string; tab: 'general' | 'members'; selfId: string; onchanged: () => void } = $props();

  let area = $state<Area | null>(null);
  let missing = $state(false);
  let form = $state({ name: '', description: '' });
  let saved = $state({ name: '', description: '' });
  let busy = $state(false);
  let error = $state('');
  let notice = $state('');

  const base = $derived(`/admin/areas/${id}`);
  const changes = $derived(countChanges(saved, form));

  $effect(() => {
    void id;
    api<Area[]>('GET', '/admin/workspaces')
      .then((all) => {
        area = all.find((a) => a.id === id) ?? null;
        missing = !area;
        if (area) form = { name: area.name, description: area.description };
        saved = { ...form };
      })
      .catch((e) => (error = errorText(codeOf(e))));
  });

  async function save() {
    busy = true;
    error = '';
    notice = '';
    try {
      await api('PATCH', `/admin/workspaces/${id}`, form);
      saved = { ...form };
      if (area) area.name = form.name;
      notice = t('settings.saved');
      onchanged();
    } catch (e) {
      error = errorText(codeOf(e));
    } finally {
      busy = false;
    }
  }
</script>

{#if missing}
  <NotFound />
{:else if area}
  <PageHead context={`${t('nav.admin')} · ${t('admin.areas')}`} title={area.name} description={t('admin.area_desc', { slug: area.slug })}>
    {#snippet tabs()}
      <nav class="page-tabs" aria-label={t('admin.area_tabs')}>
        <a href={base} aria-current={tab === 'general' ? 'page' : undefined}>{t('area_admin.general')}</a>
        <a href={`${base}/members`} aria-current={tab === 'members' ? 'page' : undefined}>{t('admin.members_link')}</a>
      </nav>
    {/snippet}
  </PageHead>

  {#if tab === 'general'}
    <form
      onsubmit={(e) => {
        e.preventDefault();
        save();
      }}
    >
      <Setting title={t('area_admin.name_section')} description={t('area_admin.name_section_desc')}>
        <label class="field">{t('common.name')} <input required maxlength="80" bind:value={form.name} /></label>
        <label class="field">
          {t('settings.description')}
          <textarea rows="3" maxlength="500" bind:value={form.description}></textarea>
          <small class="muted">{t('settings.description_hint')}</small>
        </label>
        <label class="field">
          {t('area_admin.address')}
          <input class="mono" readonly value={`/w/${area.slug}`} />
          <small class="muted">{t('admin.area_slug_fixed')}</small>
        </label>
      </Setting>
    </form>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    {#if notice && !changes}<p class="notice" role="status">{notice}</p>{/if}
    <SaveBar count={changes} {busy} onsave={save} ondiscard={() => (form = { ...saved })} />
  {:else}
    <Members endpoint={`/admin/workspaces/${id}/members`} canGrantAdmin {selfId} embedded />
  {/if}
{:else if error}
  <p class="error" role="alert">{error}</p>
{/if}
