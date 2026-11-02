#!/bin/sh
# Provisiona el dominio KUDAWIKI.TEST la primera vez y siembra los datos de prueba. Idempotente:
# cada alta se salta si ya existe. Los datos son los mismos que en OpenLDAP (lucia en GG-Wiki-TI
# por TI-Redes, marta directo, tomas deshabilitado, dani sin grupo) más pablo, que tiene que
# cambiar la contraseña (código 773 de AD).
set -eu
dc=DC=kudawiki,DC=test
# La marca de "listo" se rehace en cada arranque: el healthcheck espera a la siembra.
rm -f /var/lib/samba/seeded
if [ ! -f /var/lib/samba/private/sam.ldb ]; then
  rm -f /etc/samba/smb.conf
  samba-tool domain provision --realm=KUDAWIKI.TEST --domain=KUDAWIKI --server-role=dc --dns-backend=SAMBA_INTERNAL \
    --adminpass="$ADMIN_PASSWORD" --use-rfc2307 \
    --option="tls enabled = yes" \
    --option="tls keyfile = /var/lib/samba/private/tls/kudawiki.key" \
    --option="tls certfile = /var/lib/samba/private/tls/kudawiki.crt" \
    --option="tls cafile = /var/lib/samba/private/tls/ca.crt"
fi
cp /var/lib/samba/private/krb5.conf /etc/krb5.conf

# Las llaves vienen de un volumen montado: Samba exige que la privada sea 0600 y de root.
mkdir -p /var/lib/samba/private/tls
cp /certs/ca.crt /var/lib/samba/private/tls/ca.crt
cp /certs/samba.crt /var/lib/samba/private/tls/kudawiki.crt
cp /certs/samba.key /var/lib/samba/private/tls/kudawiki.key
chmod 600 /var/lib/samba/private/tls/kudawiki.key

# La complejidad de contraseñas de AD queda como viene: la de prueba la cumple.
ou() { ldbsearch -H /var/lib/samba/private/sam.ldb -b "OU=$1,$dc" -s base dn >/dev/null 2>&1 || samba-tool ou add "OU=$1,$dc"; }
user() { # login nombre apellido [opciones]
  login=$1 given=$2 sur=$3
  shift 3
  samba-tool user show "$login" >/dev/null 2>&1 ||
    samba-tool user create "$login" "$USER_PASSWORD" --userou=OU=Personas --given-name="$given" --surname="$sur" \
      --mail-address="$login@kudawiki.test" "$@"
}
group() { samba-tool group show "$1" >/dev/null 2>&1 || samba-tool group add "$1" --groupou=OU=Grupos; }
member() { samba-tool group listmembers "$1" | grep -qx "$2" || samba-tool group addmembers "$1" "$2"; }

ou Personas
ou Grupos
ou Servicios
samba-tool user show svc-kudawiki >/dev/null 2>&1 ||
  samba-tool user create svc-kudawiki "$USER_PASSWORD" --userou=OU=Servicios --given-name=Servicio --surname=Kudawiki
user lucia Lucía Rojas
user marta Marta Díaz
user tomas Tomás Vera
user dani Dani Soto
user pablo Pablo Mora --must-change-at-next-login
samba-tool user disable tomas >/dev/null
group TI-Redes
group GG-Wiki-TI
member TI-Redes lucia
member GG-Wiki-TI TI-Redes
member GG-Wiki-TI marta
member GG-Wiki-TI tomas
member GG-Wiki-TI pablo
touch /var/lib/samba/seeded

exec samba --foreground --no-process-group
