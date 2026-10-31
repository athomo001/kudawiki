<script lang="ts">
  import { api, codeOf, type Area, type Page, type QuizListed } from '../lib/api';
  import Dialog from './Dialog.svelte';
  import { t, errorText } from '../lib/i18n/index.svelte';
  import { navigate } from '../lib/router.svelte';

  // Los cuestionarios de la página (§4.16.1), al final, después de la confirmación de lectura: los
  // que quien lee puede hacer, con su resumen, y los que administra. creating abre el diálogo para
  // crear uno (desde "Más acciones").
  let { area, page, creating = $bindable(false) }: { area: Area; page: Page; creating?: boolean } = $props();

  const base = $derived(`/w/${area.slug}`);
  let list = $state<QuizListed[]>([]);
  let title = $state('');
  let error = $state('');

  $effect(() => {
    void page.slug;
    void page.revision;
    api<QuizListed[]>('GET', `${base}/quizzes?page=${encodeURIComponent(page.slug)}`)
      .then((l) => (list = l))
      .catch(() => (list = []));
  });

  $effect(() => {
    if (creating) {
      title = '';
      error = '';
    }
  });

  async function create(e: SubmitEvent) {
    e.preventDefault();
    error = '';
    try {
      const { id } = await api<{ id: string }>('POST', `${base}/quizzes`, { page: page.slug, title });
      creating = false;
      navigate(`${base}/quizzes/${id}`);
    } catch (err) {
      error = errorText(codeOf(err));
    }
  }
</script>

{#if list.length}
  <section class="quiz-section" aria-labelledby="quiz-section-title">
    <h2 id="quiz-section-title">{t('quiz.section')}</h2>
    <ul class="me-list">
      {#each list as q (q.id)}
        <li>
          {#if q.summary}
            <a href={`${base}/quiz/${q.id}`}>{q.title}</a>
            <small class="muted">
              {t('quiz.summary_short', { n: String(q.summary.questions), min: String(q.summary.estimated_min), pass: String(q.summary.pass_percent) })}
              {#if q.summary.passed}· {t('quiz.passed')}{/if}
            </small>
          {:else}
            <span>{q.title}</span> <small class="muted">· {t(`quiz.status_${q.status}`)}</small>
          {/if}
          {#if q.manage}<small><a href={`${base}/quizzes/${q.id}`}>{t('quiz.manage')}</a></small>{/if}
        </li>
      {/each}
    </ul>
  </section>
{/if}

<Dialog title={t('quiz.new')} bind:open={creating}>
  <form class="stack" onsubmit={create}>
    <label class="field">
      {t('quiz.title_field')}
      <input type="text" maxlength="200" required bind:value={title} />
    </label>
    <small class="muted">{t('quiz.new_hint')}</small>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <button type="submit" class="primary">{t('quiz.create')}</button>
  </form>
</Dialog>
