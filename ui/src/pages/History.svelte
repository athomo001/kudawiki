<script lang="ts">
  import { api, canEdit, codeOf, formatDate, type Area, type DiffLine, type Level, type RevisionMeta } from '../lib/api';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';
  import DiffView from '../components/DiffView.svelte';
  import Watermark from '../components/Watermark.svelte';
  import { watermarked } from '../lib/watermark.svelte';

  // level: el de la página, del árbol del área; el historial muestra su contenido y lleva la
  // misma marca de agua que el lector (§4.15).
  let { area, slug, level, onreverted }: { area: Area; slug: string; level?: Level; onreverted: () => void } = $props();

  let revisions = $state<RevisionMeta[]>([]);
  let from = $state(0);
  let to = $state(0);
  let diff = $state<DiffLine[]>([]);
  let busy = $state(false);
  let error = $state('');

  const path = $derived(`/w/${area.slug}`);
  const current = $derived(revisions[0]?.revision ?? 0);

  async function run(action: () => Promise<void>) {
    busy = true;
    error = '';
    try {
      await action();
    } catch (e) {
      error = errorText(codeOf(e));
    } finally {
      busy = false;
    }
  }

  const load = () =>
    run(async () => {
      revisions = await api<RevisionMeta[]>('GET', `${path}/history/${slug}`);
      to = current;
      from = revisions[1]?.revision ?? current;
    });

  const compare = () => run(async () => void (diff = await api<DiffLine[]>('GET', `${path}/diff/${slug}?from=${from}&to=${to}`)));

  // Revertir publica una revisión nueva con el contenido de la elegida (SPEC §4.3).
  const revert = (n: number) =>
    run(async () => {
      await api('POST', `${path}/revert/${slug}`, { revision: n, base_revision: current });
      onreverted();
      await load();
    });

  $effect(() => {
    load();
  });
</script>

{#if watermarked(level, area)}<Watermark />{/if}
<h1>{t('page.history')}{#if revisions[0]}<span class="h1-sub">{revisions[0].title}</span>{/if}</h1>
{#if error}<p class="error" role="alert">{error}</p>{/if}

<table>
  <thead>
    <tr>
      <th scope="col">#</th>
      <th scope="col">{t('history.change')}</th>
      <th scope="col">{t('history.author')}</th>
      <th scope="col">{t('history.date')}</th>
      <th scope="col"><span class="sr-only">{t('common.actions')}</span></th>
    </tr>
  </thead>
  <tbody>
    {#each revisions as r (r.revision)}
      <tr>
        <td>{r.revision}</td>
        <td>{t(`history.kind_${r.kind}`)}{#if r.summary}: {r.summary}{/if}</td>
        <td>{r.author}{#if r.approved_by}<span class="muted"> · {t('history.approved_by', { name: r.approved_by })}</span>{/if}</td>
        <td>{formatDate(r.created_at, locale())}</td>
        <td>
          {#if canEdit(area) && r.revision !== current}
            <button type="button" class="link" disabled={busy} onclick={() => revert(r.revision)}>{t('history.revert')}</button>
          {/if}
        </td>
      </tr>
    {/each}
  </tbody>
</table>
<!-- La retención del área (§4.3) borró las más viejas: se dice, para que nadie las busque. -->
{#if (revisions.at(-1)?.revision ?? 1) > 1}
  <p class="muted small">{t('history.trimmed', { n: String(revisions.at(-1)?.revision) })}</p>
{/if}

{#if revisions.length > 1}
  <h2>{t('history.compare')}</h2>
  <form class="inline" onsubmit={(e) => (e.preventDefault(), compare())}>
    <label>
      {t('history.from')}
      <select bind:value={from}>{#each revisions as r (r.revision)}<option value={r.revision}>{r.revision}</option>{/each}</select>
    </label>
    <label>
      {t('history.to')}
      <select bind:value={to}>{#each revisions as r (r.revision)}<option value={r.revision}>{r.revision}</option>{/each}</select>
    </label>
    <button type="submit" class="primary" disabled={busy}>{t('history.compare')}</button>
  </form>
  {#if diff.length}<DiffView lines={diff} />{/if}
{/if}
