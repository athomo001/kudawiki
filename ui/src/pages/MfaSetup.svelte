<script lang="ts">
  import { t } from '../lib/i18n/index.svelte';
  import Brand from '../components/Brand.svelte';
  import TotpSetup from '../components/TotpSetup.svelte';
  import PasskeyAdd from '../components/PasskeyAdd.svelte';
  import { site } from '../lib/site.svelte';
  import { passkeysSupported } from '../lib/passkey';

  // Quien tiene que usar 2FA y no lo tiene entra solo a esta pantalla (SPEC §8.1): el servidor
  // rechaza todo lo demás con mfa_setup_required hasta que lo configure.
  let { email, ondone, onlogout }: { email: string; ondone: () => void; onlogout: () => void } = $props();
</script>

<main class="login mfa-setup">
  <p class="login-brand"><Brand size={40} /></p>
  <h1>{t('totp.required_title')}</h1>
  <p>{t('totp.required_body', { email })}</p>
  <TotpSetup {ondone} />
  <!-- Una passkey cumple el 2FA igual que el TOTP (§8.1). -->
  {#if site.passkeys && passkeysSupported()}
    <h2>{t('passkeys.setup_title')}</h2>
    <p class="muted">{t('passkeys.setup_hint')}</p>
    <PasskeyAdd {ondone} />
  {/if}
  <p class="login-alt"><button type="button" class="link" onclick={onlogout}>{t('auth.logout')}</button></p>
</main>
