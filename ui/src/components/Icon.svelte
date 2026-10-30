<script lang="ts" module>
  // Trazos de los íconos del diseño aprobado (SPEC §15), 24×24 de línea. Van en el bundle:
  // sin CDN ni fuentes de íconos.
  const paths = {
    home: 'M3 11l9-7 9 7v9H3z',
    plus: 'M12 5v14M5 12h14',
    folder: 'M3 6h6l2 2h10v11H3z',
    page: 'M6 3h8l4 4v14H6z',
    edit: 'M4 20h4L19 9l-4-4L4 16z',
    history: 'M3 12a9 9 0 1 0 3-6.7L3 8M3 3v5h5M12 7v5l3 2',
    code: 'M8 7l-5 5 5 5M16 7l5 5-5 5',
    people:
      'M9 4.5a3.5 3.5 0 1 1 0 7a3.5 3.5 0 0 1 0-7M2.5 20c.8-3.5 3.5-5.5 6.5-5.5s5.7 2 6.5 5.5M16 4.5a3.5 3.5 0 0 1 0 7M18.5 14.8c1.6.8 2.7 2.6 3 5.2',
    chevron: 'M6 9l6 6 6-6',
    back: 'M15 6l-6 6 6 6',
    search: 'M11 4a7 7 0 1 1 0 14a7 7 0 0 1 0-14M20 20l-3.5-3.5',
    list: 'M4 6h16M4 12h16M4 18h10',
    trash: 'M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13',
    close: 'M6 6l12 12M18 6L6 18',
    settings: 'M4 7h10M18 7h2M4 17h4M12 17h8M14 4v6M8 14v6',
    print: 'M7 9V3h10v6M7 17H4v-7h16v7h-3M7 14h10v7H7z',
    pulse: 'M3 12h4l3-7 4 14 3-7h4',
    speaker: 'M4 9h4l5-4v14l-5-4H4zM16.5 8.5a5 5 0 0 1 0 7M19 6a8.5 8.5 0 0 1 0 12',
    pause: 'M8 5v14M16 5v14',
    stop: 'M6 6h12v12H6z',
    help: 'M12 3a9 9 0 1 1 0 18a9 9 0 0 1 0-18M9.5 9.5a2.5 2.5 0 1 1 3.5 2.3c-.6.3-1 .9-1 1.6v.6M12 17v.5',
    copy: 'M9 9h11v11H9zM5 15H4V4h11v1',
    check: 'M5 12l5 5L20 7',
  };
  export type IconName = keyof typeof paths | 'more';

  // El mismo ícono, armado fuera de un componente: lib/enhance.ts lo agrega al HTML de la página.
  export function iconSvg(name: keyof typeof paths): SVGSVGElement {
    const ns = 'http://www.w3.org/2000/svg';
    const svg = document.createElementNS(ns, 'svg');
    svg.setAttribute('class', `icon icon-${name}`);
    svg.setAttribute('width', '18');
    svg.setAttribute('height', '18');
    svg.setAttribute('viewBox', '0 0 24 24');
    svg.setAttribute('aria-hidden', 'true');
    const path = document.createElementNS(ns, 'path');
    path.setAttribute('d', paths[name]);
    svg.append(path);
    return svg;
  }
</script>

<script lang="ts">
  let { name, size = 18 }: { name: IconName; size?: number } = $props();
</script>

<svg class="icon" width={size} height={size} viewBox="0 0 24 24" aria-hidden="true">
  {#if name === 'more'}
    <circle cx="12" cy="5" r="1.6" /><circle cx="12" cy="12" r="1.6" /><circle cx="12" cy="19" r="1.6" />
  {:else}
    <path d={paths[name]} />
  {/if}
</svg>
