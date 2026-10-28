<script lang="ts">
  import { untrack } from 'svelte';
  import { api, codeOf, formatDate, type AuditEvent } from '../lib/api';
  import { t, has, errorText, locale } from '../lib/i18n/index.svelte';

  // Auditoría (SPEC §11). La del superadmin abarca todas las áreas y trae la IP; la del admin
  // de área, solo su área y la ruta de las páginas. Qué entra en cada una lo decide el servidor.
  let { endpoint, base = '', global = false }: { endpoint: string; base?: string; global?: boolean } = $props();

  let events = $state<AuditEvent[]>([]);
  let more = $state(false);
  let filter = $state({ actor: '', action: '', from: '', to: '', ws: '' });
  let error = $state('');

  const query = (before?: number) => {
    const q = new URLSearchParams();
    for (const [k, v] of Object.entries(filter)) if (v.trim()) q.set(k, v.trim());
    if (before) q.set('before', String(before));
    return q.toString();
  };

  async function load(append = false) {
    error = '';
    try {
      const res = await api<{ events: AuditEvent[]; more: boolean }>('GET', `${endpoint}?${query(append ? events.at(-1)?.id : undefined)}`);
      events = append ? [...events, ...res.events] : res.events;
      more = res.more;
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }

  // Una acción que la interfaz no conoce se muestra con su código: nunca se esconde un evento.
  const label = (action: string) => {
    const key = `audit.${action}`;
    return has(key) ? t(key) : action;
  };

  // Lo que el evento lleva en meta: números de revisión, roles, conteos (§11.2: nunca contenido).
  const detail = (e: AuditEvent) =>
    Object.entries(e.meta ?? {})
      .map(([k, v]) => `${k}: ${Array.isArray(v) ? v.join(', ') : v}`)
      .join(' · ');

  // Se recarga al cambiar de vista; los filtros se aplican con el botón, no en cada tecla.
  $effect(() => {
    void endpoint;
    untrack(() => {
      events = [];
      load();
    });
  });
</script>

<h1>{t('audit.title')}</h1>
<form class="inline" onsubmit={(e) => (e.preventDefault(), load())}>
  {#if global}<label>{t('audit.area')} <input bind:value={filter.ws} placeholder="ti" /></label>{/if}
  <label>{t('audit.actor')} <input type="email" bind:value={filter.actor} /></label>
  <label>{t('audit.action')} <input bind:value={filter.action} placeholder="page.delete" /></label>
  <label>{t('history.from')} <input type="date" bind:value={filter.from} /></label>
  <label>{t('history.to')} <input type="date" bind:value={filter.to} /></label>
  <button type="submit" class="primary">{t('audit.filter')}</button>
</form>
{#if error}<p class="error" role="alert">{error}</p>{/if}

{#if events.length}
  <table>
    <thead>
      <tr>
        <th scope="col">{t('history.date')}</th>
        <th scope="col">{t('audit.actor')}</th>
        <th scope="col">{t('audit.action')}</th>
        <th scope="col">{t('audit.target')}</th>
      </tr>
    </thead>
    <tbody>
      {#each events as e (e.id)}
        <tr>
          <td>{formatDate(e.at, locale())}</td>
          <td>
            {#if e.actor}{e.actor.name} <small class="muted">{e.actor.email}{#if e.ip} · {e.ip}{/if}</small>
            {:else}<span class="muted">{t('audit.system')}</span>{/if}
          </td>
          <td>{label(e.action)}{#if detail(e)}<br /><small class="muted">{detail(e)}</small>{/if}</td>
          <td>
            {#if e.page}<a href={`${base}/p/${e.page}`}>{e.page}</a>
            {:else if e.target_id}<small class="muted">{e.target_type} {e.target_id}</small>{/if}
            {#if e.workspace}<small class="muted"> · {e.workspace}</small>{/if}
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
  {#if more}<button type="button" onclick={() => load(true)}>{t('search.more')}</button>{/if}
{:else if !error}
  <p class="muted">{t('audit.empty')}</p>
{/if}
