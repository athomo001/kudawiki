<script lang="ts">
  import { untrack } from 'svelte';
  import PageHead from '../components/PageHead.svelte';
  import { api, codeOf, formatDate, type Role } from '../lib/api';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';

  // Revisión periódica de accesos (SPEC §21.2.7). El admin del área confirma sus miembros y
  // roles; el superadmin, quién administra (superadmins y admins de cada área) y ve cómo va la
  // revisión de cada área. Lo que no corresponde se corrige en Miembros o en Personas: confirmar
  // es dejar constancia de que la lista, tal como está, es la correcta.
  let { endpoint, context, fixHref, global = false }: { endpoint: string; context: string; fixHref: string; global?: boolean } =
    $props();

  type Last = { at: string; by: string } | null;
  type Status = { due_at: string; overdue: boolean; last: Last };
  type Member = { user_id: string; email: string; full_name: string; role: Role; ldap_group: string };
  type Admin = { user_id: string; email: string; full_name: string; superadmin: boolean; active: boolean; areas: string[] };
  type AreaRow = Status & { slug: string; name: string };
  type Review = Status & { members?: Member[]; admins?: Admin[]; areas?: AreaRow[] };

  let review = $state<Review | null>(null);
  let busy = $state(false);
  let error = $state('');
  let notice = $state('');

  const day = (iso: string) => formatDate(iso, locale());

  function load() {
    api<Review>('GET', endpoint)
      .then((r) => (review = r))
      .catch((e) => (error = errorText(codeOf(e))));
  }

  $effect(() => {
    void endpoint;
    untrack(() => {
      review = null;
      load();
    });
  });

  async function confirm() {
    busy = true;
    error = '';
    notice = '';
    try {
      await api('POST', endpoint);
      notice = t('access_review.confirmed');
      load();
    } catch (e) {
      error = errorText(codeOf(e));
    } finally {
      busy = false;
    }
  }
</script>

<PageHead {context} title={t('access_review.title')} description={t(global ? 'access_review.intro_global' : 'access_review.intro')} />
{#if error}<p class="error" role="alert">{error}</p>{/if}

{#if review}
  {@render status(review)}
  {#if review.overdue}<p class="callout callout-warning" role="status">{t('access_review.overdue')}</p>{/if}

  {#if review.members}
    <table>
      <thead>
        <tr>
          <th scope="col">{t('common.name')}</th>
          <th scope="col">{t('auth.email')}</th>
          <th scope="col">{t('members.role')}</th>
        </tr>
      </thead>
      <tbody>
        {#each review.members as m (m.user_id)}
          <tr>
            <td>{m.full_name}</td>
            <td>{m.email}</td>
            <td>{t(`role.${m.role}`)}{#if m.ldap_group}<small class="muted managed">{t('access_review.from_group', { group: m.ldap_group })}</small>{/if}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}

  {#if review.admins}
    <table>
      <thead>
        <tr>
          <th scope="col">{t('common.name')}</th>
          <th scope="col">{t('auth.email')}</th>
          <th scope="col">{t('access_review.administers')}</th>
          <th scope="col">{t('admin.status')}</th>
        </tr>
      </thead>
      <tbody>
        {#each review.admins as a (a.user_id)}
          <tr>
            <td>{a.full_name}</td>
            <td>{a.email}</td>
            <td>
              {#if a.superadmin}<strong>{t('admin.superadmin')}</strong>{#if a.areas.length}<br />{/if}{/if}
              {a.areas.join(', ')}
            </td>
            <td>{#if a.active}{t('admin.status_active')}{:else}<span class="sev-error">{t('access_review.inactive')}</span>{/if}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}

  <p class="form-actions">
    <button type="button" class="primary" disabled={busy} onclick={confirm}>{t('access_review.confirm')}</button>
    <a href={fixHref}>{t(global ? 'access_review.fix_people' : 'access_review.fix_members')}</a>
  </p>
  {#if notice}<p class="notice" role="status">{notice}</p>{/if}

  {#if review.areas?.length}
    <h2>{t('access_review.areas')}</h2>
    <table>
      <thead>
        <tr>
          <th scope="col">{t('audit.area')}</th>
          <th scope="col">{t('access_review.last')}</th>
          <th scope="col">{t('access_review.due')}</th>
        </tr>
      </thead>
      <tbody>
        {#each review.areas as ar (ar.slug)}
          <tr>
            <td>{ar.name}</td>
            <td>{#if ar.last}{t('access_review.by', { date: day(ar.last.at), name: ar.last.by })}{:else}<span class="muted">{t('access_review.never')}</span>{/if}</td>
            <td>{#if ar.overdue}<span class="sev-error">{t('access_review.overdue_since', { date: day(ar.due_at) })}</span>{:else}{day(ar.due_at)}{/if}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
{/if}

{#snippet status(s: Status)}
  <dl class="facts">
    <dt>{t('access_review.last')}</dt>
    <dd>{#if s.last}{t('access_review.by', { date: day(s.last.at), name: s.last.by })}{:else}<span class="muted">{t('access_review.never')}</span>{/if}</dd>
    <dt>{t('access_review.due')}</dt>
    <dd>{day(s.due_at)}</dd>
  </dl>
{/snippet}
