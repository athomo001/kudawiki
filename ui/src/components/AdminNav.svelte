<script lang="ts">
  import { route } from '../lib/router.svelte';
  import { t, type Key } from '../lib/i18n/index.svelte';
  import Icon, { type IconName } from './Icon.svelte';

  // Menú de la administración (SPEC §15, diseño "Kudawiki — Administración"): ocupa el lugar del
  // árbol, con los ajustes agrupados como en la administración de Wiki.js.
  type Item = { href: string; label: Key; icon: IconName; also?: string[] };
  const groups: { label: Key; items: Item[] }[] = [
    {
      label: 'admin.group_access',
      items: [
        { href: '/admin/users', label: 'admin.people', icon: 'people', also: ['/admin'] },
        { href: '/admin/areas', label: 'admin.areas', icon: 'grid' },
        { href: '/admin/ldap', label: 'ldap.title', icon: 'server' },
      ],
    },
    {
      label: 'admin.group_site',
      items: [
        { href: '/admin/appearance', label: 'appearance.title', icon: 'palette' },
        { href: '/admin/email', label: 'smtp.title', icon: 'mail' },
        { href: '/admin/templates', label: 'templates.global_title', icon: 'template' },
      ],
    },
    {
      label: 'admin.group_supervision',
      items: [
        { href: '/admin/health', label: 'health.short', icon: 'pulse' },
        { href: '/admin/sealed', label: 'sealed.list_title', icon: 'lock' },
        { href: '/admin/audit', label: 'audit.title', icon: 'list' },
      ],
    },
  ];

  // Las subpáginas (la ficha de un área, las pestañas del directorio) marcan su entrada.
  const current = (i: Item) =>
    route.path === i.href || route.path.startsWith(i.href + '/') || (i.also?.includes(route.path) ?? false);
</script>

<nav class="sidebar admin-nav" aria-label={t('nav.admin')}>
  <div class="admin-nav-head">
    <span class="sidebar-title">{t('nav.admin')}</span>
    <a class="admin-nav-back" href="/"><Icon name="back" size={14} /> {t('admin.back_to_wiki')}</a>
  </div>
  {#each groups as g (g.label)}
    <h2 class="admin-nav-group">{t(g.label)}</h2>
    <ul class="admin-nav-list">
      {#each g.items as i (i.href)}
        <li>
          <a class="nav-item" href={i.href} aria-current={current(i) ? 'page' : undefined}><Icon name={i.icon} size={16} /> {t(i.label)}</a>
        </li>
      {/each}
    </ul>
  {/each}
</nav>
