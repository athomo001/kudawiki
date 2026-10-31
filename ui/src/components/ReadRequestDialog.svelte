<script lang="ts">
  import { api, codeOf, type Area, type Audience, type QuizListed, type Role } from '../lib/api';
  import Dialog from './Dialog.svelte';
  import { t, errorText } from '../lib/i18n/index.svelte';
  import { navigate } from '../lib/router.svelte';

  // Pedir que se lea una página (§4.14). Siempre de arriba hacia abajo: el admin a todo el área, a
  // unos roles o a unas personas; un editor solo a los lectores (todos o algunos). El servidor
  // vuelve a revisarlo todo. again: la solicitud que se repite, para volver a confirmar una
  // versión nueva de la página con la misma audiencia. Se puede pedir con un cuestionario de la
  // página que quien pide administra: la lectura se cumple al aprobarlo (§4.16).
  let {
    area,
    slug,
    open = $bindable(false),
    again,
  }: { area: Area; slug: string; open: boolean; again?: { audience: Audience; statement: string; quiz_id: string } } = $props();

  type Candidate = { user_id: string; name: string; role: Role };
  const isAdmin = $derived(area.role === 'admin');
  let mode = $state<'all' | 'roles' | 'users'>('all');
  let roles = $state<Role[]>(['viewer']);
  let users = $state<string[]>([]);
  let candidates = $state<Candidate[]>([]);
  let quizzes = $state<QuizListed[]>([]);
  let quiz = $state('');
  let statement = $state('');
  let due = $state('');
  let error = $state('');
  let busy = $state(false);
  const today = new Date().toISOString().slice(0, 10);

  $effect(() => {
    if (!open) return;
    error = '';
    statement = again?.statement ?? '';
    due = '';
    const a = again?.audience;
    mode = a?.users?.length ? 'users' : a?.roles?.length && isAdmin ? 'roles' : 'all';
    roles = a?.roles?.length ? [...a.roles] : ['viewer'];
    users = a?.users ? [...a.users] : [];
    quiz = again?.quiz_id ?? '';
    api<QuizListed[]>('GET', `/w/${area.slug}/quizzes?page=${encodeURIComponent(slug)}`)
      .then((l) => (quizzes = l.filter((q) => q.manage && q.status === 'published')))
      .catch(() => (quizzes = []));
    api<Candidate[]>('GET', `/w/${area.slug}/read-requests/candidates?page=${encodeURIComponent(slug)}`)
      .then((c) => (candidates = c))
      .catch((e) => (error = errorText(codeOf(e))));
  });

  function toggle<T>(list: T[], v: T): T[] {
    return list.includes(v) ? list.filter((x) => x !== v) : [...list, v];
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = '';
    const audience: Audience = mode === 'all' ? { all: true } : mode === 'roles' ? { roles } : { users };
    // El plazo es hasta el final del día elegido, en la hora de quien pide.
    const body: Record<string, unknown> = { page: slug, statement, audience, quiz_id: quiz };
    if (due) body.due_at = new Date(`${due}T23:59:59`).toISOString();
    try {
      const { id } = await api<{ id: string }>('POST', `/w/${area.slug}/read-requests`, body);
      open = false;
      navigate(`/w/${area.slug}/reads/${id}`);
    } catch (err) {
      error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }
</script>

<Dialog title={t('reading.ask_title')} bind:open>
  <form class="stack" onsubmit={submit}>
    <fieldset class="audience">
      <legend>{t('reading.audience')}</legend>
      <label><input type="radio" bind:group={mode} value="all" /> {t(isAdmin ? 'reading.audience_all' : 'reading.audience_viewers')}</label>
      {#if isAdmin}
        <label><input type="radio" bind:group={mode} value="roles" /> {t('reading.audience_roles')}</label>
        {#if mode === 'roles'}
          <div class="indent">
            {#each ['viewer', 'editor', 'admin'] as const as r (r)}
              <label><input type="checkbox" checked={roles.includes(r)} onchange={() => (roles = toggle(roles, r))} /> {t(`role.${r}`)}</label>
            {/each}
          </div>
        {/if}
      {/if}
      <label><input type="radio" bind:group={mode} value="users" /> {t('reading.audience_users')}</label>
      {#if mode === 'users'}
        <div class="indent pick-list">
          {#each candidates as c (c.user_id)}
            <label><input type="checkbox" checked={users.includes(c.user_id)} onchange={() => (users = toggle(users, c.user_id))} /> {c.name} <span class="muted">· {t(`role.${c.role}`)}</span></label>
          {:else}
            <p class="muted">{t('reading.no_candidates')}</p>
          {/each}
        </div>
      {/if}
    </fieldset>
    <label class="field">
      {t('reading.statement')}
      <textarea rows="2" maxlength="500" bind:value={statement} placeholder={t('reading.statement_default')}></textarea>
    </label>
    {#if quizzes.length}
      <label class="field">
        {t('reading.quiz')}
        <select bind:value={quiz}>
          <option value="">{t('reading.quiz_none')}</option>
          {#each quizzes as q (q.id)}<option value={q.id}>{q.title}</option>{/each}
        </select>
      </label>
    {/if}
    <label class="field">
      {t('reading.due')}
      <input type="date" min={today} bind:value={due} />
    </label>
    <small class="muted">{t('reading.ask_hint')}</small>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <button type="submit" class="primary" disabled={busy || (mode === 'users' && !users.length) || (mode === 'roles' && !roles.length)}>{t('reading.ask_submit')}</button>
  </form>
</Dialog>
