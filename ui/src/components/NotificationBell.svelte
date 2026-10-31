<script lang="ts">
  import { api, notificationHref, timeAgo, type Notification } from '../lib/api';
  import { t, locale, type Key } from '../lib/i18n/index.svelte';
  import Dropdown from './Dropdown.svelte';
  import Icon from './Icon.svelte';

  // Campana de la barra superior (diseño aprobado, SPEC §15; avisos de §12.1). Se actualiza cada
  // minuto mientras la pestaña está a la vista, al volver a ella y al abrirla. El punto dice que
  // hay avisos sin leer; el texto accesible, cuántos.
  const POLL_MS = 60_000;
  let items = $state<Notification[]>([]);
  let unread = $state(0);

  async function load() {
    try {
      const r = await api<{ items: Notification[]; unread: number }>('GET', '/me/notifications');
      items = r.items;
      unread = r.unread;
    } catch {
      // Sin red o con la sesión vencida la campana queda como estaba: no es para interrumpir.
    }
  }

  $effect(() => {
    load();
    const timer = setInterval(() => document.visibilityState === 'visible' && load(), POLL_MS);
    addEventListener('focus', load);
    return () => {
      clearInterval(timer);
      removeEventListener('focus', load);
    };
  });

  // Varios guardados de una página, o varias propuestas nuevas, son un solo aviso con su cuenta; el
  // recordatorio de vigencia cuenta las páginas por revisar (§4.8).
  function text(n: Notification): string {
    const many = n.count > 1 && (n.kind === 'page_changed' || n.kind === 'proposal_pending' || n.kind === 'review_reminder');
    const key = `notif.kind.${n.kind}${many ? '_many' : ''}` as Key;
    return t(key, { actor: n.actor, title: n.title, count: String(n.count), days: String(n.days ?? ''), area: n.area.name || t('personal.title') });
  }

  async function read(ids: string[]) {
    try {
      await api('POST', '/me/notifications/read', { ids });
    } finally {
      await load();
    }
  }

  // Abrir un aviso lo marca como leído; la navegación sigue sola por el enlace.
  function open(n: Notification) {
    if (!n.read) void read([n.id]);
  }
</script>

<Dropdown label={unread ? t('notif.unread', { n: String(unread) }) : t('notif.title')} class="topbar-btn topbar-icon bell" onopen={load}>
  {#snippet trigger()}
    <Icon name="bell" size={20} />
    {#if unread}<span class="bell-dot" aria-hidden="true"></span>{/if}
  {/snippet}
  <section class="notif" aria-labelledby="notif-title">
    <header class="notif-head">
      <h2 id="notif-title">{t('notif.title')}</h2>
      {#if unread}<button type="button" class="link" onclick={() => read([])}>{t('notif.mark_all')}</button>{/if}
    </header>
    {#if items.length}
      <ul class="notif-list">
        {#each items as n (n.id)}
          <li class:unread={!n.read}>
            <a href={notificationHref(n)} onclick={() => open(n)}>
              <span class="notif-text">{text(n)}</span>
              <span class="notif-meta">{n.area.name || t('personal.title')} · {timeAgo(n.updated_at, locale())}</span>
            </a>
          </li>
        {/each}
      </ul>
    {:else}
      <p class="muted notif-empty">{t('notif.empty')}</p>
    {/if}
    <p class="notif-foot"><a href="/me/profile#notifications">{t('notif.prefs_link')}</a></p>
  </section>
</Dropdown>
