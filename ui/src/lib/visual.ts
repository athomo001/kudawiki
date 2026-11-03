// Ida y vuelta del modo visual (SPEC §4.2): Milkdown lee y escribe Markdown con remark, que no
// conoce la sintaxis propia de Kudawiki (§4.10). Lo que remark cambiaría de forma que el render
// del servidor ya no lo entienda se protege: las directivas (:::tabs, :::steps…) y las citas con
// clase ({.is-info}) entran al modo visual como un bloque de código "kudawiki", que se edita como
// texto y sale tal cual. Lo que solo escapa ([[enlace]], > [!NOTE]) se des-escapa al volver.

export const RAW_LANG = 'kudawiki';

const fenceRe = /^\s*(`{3,}|~{3,})/;
const directiveOpen = /^:::\s*\S/;
const directiveClose = /^:::\s*$/;
const attrLine = /^\{\.[a-z0-9-]+[^}]*\}\s*$/;

// fenceFor devuelve un cerco más largo que cualquiera de adentro.
function fenceFor(body: string): string {
  const longest = Math.max(2, ...[...body.matchAll(/`{3,}/g)].map((m) => m[0].length));
  return '`'.repeat(longest + 1);
}

function raw(lines: string[]): string[] {
  const body = lines.join('\n');
  const f = fenceFor(body);
  return [`${f}${RAW_LANG}`, body, f];
}

// Fences sigue los bloques de código línea a línea: dentro de uno nada se convierte.
class Fences {
  private open = '';
  // inside dice si la línea es parte de un bloque de código (la que lo abre y la que lo cierra, también).
  inside(line: string): boolean {
    const f = line.match(fenceRe)?.[1];
    if (this.open) {
      if (f && f[0] === this.open[0] && f.length >= this.open.length) this.open = '';
      return true;
    }
    if (f) {
      this.open = f;
      return true;
    }
    return false;
  }
}

// toVisual prepara el Markdown para Milkdown.
export function toVisual(md: string): string {
  const lines = md.split('\n');
  const out: string[] = [];
  const fences = new Fences();
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i] ?? '';
    if (fences.inside(line)) {
      out.push(line);
      continue;
    }
    // Una directiva, con sus anidadas, hasta el ::: que la cierra.
    if (directiveOpen.test(line)) {
      let depth = 0;
      let j = i;
      for (; j < lines.length; j++) {
        const l = lines[j] ?? '';
        if (directiveOpen.test(l)) depth++;
        else if (directiveClose.test(l)) depth--;
        if (depth === 0) break;
      }
      const end = Math.min(j, lines.length - 1);
      out.push(...raw(lines.slice(i, end + 1)));
      i = end;
      continue;
    }
    // Una cita seguida de su clase: la cita entera y la línea de la clase.
    if (line.startsWith('>')) {
      let j = i;
      while (j + 1 < lines.length && (lines[j + 1] ?? '').startsWith('>')) j++;
      if (attrLine.test(lines[j + 1] ?? '')) {
        out.push(...raw(lines.slice(i, j + 2)));
        i = j + 1;
        continue;
      }
    }
    out.push(line);
  }
  return out.join('\n');
}

const rawOpen = new RegExp('^(`{3,})' + RAW_LANG + '\\s*$');

// fromVisual devuelve el Markdown que escribió Milkdown con la sintaxis de Kudawiki intacta. Los
// bloques "kudawiki" salen sin su cerco. El des-escape no toca los bloques de código: ahí remark
// no escapa, y un "\[" escrito es del autor.
export function fromVisual(md: string): string {
  const lines = md.split('\n');
  const out: string[] = [];
  const fences = new Fences();
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i] ?? '';
    const open = line.match(rawOpen)?.[1];
    if (open) {
      const end = lines.findIndex((l, j) => j > i && l.trim() === open);
      if (end > 0) {
        out.push(...lines.slice(i + 1, end));
        i = end;
        continue;
      }
    }
    if (fences.inside(line)) {
      out.push(line);
      continue;
    }
    out.push(line.replace(/\\\[\\\[/g, '[[').replace(/^(\s*>\s*)\\\[!/, '$1[!'));
  }
  return out.join('\n');
}
