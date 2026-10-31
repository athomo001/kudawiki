<script lang="ts">
  import type { EditorView } from '@codemirror/view';
  import { imageInfo, imagesBeside, setImage, type ImageAlign, type ImageInfo } from '../lib/editor';
  import { t, type Key } from '../lib/i18n/index.svelte';
  import Icon from './Icon.svelte';
  import Dropdown from './Dropdown.svelte';

  // Tamaño y posición de la imagen del cursor (SPEC §4.4), pedido del dueño: escribe la sintaxis
  // de Wiki.js (=50%, {.align-center}) para que nadie tenga que recordarla. Se lee la imagen al
  // abrir el menú.
  let { view }: { view: () => EditorView | undefined } = $props();

  let menu = $state<Dropdown>();
  let info = $state<ImageInfo | null>(null);

  const sizes: { value: string; key: Key; n?: number }[] = [
    { value: '', key: 'image.size_original' },
    ...[25, 50, 75, 100].map((n) => ({ value: `${n}%`, key: 'image.size_pct' as Key, n })),
  ];
  const aligns: { value: ImageAlign; key: Key }[] = [
    { value: '', key: 'image.align_none' },
    { value: 'center', key: 'image.align_center' },
    { value: 'left', key: 'image.align_left' },
    { value: 'right', key: 'image.align_right' },
  ];

  function read() {
    const v = view();
    info = v ? imageInfo(v) : null;
  }

  function apply(run: (v: EditorView) => void) {
    const v = view();
    if (v) run(v);
    menu?.close();
  }
</script>

<Dropdown bind:this={menu} label={t('toolbar.image_size')} class="tb" align="start" onopen={read}>
  {#snippet trigger()}<Icon name="resize" size={16} />{/snippet}
  <div class="menu image-menu">
    <p class="menu-who"><strong>{t('toolbar.image_size')}</strong></p>
    {#if !info}
      <p class="muted small image-menu-none">{t('image.menu_none')}</p>
    {:else}
      <ul role="group" aria-label={t('image.size')}>
        <li class="menu-label">{t('image.size')}</li>
        {#each sizes as s (s.value)}
          <li>
            <button type="button" class="menu-item" aria-pressed={info.size === s.value} onclick={() => apply((v) => setImage(v, { size: s.value }))}>
              <span class="menu-check" aria-hidden="true">{#if info.size === s.value}<Icon name="check" size={14} />{/if}</span>
              {t(s.key, { n: String(s.n ?? '') })}
            </button>
          </li>
        {/each}
      </ul>
      <ul role="group" aria-label={t('image.align')}>
        <li class="menu-label">{t('image.align')}</li>
        {#each aligns as a (a.value)}
          <li>
            <button type="button" class="menu-item" aria-pressed={info.align === a.value} onclick={() => apply((v) => setImage(v, { align: a.value }))}>
              <span class="menu-check" aria-hidden="true">{#if info.align === a.value}<Icon name="check" size={14} />{/if}</span>
              {t(a.key)}
            </button>
          </li>
        {/each}
      </ul>
      {#if info.below}
        <ul>
          <li>
            <button type="button" class="menu-item" onclick={() => apply(imagesBeside)}>
              <span class="menu-check" aria-hidden="true"></span>{t('image.beside')}
            </button>
          </li>
        </ul>
      {/if}
      <p class="muted small image-menu-none">{t('image.hint')}</p>
    {/if}
  </div>
</Dropdown>
