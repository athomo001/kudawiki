<script lang="ts">
  import type { Me, ReadingPrefs } from '../lib/api';
  import { t, locale } from '../lib/i18n/index.svelte';
  import { countChanges } from '../lib/changes';
  import PageHead from '../components/PageHead.svelte';
  import Setting from '../components/Setting.svelte';
  import SaveBar from '../components/SaveBar.svelte';

  // Mi perfil → Idioma y lectura (SPEC §15): se aplican en todas las áreas de la persona y no
  // cambian nada para los demás. Se guardan con la barra, como el resto del molde; el botón Aa de
  // la barra superior sigue cambiando la lectura al momento.
  type Locale = 'es' | 'en';
  let { me, onprefs }: { me: Me; onprefs: (change: { locale?: Locale; reading_prefs?: ReadingPrefs }) => void } = $props();

  const fonts = ['default', 'dyslexic', 'legible'] as const;
  const locales: Locale[] = ['es', 'en'];

  // Lo guardado, para comparar y para Descartar. Se rehace si cambia desde otro lado (Aa).
  const saved = $derived({ locale: locale() as Locale, font: me.reading_prefs.font, wide: me.reading_prefs.wide });
  let form = $state({ locale: 'es' as Locale, font: 'default' as ReadingPrefs['font'], wide: false });
  $effect(() => {
    form = { ...saved };
  });
  const changes = $derived(countChanges(saved, form));

  function save() {
    const change: { locale?: Locale; reading_prefs?: ReadingPrefs } = {};
    if (form.locale !== saved.locale) change.locale = form.locale;
    if (form.font !== saved.font || form.wide !== saved.wide) change.reading_prefs = { ...me.reading_prefs, font: form.font, wide: form.wide };
    onprefs(change);
  }
</script>

<PageHead context={`${t('profile.title')} · ${t('profile.group_account')}`} title={t('profile.reading')} description={t('profile.reading_desc')} />

<Setting title={t('lang.ui')} description={t('profile.lang_desc')}>
  <div class="choices choices-row" role="radiogroup" aria-label={t('lang.ui')}>
    {#each locales as l (l)}
      <label class="choice">
        <input type="radio" name="profile-locale" value={l} bind:group={form.locale} />
        <span><strong>{t(`lang.${l}`)}</strong></span>
      </label>
    {/each}
  </div>
</Setting>

<Setting title={t('prefs.font')} description={t('profile.font_desc')}>
  <div class="choices" role="radiogroup" aria-label={t('prefs.font')}>
    {#each fonts as f (f)}
      <label class={`choice font-${f}`}>
        <input type="radio" name="profile-font" value={f} bind:group={form.font} />
        <span><strong>{t(`prefs.font_${f}`)}</strong></span>
      </label>
    {/each}
  </div>
  <label class="check check-hint">
    <input type="checkbox" bind:checked={form.wide} />
    <span><strong>{t('prefs.wide')}</strong><small>{t('prefs.note')}</small></span>
  </label>
</Setting>

<SaveBar count={changes} onsave={save} ondiscard={() => (form = { ...saved })} />
