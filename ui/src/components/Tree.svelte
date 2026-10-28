<script lang="ts" module>
  import type { PageRef } from '../lib/api';

  // El árbol se deriva de los slugs (SPEC §4.1): "redes/vpn" cuelga de "redes" aunque
  // "redes" no sea una página; en ese caso es una carpeta sin enlace.
  export type Node = { segment: string; slug: string; title?: string; level?: PageRef['level']; children: Node[] };

  export function buildTree(refs: PageRef[]): Node[] {
    const root: Node = { segment: '', slug: '', children: [] };
    for (const ref of refs) {
      let node = root;
      ref.slug.split('/').forEach((segment, i, parts) => {
        const slug = parts.slice(0, i + 1).join('/');
        let child = node.children.find((c) => c.segment === segment);
        if (!child) node.children.push((child = { segment, slug, children: [] }));
        node = child;
      });
      node.title = ref.title;
      node.level = ref.level;
    }
    return root.children;
  }
</script>

<script lang="ts">
  import Self from './Tree.svelte';
  import LevelBadge from './LevelBadge.svelte';
  import Icon from './Icon.svelte';

  let { nodes, base, current }: { nodes: Node[]; base: string; current?: string } = $props();
</script>

<!-- Árbol del menú lateral (SPEC §15): carpetas y páginas, la actual resaltada, insignias N2/N3. -->
<ul class="tree">
  {#each nodes as n (n.slug)}
    <li>
      {#if n.title}
        <a class="nav-item" href={`${base}/p/${n.slug}`} aria-current={n.slug === current ? 'page' : undefined}>
          <Icon name={n.children.length ? 'folder' : 'page'} size={15} />
          <span class="nav-label">{n.title}</span>
          <LevelBadge level={n.level} />
        </a>
      {:else}
        <span class="nav-item nav-folder"><Icon name="folder" size={15} /> <span class="nav-label">{n.segment}</span></span>
      {/if}
      {#if n.children.length}<Self nodes={n.children} {base} {current} />{/if}
    </li>
  {/each}
</ul>
