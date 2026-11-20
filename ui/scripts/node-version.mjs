// El CI y el Dockerfile compilan con el Node de .node-version (SPEC §15, §19.29). Uno distinto en
// otro equipo compila igual, pero lo dice: las diferencias aparecerían tarde (QA-16).
import { readFileSync } from 'node:fs';

const wanted = readFileSync(new URL('../.node-version', import.meta.url), 'utf8').trim();
if (process.versions.node.split('.')[0] !== wanted) {
  console.warn(`\x1b[33mwarning: Node ${process.versions.node}; the CI and the Docker image build with Node ${wanted} (ui/.node-version)\x1b[0m`);
}
