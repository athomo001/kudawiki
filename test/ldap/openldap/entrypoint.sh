#!/bin/sh
# Arranca slapd con TLS de la CA de prueba y siembra los datos. Idempotente: ldapadd -c salta lo
# que ya existe. Se siembra con slapd corriendo (no con slapadd) para que el overlay memberOf
# calcule el atributo.
set -eu
rm -f /run/slapd/seeded
conf=/etc/ldap/slapd.conf
sed "s/ROOTPW/$ADMIN_PASSWORD/" /etc/kudawiki/slapd.conf > "$conf"
if [ "${MEMBEROF:-no}" = yes ]; then
  sed -i 's/^#MEMBEROF //' "$conf"
fi

# Las llaves vienen de un volumen montado: se copian con el dueño y los permisos que slapd pide.
mkdir -p /etc/ldap/tls /run/slapd
cp /certs/ca.crt /etc/ldap/tls/ca.crt
cp "/certs/$CERT_NAME.crt" /etc/ldap/tls/server.crt
cp "/certs/$CERT_NAME.key" /etc/ldap/tls/server.key
chown -R openldap:openldap /etc/ldap/tls /run/slapd /var/lib/ldap
chmod 600 /etc/ldap/tls/server.key

slapd -u openldap -g openldap -f "$conf" -h "ldap:/// ldaps:/// ldapi:///" -d 0 &
pid=$!
i=0
until ldapsearch -x -H ldapi:/// -b "" -s base >/dev/null 2>&1; do
  i=$((i + 1))
  [ "$i" -lt 50 ] || { echo "slapd did not start" >&2; exit 1; }
  sleep 0.2
done
sed "s/PASSWORD/$USER_PASSWORD/" /etc/kudawiki/seed.ldif |
  ldapadd -x -c -H ldapi:/// -D cn=admin,dc=kudawiki,dc=test -w "$ADMIN_PASSWORD" 2>&1 | grep -v "Already exists" || true
touch /run/slapd/seeded
wait "$pid"
