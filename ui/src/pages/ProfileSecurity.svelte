<script lang="ts">
  import { untrack } from 'svelte';
  import { api, codeOf, formatDate, timeAgo, type Me, type PasskeyInfo } from '../lib/api';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';
  import { passkeysSupported } from '../lib/passkey';
  import { site } from '../lib/site.svelte';
  import PageHead from '../components/PageHead.svelte';
  import Setting from '../components/Setting.svelte';
  import TotpSetup from '../components/TotpSetup.svelte';
  import PasskeyAdd from '../components/PasskeyAdd.svelte';

  // Mi perfil → Contraseña y acceso (SPEC §8.1, §8.2, §8.5.2, §15): cómo entra la persona. Cada
  // cosa se aplica al momento con su botón, porque pide confirmar quién es; no usa la barra.
  let { me, onchanged }: { me: Me; onchanged: () => Promise<void> } = $props();

  let current = $state('');
  let next = $state('');
  let repeat = $state('');
  let revokeOthers = $state(true);
  let passwordBusy = $state(false);
  let passwordDone = $state(false);
  let passwordError = $state('');

  // Desactivar el 2FA pide la contraseña y un código (§8.1): una sesión abierta no alcanza.
  let disabling = $state(false);
  let offPassword = $state('');
  let offRevoke = $state(true);
  let offCode = $state('');
  let offBusy = $state(false);
  let offError = $state('');

  const supported = passkeysSupported();
  let passkeys = $state<PasskeyInfo[]>([]);
  let keyBusy = $state(false);
  let keyError = $state('');

  async function changePassword(e: SubmitEvent) {
    e.preventDefault();
    passwordDone = false;
    passwordError = '';
    if (next !== repeat) {
      passwordError = t('invite.mismatch');
      return;
    }
    passwordBusy = true;
    try {
      await api('POST', '/me/password', { current_password: current, new_password: next, revoke_others: revokeOthers });
      current = next = repeat = '';
      passwordDone = true;
    } catch (err) {
      passwordError = errorText(codeOf(err));
    } finally {
      passwordBusy = false;
    }
  }

  async function disableTotp(e: SubmitEvent) {
    e.preventDefault();
    offBusy = true;
    offError = '';
    try {
      await api('DELETE', '/me/totp', { password: offPassword, code: offCode, revoke_others: offRevoke });
      offPassword = offCode = '';
      disabling = false;
      await onchanged();
    } catch (err) {
      offError = errorText(codeOf(err));
    } finally {
      offBusy = false;
    }
  }

  // onchanged recarga /auth/me, que dice cuántas passkeys hay (para la reautenticación y el 2FA).
  async function keys(action?: () => Promise<unknown>) {
    keyBusy = true;
    keyError = '';
    try {
      await action?.();
      passkeys = await api<PasskeyInfo[]>('GET', '/me/passkeys');
    } catch (e) {
      keyError = errorText(codeOf(e));
    } finally {
      keyBusy = false;
    }
  }

  $effect(() => {
    if (me.passkeys.available) untrack(() => keys());
  });
</script>

<PageHead context={`${t('profile.title')} · ${t('profile.group_security')}`} title={t('profile.nav_security')} description={t('profile.security_desc')} />

<Setting title={t('profile.password')} description={me.auth_source === 'ldap' ? t('profile.password_directory') : t('profile.password_hint')}>
  {#if me.auth_source === 'ldap'}
    {#if site.password_help_url}
      <p><a href={site.password_help_url} rel="noopener noreferrer" target="_blank">{t('auth.password_help')}</a></p>
    {:else}
      <p class="muted">{t('profile.password_directory')}</p>
    {/if}
  {:else}
    <form class="setting-fields" onsubmit={changePassword}>
      <!-- El correo oculto ayuda al gestor de contraseñas a saber de qué cuenta es la clave nueva. -->
      <input class="sr-only" type="email" autocomplete="username" value={me.email} readonly tabindex="-1" aria-hidden="true" />
      <label class="field">
        {t('profile.password_current')}
        <input type="password" autocomplete="current-password" required bind:value={current} />
      </label>
      <div class="field-pair">
        <label class="field">
          {t('profile.password_new')}
          <input type="password" autocomplete="new-password" required bind:value={next} />
        </label>
        <label class="field">
          {t('auth.password_repeat')}
          <input type="password" autocomplete="new-password" required bind:value={repeat} />
        </label>
      </div>
      <label class="check">
        <input type="checkbox" bind:checked={revokeOthers} />
        {t('profile.revoke_others')}
      </label>
      {#if passwordError}<p class="error" role="alert">{passwordError}</p>{/if}
      {#if passwordDone}<p role="status" class="notice">{t('profile.password_done')}</p>{/if}
      <p class="form-actions"><button type="submit" class="primary" disabled={passwordBusy}>{passwordBusy ? t('common.saving') : t('profile.password_submit')}</button></p>
    </form>
  {/if}
</Setting>

<Setting title={t('totp.title')} description={t('totp.off_hint')}>
  {#if me.totp.enabled}
    <dl class="status-line">
      <div><dt>{t('profile.state')}</dt><dd><span class="pill ok">{t('profile.totp_on')}</span></dd></div>
      <div><dt>{t('profile.recovery_codes')}</dt><dd>{t('profile.codes_of', { n: String(me.totp.recovery_codes_left) })}</dd></div>
    </dl>
    <!-- Con una passkey al lado, el 2FA obligatorio sigue cumplido sin el TOTP (§8.1). -->
    {#if me.totp.required && me.passkeys.count === 0}
      <p class="muted small">{t('totp.required_note')}</p>
    {:else if !disabling}
      <p class="form-actions"><button type="button" class="link danger" onclick={() => (disabling = true)}>{t('totp.disable')}…</button></p>
    {:else}
      <form class="setting-fields" onsubmit={disableTotp}>
        <label class="field">
          {t('profile.password_current')}
          <input type="password" autocomplete="current-password" required bind:value={offPassword} />
        </label>
        <label class="field">
          {t('totp.code')}
          <input autocomplete="one-time-code" required aria-describedby="totp-off-hint" bind:value={offCode} />
          <small id="totp-off-hint" class="muted">{t('totp.login_hint')}</small>
        </label>
        <label class="check">
          <input type="checkbox" bind:checked={offRevoke} />
          {t('profile.revoke_others')}
        </label>
        {#if offError}<p class="error" role="alert">{offError}</p>{/if}
        <p class="form-actions">
          <button type="button" class="link" onclick={() => (disabling = false)}>{t('common.cancel')}</button>
          <button type="submit" class="danger-btn" disabled={offBusy}>{offBusy ? t('common.saving') : t('totp.disable')}</button>
        </p>
      </form>
    {/if}
  {:else}
    <TotpSetup revokeOption ondone={onchanged} />
  {/if}
</Setting>

{#if me.passkeys.available}
  <Setting title={t('passkeys.title')} description={t('passkeys.hint')} wide>
    {#if passkeys.length}
      <div class="table-scroll">
        <table class="admin-table">
          <thead>
            <tr>
              <th scope="col">{t('common.name')}</th>
              <th scope="col">{t('profile.col_added')}</th>
              <th scope="col">{t('profile.col_last_used')}</th>
              <th scope="col"><span class="sr-only">{t('common.actions')}</span></th>
            </tr>
          </thead>
          <tbody>
            {#each passkeys as p (p.id)}
              <tr>
                <td><strong>{p.name}</strong></td>
                <td>{formatDate(p.created_at, locale())}</td>
                <td>{p.last_used_at ? timeAgo(p.last_used_at, locale()) : t('passkeys.never_used')}</td>
                <td class="row-actions">
                  <button
                    type="button"
                    class="link danger"
                    disabled={keyBusy}
                    aria-label={t('passkeys.remove_label', { name: p.name })}
                    onclick={() =>
                      keys(async () => {
                        await api('DELETE', `/me/passkeys/${p.id}`);
                        await onchanged();
                      })}>{t('passkeys.remove')}</button
                  >
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
    {#if keyError}<p class="error" role="alert">{keyError}</p>{/if}
    {#if supported}
      <PasskeyAdd ondone={() => keys(onchanged)} />
    {:else}
      <p class="muted">{t('passkeys.unsupported')}</p>
    {/if}
  </Setting>
{/if}
