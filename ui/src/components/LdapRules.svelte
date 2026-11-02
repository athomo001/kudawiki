<script lang="ts">
  import { api, codeOf, ApiError } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';
  import Dialog from './Dialog.svelte';

  // Reglas grupo del directorio → (área, rol) (SPEC §8.3.1). Usan la conexión guardada, no el
  // formulario de arriba. Toda regla se previsualiza antes de guardarse (§8.3.4); crearla,
  // cambiarla o quitarla pide reautenticarse, y eso lo resuelve api().

  type Role = 'viewer' | 'editor' | 'admin';
  type Group = { guid: string; sid?: string; dn: string; name: string };
  type Rule = {
    id: string;
    group_guid: string;
    group_dn: string;
    group_name: string;
    workspace_id: string;
    workspace_name: string;
    role: Role;
  };
  type Area = { id: string; name: string };
  type Preview = { group: Group; role: Role; members: number; truncated: boolean; accounts: number; added: number; raised: number };

  const roles: Role[] = ['viewer', 'editor', 'admin'];

  let rules = $state<Rule[] | null>(null);
  let areas = $state<Area[]>([]);

  let q = $state('');
  let groups = $state<Group[] | null>(null);
  let more = $state(false);
  let searching = $state(false);
  let group = $state<Group | null>(null);
  let areaId = $state('');
  let role = $state<Role>('viewer');
  let preview = $state<Preview | null>(null);
  let busy = $state(false);
  let error = $state('');
  let detail = $state('');
  let notice = $state('');

  let changing = $state<{ rule: Rule; role: Role; preview: Preview | null } | null>(null);
  let changeOpen = $state(false);
  let removing = $state<Rule | null>(null);
  let removeOpen = $state(false);

  const areaName = (id: string) => areas.find((a) => a.id === id)?.name ?? '';

  Promise.all([api<Rule[]>('GET', '/admin/ldap/mappings'), api<Area[]>('GET', '/admin/workspaces')])
    .then(([r, a]) => {
      rules = r;
      areas = a;
    })
    .catch(fail);

  function fail(err: unknown) {
    const code = codeOf(err);
    error = code === 'ldap_incomplete' ? t('ldap.rules_need_connection') : errorText(code);
    detail = err instanceof ApiError && typeof err.detail.detail === 'string' ? err.detail.detail : '';
  }

  function clear() {
    error = '';
    detail = '';
    notice = '';
  }

  async function search(e: SubmitEvent) {
    e.preventDefault();
    searching = true;
    clear();
    try {
      const res = await api<{ groups: Group[]; truncated: boolean }>('GET', `/admin/ldap/groups?q=${encodeURIComponent(q.trim())}`);
      groups = res.groups;
      more = res.truncated;
      group = null;
      preview = null;
    } catch (err) {
      fail(err);
    } finally {
      searching = false;
    }
  }

  const input = () => ({ group_guid: group?.guid ?? '', workspace_id: areaId, role });

  async function runPreview() {
    busy = true;
    clear();
    try {
      preview = await api<Preview>('POST', '/admin/ldap/mappings/preview', input());
    } catch (err) {
      fail(err);
    } finally {
      busy = false;
    }
  }

  async function create(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    clear();
    try {
      const r = await api<Rule>('POST', '/admin/ldap/mappings', input());
      rules = [...(rules ?? []), r].sort((a, b) => a.group_name.localeCompare(b.group_name) || a.workspace_name.localeCompare(b.workspace_name));
      preview = null;
      group = null;
      groups = null;
      q = '';
      notice = t('ldap.rule_created');
    } catch (err) {
      fail(err);
    } finally {
      busy = false;
    }
  }

  // El select vuelve a mostrar el rol guardado hasta que se confirma el cambio en el diálogo.
  async function startChange(rule: Rule, el: HTMLSelectElement) {
    const next = el.value as Role;
    el.value = rule.role;
    changing = { rule, role: next, preview: null };
    changeOpen = true;
    clear();
    try {
      const p = await api<Preview>('POST', '/admin/ldap/mappings/preview', { group_guid: rule.group_guid, workspace_id: rule.workspace_id, role: next });
      if (changing?.rule.id === rule.id) changing.preview = p;
    } catch (err) {
      changeOpen = false;
      fail(err);
    }
  }

  async function saveChange(e: SubmitEvent) {
    e.preventDefault();
    if (!changing) return;
    busy = true;
    try {
      const r = await api<Rule>('PATCH', `/admin/ldap/mappings/${changing.rule.id}`, { role: changing.role });
      rules = (rules ?? []).map((x) => (x.id === r.id ? r : x));
      changeOpen = false;
    } catch (err) {
      changeOpen = false;
      fail(err);
    } finally {
      busy = false;
    }
  }

  async function remove(e: SubmitEvent) {
    e.preventDefault();
    if (!removing) return;
    const id = removing.id;
    busy = true;
    try {
      await api('DELETE', `/admin/ldap/mappings/${id}`);
      rules = (rules ?? []).filter((x) => x.id !== id);
      removeOpen = false;
    } catch (err) {
      removeOpen = false;
      fail(err);
    } finally {
      busy = false;
    }
  }
</script>

{#snippet previewText(p: Preview, area: string)}
  <p>{t('ldap.rule_preview', { group: p.group.name, role: t(`role.${p.role}`), area, n: String(p.members) })}</p>
  <p>{t('ldap.rule_preview_accounts', { accounts: String(p.accounts), added: String(p.added), raised: String(p.raised) })}</p>
  {#if p.truncated}<p class="muted">{t('ldap.rule_preview_truncated')}</p>{/if}
{/snippet}

<section aria-labelledby="ldap-rules-title" class="ldap-rules">
  <h2 id="ldap-rules-title">{t('ldap.rules')}</h2>
  <p>{t('ldap.rules_intro')}</p>

  {#if rules}
    {#if rules.length === 0}
      <p class="muted">{t('ldap.rules_empty')}</p>
    {:else}
      <table>
        <thead>
          <tr><th>{t('ldap.rule_group')}</th><th>{t('ldap.rule_area')}</th><th>{t('ldap.rule_role')}</th><th><span class="sr-only">{t('ldap.rule_remove')}</span></th></tr>
        </thead>
        <tbody>
          {#each rules as r (r.id)}
            <tr>
              <td>{r.group_name}<small class="muted ldap-dn">{r.group_dn}</small></td>
              <td>{r.workspace_name}</td>
              <td>
                <select aria-label={t('ldap.rule_role')} value={r.role} disabled={busy} onchange={(e) => startChange(r, e.currentTarget)}>
                  {#each roles as x (x)}<option value={x}>{t(`role.${x}`)}</option>{/each}
                </select>
              </td>
              <td>
                <button
                  type="button"
                  class="link danger"
                  disabled={busy}
                  onclick={() => {
                    removing = r;
                    removeOpen = true;
                  }}>{t('ldap.rule_remove')}</button
                >
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  {/if}

  <h3>{t('ldap.rule_new')}</h3>
  <form class="ldap-user-form" onsubmit={search}>
    <label class="field">
      {t('ldap.rule_search')}
      <input maxlength="256" spellcheck="false" autocomplete="off" placeholder="GG-Wiki" bind:value={q} />
    </label>
    <button type="submit" class="secondary" disabled={searching}>{searching ? t('ldap.testing') : t('ldap.rule_search_run')}</button>
  </form>

  {#if groups}
    {#if groups.length === 0}
      <p class="muted">{t('ldap.rule_search_none')}</p>
    {:else}
      <form class="stack" onsubmit={create}>
        <fieldset class="field ldap-groups">
          <legend>{t('ldap.rule_group')}</legend>
          {#each groups as g (g.guid)}
            <label class="check">
              <input type="radio" name="ldap-group" value={g.guid} checked={group?.guid === g.guid} onchange={() => ((group = g), (preview = null))} />
              <span>{g.name}<small class="muted ldap-dn">{g.dn}</small></span>
            </label>
          {/each}
          {#if more}<small class="muted">{t('ldap.rule_search_more')}</small>{/if}
        </fieldset>
        <label class="field">
          {t('ldap.rule_area')}
          <select required bind:value={areaId} onchange={() => (preview = null)}>
            <option value="" disabled>{t('ldap.rule_area_pick')}</option>
            {#each areas as a (a.id)}<option value={a.id}>{a.name}</option>{/each}
          </select>
        </label>
        <label class="field">
          {t('ldap.rule_role')}
          <select bind:value={role} onchange={() => (preview = null)}>
            {#each roles as x (x)}<option value={x}>{t(`role.${x}`)}</option>{/each}
          </select>
        </label>
        {#if preview}
          <div class="ldap-preview" role="status">{@render previewText(preview, areaName(areaId))}</div>
        {/if}
        <div class="conflict-actions">
          {#if preview}
            <button type="submit" class="primary" disabled={busy}>{t('ldap.rule_create')}</button>
          {:else}
            <button type="button" class="secondary" disabled={busy || !group || !areaId} onclick={runPreview}>{busy ? t('ldap.testing') : t('ldap.rule_preview_run')}</button>
          {/if}
        </div>
      </form>
    {/if}
  {/if}

  {#if error}
    <div role="alert">
      <p class="error">{error}</p>
      {#if detail}<pre class="smtp-detail"><code>{detail}</code></pre>{/if}
    </div>
  {/if}
  {#if notice}<p class="notice" role="status">{notice}</p>{/if}
</section>

<Dialog title={t('ldap.rule_change_title')} bind:open={changeOpen}>
  <form class="stack" onsubmit={saveChange}>
    {#if changing?.preview}
      {@render previewText(changing.preview, changing.rule.workspace_name)}
    {:else}
      <p class="muted">{t('ldap.testing')}</p>
    {/if}
    <button type="submit" class="primary" disabled={busy || !changing?.preview}>{t('ldap.rule_change_save')}</button>
  </form>
</Dialog>

<Dialog title={t('ldap.rule_remove_title')} bind:open={removeOpen}>
  <form class="stack" onsubmit={remove}>
    {#if removing}
      <p>{t('ldap.rule_remove_body', { group: removing.group_name, role: t(`role.${removing.role}`), area: removing.workspace_name })}</p>
    {/if}
    <button type="submit" class="primary danger-btn" disabled={busy}>{t('ldap.rule_remove')}</button>
  </form>
</Dialog>
