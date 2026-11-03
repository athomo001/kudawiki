# Seguridad de Kudawiki

Kudawiki guarda documentación interna, con áreas aisladas entre sí y documentos sellados cifrados
(SPEC §3, §6). Una falla de seguridad puede exponer información de otra área o de un sellado, así
que se trata antes que cualquier otro trabajo.

## Cómo reportar una vulnerabilidad

**No abras un issue ni un PR público.** Escribe al dueño del repositorio por un canal privado.
*(Falta definir el correo dedicado de seguridad: SPEC §19.41.)*

Incluye, si puedes:

- la versión (la etiqueta `vX.Y.Z` o el commit) y cómo está instalada (Docker o systemd);
- qué se puede hacer y quién puede hacerlo: un lector, un editor, alguien sin sesión, alguien de
  otra área;
- los pasos para reproducirlo, o una prueba de concepto contra una instalación propia;
- si involucra datos reales, **no los incluyas**: describe el tipo de dato.

## Qué pasa después

| Paso | Plazo objetivo |
| :--- | :--- |
| Acuse de recibo | 3 días hábiles |
| Corrección de una vulnerabilidad **crítica** | 7 días |
| Corrección de una vulnerabilidad **alta** | 30 días |
| Media o baja | En la próxima versión |

Es **crítica** si alguien sin cuenta o de otra área lee o cambia contenido que no le corresponde,
si se puede descifrar un sellado sin su contraseña o sin la MASTER_KEY, o si se puede tomar una
cuenta de administración. Es **alta** si quien ya tiene cuenta supera su nivel dentro de su área,
si una ruta, una búsqueda o un aviso delata que existe algo que no ve (SPEC §17), o si se salta el
segundo factor.

La corrección sale como versión nueva. Sus notas dicen que trae un arreglo de seguridad, a quién
afecta y qué hacer; el detalle técnico se publica cuando las instalaciones hayan podido
actualizar.

## Versiones con soporte

Solo la última versión `1.x`. Se puede saltar de cualquier `1.x` a la última (SPEC §13.9).

## Verificar lo que se descarga

Cada release trae los binarios, el SBOM (`kudawiki-sbom.cdx.json`, CycloneDX), `SHA256SUMS`
firmado con cosign sin llave (`SHA256SUMS.sigstore.json`) y la imagen firmada en `ghcr.io`, con el
SBOM adjunto. Los comandos para comprobarlos están en
[docs/operacion/endurecimiento.md](docs/operacion/endurecimiento.md).

## Lo que ya revisa la propia wiki

`kudawiki doctor --security` revisa permisos de archivos, dónde está la MASTER_KEY, TLS, admins
sin segundo factor, que exista un superadmin local para emergencias y los respaldos.
