<script lang="ts">
  import { api, codeOf, formatDate } from '../lib/api';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';
  import Dialog from './Dialog.svelte';

  // Dar de baja a una persona (SPEC §8.4): el resumen de lo que se transfiere y lo que se borra,
  // quién lo recibe en cada área y, si se quiere, completarla sin esperar la gracia. Con una baja
  // ya en curso, el diálogo solo la adelanta.
  type Admin = { id: string; full_name: string };
  type PlanArea = {
    id: string;
    name: string;
    pages: number;
    sealed: number;
    quizzes: number;
    read_requests: number;
    admins: Admin[];
    receiver: string;
    to_you: boolean;
    needs_admin: boolean;
  };
  type Plan = {
    areas: PlanArea[];
    personal_pages: number;
    personal_media: number;
    personal_sealed: number;
    drafts: number;
    proposals: number;
    grace_days: number;
    pending?: { until: string; receivers: Record<string, string> };
  };

  let {
    user,
    open = $bindable(false),
    ondone,
  }: { user: { id: string; full_name: string } | null; open?: boolean; ondone: (message: string) => void } = $props();

  let plan = $state<Plan | null>(null);
  let receivers = $state<Record<string, string>>({});
  let now = $state(false);
  let busy = $state(false);
  let error = $state('');

  const items = (a: PlanArea) => a.pages + a.sealed + a.quizzes + a.read_requests;
  const blocked = $derived(plan?.areas.some((a) => a.needs_admin) ?? false);
  const anything = $derived(plan?.areas.some((a) => items(a) > 0) ?? false);
  // Cuándo se completa: la fecha de la baja en curso, o hoy más la gracia.
  const until = $derived(plan?.pending?.until ?? new Date(Date.now() + (plan?.grace_days ?? 0) * 86_400_000).toISOString());

  $effect(() => {
    if (!open || !user) return;
    const id = user.id;
    plan = null;
    error = '';
    now = false;
    api<Plan>('GET', `/admin/users/${id}/offboarding`)
      .then((p) => {
        // Lo elegido al iniciar la baja manda; si no, el admin más antiguo de cada área.
        const chosen = p.pending?.receivers ?? {};
        receivers = Object.fromEntries(p.areas.filter((a) => a.admins.length).map((a) => [a.id, chosen[a.id] ?? a.receiver]));
        plan = p;
      })
      .catch((e) => (error = errorText(codeOf(e))));
  });

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (!user || !plan) return;
    const name = user.full_name;
    busy = true;
    error = '';
    try {
      const res = await api<{ status: 'pending' | 'completed'; until?: string }>('POST', `/admin/users/${user.id}/offboarding`, {
        receivers,
        now: now || !!plan.pending,
      });
      open = false;
      ondone(
        res.status === 'completed'
          ? t('admin.offboard_completed', { name })
          : t('admin.offboard_started', { name, date: formatDate(res.until ?? until, locale()) }),
      );
    } catch (err) {
      error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }
</script>

<Dialog title={t('admin.offboard_title', { name: user?.full_name ?? '' })} bind:open>
  {#if plan}
    <form class="stack offboard" onsubmit={submit}>
      <p>{t(plan.pending ? 'admin.offboard_pending_intro' : 'admin.offboard_intro', { date: formatDate(until, locale()) })}</p>

      <h3>{t('admin.offboard_transfer_title')}</h3>
      {#if !anything}<p class="muted small">{t('admin.offboard_nothing')}</p>{/if}
      {#each plan.areas as a (a.id)}
        {#if a.needs_admin}
          <p class="error">
            {t('admin.offboard_needs_admin', { area: a.name })}
            <a href={`/admin/areas/${a.id}`}>{t('admin.offboard_members_link', { area: a.name })}</a>
          </p>
        {/if}
        {#if items(a) > 0}
          <div class="offboard-area">
            <strong>{a.name}</strong>
            <span class="muted small">
              {t('admin.offboard_area_items', { pages: String(a.pages), sealed: String(a.sealed), quizzes: String(a.quizzes), reads: String(a.read_requests) })}
            </span>
            {#if a.admins.length}
              <label class="field">
                {t('admin.offboard_receiver', { area: a.name })}
                <select bind:value={receivers[a.id]}>
                  {#each a.admins as ad (ad.id)}<option value={ad.id}>{ad.full_name}</option>{/each}
                </select>
              </label>
            {:else if a.to_you}
              <span class="muted small">{t('admin.offboard_to_you')}</span>
            {/if}
          </div>
        {/if}
      {/each}

      <h3>{t('admin.offboard_delete_title')}</h3>
      <p class="muted small">
        {t('admin.offboard_delete', {
          pages: String(plan.personal_pages),
          media: String(plan.personal_media),
          sealed: String(plan.personal_sealed),
          drafts: String(plan.drafts),
          proposals: String(plan.proposals),
        })}
      </p>
      <p class="muted small">{t('admin.offboard_keep')}</p>

      {#if !plan.pending}
        <label class="check">
          <input type="checkbox" bind:checked={now} disabled={blocked} />
          {t('admin.offboard_now')}
        </label>
      {/if}
      {#if error}<p class="error" role="alert">{error}</p>{/if}
      <button type="submit" class="primary danger-btn" disabled={busy || ((now || !!plan.pending) && blocked)}>
        {t(plan.pending || now ? 'admin.offboard_complete_now' : 'admin.offboard')}
      </button>
    </form>
  {:else if error}
    <p class="error" role="alert">{error}</p>
  {/if}
</Dialog>
