import { ApiError, api } from './api';

// Passkeys (SPEC §8.1) con la API del navegador, sin librerías: el servidor manda las opciones en
// el JSON de WebAuthn y recibe la credencial tal como la da toJSON().

type Options = { publicKey: PublicKeyCredentialCreationOptionsJSON | PublicKeyCredentialRequestOptionsJSON };

// El navegador sabe leer y escribir ese JSON (Chrome 129, Firefox 119, Safari 18.4 en adelante).
export function passkeysSupported(): boolean {
  return typeof PublicKeyCredential !== 'undefined' && typeof PublicKeyCredential.parseCreationOptionsFromJSON === 'function';
}

// Lo que el navegador corta (la persona cancela, se vence el tiempo, ya está registrada en ese
// aparato) llega como ApiError, para mostrarlo igual que los errores del servidor.
async function ceremony(run: () => Promise<Credential | null>): Promise<unknown> {
  let cred: Credential | null;
  try {
    cred = await run();
  } catch (e) {
    const name = e instanceof DOMException ? e.name : '';
    throw new ApiError(0, name === 'InvalidStateError' ? 'passkey_exists' : 'passkey_cancelled');
  }
  if (!(cred instanceof PublicKeyCredential)) throw new ApiError(0, 'passkey_cancelled');
  return cred.toJSON();
}

// createPasskey registra una passkey nueva con el nombre que le dio la persona.
export async function createPasskey(name: string): Promise<void> {
  const opts = await api<Options>('POST', '/me/passkeys/begin');
  const publicKey = PublicKeyCredential.parseCreationOptionsFromJSON(opts.publicKey as PublicKeyCredentialCreationOptionsJSON);
  const credential = await ceremony(() => navigator.credentials.create({ publicKey }));
  await api('POST', '/me/passkeys/finish', { name, credential });
}

// signWithPasskey pide las opciones en begin, hace firmar el desafío y manda la firma a finish.
// Sirve para el login (con o sin contraseña antes) y para la reautenticación.
export async function signWithPasskey(begin: string, finish: string): Promise<void> {
  const opts = await api<Options>('POST', begin);
  const publicKey = PublicKeyCredential.parseRequestOptionsFromJSON(opts.publicKey as PublicKeyCredentialRequestOptionsJSON);
  await api('POST', finish, await ceremony(() => navigator.credentials.get({ publicKey })));
}
