<script lang="ts">
  import type { DiffLine } from '../lib/api';
  import { t } from '../lib/i18n/index.svelte';

  // Diff por líneas con lo que cambió dentro de cada una (SPEC §4.3). El signo − / + es visible y
  // el tramo cambiado va en negrita: el color no es el único portador del cambio
  // (skills/UX-Svelte-Patterns.md §8.4). Las líneas iguales lejos de un cambio se pliegan.
  // numbered: el número de línea del texto nuevo, como en la bandeja de propuestas (§5.2).
  let { lines, context = 3, numbered = false }: { lines: DiffLine[]; context?: number; numbered?: boolean } = $props();

  // Número de cada línea en el texto nuevo; las borradas no tienen.
  const numbers = $derived.by(() => {
    let n = 0;
    return lines.map((d) => (d.op === '-' ? '' : String(++n)));
  });

  type Row = { line: DiffLine; n: string } | { skipped: number };
  const rows = $derived.by(() => {
    const near = lines.map(() => false);
    lines.forEach((d, i) => {
      if (d.op === '=') return;
      for (let k = Math.max(0, i - context); k <= Math.min(lines.length - 1, i + context); k++) near[k] = true;
    });
    const out: Row[] = [];
    for (let i = 0; i < lines.length; ) {
      const line = lines[i];
      if (near[i] && line) {
        out.push({ line, n: numbers[i] ?? '' });
        i++;
        continue;
      }
      let j = i;
      while (j < lines.length && !near[j]) j++;
      out.push({ skipped: j - i });
      i = j;
    }
    return out;
  });
</script>

<pre class="diff" class:numbered>{#each rows as r, i (i)}{#if 'skipped' in r}<span class="diff-line diff-skip">⋯ {t('diff.skipped', { n: String(r.skipped) })}</span>{:else if r.line.op === '+'}<ins class="diff-line">{@render num(r.n)}+ {@render text(r.line)}</ins>{:else if r.line.op === '-'}<del class="diff-line">{@render num(r.n)}− {@render text(r.line)}</del>{:else}<span class="diff-line">{@render num(r.n)}  {r.line.text}</span>{/if}{/each}</pre>

{#snippet num(n: string)}{#if numbered}<span class="diff-num" aria-hidden="true">{n}</span>{/if}{/snippet}

{#snippet text(d: DiffLine)}{#if d.parts}{#each d.parts as p, j (j)}{#if p.changed}<strong class="diff-hl">{p.text}</strong>{:else}{p.text}{/if}{/each}{:else}{d.text}{/if}{/snippet}
