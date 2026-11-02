#!/bin/sh
# Siembra 389 DS (corre dentro del contenedor, con `docker compose exec`). Idempotente: el backend
# y el plugin se crean solo si faltan, y ldapadd -c salta lo que ya existe. Mismos datos que
# OpenLDAP; tomas está bloqueado con nsAccountLock, como lo hace 389.
set -eu
suffix=dc=kudawiki,dc=test
dm() { "$@" -x -H ldapi://%2fdata%2frun%2fslapd-localhost.socket -D "cn=Directory Manager" -w "$DS_DM_PASSWORD"; }

dsconf localhost backend suffix list | grep -q "$suffix" ||
  dsconf localhost backend create --suffix "$suffix" --be-name userroot
# memberOf: activable sin reiniciar con los plugins dinámicos.
dsconf localhost config replace nsslapd-dynamic-plugins=on >/dev/null
dsconf localhost plugin memberof status | grep -q "enabled" ||
  dsconf localhost plugin memberof enable

sed "s/PASSWORD/$USER_PASSWORD/" <<EOF | dm ldapadd -c 2>&1 | grep -v "Already exists" || true
dn: $suffix
objectClass: domain
dc: kudawiki
aci: (targetattr!="userPassword")(version 3.0; acl "Kudawiki: la cuenta de servicio lee"; allow (read,search,compare) userdn="ldap:///cn=svc-kudawiki,$suffix";)
aci: (targetattr="*")(version 3.0; acl "Kudawiki: cada uno se lee"; allow (read,search,compare) userdn="ldap:///self";)

dn: ou=Personas,$suffix
objectClass: organizationalUnit
ou: Personas

dn: ou=Grupos,$suffix
objectClass: organizationalUnit
ou: Grupos

dn: cn=svc-kudawiki,$suffix
objectClass: applicationProcess
objectClass: simpleSecurityObject
cn: svc-kudawiki
userPassword: PASSWORD

dn: uid=lucia,ou=Personas,$suffix
objectClass: inetOrgPerson
uid: lucia
cn: Lucía Rojas
sn: Rojas
mail: lucia@kudawiki.test
userPassword: PASSWORD

dn: uid=marta,ou=Personas,$suffix
objectClass: inetOrgPerson
uid: marta
cn: Marta Díaz
sn: Díaz
mail: marta@kudawiki.test
userPassword: PASSWORD

dn: uid=tomas,ou=Personas,$suffix
objectClass: inetOrgPerson
uid: tomas
cn: Tomás Vera
sn: Vera
mail: tomas@kudawiki.test
userPassword: PASSWORD

dn: uid=dani,ou=Personas,$suffix
objectClass: inetOrgPerson
uid: dani
cn: Dani Soto
sn: Soto
mail: dani@kudawiki.test
userPassword: PASSWORD

dn: cn=TI-Redes,ou=Grupos,$suffix
objectClass: groupOfNames
cn: TI-Redes
member: uid=lucia,ou=Personas,$suffix

dn: cn=GG-Wiki-TI,ou=Grupos,$suffix
objectClass: groupOfNames
cn: GG-Wiki-TI
member: cn=TI-Redes,ou=Grupos,$suffix
member: uid=marta,ou=Personas,$suffix
member: uid=tomas,ou=Personas,$suffix
EOF

dm ldapmodify >/dev/null <<EOF
dn: uid=tomas,ou=Personas,$suffix
changetype: modify
replace: nsAccountLock
nsAccountLock: TRUE
EOF
