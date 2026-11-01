<script lang="ts">
  import { codeOf } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';
  import { createPasskey } from '../lib/passkey';

  // Registrar una passkey (SPEC §8.1). El nombre es para reconocerla al quitarla; el servidor pide
  // antes reautenticarse, y el diálogo de App lo resuelve sin salir de aquí.
  let { ondone }: { ondone: () => void } = $props();

  let name = $state('');
  let busy = $state(false);
  let error = $state('');

  async function add(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      await createPasskey(name);
      name = '';
      ondone();
    } catch (err) {
      error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }
</script>

<form class="stack" onsubmit={add}>
  <label class="field">
    {t('passkeys.name')}
    <input required maxlength="64" aria-describedby="passkey-name-hint" bind:value={name} />
  </label>
  <small id="passkey-name-hint" class="muted">{t('passkeys.name_hint')}</small>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  <button type="submit" class="passkey-action" disabled={busy}>{busy ? t('passkeys.adding') : t('passkeys.add')}</button>
</form>
