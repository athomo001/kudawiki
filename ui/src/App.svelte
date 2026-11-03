<script lang="ts">
  import { api, ApiError, canEdit, canWrite, codeOf, isPersonalSlug, type Area as AreaT, type Me, type ReadingPrefs } from './lib/api';
  import { viewer } from './lib/watermark.svelte';
  import { setLocale, errorText, t } from './lib/i18n/index.svelte';
  import { applyPrefs } from './lib/prefs';
  import { navigate, route } from './lib/router.svelte';
  import TopBar from './components/TopBar.svelte';
  import ReauthDialog from './components/ReauthDialog.svelte';
  import Login from './Login.svelte';
  import MfaSetup from './pages/MfaSetup.svelte';
  import Home from './pages/Home.svelte';
  import MyHome from './pages/MyHome.svelte';
  import Profile from './pages/Profile.svelte';
  import AdminNav from './components/AdminNav.svelte';
  import People from './pages/People.svelte';
  import Areas from './pages/Areas.svelte';
  import AdminArea from './pages/AdminArea.svelte';
  import Templates from './pages/Templates.svelte';
  import Audit from './pages/Audit.svelte';
  import Health from './pages/Health.svelte';
  import Appearance from './pages/Appearance.svelte';
  import Email from './pages/Email.svelte';
  import Directory from './pages/Directory.svelte';
  import SiteFooter from './components/SiteFooter.svelte';
  import Area from './pages/Area.svelte';
  import Sealed from './pages/Sealed.svelte';
  import SealedList from './pages/SealedList.svelte';
  import Invite from './pages/Invite.svelte';
  import Forgot from './pages/Forgot.svelte';
  import Reset from './pages/Reset.svelte';
  import Unsubscribe from './pages/Unsubscribe.svelte';
  import NotFound from './pages/NotFound.svelte';

  let me = $state<Me | null>(null);
  let ready = $state(false);
  let error = $state('');
  let avatarRev = $state(0);

  // El idioma y la lectura siguen al perfil; sin sesión, el navegador y la fuente por defecto.
  $effect(() => {
    if (me) setLocale(me.locale);
    applyPrefs(me?.reading_prefs);
  });

  async function loadMe() {
    error = '';
    try {
      me = await api<Me>('GET', '/auth/me');
      viewer.name = me.full_name;
      viewer.email = me.email;
    } catch (e) {
      me = null;
      // 401 es el estado normal sin sesión; cualquier otra cosa se muestra.
      if (!(e instanceof ApiError && e.status === 401)) error = errorText(codeOf(e));
    } finally {
      ready = true;
    }
  }

  async function logout() {
    try {
      await api('POST', '/auth/logout');
      me = null;
    } catch (e) {
      error = errorText(codeOf(e));
    }
  }

  // El perfil cambió el nombre o la foto: se vuelve a leer y la foto se pide con otra URL.
  async function profileChanged() {
    await loadMe();
    avatarRev++;
  }

  // El cambio se ve al instante, sin recargar (SPEC §15); si el servidor lo rechaza, se deshace.
  async function savePrefs(change: { locale?: 'es' | 'en'; reading_prefs?: ReadingPrefs }) {
    if (!me) return;
    const before = { locale: me.locale, reading_prefs: me.reading_prefs };
    Object.assign(me, change);
    error = '';
    try {
      await api('PATCH', '/me', change);
    } catch (e) {
      if (me) Object.assign(me, before);
      error = errorText(codeOf(e));
    }
  }

  // Lo que no calza o no corresponde muestra el mismo "no encontrado" (SPEC §3.3).
  const areaPath = $derived(route.path.match(/^\/w\/(~?[a-z0-9-]+)(?:\/(.*))?$/));
  // Administración (diseño "Kudawiki — Administración", §15): solo el superadmin; para el resto
  // es una ruta más que no existe.
  const admin = $derived(!!me?.is_global_admin && (route.path === '/admin' || route.path.startsWith('/admin/')));
  const adminArea = $derived(route.path.match(/^\/admin\/areas\/([0-9A-Z]{26})(\/members)?$/));
  // Un sellado fuera de su área (§6): el superadmin no es miembro, y el aviso al autor lleva aquí.
  // Si quien lo abre es del área, se ve dentro de ella, con su árbol.
  const sealedPath = $derived(route.path.match(/^\/sealed\/([0-9A-Z]{26})$/));
  const ldapTab = $derived(route.path.match(/^\/admin\/ldap(?:\/(rules|sync|test))?$/));
  // El espacio personal (§7) no viene entre las áreas: el propio se arma con el rol de su dueño
  // (admin) y uno ajeno, solo para el superadmin, como lector y en modo auditoría. Si no existe,
  // el servidor responde el 404 de siempre.
  const area = $derived.by((): AreaT | undefined => {
    const slug = areaPath?.[1];
    if (!me || !slug) return undefined;
    const space = { slug, description: '', viewers_can_propose: false, require_change_summary: false, personal: true };
    if (slug === me.personal.slug) return { ...space, name: t('personal.title'), role: 'admin' };
    if (isPersonalSlug(slug) && me.is_global_admin) return { ...space, name: t('personal.audit_title'), role: 'viewer', audit: true };
    return me.workspaces.find((w) => w.slug === slug);
  });
  // El editor tiene su propia cabecera (diseño aprobado, SPEC §15): mientras se edita no hay barra superior.
  // Igual que en Area.svelte: un lector abre el editor solo para proponer (§19.5).
  const editing = $derived.by(() => {
    const sub = areaPath?.[2] ?? '';
    if (!area) return false;
    return (sub === 'new' && canEdit(area)) || (sub.startsWith('edit/') && canWrite(area)) || /^proposals\/[^/]+\/edit$/.test(sub);
  });

  // Al entrar se cae directo en la wiki (SPEC §15): en la última área abierta en este navegador,
  // si la persona sigue siendo miembro, o en la primera. Inicio queda solo para quien no tiene
  // áreas. El identificador del área ya está en el historial del navegador: guardarlo no agrega nada.
  const LAST_AREA_KEY = 'kw.last_area';
  $effect(() => {
    if (!me || route.path !== '/') return;
    let last: string | null = null;
    try {
      last = localStorage.getItem(LAST_AREA_KEY);
    } catch {
      // sin almacenamiento (modo privado estricto): la primera área
    }
    const to = me.workspaces.find((w) => w.slug === last) ?? me.workspaces[0];
    if (to) navigate(`/w/${to.slug}`, { replace: true });
  });
  $effect(() => {
    if (!area) return;
    try {
      localStorage.setItem(LAST_AREA_KEY, area.slug);
    } catch {
      // ídem: solo se pierde el recuerdo
    }
  });

  loadMe();
</script>

{#if route.path === '/invite'}
  <Invite />
  <SiteFooter />
{:else if route.path === '/reset'}
  <Reset />
  <SiteFooter />
{:else if route.path === '/unsubscribe'}
  <Unsubscribe />
  <SiteFooter />
{:else if route.path === '/forgot'}
  <Forgot />
  <SiteFooter />
{:else if ready}
  <!-- También en la configuración obligatoria del 2FA: registrar una passkey pide reautenticarse. -->
  {#if me}<ReauthDialog totp={me.totp.enabled} passkeys={me.passkeys.count > 0 && me.passkeys.available} />{/if}
  {#if me?.mfa_setup_required}
    <MfaSetup email={me.email} ondone={loadMe} onlogout={logout} />
    <SiteFooter />
  {:else if me}
    {#if !editing}<TopBar {me} {area} {avatarRev} onprefs={savePrefs} onlogout={logout} />{/if}
    {#if error}<p class="error banner" role="alert">{error}</p>{/if}
    {#if area}
      <Area {area} sub={areaPath?.[2] ?? ''} selfId={me.id} onareachanged={loadMe} />
    {:else if admin}
      {@const site = `${t('nav.admin')} · ${t('admin.group_site')}`}
      {@const supervision = `${t('nav.admin')} · ${t('admin.group_supervision')}`}
      <div class="area">
        <AdminNav />
        <main class="area-main">
          <div class="view admin-view">
            {#if route.path === '/admin' || route.path === '/admin/users'}
              <People selfId={me.id} />
            {:else if route.path === '/admin/areas'}
              <Areas onchanged={loadMe} />
            {:else if adminArea}
              <AdminArea id={adminArea[1] ?? ''} tab={adminArea[2] ? 'members' : 'general'} selfId={me.id} onchanged={loadMe} />
            {:else if ldapTab}
              <!-- Una sola rama para las cuatro pestañas: el formulario sin guardar no se pierde al cambiar de pestaña. -->
              <Directory tab={(ldapTab[1] as 'rules' | 'sync' | 'test' | undefined) ?? 'connection'} />
            {:else if route.path === '/admin/appearance'}
              <Appearance />
            {:else if route.path === '/admin/email'}
              <Email email={me.email} />
            {:else if route.path === '/admin/templates'}
              <Templates endpoint="/admin/templates" context={site} global />
            {:else if route.path === '/admin/health'}
              <Health endpoint="/admin/health" context={supervision} global />
            {:else if route.path === '/admin/sealed'}
              <SealedList context={supervision} />
            {:else if route.path === '/admin/audit'}
              <Audit endpoint="/admin/audit" context={supervision} global />
            {:else}
              <NotFound />
            {/if}
          </div>
          <SiteFooter />
        </main>
      </div>
    {:else}
      <main class="view">
        {#if route.path === '/'}
          {#if !me.workspaces.length}<Home {me} />{/if}
        {:else if route.path === '/me'}
          <MyHome {me} />
        {:else if route.path === '/me/profile'}
          <Profile {me} {avatarRev} onprefs={savePrefs} onchanged={profileChanged} />
        {:else if sealedPath}
          <Sealed id={sealedPath[1] ?? ''} memberOf={[me.personal.slug, ...me.workspaces.map((w) => w.slug)]} />
        {:else}
          <NotFound />
        {/if}
      </main>
      <SiteFooter />
    {/if}
  {:else}
    <Login onlogin={loadMe} />
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <SiteFooter />
  {/if}
{/if}
