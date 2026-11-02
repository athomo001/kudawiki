<script lang="ts">
  import { api, codeOf, formatDate, type Area, type Share } from '../lib/api';
  import PageHead from '../components/PageHead.svelte';
  import Dialog from '../components/Dialog.svelte';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';

  // Lo que el área comparte con otras (§4.19), para su admin: los pedidos de los editores, que
  // aprueba o rechaza con un motivo, y lo que ya se comparte, que puede retirar.
  let { area, context }: { area: Area; context: string } = $props();

  let items = $state<Share[]>([]);
  let error = $state('');
  let busy = $state(false);
  let rejecting = $state<Share | null>(null);
  let comment = $state('');
  let showReject = $state(false);
  const base = $derived(`/w/${area.slug}`);
  const pending = $derived(items.filter((s) => s.status === 'pending'));
  const active = $derived(items.filter((s) => s.status === 'active'));

  async function load() {
    try {
      items = await api<Share[]>('GET', `${base}/shares`);
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }

  async function act(method: 'POST' | 'DELETE', path: string, body?: unknown) {
    busy = true;
    error = '';
    try {
      await api(method, `${base}/shares/${path}`, body);
      return true;
    } catch (e) {
      error = errorText(codeOf(e));
      return false;
    } finally {
      busy = false;
      await load();
    }
  }

  function askReject(s: Share) {
    rejecting = s;
    comment = '';
    showReject = true;
  }

  async function reject(e: SubmitEvent) {
    e.preventDefault();
    if (rejecting && (await act('POST', `${rejecting.id}/reject`, { comment }))) showReject = false;
  }

  $effect(() => {
    items = [];
    load();
  });
</script>

<PageHead {context} title={t('shares.title')} description={t('shares.intro')} />
{#if error && !showReject}<p class="error" role="alert">{error}</p>{/if}

<h2>{t('shares.pending')}</h2>
{#if pending.length}
  <table>
    <thead>
      <tr>
        <th scope="col">{t('page.title')}</th>
        <th scope="col">{t('shares.target')}</th>
        <th scope="col">{t('shares.requested')}</th>
        <th scope="col"><span class="sr-only">{t('common.actions')}</span></th>
      </tr>
    </thead>
    <tbody>
      {#each pending as s (s.id)}
        <tr>
          <td><a href={`${base}/p/${s.page.slug}`}>{s.page.title}</a></td>
          <td>{s.target.name}</td>
          <td>{s.requested_by} · {formatDate(s.created_at, locale())}</td>
          <td>
            <button type="button" class="link" disabled={busy} onclick={() => act('POST', `${s.id}/approve`)}>{t('shares.approve')}</button>
            <button type="button" class="link" disabled={busy} onclick={() => askReject(s)}>{t('shares.reject')}</button>
          </td>
        </tr>
      {/each}
    </tbody>
  </table>
{:else if !error}
  <p class="muted">{t('shares.no_pending')}</p>
{/if}

<h2>{t('shares.active')}</h2>
{#if active.length}
  <table>
    <thead>
      <tr>
        <th scope="col">{t('page.title')}</th>
        <th scope="col">{t('shares.target')}</th>
        <th scope="col">{t('shares.approved_by')}</th>
        <th scope="col"><span class="sr-only">{t('common.actions')}</span></th>
      </tr>
    </thead>
    <tbody>
      {#each active as s (s.id)}
        <tr>
          <td><a href={`${base}/p/${s.page.slug}`}>{s.page.title}</a></td>
          <td>{s.target.name}</td>
          <td>{s.reviewer || s.requested_by}{s.reviewed_at ? ` · ${formatDate(s.reviewed_at, locale())}` : ''}</td>
          <td><button type="button" class="link" disabled={busy} onclick={() => act('DELETE', s.id)}>{t('shares.unshare')}</button></td>
        </tr>
      {/each}
    </tbody>
  </table>
{:else if !error}
  <p class="muted">{t('shares.no_active')}</p>
{/if}

<Dialog title={t('shares.reject')} bind:open={showReject}>
  <form class="stack" onsubmit={reject}>
    {#if rejecting}<p class="muted">{t('shares.reject_hint', { title: rejecting.page.title, area: rejecting.target.name })}</p>{/if}
    <label class="field">
      {t('shares.comment')}
      <textarea required rows="3" bind:value={comment}></textarea>
    </label>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <button type="submit" class="primary" disabled={busy || !comment.trim()}>{t('shares.reject')}</button>
  </form>
</Dialog>
