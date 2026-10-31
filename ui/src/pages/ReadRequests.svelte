<script lang="ts">
  import { api, codeOf, formatDate, formatPercent, timeAgo, type Area, type ReadRequest, type ReadRequestDetail } from '../lib/api';
  import LevelBadge from '../components/LevelBadge.svelte';
  import ReadRequestDialog from '../components/ReadRequestDialog.svelte';
  import Dialog from '../components/Dialog.svelte';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';

  // Solicitudes de lectura (§4.14): el admin ve todas las del área; un editor, las que pidió. Con
  // id, el panel de una: el estado de cada destinatario, exportable a CSV.
  let { area, id }: { area: Area; id: string } = $props();

  const base = $derived(`/w/${area.slug}`);
  let list = $state<ReadRequest[]>([]);
  let detail = $state<ReadRequestDetail | null>(null);
  let pageRevision = $state(0);
  let loaded = $state(false);
  let error = $state('');
  let again = $state(false);
  // Intentos extra para quien se quedó sin aprobar (§4.16.5).
  let extraFor = $state<{ user_id: string; name: string } | null>(null);
  let extraOpen = $state(false);
  let extra = $state(1);
  let reason = $state('');

  async function load() {
    loaded = false;
    error = '';
    try {
      if (id) {
        detail = await api<ReadRequestDetail>('GET', `${base}/read-requests/${id}`);
        // Si la página cambió desde la solicitud, se ofrece pedir que vuelvan a confirmar.
        pageRevision = (await api<{ revision: number }>('GET', `${base}/pages/${detail.page.slug}`)).revision;
      } else {
        list = await api<ReadRequest[]>('GET', `${base}/read-requests`);
      }
    } catch (e) {
      error = errorText(codeOf(e));
    } finally {
      loaded = true;
    }
  }

  $effect(() => {
    void id;
    detail = null;
    load();
  });

  async function close() {
    error = '';
    try {
      await api('POST', `${base}/read-requests/${id}/close`);
      await load();
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }

  async function grant(e: SubmitEvent) {
    e.preventDefault();
    if (!detail || !extraFor) return;
    error = '';
    try {
      await api('POST', `${base}/quizzes/${detail.quiz_id}/extra-attempts`, { user_id: extraFor.user_id, request_id: id, extra, reason });
      extraOpen = false;
      await load();
    } catch (err) {
      error = errorText(codeOf(err));
    }
  }

  const ago = (iso: string) => timeAgo(iso, locale());
</script>

{#if id}
  {#if detail}
    <h1>{t('reading.panel_title', { title: detail.page.title })}</h1>
    <p class="muted">
      <a href={`${base}/p/${detail.page.slug}`}>{detail.page.title}</a> <LevelBadge level={detail.page.level} />
      · {t('reading.asked_by', { name: detail.created_by, ago: ago(detail.created_at) })}
      · {t('page.revision', { n: String(detail.revision) })}
      {#if detail.due_at}· {t('reading.due_line', { date: formatDate(detail.due_at, locale()) })}{/if}
      {#if detail.closed_at}· <strong>{t('reading.closed')}</strong>{/if}
    </p>
    {#if detail.statement}<p>«{detail.statement}»</p>{/if}
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <p class="actions-row">
      <a class="secondary" href={`/api${base}/read-requests/${id}?format=csv`} download>{t('reading.csv')}</a>
      {#if !detail.closed_at}<button type="button" class="secondary" onclick={close}>{t('reading.close')}</button>{/if}
      {#if pageRevision > detail.revision}
        <button type="button" class="secondary" onclick={() => (again = true)}>{t('reading.again')}</button>
      {/if}
      {#if detail.quiz_id}<a class="secondary" href={`${base}/quizzes/${detail.quiz_id}#results`}>{t('reading.quiz_results')}</a>{/if}
    </p>
    {#if pageRevision > detail.revision}<p class="muted">{t('reading.page_changed', { n: String(pageRevision) })}</p>{/if}
    {#if detail.people.length}
      <table>
        <thead>
          <tr>
            <th scope="col">{t('common.name')}</th>
            <th scope="col">{t('reading.status')}</th>
            <th scope="col">{t('reading.opened')}</th>
            <th scope="col">{t(detail.quiz_id ? 'reading.passed_col' : 'reading.confirmed_col')}</th>
            {#if detail.quiz_id}<th scope="col">{t('quiz.attempts_col')}</th>{/if}
          </tr>
        </thead>
        <tbody>
          {#each detail.people as p (p.user_id)}
            <tr>
              <td>{p.name}<br /><small class="muted">{t(`role.${p.role}`)}</small></td>
              <td class={`read-status-${p.status}`}>{t(`reading.status_${p.status}`)}</td>
              <td>
                {#if p.first_opened_at && p.last_opened_at}
                  {t('reading.opens', { n: String(p.opens) })}
                  <br /><small class="muted">{t('reading.opened_range', { first: formatDate(p.first_opened_at, locale()), last: ago(p.last_opened_at) })}</small>
                {:else}—{/if}
              </td>
              <td>
                {#if p.confirmed_at}
                  <time datetime={p.confirmed_at} title={formatDate(p.confirmed_at, locale())}>{ago(p.confirmed_at)}</time>
                  <br /><small class="muted">{t('page.revision', { n: String(p.confirmed_revision) })}</small>
                {:else}—{/if}
              </td>
              {#if detail.quiz_id}
                <td>
                  {p.attempts}{#if p.extra_attempts} <small class="muted">(+{p.extra_attempts})</small>{/if}
                  {#if p.attempts}<br /><small class="muted">{t('reading.best', { percent: formatPercent(p.best_percent, locale()) })}</small>{/if}
                  {#if p.status !== 'passed' && !detail.closed_at}
                    <br /><button type="button" class="link" onclick={() => ((extraFor = p), (extra = 1), (reason = ''), (extraOpen = true))}>{t('quiz.give_extra')}</button>
                  {/if}
                </td>
              {/if}
            </tr>
          {/each}
        </tbody>
      </table>
    {:else}
      <p class="muted">{t('reading.nobody')}</p>
    {/if}
    <Dialog title={t('quiz.give_extra')} bind:open={extraOpen}>
      <form class="stack" onsubmit={grant}>
        <p>{extraFor?.name}</p>
        <label class="field">
          {t('quiz.extra_n')}
          <input type="number" min="1" max="10" required bind:value={extra} />
        </label>
        <label class="field">
          {t('quiz.extra_reason')}
          <input type="text" maxlength="500" required bind:value={reason} />
        </label>
        <button type="submit" class="primary">{t('quiz.give_extra')}</button>
      </form>
    </Dialog>
    <ReadRequestDialog {area} slug={detail.page.slug} bind:open={again} again={{ audience: detail.audience, statement: detail.statement, quiz_id: detail.quiz_id }} />
  {:else if error}
    <p class="error" role="alert">{error}</p>
  {/if}
{:else}
  <h1>{t('reading.title')}</h1>
  <p class="muted">{t(area.role === 'admin' ? 'reading.intro_admin' : 'reading.intro_editor')}</p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#if list.length}
    <table>
      <thead>
        <tr>
          <th scope="col">{t('page.title')}</th>
          <th scope="col">{t('reading.progress')}</th>
          <th scope="col">{t('reading.due')}</th>
        </tr>
      </thead>
      <tbody>
        {#each list as r (r.id)}
          <tr>
            <td>
              <a href={`${base}/reads/${r.id}`}>{r.page.title}</a> <LevelBadge level={r.page.level} />
              <br /><small class="muted">{t('reading.asked_by', { name: r.created_by, ago: ago(r.created_at) })}{#if r.closed_at} · {t('reading.closed')}{/if}</small>
            </td>
            <td>{t('reading.progress_line', { confirmed: String(r.confirmed ?? 0), total: String(r.recipients ?? 0), opened: String(r.opened ?? 0) })}</td>
            <td>{r.due_at ? formatDate(r.due_at, locale()) : '—'}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {:else if loaded && !error}
    <p class="muted">{t('reading.empty')}</p>
  {/if}
{/if}
