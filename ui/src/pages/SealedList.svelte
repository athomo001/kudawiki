<script lang="ts">
  import PageHead from '../components/PageHead.svelte';
  import { api, codeOf, formatDate, timeAgo, type AdminSealed } from '../lib/api';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';

  // Todos los sellados, para el superadmin (§6.1): no recorre las áreas, así que sin esta lista no
  // llegaría a un pedido de restablecimiento ni a lo que hay que desclasificar. Solo lo que se
  // guarda en claro; aprobar y desclasificar se hacen en el documento. Primero los pedidos.
  let { context }: { context: string } = $props();

  let list = $state<AdminSealed[] | null>(null);
  let error = $state('');

  $effect(() => {
    api<AdminSealed[]>('GET', '/admin/sealed')
      .then((r) => (list = r))
      .catch((e) => (error = errorText(codeOf(e))));
  });
</script>

<PageHead {context} title={t('sealed.list_title')} description={t('sealed.list_intro')} />
{#if error}<p class="error" role="alert">{error}</p>{/if}

{#if list && !list.length}
  <p class="muted">{t('sealed.list_none')}</p>
{:else if list}
  <table>
    <thead>
      <tr>
        <th scope="col">{t('sealed.col_path')}</th>
        <th scope="col">{t('audit.area')}</th>
        <th scope="col">{t('sealed.col_author')}</th>
        <th scope="col">{t('sealed.col_updated')}</th>
        <th scope="col">{t('sealed.col_state')}</th>
      </tr>
    </thead>
    <tbody>
      {#each list as s (s.id)}
        <tr>
          <td><a href={`/sealed/${s.id}`}>{s.slug}</a></td>
          <td>{s.area.personal ? t('sealed.personal_of', { name: s.author }) : s.area.name}</td>
          <td>{s.author}</td>
          <td><time datetime={s.updated_at} title={formatDate(s.updated_at, locale())}>{timeAgo(s.updated_at, locale())}</time></td>
          <td>{#if s.reset}<strong>{t(`sealed.state_${s.reset}`)}</strong>{:else}<span class="muted">—</span>{/if}</td>
        </tr>
      {/each}
    </tbody>
  </table>
{/if}
