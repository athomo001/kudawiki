<script lang="ts">
  import { api, codeOf, type Area, type AttemptView } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';

  // Un intento en curso (§4.16.4): las respuestas se guardan solas mientras se responde, así se
  // retoma donde iba si se corta la conexión. El reloj es solo para mostrar: el tiempo lo controla
  // el servidor, que corrige con lo guardado si se pasa (§4.16.7). onclosed: el servidor ya lo
  // cerró (se acabó el tiempo en otra pestaña) y hay que ir a buscar el resultado.
  let {
    area,
    attempt,
    onfinished,
    onclosed,
  }: { area: Area; attempt: AttemptView; onfinished: (a: AttemptView) => void; onclosed: () => void } = $props();

  const SAVE_MS = 800;
  const url = $derived(`/w/${area.slug}/attempts/${attempt.id}`);
  let answers = $state<Record<string, string[]>>({});
  let saved = $state(true);
  let busy = $state(false);
  let error = $state('');
  let left = $state<number | null>(null);
  let timer: ReturnType<typeof setTimeout> | undefined;

  $effect(() => {
    answers = $state.snapshot(attempt.answers);
  });

  $effect(() => {
    if (!attempt.deadline_at) return;
    const end = new Date(attempt.deadline_at).getTime();
    let sent = false;
    // Al llegar a cero se envía una vez; el servidor corrige con lo guardado hasta el límite.
    const tickClock = () => {
      left = Math.max(0, Math.round((end - Date.now()) / 1000));
      if (left === 0 && !sent) {
        sent = true;
        void submit();
      }
    };
    tickClock();
    const id = setInterval(tickClock, 1000);
    return () => clearInterval(id);
  });

  $effect(() => () => clearTimeout(timer));

  function choose(q: string, option: string, multiple: boolean) {
    const now = answers[q] ?? [];
    answers[q] = multiple ? (now.includes(option) ? now.filter((o) => o !== option) : [...now, option]) : [option];
    saved = false;
    clearTimeout(timer);
    timer = setTimeout(save, SAVE_MS);
  }

  async function save() {
    try {
      await api('PUT', url, { answers: $state.snapshot(answers) });
      saved = true;
    } catch (e) {
      if (codeOf(e) === 'attempt_closed') onclosed();
      else error = errorText(codeOf(e));
    }
  }

  async function submit(e?: SubmitEvent) {
    e?.preventDefault();
    if (busy) return;
    busy = true;
    error = '';
    clearTimeout(timer);
    try {
      onfinished(await api<AttemptView>('POST', `${url}/submit`, { answers: $state.snapshot(answers) }));
    } catch (err) {
      if (codeOf(err) === 'attempt_closed') onclosed();
      else error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }

  const clock = (s: number) => `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
  const answered = $derived(attempt.questions.filter((q) => answers[q.id]?.length).length);
</script>

<form class="quiz-attempt" onsubmit={submit}>
  <p class="quiz-status" aria-live="polite">
    {t('quiz.answered', { n: String(answered), total: String(attempt.questions.length) })}
    · <span class="muted">{t(saved ? 'quiz.saved' : 'quiz.saving')}</span>
    {#if left !== null}· <strong class:quiz-late={left < 60}>{t('quiz.time_left', { time: clock(left) })}</strong>{/if}
  </p>
  {#each attempt.questions as q, i (q.id)}
    <fieldset class="quiz-question">
      <legend>{t('quiz.question_n', { n: String(i + 1) })}{#if q.points > 1} · {t('quiz.points', { n: String(q.points) })}{/if}</legend>
      <!-- HTML saneado por bluemonday en el servidor, como el de la página (SPEC §4.1). -->
      <div class="prose quiz-prompt">{@html q.prompt_html}</div>
      {#if q.kind === 'multiple'}<small class="muted">{t('quiz.pick_all')}</small>{/if}
      {#each q.options as o (o.id)}
        <label class="quiz-option">
          <input
            type={q.kind === 'multiple' ? 'checkbox' : 'radio'}
            name={q.id}
            checked={answers[q.id]?.includes(o.id) ?? false}
            onchange={() => choose(q.id, o.id, q.kind === 'multiple')}
          />
          {q.kind === 'true_false' ? t(o.id === 'true' ? 'quiz.true' : 'quiz.false') : o.text}
        </label>
      {/each}
    </fieldset>
  {/each}
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  <button type="submit" class="primary" disabled={busy}>{t(attempt.preview ? 'quiz.finish_preview' : 'quiz.submit')}</button>
</form>
