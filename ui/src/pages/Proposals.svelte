<script lang="ts">
  import { api, codeOf, timeAgo, type Area, type DiffLine, type Proposal, type ProposalDetail } from '../lib/api';
  import { t, errorText, locale, type Key } from '../lib/i18n/index.svelte';
  import { navigate } from '../lib/router.svelte';
  import { enhance } from '../lib/enhance';
  import DiffView from '../components/DiffView.svelte';

  // Bandeja de propuestas con el diseño aprobado (SPEC §5.2, §15): la lista a la izquierda y el
  // detalle a la derecha. El admin del área revisa todas; los demás ven solo las suyas, que es
  // también lo único que el servidor les entrega.
  let { area, id, selfId, onchanged }: { area: Area; id: string; selfId: string; onchanged: () => void } = $props();

  type Tab = 'pending' | 'resolved' | 'mine';
  const admin = $derived(area.role === 'admin');
  const tabs = $derived<Tab[]>(admin ? ['pending', 'resolved', 'mine'] : ['mine']);
  let tab = $state<Tab>('pending');
  let list = $state<Proposal[]>([]);
  let pendingCount = $state(0);
  let detail = $state<ProposalDetail | null>(null);
  let pane = $state<'changes' | 'current' | 'preview'>('changes');
  let comment = $state('');
  let busy = $state(false);
  let error = $state('');
  let preview = $state<HTMLElement>();

  const base = $derived(`/w/${area.slug}`);
  // "Esperando hace N días" sale cuando el servidor dice que ya le tocó el recordatorio al
  // aprobador (overdue, approval.remind_after_days; SPEC §5.3).
  const DAY = 86_400_000;

  const daysWaiting = (p: Proposal) => Math.floor((Date.now() - new Date(p.created_at).getTime()) / DAY);
  const pathOf = (slug: string) => [area.slug, ...slug.split('/')].join(' / ');
  const statusKey = (p: Proposal) => `proposals.status_${p.status}` as Key;
  const count = (lines: DiffLine[] | null, op: '+' | '-') => (lines ?? []).filter((l) => l.op === op).length;
  const shown = $derived(pane === 'current' ? detail?.against_current : detail?.changes);
  const isAuthor = $derived(detail?.author_id === selfId);

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

  async function loadList() {
    try {
      list = await api<Proposal[]>('GET', `${base}/proposals?view=${tab}`);
      if (tab === 'pending') {
        pendingCount = list.length;
        // Ver las pendientes deja leídos sus avisos de la campana y ya no salen por correo (§12.1).
        void api('POST', `${base}/proposals/seen`).catch(() => {});
      }
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }

  async function loadDetail() {
    detail = null;
    comment = '';
    pane = 'changes';
    if (!id) return;
    try {
      detail = await api<ProposalDetail>('GET', `${base}/proposals/${id}`);
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }

  // Quien no es admin solo tiene "Mías"; el admin empieza por lo que espera revisión.
  $effect(() => {
    tab = admin ? 'pending' : 'mine';
  });
  $effect(() => {
    void tab;
    loadList();
  });
  $effect(() => {
    void id;
    loadDetail();
  });
  $effect(() => {
    if (pane === 'preview' && detail && preview) enhance(preview);
  });

  // Después de resolver una, la lista y el contador del menú cambian.
  async function refresh() {
    onchanged();
    await Promise.all([loadList(), loadDetail()]);
  }

  const approve = () =>
    run(async () => {
      if (!detail) return;
      await api('POST', `${base}/proposals/${detail.id}/approve`, { comment });
      onchanged();
      navigate(`${base}/p/${detail.slug}`);
    });

  const reject = () =>
    run(async () => {
      if (!detail) return;
      await api('POST', `${base}/proposals/${detail.id}/reject`, { comment });
      await refresh();
    });

  const act = (action: 'submit' | 'withdraw') =>
    run(async () => {
      if (!detail) return;
      await api('POST', `${base}/proposals/${detail.id}/${action}`);
      await refresh();
    });

  function onTabKey(e: KeyboardEvent, i: number) {
    const step = { ArrowLeft: -1, ArrowRight: 1 }[e.key];
    if (!step) return;
    e.preventDefault();
    const next = tabs[(i + step + tabs.length) % tabs.length];
    if (next) tab = next;
    document.getElementById(`ptab-${next}`)?.focus();
  }
</script>

<div class="proposals" class:has-detail={!!id}>
  <section class="proposal-list" aria-label={t('proposals.list')}>
    <div class="proposal-tabs" role="tablist">
      {#each tabs as k, i (k)}
        <button
          type="button"
          role="tab"
          id={`ptab-${k}`}
          aria-selected={tab === k}
          tabindex={tab === k ? 0 : -1}
          onclick={() => (tab = k)}
          onkeydown={(e) => onTabKey(e, i)}
        >
          {t(`proposals.tab_${k}`)}{#if k === 'pending' && pendingCount}<span class="tab-count">{pendingCount}</span>{/if}
        </button>
      {/each}
    </div>
    {#if list.length}
      <ul role="list">
        {#each list as p (p.id)}
          <li>
            <a class="proposal-row" href={`${base}/proposals/${p.id}`} aria-current={p.id === id ? 'true' : undefined}>
              <span class="proposal-row-title">{p.title}</span>
              <span class="muted">{pathOf(p.slug)}</span>
              <span class="proposal-row-meta">
                <span>{p.author} · {timeAgo(p.created_at, locale())}</span>
                {#if p.status !== 'pending'}
                  <span class={`flag flag-${p.status}`}>{t(statusKey(p))}</span>
                {:else if p.new_page}
                  <span class="flag flag-new">{t('proposals.flag_new')}</span>
                {:else if p.current_revision > p.base_revision}
                  <span class="flag flag-changed">{t('proposals.flag_changed')}</span>
                {/if}
                {#if p.overdue}
                  <span class="flag flag-waiting">{t('proposals.flag_waiting', { n: String(daysWaiting(p)) })}</span>
                {/if}
              </span>
            </a>
          </li>
        {/each}
      </ul>
    {:else}
      <p class="muted proposal-empty">{t('proposals.empty')}</p>
    {/if}
  </section>

  <section class="proposal-detail" aria-label={t('proposals.detail')}>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    {#if detail}
      <header class="proposal-head">
        <span class="muted">{pathOf(detail.slug)}</span>
        <h1>{detail.title}</h1>
        <span class="muted">
          {t('proposals.by', { name: detail.author, when: timeAgo(detail.created_at, locale()) })} ·
          {detail.new_page ? t('proposals.new_page') : t('proposals.on_revision', { n: String(detail.base_revision) })}
        </span>
      </header>

      {#if detail.status === 'pending' && detail.page_changed}
        <p class="proposal-note" class:clash={detail.conflict}>
          {t(detail.conflict ? 'proposals.changed_conflict' : 'proposals.changed_clean', { n: String(detail.current_revision) })}
        </p>
      {/if}
      {#if detail.status === 'merged'}
        <p class="proposal-status">
          {detail.merged_revision
            ? t('proposals.merged_line', { n: String(detail.merged_revision), name: detail.reviewer })
            : t('proposals.merged_same', { name: detail.reviewer })}
          <a href={`${base}/p/${detail.slug}`}>{t('proposals.view_page')}</a>
        </p>
      {:else if detail.status === 'rejected'}
        <p class="proposal-status">{t(isAuthor ? 'proposals.rejected_line' : 'proposals.rejected_admin', { name: detail.reviewer })}</p>
      {:else if detail.status === 'withdrawn'}
        <p class="proposal-status">{t('proposals.withdrawn_line')}</p>
      {/if}
      {#if detail.comment}
        <blockquote class="proposal-comment"><strong>{t('proposals.reviewer_comment', { name: detail.reviewer })}</strong> {detail.comment}</blockquote>
      {/if}
      {#if detail.summary}<p><strong>{t('proposals.summary')}</strong> {detail.summary}</p>{/if}

      <div class="proposal-panes">
        <div role="tablist">
          <button type="button" role="tab" aria-selected={pane === 'changes'} onclick={() => (pane = 'changes')}>{t('proposals.tab_changes')}</button>
          {#if detail.against_current}
            <button type="button" role="tab" aria-selected={pane === 'current'} onclick={() => (pane = 'current')}>{t('proposals.tab_current')}</button>
          {/if}
          <button type="button" role="tab" aria-selected={pane === 'preview'} onclick={() => (pane = 'preview')}>{t('proposals.tab_preview')}</button>
        </div>
        {#if pane !== 'preview' && shown}
          <span class="muted">{t('proposals.lines', { added: String(count(shown, '+')), removed: String(count(shown, '-')) })}</span>
        {/if}
      </div>
      <div role="tabpanel">
        {#if pane === 'preview'}
          <div class="prose proposal-preview" lang={detail.lang} bind:this={preview}>{@html detail.html}</div>
        {:else if shown}
          <DiffView lines={shown} numbered />
        {/if}
      </div>

      {#if detail.status === 'pending' && admin}
        <label class="field">
          {t('proposals.comment')}
          <textarea rows="3" maxlength="2000" placeholder={t('proposals.comment_hint')} bind:value={comment}></textarea>
        </label>
        <div class="proposal-actions">
          <button type="button" class="secondary reject" disabled={busy} onclick={reject}>{t('proposals.reject')}</button>
          {#if !isAuthor}
            <a class="secondary" href={`${base}/proposals/${detail.id}/edit`}>{t('proposals.edit_approve')}</a>
          {/if}
          <button type="button" class="primary" disabled={busy || detail.conflict} onclick={approve}>{t('proposals.approve')}</button>
        </div>
      {/if}
      {#if isAuthor && (detail.status === 'pending' || detail.status === 'rejected')}
        <div class="proposal-actions">
          <button type="button" class="secondary" disabled={busy} onclick={() => act('withdraw')}>{t('proposals.withdraw')}</button>
          <a class="secondary" href={`${base}/proposals/${detail.id}/edit`}>{t('proposals.edit')}</a>
          {#if detail.status === 'rejected'}
            <button type="button" class="primary" disabled={busy} onclick={() => act('submit')}>{t('proposals.resubmit')}</button>
          {/if}
        </div>
      {/if}
    {:else if !id}
      <p class="muted">{t('proposals.pick')}</p>
    {/if}
  </section>
</div>
