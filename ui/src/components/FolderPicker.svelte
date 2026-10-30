<script lang="ts" module>
  // Ruta sugerida a partir del título: "Configuración VPN" → "configuracion-vpn".
  export function slugify(title: string): string {
    return title
      .normalize('NFD')
      .replace(/\p{M}/gu, '')
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '');
  }
</script>

<script lang="ts">
  import { SvelteSet } from 'svelte/reactivity';
  import type { PageRef } from '../lib/api';
  import { t } from '../lib/i18n/index.svelte';
  import { buildTree, type Node } from './Tree.svelte';
  import Icon from './Icon.svelte';

  // Elegir la carpeta en el árbol del área en vez de adivinar la ruta (SPEC §15). Cambia la parte
  // de carpetas de path y deja el último tramo; el campo de texto sigue sirviendo para escribirla
  // a mano, incluso con carpetas nuevas. exclude: la página que se mueve, que no puede quedar
  // dentro de sí misma (§4.1).
  let {
    refs,
    rootLabel,
    path = $bindable(),
    title = '',
    exclude,
  }: { refs: PageRef[]; rootLabel: string; path: string; title?: string; exclude?: string } = $props();

  const id = $props.id();
  const tree = $derived(buildTree(refs));
  const parent = $derived(path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '');
  const excluded = (slug: string) => !!exclude && (slug === exclude || slug.startsWith(`${exclude}/`));

  // Abiertas: las carpetas que llevan hasta la actual.
  const open = new SvelteSet<string>();
  $effect.pre(() => {
    parent.split('/').forEach((_, i, parts) => open.add(parts.slice(0, i + 1).join('/')));
  });

  function choose(folder: string) {
    const leaf = path.split('/').filter(Boolean).pop() ?? slugify(title);
    path = folder ? `${folder}/${leaf}` : leaf;
  }

  function toggle(slug: string) {
    if (!open.delete(slug)) open.add(slug);
  }
</script>

{#snippet option(slug: string, label: string, hint: string)}
  <label class="picker-option">
    <input type="radio" name={id} checked={parent === slug} onchange={() => choose(slug)} />
    <Icon name="folder" size={15} />
    <span>{label}</span>
    {#if hint}<span class="muted small">{hint}</span>{/if}
  </label>
{/snippet}

{#snippet branch(nodes: Node[])}
  <ul>
    {#each nodes.filter((n) => !excluded(n.slug)) as n (n.slug)}
      {@const label = n.title ?? n.segment}
      <li>
        <div class="picker-row">
          {#if n.children.some((c) => !excluded(c.slug))}
            <button
              type="button"
              class="picker-toggle"
              aria-expanded={open.has(n.slug)}
              aria-label={t('picker.toggle', { name: label })}
              onclick={() => toggle(n.slug)}><Icon name="chevron" size={14} /></button
            >
          {:else}
            <span class="picker-toggle"></span>
          {/if}
          {@render option(n.slug, label, n.title && n.title !== n.segment ? n.segment : '')}
        </div>
        {#if open.has(n.slug)}{@render branch(n.children)}{/if}
      </li>
    {/each}
  </ul>
{/snippet}

<fieldset class="picker">
  <legend class="side-title">{t('picker.legend')}</legend>
  <div class="picker-tree">
    <div class="picker-row"><span class="picker-toggle"></span>{@render option('', rootLabel, t('picker.root'))}</div>
    {@render branch(tree)}
  </div>
</fieldset>
