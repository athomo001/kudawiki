<script lang="ts">
  import { api, canWrite, formatDate, formatPercent, timeAgo, type Level, type Me, type Proposal } from '../lib/api';
  import LevelBadge from '../components/LevelBadge.svelte';
  import { t, locale } from '../lib/i18n/index.svelte';

  // Inicio personal (§15, B5): lo que espera a la persona en todas sus áreas. Se abre desde el
  // menú de usuario; al entrar a la wiki se sigue cayendo en el área (§15, decidido 2026-10-29).
  let { me }: { me: Me } = $props();

  type AreaRef = { slug: string; name: string };
  type Watched = { area: AreaRef; slug: string; title: string; updated_by: string; updated_at: string; subtree: boolean };
  type OwnReview = { area: AreaRef; slug: string; title: string; level: Level; verified_at: string; due_at: string };
  type AreaProposal = Proposal & { area: AreaRef };
  type PendingRead = { id: string; area: AreaRef; page: { slug: string; title: string }; requested_by: string; due_at: string | null; quiz_id: string };
  type MyAttempt = { id: string; quiz: { id: string; title: string }; area: AreaRef; status: string; started_at: string; percent: number; passed: boolean };

  let toApprove = $state<AreaProposal[]>([]);
  let mine = $state<AreaProposal[]>([]);
  let reviews = $state<OwnReview[]>([]);
  let watching = $state<Watched[]>([]);
  let reads = $state<PendingRead[]>([]);
  let attempts = $state<MyAttempt[]>([]);
  let loaded = $state(false);

  // Las propuestas se piden a cada área, con las reglas de su bandeja (§5.2): las pendientes,
  // a las áreas donde se es admin; las propias, a las que se puede escribir. Un área que falla
  // no deja en blanco a las demás.
  async function proposals(view: 'pending' | 'mine', areas: AreaRef[]): Promise<AreaProposal[]> {
    const lists = await Promise.all(
      areas.map((a) =>
        api<Proposal[]>('GET', `/w/${a.slug}/proposals?view=${view}`)
          .then((ps) => ps.map((p) => ({ ...p, area: { slug: a.slug, name: a.name } })))
          .catch(() => [] as AreaProposal[]),
      ),
    );
    return lists.flat().sort((a, b) => b.created_at.localeCompare(a.created_at));
  }

  $effect(() => {
    loaded = false;
    const admin = me.workspaces.filter((a) => a.role === 'admin');
    Promise.all([
      proposals('pending', admin).then((ps) => (toApprove = ps)),
      // Las propias que siguen abiertas: las que esperan y las devueltas para corregir.
      proposals('mine', me.workspaces.filter(canWrite)).then((ps) => (mine = ps.filter((p) => p.status === 'pending' || p.status === 'rejected'))),
      api<OwnReview[]>('GET', '/me/review-queue').then((r) => (reviews = r)).catch(() => {}),
      api<PendingRead[]>('GET', '/me/read-requests').then((r) => (reads = r)).catch(() => {}),
      api<MyAttempt[]>('GET', '/me/quizzes').then((a) => (attempts = a)).catch(() => {}),
      api<Watched[]>('GET', '/me/watching').then((w) => (watching = w)).catch(() => {}),
    ]).finally(() => (loaded = true));
  });

  const ago = (iso: string) => timeAgo(iso, locale());
</script>

<h1>{t('home.greeting', { name: me.full_name })}</h1>
<p class="lead">{t('me.intro')} <a href={`/w/${me.personal.slug}`}>{t('personal.title')}</a></p>

{#if reads.length}
  <section class="area-section" aria-labelledby="me-reads">
    <h2 id="me-reads">{t('me.reads')}</h2>
    <ul class="me-list">
      {#each reads as r (r.id)}
        <li>
          {#if r.quiz_id}
            <a href={`/w/${r.area.slug}/quiz/${r.quiz_id}`}>{r.page.title}</a>
          {:else}
            <a href={`/w/${r.area.slug}/p/${r.page.slug}#read-confirm`}>{r.page.title}</a>
          {/if}
          <small class="muted">
            {r.area.name}{#if r.quiz_id} · {t('me.with_quiz')}{/if} · {t('me.read_by', { name: r.requested_by })}{#if r.due_at} · {t('reading.due_line', { date: formatDate(r.due_at, locale()) })}{/if}
          </small>
        </li>
      {/each}
    </ul>
  </section>
{/if}

{#if toApprove.length}
  <section class="area-section" aria-labelledby="me-approve">
    <h2 id="me-approve">{t('me.to_approve')}</h2>
    <ul class="me-list">
      {#each toApprove as p (p.id)}
        <li>
          <a href={`/w/${p.area.slug}/proposals/${p.id}`}>{p.title}</a>
          <small class="muted">{p.area.name} · {p.author} · <time datetime={p.created_at} title={formatDate(p.created_at, locale())}>{ago(p.created_at)}</time></small>
        </li>
      {/each}
    </ul>
  </section>
{/if}

{#if mine.length}
  <section class="area-section" aria-labelledby="me-mine">
    <h2 id="me-mine">{t('me.my_proposals')}</h2>
    <ul class="me-list">
      {#each mine as p (p.id)}
        <li>
          <a href={`/w/${p.area.slug}/proposals/${p.id}`}>{p.title}</a>
          <small class="muted">{p.area.name} · {t(`proposals.status_${p.status}`)} · <time datetime={p.created_at}>{ago(p.created_at)}</time></small>
        </li>
      {/each}
    </ul>
  </section>
{/if}

{#if reviews.length}
  <section class="area-section" aria-labelledby="me-review">
    <h2 id="me-review">{t('review.queue_mine')}</h2>
    <ul class="me-list">
      {#each reviews as r (r.area.slug + r.slug)}
        <li>
          <a href={`/w/${r.area.slug}/p/${r.slug}`}>{r.title}</a> <LevelBadge level={r.level} />
          <small class="muted">{r.area.name} · {t('me.overdue_since', { ago: ago(r.due_at) })}</small>
        </li>
      {/each}
    </ul>
  </section>
{/if}

{#if attempts.length}
  <section class="area-section" aria-labelledby="me-quizzes">
    <h2 id="me-quizzes">{t('me.quizzes')}</h2>
    <ul class="me-list">
      {#each attempts.slice(0, 10) as a (a.id)}
        <li>
          <a href={`/w/${a.area.slug}/quiz/${a.quiz.id}`}>{a.quiz.title}</a>
          <small class="muted">
            {a.area.name} · {a.status === 'in_progress' ? t('quiz.in_progress') : `${formatPercent(a.percent, locale())} % · ${t(a.passed ? 'quiz.passed' : 'quiz.failed')}`}
            · <time datetime={a.started_at}>{ago(a.started_at)}</time>
          </small>
        </li>
      {/each}
    </ul>
  </section>
{/if}

<section class="area-section" aria-labelledby="me-watching">
  <h2 id="me-watching">{t('me.watching')}</h2>
  {#if watching.length}
    <ul class="me-list">
      {#each watching as w (w.area.slug + w.slug)}
        <li>
          <a href={`/w/${w.area.slug}/p/${w.slug}`}>{w.title}</a>
          <small class="muted">
            {w.area.name} · {t('page.edited_by', { name: w.updated_by })}
            <time datetime={w.updated_at} title={formatDate(w.updated_at, locale())}>{ago(w.updated_at)}</time>
          </small>
        </li>
      {/each}
    </ul>
  {:else if loaded}
    <p class="muted">{t('me.watching_empty')}</p>
  {/if}
</section>

{#if loaded && !reads.length && !toApprove.length && !mine.length && !reviews.length}
  <p class="muted">{t('me.nothing_pending')}</p>
{/if}
