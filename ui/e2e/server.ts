// Levanta Kudawiki para las pruebas E2E (SPEC §17): el binario real, con la UI embebida de
// ui/dist, en una carpeta temporal que se borra al terminar. No toca ninguna otra instalación.
import { spawn, spawnSync, type SpawnSyncOptions } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { BASE_URL, PASSWORD, PORT, SUPER } from './env.ts';

const root = resolve(import.meta.dirname, '../..');
const dir = mkdtempSync(join(tmpdir(), 'kudawiki-e2e-'));
const bin = join(dir, process.platform === 'win32' ? 'kudawiki.exe' : 'kudawiki');
const env = {
  ...process.env,
  KUDAWIKI_DATA_DIR: join(dir, 'data'),
  KUDAWIKI_MASTER_KEY_FILE: join(dir, 'keys', 'master.key'),
  KUDAWIKI_BASE_URL: BASE_URL,
  KUDAWIKI_LISTEN: `127.0.0.1:${PORT}`,
  KUDAWIKI_INIT_EMAIL: SUPER.email,
  KUDAWIKI_INIT_NAME: SUPER.name,
};

function run(cmd: string, args: string[], opts: SpawnSyncOptions = {}) {
  const r = spawnSync(cmd, args, { env, stdio: ['pipe', 'inherit', 'inherit'], ...opts });
  if (r.status !== 0) {
    rmSync(dir, { recursive: true, force: true });
    throw new Error(`${cmd} ${args.join(' ')} failed (${r.status ?? r.error})`);
  }
}

run('go', ['build', '-o', bin, './cmd/kudawiki'], { cwd: root });
// Sin terminal, init lee la contraseña de una línea de stdin.
run(bin, ['init'], { input: PASSWORD + '\n' });

const server = spawn(bin, ['serve'], { env, stdio: 'inherit' });
const stop = () => server.kill();
process.on('SIGINT', stop);
process.on('SIGTERM', stop);
server.on('exit', (code) => {
  rmSync(dir, { recursive: true, force: true });
  process.exit(code ?? 0);
});
