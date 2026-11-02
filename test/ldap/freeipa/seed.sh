#!/bin/sh
# Siembra FreeIPA (corre dentro del contenedor, con `docker compose exec`). Idempotente: cada
# alta se salta si ya existe. Mismos datos que los otros directorios; tomas queda deshabilitado
# (nsAccountLock) y la cuenta de servicio es una cuenta de sistema, como lo recomienda FreeIPA.
set -eu
suffix=dc=kudawiki,dc=test
echo "$ADMIN_PASSWORD" | kinit admin >/dev/null

user() { # login nombre apellido
  ipa user-show "$1" >/dev/null 2>&1 && return
  echo "$USER_PASSWORD" | ipa user-add "$1" --first="$2" --last="$3" --email="$1@kudawiki.test" --password >/dev/null
  # Una contraseña puesta por el admin nace vencida; en la prueba tiene que servir para entrar.
  ipa user-mod "$1" --setattr=krbPasswordExpiration=20991231000000Z >/dev/null
}
group() { ipa group-show "$1" >/dev/null 2>&1 || ipa group-add "$1" >/dev/null; }

user lucia Lucía Rojas
user marta Marta Díaz
user tomas Tomás Vera
user dani Dani Soto
ipa user-show tomas | grep -q "Account disabled: True" || ipa user-disable tomas >/dev/null
group ti-redes
group gg-wiki-ti
ipa group-add-member ti-redes --users=lucia >/dev/null || true
# Un valor por opción: ipa toma "marta,tomas" como un solo nombre. Falla si ya es miembro.
ipa group-add-member gg-wiki-ti --groups=ti-redes --users=marta --users=tomas >/dev/null || true

ldapadd -x -c -H ldapi://%2frun%2fslapd-KUDAWIKI-TEST.socket -D "cn=Directory Manager" -w "$ADMIN_PASSWORD" 2>&1 <<EOF | grep -v "Already exists" || true
dn: uid=svc-kudawiki,cn=sysaccounts,cn=etc,$suffix
objectClass: account
objectClass: simpleSecurityObject
uid: svc-kudawiki
userPassword: $USER_PASSWORD
passwordExpirationTime: 20991231000000Z
nsIdleTimeout: 0
EOF
cp /etc/ipa/ca.crt /certs-out/freeipa-ca.crt
