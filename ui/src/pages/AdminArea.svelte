<script lang="ts">
  import { api, codeOf, formatDate } from '../lib/api';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';
  import { countChanges } from '../lib/changes';
  import PageHead from '../components/PageHead.svelte';
  import Setting from '../components/Setting.svelte';
  import SaveBar from '../components/SaveBar.svelte';
  import Members from './Members.svelte';
  import NotFound from './NotFound.svelte';

  // Ficha de un área en la administración (SPEC §14.2, §15): General (nombre y descripción, que el
  // superadmin cambia sin ser miembro; la dirección y la aprobación son de otro lado) y Miembros,
  // donde además nombra admins y desancla filas del directorio.
  // status y delete_at: el ciclo de vida (§3.5), que va en su propia sección y se aplica al momento.
  type Status = 'active' | 'archived' | 'deactivated';
  type Area = { id: string; slug: string; name: string; description: string; status: Status; delete_at?: string };

  // onchanged recarga la sesión: el nombre nuevo sale también en el selector y el menú.
  let { id, tab, selfId, onchanged }: { id: string; tab: 'general' | 'members'; selfId: string; onchanged: () => void } = $props();

  let area = $state<Area | null>(null);
  let missing = $state(false);
  let form = $state({ name: '', description: '' });
  let saved = $state({ name: '', description: '' });
  let busy = $state(false);
  let error = $state('');
  let notice = $state('');

  let slug = $state('');
  let status = $state<Status>('active');
  let confirmSlug = $state('');
  let lifeError = $state('');
  let lifeNotice = $state('');

  const base = $derived(`/admin/areas/${id}`);
  const changes = $derived(countChanges(saved, form));

  $effect(() => {
    void id;
    api<Area[]>('GET', '/admin/workspaces')
      .then((all) => {
        area = all.find((a) => a.id === id) ?? null;
        missing = !area;
        if (area) {
          form = { name: area.name, description: area.description };
          slug = area.slug;
          status = area.status;
        }
        saved = { ...form };
      })
      .catch((e) => (error = errorText(codeOf(e))));
  });

  // Cada acción del ciclo de vida va sola y recarga la ficha: no se mezcla con la barra de guardar.
  async function life(method: string, path: string, body: unknown, done: string) {
    lifeError = '';
    lifeNotice = '';
    try {
      await api(method, `/admin/workspaces/${id}${path}`, body);
      const all = await api<Area[]>('GET', '/admin/workspaces');
      area = all.find((a) => a.id === id) ?? null;
      if (area) {
        slug = area.slug;
        status = area.status;
      }
      confirmSlug = '';
      lifeNotice = done;
      onchanged();
    } catch (e) {
      lifeError = errorText(codeOf(e));
    }
  }

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
      </Setting>
    </form>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    {#if notice && !changes}<p class="notice" role="status">{notice}</p>{/if}

    <!-- El slug y el ciclo de vida (§3.5, B17) se aplican al momento, cada uno con su botón. -->
    <Setting title={t('area_admin.address')} description={t('lifecycle.slug_desc')}>
      <form
        class="form-actions"
        onsubmit={(e) => {
          e.preventDefault();
          life('PUT', '/slug', { slug }, t('lifecycle.slug_done'));
        }}
      >
        <label class="field">{t('area_admin.address')} <input class="mono" required pattern="[a-z0-9][a-z0-9\-]*[a-z0-9]" maxlength="40" bind:value={slug} /></label>
        <button type="submit" class="secondary" disabled={slug === area.slug || !!area.delete_at}>{t('lifecycle.slug_apply')}</button>
      </form>
    </Setting>
    <Setting title={t('lifecycle.status')} description={t('lifecycle.status_desc')}>
      <fieldset class="choices" disabled={!!area.delete_at}>
        <legend class="sr-only">{t('lifecycle.status')}</legend>
        {#each ['active', 'archived', 'deactivated'] as const as s (s)}
          <label class="check">
            <input type="radio" name="status" value={s} bind:group={status} />
            <span>{t(`lifecycle.${s}`)}<br /><small class="muted">{t(`lifecycle.${s}_desc`)}</small></span>
          </label>
        {/each}
      </fieldset>
      <p class="form-actions">
        <button
          type="button"
          class="secondary"
          disabled={status === area.status || !!area.delete_at}
          onclick={() => life('PUT', '/status', { status }, t('lifecycle.status_done'))}>{t('lifecycle.status_apply')}</button
        >
      </p>
    </Setting>
    <Setting title={t('lifecycle.delete')} description={t('lifecycle.delete_desc')}>
      {#if area.delete_at}
        <p class="callout callout-danger" role="status">{t('lifecycle.delete_pending', { date: formatDate(area.delete_at, locale()) })}</p>
        <p class="form-actions">
          <button type="button" class="secondary" onclick={() => life('DELETE', '/delete', undefined, t('lifecycle.delete_cancelled'))}
            >{t('lifecycle.delete_cancel')}</button
          >
        </p>
      {:else}
        <form
          class="form-actions"
          onsubmit={(e) => {
            e.preventDefault();
            life('POST', '/delete', { confirm: confirmSlug }, t('lifecycle.delete_requested'));
          }}
        >
          <label class="field">{t('lifecycle.delete_confirm', { slug: area.slug })} <input class="mono" autocomplete="off" bind:value={confirmSlug} /></label>
          <button type="submit" class="danger-btn" disabled={confirmSlug !== area.slug}>{t('lifecycle.delete_apply')}</button>
        </form>
      {/if}
    </Setting>
    {#if lifeError}<p class="error" role="alert">{lifeError}</p>{/if}
    {#if lifeNotice}<p class="notice" role="status">{lifeNotice}</p>{/if}
    <SaveBar count={changes} {busy} onsave={save} ondiscard={() => (form = { ...saved })} />
  {:else}
    <Members endpoint={`/admin/workspaces/${id}/members`} canGrantAdmin {selfId} embedded />
  {/if}
{:else if error}
  <p class="error" role="alert">{error}</p>
{/if}
