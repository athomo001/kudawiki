<script lang="ts">
  import type { ReadingPrefs } from '../lib/api';
  import { t } from '../lib/i18n/index.svelte';

  // Fuente y espaciado de lectura (SPEC §15): en el menú "Aa" de la barra y en el perfil. El id
  // separa los grupos de radios si las dos copias están en pantalla a la vez.
  let { prefs, onchange }: { prefs: ReadingPrefs; onchange: (p: ReadingPrefs) => void } = $props();

  const id = $props.id();
  const fonts = ['default', 'dyslexic', 'legible'] as const;
</script>

<fieldset>
  <legend class="side-title">{t('prefs.font')}</legend>
  {#each fonts as f (f)}
    <label class={`prefs-option font-${f}`}>
      <input type="radio" name={`reading-font-${id}`} checked={prefs.font === f} onchange={() => onchange({ ...prefs, font: f })} />
      {t(`prefs.font_${f}`)}
    </label>
  {/each}
</fieldset>
<label class="prefs-option">
  <input type="checkbox" checked={prefs.wide} onchange={(e) => onchange({ ...prefs, wide: e.currentTarget.checked })} />
  {t('prefs.wide')}
</label>
