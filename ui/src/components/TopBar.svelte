<script lang="ts">
  import type { Area, Me, ReadingPrefs } from '../lib/api';
  import { t, locale } from '../lib/i18n/index.svelte';
  import Logo from './Logo.svelte';
  import Icon from './Icon.svelte';
  import Dropdown from './Dropdown.svelte';
  import SearchBox from './SearchBox.svelte';

  // Barra superior del diseño aprobado (SPEC §15): logo, área, búsqueda, idioma, lectura y usuario.
  // La campana de notificaciones llega con F4.
  let {
    me,
    area,
    onprefs,
    onlogout,
  }: {
    me: Me;
    area?: Area;
    onprefs: (change: { locale?: 'es' | 'en'; reading_prefs?: ReadingPrefs }) => void;
    onlogout: () => void;
  } = $props();

  const fonts = ['default', 'dyslexic', 'legible'] as const;
  const initials = $derived(
    me.full_name
      .split(/\s+/)
      .filter(Boolean)
      .slice(0, 2)
      .map((w) => w.charAt(0).toUpperCase())
      .join(''),
  );
</script>

<header class="topbar">
  <a class="brand" href="/"><Logo /> {t('app.name')}</a>

  {#if me.workspaces.length}
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
        <fieldset>
          <legend class="side-title">{t('prefs.font')}</legend>
          {#each fonts as f (f)}
            <label class={`prefs-option font-${f}`}>
              <input
                type="radio"
                name="reading-font"
                checked={me.reading_prefs.font === f}
                onchange={() => onprefs({ reading_prefs: { ...me.reading_prefs, font: f } })}
              />
              {t(`prefs.font_${f}`)}
            </label>
          {/each}
        </fieldset>
        <label class="prefs-option">
          <input
            type="checkbox"
            checked={me.reading_prefs.wide}
            onchange={(e) => onprefs({ reading_prefs: { ...me.reading_prefs, wide: e.currentTarget.checked } })}
          />
          {t('prefs.wide')}
        </label>
        <p class="muted small">{t('prefs.note')}</p>
      </section>
    </Dropdown>

    <Dropdown label={t('nav.user_menu')} class="topbar-btn">
      {#snippet trigger()}<span class="avatar">{initials}</span>{/snippet}
      <div class="menu">
        <p class="menu-who"><strong>{me.full_name}</strong><span class="muted">{me.email}</span></p>
        {#if me.is_global_admin}
          <ul>
            <li><a href="/admin">{t('nav.admin')}</a></li>
            <li><a href="/admin/audit">{t('audit.title')}</a></li>
            <li><a href="/admin/health">{t('health.short')}</a></li>
          </ul>
        {/if}
        <button type="button" class="menu-item" onclick={onlogout}>{t('auth.logout')}</button>
      </div>
    </Dropdown>
  </div>
</header>
