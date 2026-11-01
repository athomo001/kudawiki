<script lang="ts">
  import { api, codeOf, onReauth } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';
  import { passkeysSupported, signWithPasskey } from '../lib/passkey';
  import Dialog from './Dialog.svelte';

  // Reautenticación (SPEC §8.2): cuando el servidor responde reauth_required, api() llama a este
  // diálogo y espera. Confirmar repite el pedido que lo pidió; cerrarlo lo deja fallar con su error.
  // totp: la persona tiene 2FA y se le pide también un código. passkeys: tiene alguna y puede
  // usarla en lugar de contraseña y código (§8.1).
  let { totp, passkeys }: { totp: boolean; passkeys: boolean } = $props();

  let open = $state(false);
  let password = $state('');
  let code = $state('');
  let busy = $state(false);
  let error = $state('');
  let resolve: ((ok: boolean) => void) | null = null;

  $effect(() => {
    onReauth(
      () =>
        new Promise<boolean>((r) => {
          resolve = r;
          password = code = error = '';
          open = true;
        }),
    );
    return () => onReauth(null);
  });

  // Cerrar con Esc o con la X es no confirmar.
  $effect(() => {
    if (!open) finish(false);
  });

  function finish(ok: boolean) {
    resolve?.(ok);
    resolve = null;
  }

  async function confirm(run: () => Promise<void>) {
    busy = true;
    error = '';
    try {
      await run();
      password = code = '';
      finish(true);
      open = false;
    } catch (err) {
      error = errorText(codeOf(err));
      code = '';
    } finally {
      busy = false;
    }
  }

  function submit(e: SubmitEvent) {
    e.preventDefault();
    confirm(() => api('POST', '/auth/reauth', { password, code }));
  }

  const withPasskey = () => confirm(() => signWithPasskey('/auth/reauth/passkey/begin', '/auth/reauth/passkey/finish'));
</script>

<Dialog title={t('reauth.title')} bind:open>
  <form class="stack" onsubmit={submit}>
    <p class="muted">{t('reauth.hint')}</p>
    <label class="field">
      {t('profile.password_current')}
      <!-- svelte-ignore a11y_autofocus -->
      <input type="password" autocomplete="current-password" required autofocus bind:value={password} />
    </label>
    {#if totp}
      <label class="field">
        {t('totp.code')}
        <input autocomplete="one-time-code" required aria-describedby="reauth-code-hint" bind:value={code} />
      </label>
      <small id="reauth-code-hint" class="muted">{t('totp.login_hint')}</small>
    {/if}
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <button type="submit" class="primary" disabled={busy}>{busy ? t('reauth.confirming') : t('reauth.confirm')}</button>
    {#if passkeys && passkeysSupported()}
      <button type="button" class="passkey-action" disabled={busy} onclick={withPasskey}>{t('passkeys.use')}</button>
    {/if}
  </form>
</Dialog>
