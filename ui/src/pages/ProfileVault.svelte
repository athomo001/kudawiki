<script lang="ts">
  import { api, codeOf } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';
  import PageHead from '../components/PageHead.svelte';
  import Setting from '../components/Setting.svelte';

  // Mi perfil → Clave de bóveda (§6.3, §15): abre los sellados que le comparten a esta persona.
  // Se crea una vez; si se olvida, se pone otra y lo compartido con la anterior hay que volver a
  // compartirlo. Pide confirmar quién eres (api() abre el diálogo de reautenticación).
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

<PageHead context={`${t('profile.title')} · ${t('profile.group_security')}`} title={t('vault.title')} description={t('vault.off_hint')}>
  {#snippet status()}
    <span class={`pill ${hasVault ? 'ok' : 'warn'}`}>{hasVault ? t('profile.vault_ready') : t('profile.pending')}</span>
  {/snippet}
</PageHead>

{#if done}<p role="status" class="notice">{done}</p>{/if}

<Setting title={t(hasVault ? 'vault.rotate' : 'vault.create')} description={t(hasVault ? 'vault.rotate_warning' : 'profile.vault_desc')}>
  <form class="setting-fields" onsubmit={submit} autocomplete="off">
    <div class="field-pair">
      <label class="field">{t(hasVault ? 'vault.new_key' : 'vault.key')} <input type="password" required minlength="12" autocomplete="new-password" bind:value={key} /></label>
      <label class="field">{t('sealed.password_repeat')} <input type="password" required minlength="12" autocomplete="new-password" bind:value={repeat} /></label>
    </div>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <p class="form-actions"><button type="submit" class={hasVault ? 'secondary' : 'primary'} disabled={busy}>{t(hasVault ? 'vault.rotate' : 'vault.create')}</button></p>
  </form>
</Setting>
