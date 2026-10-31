<script lang="ts">
  import { api, codeOf, type Area, type Page, type WatchMode } from '../lib/api';
  import { t, errorText } from '../lib/i18n/index.svelte';
  import Dropdown from './Dropdown.svelte';
  import Icon from './Icon.svelte';

  // "Seguir página" en la columna de acciones (diseño aprobado, SPEC §12.1): sola o con sus
  // subpáginas. Lo puede cualquiera que la lea. Si ya la sigue por un padre, se lo dice.
  let { area, page }: { area: Area; page: Page } = $props();

  // Sale de la página y se pisa al elegir; al abrir otra página vuelve a salir de ella.
  let mode = $derived<WatchMode>(page.watch);
  let error = $state('');
  let busy = $state(false);
  let menu = $state<Dropdown>();

  const options: { value: WatchMode; key: 'watch.none' | 'watch.page' | 'watch.subtree' }[] = [
    { value: '', key: 'watch.none' },
    { value: 'page', key: 'watch.page' },
    { value: 'subtree', key: 'watch.subtree' },
  ];

  async function choose(value: WatchMode) {
    if (value === mode || busy) return;
    busy = true;
    error = '';
    try {
      const path = `/w/${area.slug}/watch/${page.slug}`;
      if (value) await api('PUT', path, { subtree: value === 'subtree' });
      else await api('DELETE', path);
      mode = value;
      menu?.close(true);
    } catch (e) {
      error = errorText(codeOf(e));
    } finally {
      busy = false;
    }
  }
</script>

<Dropdown bind:this={menu} label={t('watch.label')} class={`action${mode || page.watch_via ? ' watching' : ''}`}>
  {#snippet trigger()}<Icon name="bell" />{/snippet}
  <div class="menu watch-menu">
    <p class="menu-who"><strong>{t('watch.menu')}</strong><span class="muted">{t('watch.note')}</span></p>
    <ul role="group" aria-label={t('watch.menu')}>
      {#each options as o (o.value)}
        <li>
          <button type="button" class="menu-item" aria-pressed={mode === o.value} disabled={busy} onclick={() => choose(o.value)}>
            <span class="menu-check" aria-hidden="true">{#if mode === o.value}<Icon name="check" size={14} />{/if}</span>
            {t(o.key)}
          </button>
        </li>
      {/each}
    </ul>
    {#if page.watch_via}<p class="muted small watch-via">{t('watch.via', { page: page.watch_via })}</p>{/if}
    {#if error}<p class="error small" role="alert">{error}</p>{/if}
  </div>
</Dropdown>
