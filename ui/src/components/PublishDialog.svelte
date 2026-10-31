<script lang="ts">
  import { api, canEdit, codeOf, type Area, type Me, type Page, type PageRef } from '../lib/api';
  import Dialog from './Dialog.svelte';
  import FolderPicker from './FolderPicker.svelte';
  import { t, errorText } from '../lib/i18n/index.svelte';
  import { navigate } from '../lib/router.svelte';

  // Publicar desde el espacio personal en un área donde se es editor o admin (§7). Según el área,
  // la página se muda con su historial o queda una propuesta y el borrador sigue aquí.
  // onsent recarga la página para mostrar en qué quedó lo enviado; onmoved, el árbol.
  let {
    area,
    page,
    open = $bindable(false),
    onsent,
    onmoved,
  }: { area: Area; page: Page; open: boolean; onsent: () => void; onmoved: () => void } = $props();

  let targets = $state<Area[]>([]);
  let target = $state('');
  let to = $state('');
  let refs = $state<PageRef[]>([]);
  // null: todavía no se sabe (o la ruta no es válida) si el área pide aprobación para esa ruta.
  let mustPropose = $state<boolean | null>(null);
  let error = $state('');
  let busy = $state(false);
  const valid = $derived(/^[a-z0-9]+(-[a-z0-9]+)*(\/[a-z0-9]+(-[a-z0-9]+)*)*$/.test(to.trim()));
  const targetName = $derived(targets.find((a) => a.slug === target)?.name ?? '');

  $effect(() => {
    if (!open) return;
    to = page.slug;
    error = '';
    api<Me>('GET', '/auth/me')
      .then((me) => {
        targets = me.workspaces.filter(canEdit);
        if (!targets.some((a) => a.slug === target)) target = targets[0]?.slug ?? '';
      })
      .catch((e) => (error = errorText(codeOf(e))));
  });

  // El árbol del área elegida, para elegir la carpeta (§15).
  $effect(() => {
    refs = [];
    if (!open || !target) return;
    api<PageRef[]>('GET', `/w/${target}/tree`)
      .then((r) => (refs = r))
      .catch(() => {});
  });

  // Qué va a pasar con esa ruta en esa área: las excepciones por subárbol cuentan (§5.2.1).
  $effect(() => {
    mustPropose = null;
    const path = to.trim();
    if (!open || !target || !valid) return;
    api<{ must_propose: boolean }>('GET', `/w/${target}/approval/${path}`)
      .then((r) => {
        if (path === to.trim()) mustPropose = r.must_propose;
      })
      .catch(() => {});
  });

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = '';
    const path = to.trim();
    try {
      const res = await api<{ slug?: string; proposal?: string }>('POST', `/w/${area.slug}/publish/${page.slug}`, { area: target, to: path });
      open = false;
      if (res.proposal) {
        onsent();
      } else {
        onmoved();
        navigate(`/w/${target}/p/${path}`);
      }
    } catch (err) {
      error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }
</script>

<Dialog title={t('publish.title')} bind:open>
  {#if targets.length}
    <form class="stack" onsubmit={submit}>
      <label class="field">
        {t('publish.area')}
        <select bind:value={target}>
          {#each targets as a (a.slug)}<option value={a.slug}>{a.name}</option>{/each}
        </select>
      </label>
      <label class="field">
        {t('page.move_to')}
        <input required pattern="[a-z0-9]+(-[a-z0-9]+)*(/[a-z0-9]+(-[a-z0-9]+)*)*" bind:value={to} />
      </label>
      <FolderPicker {refs} rootLabel={targetName} bind:path={to} title={page.title} />
      {#if mustPropose !== null}
        <p class="muted" role="status">{mustPropose ? t('publish.will_propose', { area: targetName }) : t('publish.will_move', { area: targetName })}</p>
      {/if}
      {#if error}<p class="error" role="alert">{error}</p>{/if}
      <button type="submit" class="primary" disabled={busy || !valid}>{mustPropose ? t('publish.submit_propose') : t('publish.submit')}</button>
    </form>
  {:else}
    <p class="muted">{error || t('publish.no_areas')}</p>
  {/if}
</Dialog>
