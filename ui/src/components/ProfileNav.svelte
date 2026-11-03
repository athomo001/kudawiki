<script lang="ts">
  import { route } from '../lib/router.svelte';
  import { t, type Key } from '../lib/i18n/index.svelte';
  import type { Me } from '../lib/api';
  import Icon, { type IconName } from './Icon.svelte';
  import Avatar from './Avatar.svelte';

  // Menú de Mi perfil (SPEC §15, diseño "Kudawiki — Mi perfil"): el mismo molde que la
  // administración, con la foto y el nombre arriba. pending marca lo que falta configurar.
  let { me, avatarRev }: { me: Me; avatarRev: number } = $props();

  type Item = { href: string; label: Key; icon: IconName; pending?: boolean };
  const groups = $derived<{ label: Key; items: Item[] }[]>([
    {
      label: 'profile.group_account',
      items: [
        { href: '/me/profile', label: 'profile.nav_account', icon: 'people' },
        { href: '/me/profile/preferences', label: 'profile.reading', icon: 'settings' },
        { href: '/me/profile/notifications', label: 'notif.prefs_title', icon: 'bell' },
      ],
    },
    {
      label: 'profile.group_security',
      items: [
        { href: '/me/profile/security', label: 'profile.nav_security', icon: 'lock' },
        { href: '/me/profile/sessions', label: 'sessions.title', icon: 'server' },
        { href: '/me/profile/vault', label: 'vault.title', icon: 'key', pending: !me.vault },
      ],
    },
  ]);
</script>

<nav class="sidebar admin-nav" aria-label={t('profile.title')}>
  <div class="admin-nav-head profile-nav-head">
    <Avatar {me} rev={avatarRev} />
    <div>
      <span class="sidebar-kicker">{t('profile.title')}</span>
      <span class="sidebar-title">{me.full_name}</span>
    </div>
  </div>
  <a class="admin-nav-back profile-nav-back" href="/"><Icon name="back" size={14} /> {t('admin.back_to_wiki')}</a>
  {#each groups as g (g.label)}
    <h2 class="admin-nav-group">{t(g.label)}</h2>
    <ul class="admin-nav-list">
      {#each g.items as i (i.href)}
        <li>
          <a class="nav-item" href={i.href} aria-current={route.path === i.href ? 'page' : undefined}>
            <Icon name={i.icon} size={16} />
            {t(i.label)}
            {#if i.pending}<span class="nav-dot" title={t('profile.pending')}><span class="sr-only">{t('profile.pending')}</span></span>{/if}
          </a>
        </li>
      {/each}
    </ul>
  {/each}
</nav>
