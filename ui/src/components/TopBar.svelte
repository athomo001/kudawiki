<script lang="ts">
  import type { Area, Me, ReadingPrefs } from '../lib/api';
  import { t, locale } from '../lib/i18n/index.svelte';
  import Avatar from './Avatar.svelte';
  import Brand from './Brand.svelte';
  import Icon from './Icon.svelte';
  import Dropdown from './Dropdown.svelte';
  import SearchBox from './SearchBox.svelte';
  import ReadingPrefsForm from './ReadingPrefsForm.svelte';
  import NotificationBell from './NotificationBell.svelte';

  // Barra superior del diseño aprobado (SPEC §15): logo, área, búsqueda, idioma, lectura,
  // notificaciones (§12.1) y usuario.
  let {
    me,
    area,
    avatarRev,
    onprefs,
    onlogout,
  }: {
    me: Me;
    area?: Area;
    avatarRev: number;
    onprefs: (change: { locale?: 'es' | 'en'; reading_prefs?: ReadingPrefs }) => void;
    onlogout: () => void;
  } = $props();
</script>

<header class="topbar">
  <a class="brand" href="/"><Brand onBar /></a>

  <!-- Con una sola área no hay nada que elegir (SPEC §15). -->
  {#if me.workspaces.length > 1}
    <Dropdown label={t('nav.areas_menu')} class="topbar-btn area-switch" align="start">
      {#snippet trigger()}
        <span class="topbar-muted">{t('nav.area')}</span>
        {area?.name ?? t('nav.pick_area')}
        <Icon name="chevron" size={14} />
      {/snippet}
      <ul class="menu">
        {#each me.workspaces as w (w.slug)}
          <li><a href={`/w/${w.slug}`} aria-current={w.slug === area?.slug ? 'true' : undefined}>{w.name}</a></li>
        {/each}
      </ul>
    </Dropdown>
  {/if}

  <div class="topbar-search">
    {#if area}{#key area.slug}<SearchBox {area} />{/key}{/if}
  </div>

  <div class="topbar-tools">
    <div class="lang-switch" role="group" aria-label={t('lang.ui')}>
      {#each ['es', 'en'] as const as l (l)}
        <button type="button" lang={l} aria-pressed={locale() === l} onclick={() => onprefs({ locale: l })}>{l.toUpperCase()}</button>
      {/each}
    </div>

    <Dropdown label={t('prefs.title')} class="topbar-btn topbar-icon">
      {#snippet trigger()}Aa{/snippet}
      <section class="prefs" aria-label={t('prefs.title')}>
        <h2>{t('prefs.title')}</h2>
        <ReadingPrefsForm prefs={me.reading_prefs} onchange={(p) => onprefs({ reading_prefs: p })} />
        <p class="muted small">{t('prefs.note')}</p>
      </section>
    </Dropdown>

    <NotificationBell />

    <Dropdown label={t('nav.user_menu')} class="topbar-btn">
      {#snippet trigger()}<Avatar {me} rev={avatarRev} />{/snippet}
      <div class="menu">
        <p class="menu-who"><strong>{me.full_name}</strong><span class="muted">{me.email}</span></p>
        <ul>
          <li><a href="/me/profile">{t('nav.profile')}</a></li>
        </ul>
        {#if me.is_global_admin}
          <ul>
            <li><a href="/admin">{t('nav.admin')}</a></li>
            <li><a href="/admin/audit">{t('audit.title')}</a></li>
            <li><a href="/admin/health">{t('health.short')}</a></li>
            <li><a href="/admin/appearance">{t('appearance.title')}</a></li>
            <li><a href="/admin/email">{t('smtp.title')}</a></li>
          </ul>
        {/if}
        <button type="button" class="menu-item" onclick={onlogout}>{t('auth.logout')}</button>
      </div>
    </Dropdown>
  </div>
</header>
