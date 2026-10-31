<script lang="ts">
  import { api, codeOf, type TOTPSetup } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';

  // Alta del 2FA (SPEC §8.1) en tres pasos: el QR (o el secreto a mano), un código de la app
  // para confirmar y los diez códigos de recuperación, que se ven esta única vez.
  // revokeOption: ofrecer cerrar las demás sesiones (§8.2); en la configuración obligatoria del
  // primer login no hay otras que cerrar.
  let { ondone, revokeOption = false }: { ondone: () => void; revokeOption?: boolean } = $props();

  let setup = $state<TOTPSetup | null>(null);
  let code = $state('');
  let revokeOthers = $state(true);
  let codes = $state<string[]>([]);
  let busy = $state(false);
  let error = $state('');
  let copied = $state(false);

  // Un módulo negro por cuadrado de 1×1, con el margen de 4 módulos que pide el estándar.
  const qrPath = $derived(
    setup?.qr.flatMap((row, y) => [...row].map((c, x) => (c === '1' ? `M${x + 4} ${y + 4}h1v1h-1z` : ''))).join('') ?? '',
  );
  const qrSize = $derived((setup?.qr.length ?? 0) + 8);
  // De a cuatro, como lo piden las apps al escribirlo a mano.
  const secretGroups = $derived(setup?.secret.match(/.{1,4}/g)?.join(' ') ?? '');

  async function start() {
    busy = true;
    error = '';
    try {
      setup = await api<TOTPSetup>('POST', '/me/totp');
    } catch (e) {
      error = errorText(codeOf(e));
    } finally {
      busy = false;
    }
  }

  async function confirm(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      const res = await api<{ recovery_codes: string[] }>('POST', '/me/totp/confirm', { code, revoke_others: revokeOption && revokeOthers });
      codes = res.recovery_codes;
      setup = null;
    } catch (err) {
      error = errorText(codeOf(err));
    } finally {
      busy = false;
    }
  }

  async function copyCodes() {
    await navigator.clipboard.writeText(codes.join('\n'));
    copied = true;
  }
</script>

{#if codes.length}
  <div class="stack">
    <h3>{t('totp.codes_title')}</h3>
    <p>{t('totp.codes_hint')}</p>
    <ol class="recovery-codes">
      {#each codes as c (c)}<li><code>{c}</code></li>{/each}
    </ol>
    <p class="actions">
      <button type="button" onclick={copyCodes}>{copied ? t('common.copied') : t('common.copy')}</button>
      <button type="button" class="primary" onclick={ondone}>{t('totp.codes_saved')}</button>
    </p>
  </div>
{:else if setup}
  <form class="stack" onsubmit={confirm}>
    <p>{t('totp.scan')}</p>
    <svg class="totp-qr" viewBox={`0 0 ${qrSize} ${qrSize}`} width="200" height="200" role="img" aria-label={t('totp.qr_label')}>
      <rect width={qrSize} height={qrSize} fill="#fff" />
      <path d={qrPath} fill="#000" shape-rendering="crispEdges" />
    </svg>
    <p class="muted small">{t('totp.manual')} <code class="totp-secret">{secretGroups}</code></p>
    <label class="field">
      {t('totp.code')}
      <input
        required
        autocomplete="one-time-code"
        inputmode="numeric"
        aria-describedby="totp-code-hint"
        bind:value={code}
      />
    </label>
    <small id="totp-code-hint" class="muted">{t('totp.code_hint')}</small>
    {#if revokeOption}
      <label class="check">
        <input type="checkbox" bind:checked={revokeOthers} />
        {t('profile.revoke_others')}
      </label>
    {/if}
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <button type="submit" class="primary" disabled={busy}>{busy ? t('common.saving') : t('totp.confirm')}</button>
  </form>
{:else}
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  <p><button type="button" class="primary" disabled={busy} onclick={start}>{t('totp.start')}</button></p>
{/if}
