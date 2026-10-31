<script lang="ts">
  import { api, codeOf, formatDate, timeAgo, type Area, type Member, type Page } from '../lib/api';
  import Dialog from './Dialog.svelte';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';

  // Vigencia de la página (§4.8): el intervalo lo cambian el dueño o un admin; el dueño, solo un
  // admin, entre quienes pueden editarla. onchanged recarga la página.
  let { area, page, open = $bindable(false), onchanged }: { area: Area; page: Page; open: boolean; onchanged: () => void } = $props();

  const presets = [30, 90, 180, 365, 730];
  const rank = { viewer: 1, editor: 2, admin: 3 } as const;
  let days = $state(0);
  let owner = $state('');
  let members = $state<Member[]>([]);
  let error = $state('');
  let busy = $state(false);
  const isAdmin = $derived(area.role === 'admin');
  // Un intervalo que no está en la lista (puesto por la API) se muestra igual, para no perderlo al guardar.
  const options = $derived(
    !page.review.interval_days || presets.includes(page.review.interval_days) ? presets : [...presets, page.review.interval_days].sort((a, b) => a - b),
  );
  // Pueden ser dueños los que pueden editarla: editor o admin, con el nivel de la página.
  const candidates = $derived(members.filter((m) => rank[m.role] >= Math.max(2, page.level)));

  $effect(() => {
    if (!open) return;
    days = page.review.interval_days;
    owner = page.review.owner_id;
    error = '';
    if (isAdmin) {
      api<Member[]>('GET', `/w/${area.slug}/members`)
        .then((list) => (members = list))
        .catch((e) => (error = errorText(codeOf(e))));
    }
  });

  async function run(fn: () => Promise<unknown>) {
    busy = true;
    error = '';
    try {
      await fn();
      open = false;
      onchanged();
    } catch (e) {
      error = errorText(codeOf(e));
    } finally {
      busy = false;
    }
  }

  function save(e: SubmitEvent) {
    e.preventDefault();
    const body: { review_interval_days: number; owner_id?: string } = { review_interval_days: days };
    if (isAdmin && owner) body.owner_id = owner;
    run(() => api('PATCH', `/w/${area.slug}/meta/${page.slug}`, body));
  }
</script>

<Dialog title={t('review.title')} bind:open>
  <form class="stack" onsubmit={save}>
    <p class="muted">
      {t('review.verified', { ago: timeAgo(page.review.verified_at, locale()), name: page.review.verified_by })}
      ·
      <time datetime={page.review.verified_at}>{formatDate(page.review.verified_at, locale())}</time>
    </p>
    <label class="field">
      {t('review.interval')}
      <select bind:value={days}>
        <option value={0}>
          {page.review.area_interval_days ? t('review.area_default', { days: String(page.review.area_interval_days) }) : t('review.area_none')}
        </option>
        {#each options as d (d)}<option value={d}>{t('review.every_days', { days: String(d) })}</option>{/each}
      </select>
    </label>
    {#if isAdmin}
      <label class="field">
        {t('review.owner')}
        <select bind:value={owner}>
          {#if page.review.owner_id && !candidates.some((m) => m.user_id === page.review.owner_id)}
            <option value={page.review.owner_id}>{t('review.owner_cannot_edit', { name: page.review.owner || '—' })}</option>
          {/if}
          {#each candidates as m (m.user_id)}<option value={m.user_id}>{m.full_name} · {t(`role.${m.role}`)}</option>{/each}
        </select>
      </label>
      <small class="muted">{t('review.owner_hint')}</small>
    {/if}
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <div class="form-actions">
      <button type="submit" class="primary" disabled={busy}>{t('review.save')}</button>
      <button type="button" disabled={busy} onclick={() => run(() => api('POST', `/w/${area.slug}/verify/${page.slug}`))}>{t('review.verify')}</button>
    </div>
  </form>
</Dialog>
