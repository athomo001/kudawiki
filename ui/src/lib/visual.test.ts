// Ida y vuelta del modo visual (SPEC §4.2): pasar por remark, con las mismas opciones que
// VisualEditor le da a Milkdown, no cambia la sintaxis propia de Kudawiki (§4.10). Corre con
// `pnpm test` (node --test).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { unified } from 'unified';
import remarkParse from 'remark-parse';
import remarkStringify from 'remark-stringify';
import remarkGfm from 'remark-gfm';
import { toVisual, fromVisual } from './visual.ts';

async function roundTrip(md: string): Promise<string> {
  const out = await unified()
    .use(remarkParse)
    .use(remarkGfm)
    .use(remarkStringify, { bullet: '-', emphasis: '*', strong: '*', rule: '-' })
    .process(toVisual(md));
  return fromVisual(String(out));
}

const cases: Record<string, string> = {
  'wiki links': 'Ver [[red/vpn]] y [[red/vpn|la VPN]] o [[Ctrl+C]].',
  callout: '> [!NOTE]\n> hola',
  'quote with class': '> aviso\n{.is-info}',
  'nested tabs with code': ':::tabs\n::: tab Linux\n- a\n- b\n:::\n::: tab Windows\n```sh\ndir\n```\n:::\n:::',
  'code keeps escapes': '```js\nconst x = "\\[\\[no tocar";\n```',
  math: 'Texto $x^2$ y\n\n$$\na\n$$',
  footnote: 'Nota[^1]\n\n[^1]: pie',
  'lists and table': '- uno\n- dos\n\n1. a\n2. b\n\n| a | b |\n| - | - |\n| 1 | 2 |',
  emphasis: '**negrita**, *cursiva* y ~~tachado~~',
  'hidden text': '||secreto||',
};

for (const [name, md] of Object.entries(cases)) {
  test(name, async () => {
    assert.equal((await roundTrip(md)).trim(), md.trim());
  });
}

test('a raw block stays raw when edited around it', () => {
  const v = toVisual('antes\n\n:::steps\n1. a\n:::\n\ndespués');
  assert.match(v, /^````?kudawiki\n:::steps\n1\. a\n:::\n````?$/m);
  assert.equal(fromVisual(v), 'antes\n\n:::steps\n1. a\n:::\n\ndespués');
});
