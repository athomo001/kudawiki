<script lang="ts">
  import { api, codeOf, formatDate, timeAgo, type PasskeyInfo } from '../lib/api';
  import { t, errorText, locale } from '../lib/i18n/index.svelte';
  import { passkeysSupported } from '../lib/passkey';
  import PasskeyAdd from './PasskeyAdd.svelte';

  // Mis passkeys (SPEC §8.1): cuáles hay, cuándo se usaron, y agregar o quitar. onchanged
  // recarga /auth/me, que dice cuántas hay (para el diálogo de reautenticación y el 2FA).
  let { onchanged }: { onchanged: () => Promise<void> } = $props();

  const supported = passkeysSupported();
  let list = $state<PasskeyInfo[]>([]);
  let busy = $state(false);
  let error = $state('');

  async function run(action?: () => Promise<unknown>) {
    busy = true;
    error = '';
    try {
      await action?.();
      list = await api<PasskeyInfo[]>('GET', '/me/passkeys');
    } catch (e) {
      error = errorText(codeOf(e));
    } finally {
      busy = false;
    }
  }

  run();
</script>

<section class="stack" id="passkeys" aria-labelledby="passkeys-title">
  <h2 id="passkeys-title">{t('passkeys.title')}</h2>
  <p class="muted small">{t('passkeys.hint')}</p>
  {#if list.length}
    <ul class="plain passkey-list">
      {#each list as p (p.id)}
        <li>
          <div>
            <strong>{p.name}</strong>
            <br />
            <span class="muted small">
              {t('passkeys.added', { when: formatDate(p.created_at, locale()) })} ·
              {p.last_used_at ? t('passkeys.last_used', { ago: timeAgo(p.last_used_at, locale()) }) : t('passkeys.never_used')}
            </span>
          </div>
          <button
            type="button"
            class="link"
            disabled={busy}
            aria-label={t('passkeys.remove_label', { name: p.name })}
            onclick={() =>
              run(async () => {
                await api('DELETE', `/me/passkeys/${p.id}`);
                await onchanged();
              })}>{t('passkeys.remove')}</button
          >
        </li>
      {/each}
    </ul>
  {/if}
  {#if error}<p class="error" role="alert">{error}</p>{/if}
  {#if supported}
    <PasskeyAdd ondone={() => run(onchanged)} />
  {:else}
    <p class="muted">{t('passkeys.unsupported')}</p>
  {/if}
</section>
