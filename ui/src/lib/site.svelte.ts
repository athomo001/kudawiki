// Marca de la empresa (SPEC §15.1). El servidor la deja en index.html (#kw-site) junto con los
// colores, así la primera pintura ya sale con el logo y el título de la empresa, sin pedirlos.
export type Site = {
  title: string;
  company: string;
  login_message: string;
  show_powered_by: boolean;
  header_dark: boolean; // sobre la barra va texto claro: ahí se usa el logo para fondo oscuro
  logo: string; // versión del logo subido, o vacío (logo de Kudawiki)
  logo_dark: string;
  password_reset: boolean; // el login ofrece "¿Olvidaste tu contraseña?" (§8.5.1): hay correo
  passkeys: boolean; // el login ofrece "Entrar con una passkey" (§8.1): base_url las admite
  directory: boolean; // el login acepta un usuario del directorio, no solo un correo (§8.6.2)
  password_help_url: string; // la página de la empresa para la contraseña del directorio (§8.6.3)
};

const fallback: Site = {
  title: 'Kudawiki',
  company: '',
  login_message: '',
  show_powered_by: true,
  header_dark: true,
  logo: '',
  logo_dark: '',
  password_reset: false,
  passkeys: false,
  directory: false,
  password_help_url: '',
};

function embedded(): Site {
  try {
    return { ...fallback, ...JSON.parse(document.getElementById('kw-site')?.textContent ?? '{}') };
  } catch {
    return fallback; // en desarrollo con Vite no hay #kw-site
  }
}

export const site = $state<Site>(embedded());

// logoURL elige el logo para un fondo: sobre uno oscuro, el de fondo oscuro si se subió; sobre
// uno claro, el normal. Si solo hay uno, va ese. Vacío es el de Kudawiki.
export function logoURL(dark: boolean): string {
  if (site.logo_dark && (dark || !site.logo)) return `/_site/logo-dark?v=${site.logo_dark}`;
  return site.logo ? `/_site/logo?v=${site.logo}` : '';
}

// La pestaña dice qué se lee y de qué wiki: "Conexión VPN · Wiki ACME" (§15.1.1).
export function setDocumentTitle(page?: string) {
  document.title = page ? `${page} · ${site.title}` : site.title;
}
