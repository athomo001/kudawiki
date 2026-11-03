<script lang="ts" module>
  import type { PageRef } from '../lib/api';

  // El árbol se deriva de los slugs (SPEC §4.1): "redes/vpn" cuelga de "redes" aunque
  // "redes" no sea una página; en ese caso es una carpeta sin enlace.
  export type Node = {
    segment: string;
    slug: string;
    title?: string;
    level?: PageRef['level'];
    icon?: string;
    position?: number;
    children: Node[];
  };

  // Lo que el árbol puede pedir a quien lo muestra si deja arrastrar (§15, B7): mover una página
  // con sus subpáginas a otra ruta, o fijar el orden de las hermanas de una carpeta.
  export type TreeDrag = {
    onmove: (from: string, to: string) => void;
    onreorder: (parent: string, slugs: string[]) => void;
  };

  export const parentOf = (slug: string) => slug.split('/').slice(0, -1).join('/');

  // Primero las hermanas con orden manual, de menor a mayor; después las demás por ruta (sort es
  // estable y las rutas llegan ordenadas).
  const rank = (n: Node) => n.position || Number.MAX_SAFE_INTEGER;
  function sortTree(nodes: Node[]) {
    nodes.sort((a, b) => rank(a) - rank(b));
    for (const n of nodes) sortTree(n.children);
  }

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
      node.icon = ref.icon;
      node.position = ref.position;
    }
    sortTree(root.children);
    return root.children;
  }
</script>

<script lang="ts">
  import Self from './Tree.svelte';
  import LevelBadge from './LevelBadge.svelte';
  import Icon from './Icon.svelte';
  import { t } from '../lib/i18n/index.svelte';

  let { nodes, base, current, drag }: { nodes: Node[]; base: string; current?: string; drag?: TreeDrag } = $props();

  // El tipo propio evita que un texto o un enlace arrastrado desde afuera se tome por una página.
  const MIME = 'application/x-kudawiki-page';
  type Zone = 'before' | 'inside' | 'after';
  let over = $state<{ slug: string; zone: Zone } | null>(null);

  // El cuarto de arriba y el de abajo ordenan entre hermanas; el centro mete la página adentro.
  function zoneOf(e: DragEvent): Zone {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const y = e.clientY - r.top;
    return y < r.height / 4 ? 'before' : y > (r.height * 3) / 4 ? 'after' : 'inside';
  }

  // Las carpetas sin página no tienen posición propia: el orden se manda solo con páginas.
  const pages = (slugs: string[]) => slugs.filter((s) => nodes.some((n) => n.slug === s && n.title));

  function drop(e: DragEvent, n: Node) {
    e.preventDefault();
    const zone = zoneOf(e);
    over = null;
    const from = e.dataTransfer?.getData(MIME);
    if (!drag || !from || from === n.slug || n.slug.startsWith(from + '/')) return;
    const segment = from.split('/').pop()!;
    if (zone === 'inside') return drag.onmove(from, `${n.slug}/${segment}`);
    const parent = parentOf(n.slug);
    if (parentOf(from) !== parent) return drag.onmove(from, parent ? `${parent}/${segment}` : segment);
    const order = nodes.map((x) => x.slug).filter((s) => s !== from);
    order.splice(order.indexOf(n.slug) + (zone === 'after' ? 1 : 0), 0, from);
    drag.onreorder(parent, pages(order));
  }

  // Alt+↑/↓ sube o baja la página entre sus hermanas: la alternativa de teclado al arrastre
  // (WCAG 2.5.7). Para mover a otra carpeta está "Mover" en la página.
  function keydown(e: KeyboardEvent, n: Node) {
    if (!drag || !e.altKey || (e.key !== 'ArrowUp' && e.key !== 'ArrowDown')) return;
    const order = pages(nodes.map((x) => x.slug));
    const i = order.indexOf(n.slug);
    const j = i + (e.key === 'ArrowUp' ? -1 : 1);
    if (i < 0 || j < 0 || j >= order.length) return;
    e.preventDefault();
    order.splice(j, 0, ...order.splice(i, 1));
    drag.onreorder(parentOf(n.slug), order);
  }

  // Los manejadores del arrastre, iguales para una página y para una carpeta sin página.
  function target(n: Node) {
    if (!drag) return {};
    return {
      ondragover: (e: DragEvent) => {
        if (!e.dataTransfer?.types.includes(MIME)) return;
        e.preventDefault();
        e.dataTransfer.dropEffect = 'move';
        over = { slug: n.slug, zone: zoneOf(e) };
      },
      ondragleave: () => {
        if (over?.slug === n.slug) over = null;
      },
      ondrop: (e: DragEvent) => drop(e, n),
    };
  }

  const dropClass = (n: Node) => (over?.slug === n.slug ? `drop-${over.zone}` : undefined);
</script>

<!-- Árbol del menú lateral (SPEC §15): carpetas y páginas, la actual resaltada, insignias N2/N3. -->
<ul class="tree">
  {#each nodes as n (n.slug)}
    <li>
      {#if n.title}
        <a
          class={['nav-item', dropClass(n)]}
          href={`${base}/p/${n.slug}`}
          aria-current={n.slug === current ? 'page' : undefined}
          title={drag ? t('tree.drag_hint') : undefined}
          draggable={drag ? 'true' : undefined}
          ondragstart={(e) => {
            e.dataTransfer?.setData(MIME, n.slug);
            if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move';
          }}
          ondragend={() => (over = null)}
          onkeydown={(e) => keydown(e, n)}
          {...target(n)}
        >
          {#if n.icon}<span class="tree-emoji" aria-hidden="true">{n.icon}</span>{:else}<Icon name={n.children.length ? 'folder' : 'page'} size={15} />{/if}
          <span class="nav-label">{n.title}</span>
          <LevelBadge level={n.level} />
        </a>
      {:else}
        <span class={['nav-item', 'nav-folder', dropClass(n)]} {...target(n)}><Icon name="folder" size={15} /> <span class="nav-label">{n.segment}</span></span>
      {/if}
      {#if n.children.length}<Self nodes={n.children} {base} {current} {drag} />{/if}
    </li>
  {/each}
</ul>
