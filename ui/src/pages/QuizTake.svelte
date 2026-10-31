<script lang="ts">
  import { api, codeOf, formatDate, formatPercent, timeAgo, type Area, type AttemptView, type Quiz } from '../lib/api';
  import QuizAttempt from '../components/QuizAttempt.svelte';
  import AttemptResult from '../components/AttemptResult.svelte';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';

  // Hacer un cuestionario (§4.16.4): antes de empezar, el resumen y para qué sirven los resultados
  // (§13.10); después, el intento y su corrección; abajo, el historial propio (§4.16.5).
  let { area, id }: { area: Area; id: string } = $props();

  const base = $derived(`/w/${area.slug}`);
  let quiz = $state<Quiz | null>(null);
  let history = $state<AttemptView[]>([]);
  let attempt = $state<AttemptView | null>(null);
  let last = $state<AttemptView | null>(null);
  let error = $state('');
  let loaded = $state(false);

  async function load() {
    error = '';
    try {
      [quiz, history] = await Promise.all([
        api<Quiz>('GET', `${base}/quizzes/${id}`),
        api<AttemptView[]>('GET', `${base}/quizzes/${id}/my-attempts`),
      ]);
    } catch (e) {
      error = errorText(codeOf(e));
    } finally {
      loaded = true;
    }
  }

  $effect(() => {
    void id;
    attempt = last = null;
    load();
  });

  async function start() {
    error = '';
    try {
      attempt = await api<AttemptView>('POST', `${base}/quizzes/${id}/attempts`);
      last = null;
    } catch (e) {
      error = errorText(codeOf(e));
      await load();
    }
  }

  async function finished(a: AttemptView) {
    attempt = null;
    last = a;
    await load();
  }

  // Se acabó el tiempo en otra pestaña: el resultado es el último del historial.
  async function closed() {
    attempt = null;
    await load();
    last = history[0] ?? null;
  }

  const s = $derived(quiz?.summary ?? null);
  const canStart = $derived(!!s && (s.remaining !== 0 || !!s.in_progress) && !s.next_at);
  const attemptsLine = $derived(
    !s ? '' : s.max_attempts === 0 ? t('quiz.attempts_unlimited') : t('quiz.attempts_n', { n: String(s.max_attempts) }),
  );
</script>

{#if quiz}
  <h1>{quiz.title}</h1>
  <p class="muted">
    {t('quiz.about_page')} <a href={`${base}/p/${quiz.page.slug}`}>{quiz.page.title}</a>
    {#if quiz.manage}· <a href={`${base}/quizzes/${quiz.id}`}>{t('quiz.manage')}</a>{/if}
  </p>
  {#if attempt}
    <QuizAttempt {area} {attempt} onfinished={finished} onclosed={closed} />
  {:else}
    {#if last}<AttemptResult {area} slug={quiz.page.slug} attempt={last} />{/if}
    {#if s}
      <section class="quiz-summary" aria-label={t('quiz.summary')}>
        <p class="lead">
          {t('quiz.summary_line', { n: String(s.questions), min: String(s.estimated_min), attempts: attemptsLine, pass: String(s.pass_percent) })}
          {#if s.time_limit_s}<br />{t('quiz.time_limit', { min: String(Math.round(s.time_limit_s / 60)) })}{/if}
          {#if s.due_at}<br />{t('quiz.due', { date: formatDate(s.due_at, locale()) })}{/if}
        </p>
        <p class="muted">{t('quiz.privacy')}</p>
        {#if s.passed}<p class="quiz-passed">{t('quiz.already_passed')}</p>{/if}
        {#if s.remaining > 0}<p>{t('quiz.remaining', { n: String(s.remaining) })}</p>{/if}
        {#if s.remaining === 0 && !s.in_progress && !s.passed}<p class="quiz-failed">{t('quiz.no_attempts')}</p>{/if}
        {#if s.next_at}<p>{t('quiz.cooldown_until', { ago: timeAgo(s.next_at, locale()) })}</p>{/if}
        {#if error}<p class="error" role="alert">{error}</p>{/if}
        {#if canStart}
          <button type="button" class="primary" onclick={start}>{t(s.in_progress ? 'quiz.resume' : history.length ? 'quiz.retry' : 'quiz.start')}</button>
        {/if}
      </section>
    {:else if error}
      <p class="error" role="alert">{error}</p>
    {/if}
    {#if history.length}
      <section class="area-section" aria-labelledby="quiz-history">
        <h2 id="quiz-history">{t('quiz.history')}</h2>
        <ul class="quiz-history">
          {#each history as h (h.id)}
            <li>
              <details>
                <summary>
                  {t('quiz.history_line', { n: String(h.number), percent: formatPercent(h.result?.percent ?? 0, locale()) })}
                  · {t(h.result?.passed ? 'quiz.passed' : 'quiz.failed')}
                  · <span class="muted">{formatDate(h.started_at, locale())}</span>
                </summary>
                <AttemptResult {area} slug={quiz.page.slug} attempt={h} />
              </details>
            </li>
          {/each}
        </ul>
      </section>
    {/if}
  {/if}
{:else if loaded && error}
  <p class="error" role="alert">{error}</p>
{/if}
