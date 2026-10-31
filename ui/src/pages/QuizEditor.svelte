<script lang="ts">
  import {
    api,
    codeOf,
    formatDate,
    formatPercent,
    type Area,
    type AttemptView,
    type Page,
    type Quiz,
    type QuizDraft,
    type QuizKind,
    type QuizQuestion,
    type QuizResults,
  } from '../lib/api';
  import Dialog from '../components/Dialog.svelte';
  import QuizAttempt from '../components/QuizAttempt.svelte';
  import AttemptResult from '../components/AttemptResult.svelte';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';
  import { navigate, route } from '../lib/router.svelte';

  // Administrar un cuestionario (§4.16): el borrador con sus preguntas y su configuración, publicar
  // (cada publicación es una versión, §4.16.6), probarlo sin que cuente y ver los resultados por
  // persona y por pregunta (§4.16.5). Quien solo lo asignó llega aquí a ver los resultados.
  let { area, id }: { area: Area; id: string } = $props();

  const base = $derived(`/w/${area.slug}`);
  let quiz = $state<Quiz | null>(null);
  let draft = $state<QuizDraft | null>(null);
  let title = $state('');
  let open = $state(false);
  let headings = $state<{ id: string; text: string }[]>([]);
  let tab = $state<'questions' | 'settings' | 'results' | 'preview'>('questions');
  let results = $state<QuizResults | null>(null);
  let preview = $state<AttemptView | null>(null);
  let error = $state('');
  let notice = $state('');
  let busy = $state(false);
  let changed = $state(false);
  let confirmDelete = $state(false);
  let extraFor = $state<{ user_id: string; name: string } | null>(null);
  let extraOpen = $state(false);
  let extra = $state(1);
  let reason = $state('');

  async function load() {
    error = '';
    try {
      quiz = await api<Quiz>('GET', `${base}/quizzes/${id}`);
      if (quiz.draft) {
        draft = $state.snapshot(quiz.draft) as QuizDraft;
        title = quiz.title;
        open = quiz.open_to_readers;
        changed = false;
        const page = await api<Page>('GET', `${base}/pages/${quiz.page.slug}`);
        headings = (page.toc ?? []).map((h) => ({ id: h.id, text: h.text }));
      } else {
        tab = 'results';
      }
      if (tab === 'results') await loadResults();
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }

  // #results: se llega desde el panel de una solicitud de lectura a ver quién aprobó (§4.16.5).
  $effect(() => {
    void id;
    preview = results = null;
    tab = route.hash === '#results' ? 'results' : 'questions';
    load();
  });

  async function loadResults() {
    results = await api<QuizResults>('GET', `${base}/quizzes/${id}/results`);
  }

  function show(next: typeof tab) {
    tab = next;
    error = notice = '';
    if (next === 'results') loadResults().catch((e) => (error = errorText(codeOf(e))));
  }

  const newId = () => crypto.randomUUID();

  function addQuestion(kind: QuizKind) {
    if (!draft) return;
    const options =
      kind === 'true_false'
        ? [
            { id: 'true', text: '', correct: true },
            { id: 'false', text: '', correct: false },
          ]
        : [
            { id: newId(), text: '', correct: true },
            { id: newId(), text: '', correct: false },
          ];
    draft.questions.push({ id: newId(), kind, prompt_md: '', points: 1, explanation_md: '', anchor: '', options });
    changed = true;
  }

  function move(i: number, by: number) {
    if (!draft) return;
    const [q] = draft.questions.splice(i, 1);
    draft.questions.splice(i + by, 0, q!);
    changed = true;
  }

  function remove(i: number) {
    draft?.questions.splice(i, 1);
    changed = true;
  }

  // Alternativa única y verdadero/falso: una sola correcta; selección múltiple: las que sean.
  function markCorrect(q: QuizQuestion, optionId: string) {
    for (const o of q.options) o.correct = q.kind === 'multiple' ? (o.id === optionId ? !o.correct : o.correct) : o.id === optionId;
    changed = true;
  }

  async function save(): Promise<boolean> {
    if (!draft) return false;
    busy = true;
    error = notice = '';
    try {
      const r = await api<{ draft: QuizDraft }>('PATCH', `${base}/quizzes/${id}`, { title, open_to_readers: open, draft: $state.snapshot(draft) });
      draft = r.draft;
      changed = false;
      notice = t('quiz.saved_draft');
      return true;
    } catch (e) {
      error = errorText(codeOf(e));
      return false;
    } finally {
      busy = false;
    }
  }

  async function publish() {
    if (!(await save())) return;
    busy = true;
    try {
      const { version } = await api<{ version: number }>('POST', `${base}/quizzes/${id}/publish`);
      await load();
      notice = t('quiz.published', { n: String(version) });
    } catch (e) {
      error = errorText(codeOf(e));
    } finally {
      busy = false;
    }
  }

  async function archive() {
    error = '';
    try {
      await api('POST', `${base}/quizzes/${id}/archive`);
      await load();
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }

  async function removeQuiz() {
    try {
      await api('DELETE', `${base}/quizzes/${id}`);
      navigate(`${base}/p/${quiz?.page.slug ?? ''}`);
    } catch (e) {
      error = errorText(codeOf(e));
      confirmDelete = false;
    }
  }

  async function startPreview() {
    error = '';
    if (changed && !(await save())) return;
    try {
      preview = await api<AttemptView>('POST', `${base}/quizzes/${id}/preview`);
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }

  async function grant(e: SubmitEvent) {
    e.preventDefault();
    if (!extraFor) return;
    error = '';
    try {
      await api('POST', `${base}/quizzes/${id}/extra-attempts`, { user_id: extraFor.user_id, extra, reason });
      extraOpen = false;
      notice = t('quiz.extra_given');
    } catch (err) {
      error = errorText(codeOf(err));
    }
  }

  // El tiempo que se anuncia si nadie lo corrige (§4.16.3), igual que lo calcula el servidor.
  const autoMinutes = $derived.by(() => {
    if (!draft) return 1;
    const qs = draft.questions;
    let secs = qs.reduce((s, q) => s + (q.kind === 'true_false' ? 30 : q.kind === 'single' ? 45 : 60), 0);
    const pick = draft.settings.pick;
    if (pick > 0 && pick < qs.length) secs = (secs * pick) / qs.length;
    return Math.max(1, Math.ceil(secs / 60));
  });
  const percent = (correct: number, answered: number) => (answered ? Math.round((correct / answered) * 100) : 0);
  const wrongLabel = (q: QuizResults['questions'][number]) =>
    q.kind === 'true_false' ? t(q.top_wrong === 'true' ? 'quiz.true' : 'quiz.false') : q.top_wrong;
</script>

{#if quiz}
  <h1>{quiz.title}</h1>
  <p class="muted">
    {t(`quiz.status_${quiz.status}`)}{' '}{#if quiz.published_version} · {t('quiz.version', { n: String(quiz.published_version) })}{/if}
    · {t('quiz.about_page')} <a href={`${base}/p/${quiz.page.slug}`}>{quiz.page.title}</a>
    {#if quiz.summary}· <a href={`${base}/quiz/${quiz.id}`}>{t('quiz.take_link')}</a>{/if}
  </p>
  {#if quiz.page_changed}<p class="review-notice strong" role="note">{t('quiz.page_changed')}</p>{/if}
  {#if quiz.dirty && quiz.status !== 'draft'}<p class="muted">{t('quiz.dirty')}</p>{/if}

  <div class="tabs">
    <div role="tablist">
      {#if draft}
        <button type="button" role="tab" aria-selected={tab === 'questions'} onclick={() => show('questions')}>{t('quiz.tab_questions')}</button>
        <button type="button" role="tab" aria-selected={tab === 'settings'} onclick={() => show('settings')}>{t('quiz.tab_settings')}</button>
      {/if}
      {#if quiz.results}
        <button type="button" role="tab" aria-selected={tab === 'results'} onclick={() => show('results')}>{t('quiz.tab_results')}</button>
      {/if}
      {#if draft}
        <button type="button" role="tab" aria-selected={tab === 'preview'} onclick={() => show('preview')}>{t('quiz.tab_preview')}</button>
      {/if}
    </div>
  </div>

  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#if notice}<p class="muted" role="status">{notice}</p>{/if}

  <div role="tabpanel" class="quiz-panel">
    {#if tab === 'questions' && draft}
      <label class="field">
        {t('quiz.title_field')}
        <input type="text" maxlength="200" bind:value={title} oninput={() => (changed = true)} />
      </label>
      <label class="check">
        <input type="checkbox" bind:checked={open} onchange={() => (changed = true)} />
        {t('quiz.open_to_readers')}
      </label>
      {#each draft.questions as q, i (q.id)}
        <fieldset class="quiz-edit">
          <legend>{t('quiz.question_n', { n: String(i + 1) })} · {t(`quiz.kind_${q.kind}`)}</legend>
          <label class="field">
            {t('quiz.prompt')}
            <textarea rows="3" maxlength="4000" bind:value={q.prompt_md} oninput={() => (changed = true)}></textarea>
          </label>
          <div class="quiz-options">
            {#each q.options as o, j (o.id)}
              <div class="quiz-option-edit">
                <input
                  type={q.kind === 'multiple' ? 'checkbox' : 'radio'}
                  name={`correct-${q.id}`}
                  checked={o.correct}
                  aria-label={t('quiz.is_correct')}
                  onchange={() => markCorrect(q, o.id)}
                />
                {#if q.kind === 'true_false'}
                  <span>{t(o.id === 'true' ? 'quiz.true' : 'quiz.false')}</span>
                {:else}
                  <input type="text" maxlength="500" aria-label={t('quiz.option_n', { n: String(j + 1) })} bind:value={o.text} oninput={() => (changed = true)} />
                  {#if q.options.length > 2}
                    <button type="button" class="link" onclick={() => (q.options.splice(j, 1), (changed = true))}>{t('quiz.remove_option')}</button>
                  {/if}
                {/if}
              </div>
            {/each}
            {#if q.kind !== 'true_false' && q.options.length < 10}
              <button type="button" class="link" onclick={() => (q.options.push({ id: newId(), text: '', correct: false }), (changed = true))}>{t('quiz.add_option')}</button>
            {/if}
            <small class="muted">{t(q.kind === 'multiple' ? 'quiz.correct_hint_multiple' : 'quiz.correct_hint')}</small>
          </div>
          <div class="quiz-row">
            <label class="field">
              {t('quiz.points_field')}
              <input type="number" min="1" max="100" bind:value={q.points} oninput={() => (changed = true)} />
            </label>
            <label class="field">
              {t('quiz.anchor')}
              <select bind:value={q.anchor} onchange={() => (changed = true)}>
                <option value="">—</option>
                {#each headings as h (h.id)}<option value={h.id}>{h.text}</option>{/each}
              </select>
            </label>
          </div>
          <label class="field">
            {t('quiz.explanation')}
            <textarea rows="2" maxlength="4000" bind:value={q.explanation_md} oninput={() => (changed = true)}></textarea>
          </label>
          <p class="actions-row">
            {#if i > 0}<button type="button" class="secondary" onclick={() => move(i, -1)}>{t('quiz.move_up')}</button>{/if}
            {#if i < draft.questions.length - 1}<button type="button" class="secondary" onclick={() => move(i, 1)}>{t('quiz.move_down')}</button>{/if}
            <button type="button" class="secondary" onclick={() => remove(i)}>{t('quiz.remove_question')}</button>
          </p>
        </fieldset>
      {:else}
        <p class="muted">{t('quiz.no_questions')}</p>
      {/each}
      <p class="actions-row">
        <button type="button" class="secondary" onclick={() => addQuestion('single')}>{t('quiz.add_single')}</button>
        <button type="button" class="secondary" onclick={() => addQuestion('multiple')}>{t('quiz.add_multiple')}</button>
        <button type="button" class="secondary" onclick={() => addQuestion('true_false')}>{t('quiz.add_true_false')}</button>
      </p>
    {:else if tab === 'settings' && draft}
      {@const st = draft.settings}
      <div class="stack quiz-settings">
        <label class="field">
          {t('quiz.max_attempts')}
          <input type="number" min="0" max="100" bind:value={st.max_attempts} oninput={() => (changed = true)} />
          <small class="muted">{t('quiz.max_attempts_hint')}</small>
        </label>
        <label class="field">
          {t('quiz.pass_percent')}
          <input type="number" min="1" max="100" bind:value={st.pass_percent} oninput={() => (changed = true)} />
        </label>
        <label class="field">
          {t('quiz.score_policy')}
          <select bind:value={st.score_policy} onchange={() => (changed = true)}>
            {#each ['best', 'last', 'first'] as const as v (v)}<option value={v}>{t(`quiz.score_${v}`)}</option>{/each}
          </select>
        </label>
        <label class="field">
          {t('quiz.cooldown')}
          <select bind:value={st.cooldown_min} onchange={() => (changed = true)}>
            {#each [0, 15, 60, 1440] as v (v)}<option value={v}>{t(`quiz.cooldown_${v as 0 | 15 | 60 | 1440}`)}</option>{/each}
          </select>
        </label>
        <label class="field">
          {t('quiz.estimated')}
          <input type="number" min="0" max="600" bind:value={st.estimated_min} oninput={() => (changed = true)} />
          <small class="muted">{t('quiz.estimated_hint', { n: String(autoMinutes) })}</small>
        </label>
        <label class="field">
          {t('quiz.time_limit_field')}
          <input
            type="number"
            min="0"
            max="1440"
            value={st.time_limit_s / 60}
            oninput={(e) => ((st.time_limit_s = Math.round(Number(e.currentTarget.value) * 60)), (changed = true))}
          />
          <small class="muted">{t('quiz.time_limit_hint')}</small>
        </label>
        <label class="check"><input type="checkbox" bind:checked={st.shuffle_questions} onchange={() => (changed = true)} /> {t('quiz.shuffle_questions')}</label>
        <label class="check"><input type="checkbox" bind:checked={st.shuffle_options} onchange={() => (changed = true)} /> {t('quiz.shuffle_options')}</label>
        <label class="field">
          {t('quiz.pick')}
          <input type="number" min="0" max={draft.questions.length} bind:value={st.pick} oninput={() => (changed = true)} />
          <small class="muted">{t('quiz.pick_hint', { total: String(draft.questions.length) })}</small>
        </label>
        <label class="field">
          {t('quiz.feedback')}
          <select bind:value={st.feedback} onchange={() => (changed = true)}>
            {#each ['score', 'wrong', 'all'] as const as v (v)}<option value={v}>{t(`quiz.feedback_${v}`)}</option>{/each}
          </select>
        </label>
        <label class="field">
          {t('quiz.reveal')}
          <select bind:value={st.reveal} onchange={() => (changed = true)} disabled={st.feedback !== 'all'}>
            {#each ['never', 'after_final', 'always'] as const as v (v)}<option value={v}>{t(`quiz.reveal_${v}`)}</option>{/each}
          </select>
        </label>
        <label class="field">
          {t('quiz.multiple_scoring')}
          <select bind:value={st.multiple_scoring} onchange={() => (changed = true)}>
            {#each ['all_or_nothing', 'partial'] as const as v (v)}<option value={v}>{t(`quiz.scoring_${v}`)}</option>{/each}
          </select>
        </label>
      </div>
    {:else if tab === 'results'}
      {#if results}
        <p class="actions-row">
          <a class="secondary" href={`/api${base}/quizzes/${id}/results?format=csv`} download>{t('reading.csv')}</a>
        </p>
        <h2>{t('quiz.by_question')}</h2>
        {#if results.questions.length}
          <table>
            <thead>
              <tr>
                <th scope="col">{t('quiz.question')}</th>
                <th scope="col">{t('quiz.right_rate')}</th>
                <th scope="col">{t('quiz.top_wrong')}</th>
              </tr>
            </thead>
            <tbody>
              {#each results.questions as q (q.id)}
                <tr>
                  <td>{q.prompt}</td>
                  <td>{t('quiz.rate_line', { percent: String(percent(q.correct, q.answered)), correct: String(q.correct), total: String(q.answered) })}</td>
                  <td>{#if q.top_wrong_count}{wrongLabel(q)} <small class="muted">({q.top_wrong_count})</small>{:else}—{/if}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        {:else}
          <p class="muted">{t('quiz.no_results')}</p>
        {/if}
        <h2>{t('quiz.by_person')}</h2>
        {#if results.people.length}
          <table>
            <thead>
              <tr>
                <th scope="col">{t('common.name')}</th>
                <th scope="col">{t('quiz.attempts_col')}</th>
                <th scope="col">{t('quiz.score_col')}</th>
                <th scope="col">{t('quiz.last_col')}</th>
              </tr>
            </thead>
            <tbody>
              {#each results.people as p (p.user_id)}
                <tr>
                  <td>
                    {p.name}
                    {#if quiz.manage && quiz.open_to_readers}
                      <br /><button type="button" class="link" onclick={() => ((extraFor = p), (extra = 1), (reason = ''), (extraOpen = true))}>{t('quiz.give_extra')}</button>
                    {/if}
                  </td>
                  <td>{p.attempts}{#if p.in_progress} · {t('quiz.in_progress')}{/if}</td>
                  <td class={p.passed ? 'read-status-confirmed' : ''}>{p.attempts ? `${formatPercent(p.percent, locale())} %` : '—'} {#if p.attempts}· {t(p.passed ? 'quiz.passed' : 'quiz.failed')}{/if}</td>
                  <td>{#if p.last_attempt_at}{formatDate(p.last_attempt_at, locale())}<br /><small class="muted">{t('quiz.duration', { min: String(Math.max(1, Math.round(p.last_duration_s / 60))) })}</small>{:else}—{/if}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        {:else}
          <p class="muted">{t('quiz.no_results')}</p>
        {/if}
      {/if}
    {:else if tab === 'preview'}
      {#if preview?.status === 'in_progress'}
        <QuizAttempt {area} attempt={preview} onfinished={(a) => (preview = a)} onclosed={() => (preview = null)} />
      {:else}
        {#if preview}<AttemptResult {area} slug={quiz.page.slug} attempt={preview} />{/if}
        <p class="muted">{t('quiz.preview_hint')}</p>
        <button type="button" class="primary" onclick={startPreview}>{t('quiz.start_preview')}</button>
      {/if}
    {/if}
  </div>

  {#if draft && (tab === 'questions' || tab === 'settings')}
    <p class="actions-row quiz-actions">
      <button type="button" class="secondary" disabled={busy || !changed} onclick={save}>{t('quiz.save_draft')}</button>
      <button type="button" class="primary" disabled={busy} onclick={publish}>{t(quiz.published_version ? 'quiz.publish_again' : 'quiz.publish')}</button>
      {#if quiz.status === 'published'}<button type="button" class="secondary" onclick={archive}>{t('quiz.archive')}</button>{/if}
      {#if !quiz.published_version}<button type="button" class="danger-btn" onclick={() => (confirmDelete = true)}>{t('quiz.delete')}</button>{/if}
    </p>
  {/if}

  <Dialog title={t('quiz.delete')} bind:open={confirmDelete}>
    <p>{t('quiz.delete_confirm')}</p>
    <button type="button" class="danger-btn" onclick={removeQuiz}>{t('quiz.delete')}</button>
  </Dialog>
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
{:else if error}
  <p class="error" role="alert">{error}</p>
{/if}
