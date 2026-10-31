<script lang="ts">
  import { formatDate, formatPercent, type Area, type AttemptView } from '../lib/api';
  import { t, locale } from '../lib/i18n/index.svelte';

  // La corrección de un intento (§4.16.5), con lo que el cuestionario deja ver: solo el puntaje, qué
  // preguntas falló (con el enlace a la sección que las responde) o todo con las respuestas.
  let { area, slug, attempt }: { area: Area; slug: string; attempt: AttemptView } = $props();

  const r = $derived(attempt.result!);
  const minutes = (s: number) => Math.max(1, Math.round(s / 60));
  const optionText = (q: AttemptView['questions'][number], id: string) =>
    q.kind === 'true_false' ? t(id === 'true' ? 'quiz.true' : 'quiz.false') : (q.options.find((o) => o.id === id)?.text ?? '');
</script>

<div class="quiz-result">
  <p class={r.passed ? 'quiz-passed' : 'quiz-failed'}>
    <strong>{t(r.passed ? 'quiz.result_passed' : 'quiz.result_failed', { percent: formatPercent(r.percent, locale()), pass: String(attempt.pass_percent) })}</strong>
  </p>
  <p class="muted">
    {t('quiz.result_line', { score: String(Math.round(r.score * 10) / 10), max: String(r.max), min: String(minutes(r.duration_s)) })}
    · {formatDate(r.submitted_at, locale())}
    {#if r.timed_out}· {t('quiz.timed_out')}{/if}
    {#if attempt.preview}· {t('quiz.preview_note')}{/if}
  </p>
  {#if attempt.questions.length}
    <ol class="quiz-review">
      {#each attempt.questions as q (q.id)}
        <li class:quiz-wrong={!q.correct}>
          <span class="quiz-mark" aria-label={t(q.correct ? 'quiz.correct' : 'quiz.incorrect')}>{q.correct ? '✓' : '✗'}</span>
          <div class="prose quiz-prompt">{@html q.prompt_html}</div>
          {#if (attempt.answers[q.id] ?? []).length}
            <small class="muted">{t('quiz.your_answer', { answer: (attempt.answers[q.id] ?? []).map((id) => optionText(q, id)).join(', ') })}</small>
          {/if}
          {#if q.correct_options?.length}
            <small>{t('quiz.right_answer', { answer: q.correct_options.map((id) => optionText(q, id)).join(', ') })}</small>
          {/if}
          {#if q.explanation_html}<div class="prose quiz-explanation">{@html q.explanation_html}</div>{/if}
          {#if q.anchor}<a href={`/w/${area.slug}/p/${slug}#${q.anchor}`}>{t('quiz.review_section')}</a>{/if}
        </li>
      {/each}
    </ol>
  {/if}
</div>
