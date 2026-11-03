<script lang="ts">
  import { api, codeOf } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';

  // Clave de bóveda (§6.3): abre los sellados que le comparten a esta persona. Se crea una vez;
  // si se olvida, se pone otra y lo compartido con la anterior hay que volver a compartirlo.
  // Pide confirmar quién eres (api() abre el diálogo de reautenticación).
  let { hasVault, onchanged }: { hasVault: boolean; onchanged: () => void } = $props();

  let key = $state('');
  let repeat = $state('');
  let busy = $state(false);
  let error = $state('');
  let done = $state('');

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    error = done = '';
    if (key !== repeat) {
      error = t('sealed.password_mismatch');
      return;
    }
    busy = true;
    try {
      const { lost } = await api<{ lost: number }>('POST', '/me/vault', { vault_key: key });
      key = repeat = '';
      done = lost ? t('vault.done_lost', { n: String(lost) }) : t('vault.done');
      onchanged();
    } catch (err) {
      error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }
</script>

<section class="stack" aria-labelledby="vault-title">
  <h2 id="vault-title">{t('vault.title')}</h2>
  <p class="muted">{t(hasVault ? 'vault.on' : 'vault.off_hint')}</p>
  {#if hasVault}<p class="muted small">{t('vault.rotate_warning')}</p>{/if}
  <form class="stack sealed-form" onsubmit={submit} autocomplete="off">
    <div class="sealed-passwords">
      <label class="field">{t(hasVault ? 'vault.new_key' : 'vault.key')} <input type="password" required minlength="12" autocomplete="new-password" bind:value={key} /></label>
      <label class="field">{t('sealed.password_repeat')} <input type="password" required minlength="12" autocomplete="new-password" bind:value={repeat} /></label>
    </div>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    {#if done}<p role="status" class="muted">{done}</p>{/if}
    <button type="submit" class={hasVault ? undefined : 'primary'} disabled={busy}>{t(hasVault ? 'vault.rotate' : 'vault.create')}</button>
  </form>
</section>
