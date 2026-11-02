<script lang="ts">
  import { t } from '../lib/i18n/index.svelte';

  // Barra de guardado al pie (SPEC §15, diseño "Kudawiki — Administración"): aparece solo con
  // cambios sin guardar, para que no se pierdan al bajar por una página larga.
  // count: cuántos ajustes cambiaron; note: lo que pasa al guardar, si hay algo que decir.
  let {
    count,
    note = '',
    busy = false,
    onsave,
    ondiscard,
  }: { count: number; note?: string; busy?: boolean; onsave: () => void; ondiscard: () => void } = $props();
</script>

{#if count > 0}
  <div class="savebar" role="status">
    <p>
      <strong>{count === 1 ? t('admin.unsaved_one') : t('admin.unsaved_other', { n: String(count) })}</strong>
      {#if note}<span class="muted">{note}</span>{/if}
    </p>
    <button type="button" class="link" disabled={busy} onclick={ondiscard}>{t('common.discard')}</button>
    <button type="button" class="primary" disabled={busy} onclick={onsave}>{busy ? t('common.saving') : t('common.save')}</button>
  </div>
{/if}
