<script lang="ts">
  import { api, codeOf, type Area } from '../lib/api';
  import Dialog from './Dialog.svelte';
  import { t, errorText } from '../lib/i18n/index.svelte';

  // Compartir un artículo N1 con otras áreas (§4.19). Se ven los nombres de todas las áreas, y
  // solo eso (§19.40 a). Lo que pide un editor espera al admin del área; lo que pide un admin
  // queda compartido de una. El servidor vuelve a revisar el nivel y las áreas.
  let { area, slug, open = $bindable(false) }: { area: Area; slug: string; open: boolean } = $props();

  let targets = $state<{ id: string; name: string }[]>([]);
  let chosen = $state<string[]>([]);
  let error = $state('');
  let busy = $state(false);
  let done = $state<'' | 'approved' | 'pending'>('');

  $effect(() => {
    if (!open) return;
    error = '';
    done = '';
    chosen = [];
    api<{ id: string; name: string }[]>('GET', `/w/${area.slug}/share-targets`)
      .then((l) => (targets = l))
      .catch((e) => (error = errorText(codeOf(e))));
  });

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      const { approved } = await api<{ approved: boolean }>('POST', `/w/${area.slug}/shares`, { page: slug, targets: chosen });
      done = approved ? 'approved' : 'pending';
    } catch (err) {
      error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }
</script>

<Dialog title={t('share.title')} bind:open>
  {#if done}
    <div class="stack">
      <p role="status">{t(done === 'approved' ? 'share.done_approved' : 'share.done_pending')}</p>
      <button type="button" class="primary" onclick={() => (open = false)}>{t('share.close')}</button>
    </div>
  {:else}
    <form class="stack" onsubmit={submit}>
      <p class="muted">{t('share.hint')}</p>
      {#if targets.length}
        <fieldset class="audience">
          <legend>{t('share.targets')}</legend>
          {#each targets as a (a.id)}
            <label><input type="checkbox" bind:group={chosen} value={a.id} /> {a.name}</label>
          {/each}
        </fieldset>
      {:else if !error}
        <p class="muted">{t('share.no_targets')}</p>
      {/if}
      {#if area.role !== 'admin'}<small class="muted">{t('share.needs_review')}</small>{/if}
      {#if error}<p class="error" role="alert">{error}</p>{/if}
      <button type="submit" class="primary" disabled={busy || !chosen.length}>{t(area.role === 'admin' ? 'share.submit' : 'share.request')}</button>
    </form>
  {/if}
</Dialog>
