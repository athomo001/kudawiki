#!/bin/sh
# Certificados de prueba de F6 (skills/DevSecOps/docker.md §2): una CA propia que firma el
# certificado de cada directorio, y otra CA que no firmó a nadie, para probar "CA desconocida"
# (SPEC §17). Idempotente: si ya están, no los rehace (los servidores ya los cargaron).
set -eu
out=/certs
[ -f "$out/ca.crt" ] && [ -f "$out/other-ca.crt" ] && exit 0
mkdir -p "$out/dirsrv/ca"
cd "$out"

openssl req -x509 -newkey rsa:3072 -nodes -days 3650 -subj "/CN=Kudawiki Test CA" \
  -keyout ca.key -out ca.crt -addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign,cRLSign"
openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -subj "/CN=Otra CA" \
  -keyout other-ca.key -out other-ca.crt -addext "basicConstraints=critical,CA:TRUE"

# Un certificado por servidor, con su nombre en la red del compose.
for name in samba openldap openldap-plain dirsrv; do
  openssl req -newkey rsa:2048 -nodes -subj "/CN=$name.kudawiki.test" -keyout "$name.key" -out "$name.csr"
  printf 'subjectAltName=DNS:%s.kudawiki.test\nextendedKeyUsage=serverAuth\nkeyUsage=critical,digitalSignature,keyEncipherment\n' "$name" > "$name.ext"
  openssl x509 -req -in "$name.csr" -CA ca.crt -CAkey ca.key -CAcreateserial -days 3650 -extfile "$name.ext" -out "$name.crt"
  rm "$name.csr" "$name.ext"
done

# 389 DS los importa de /data/tls con estos nombres.
cp dirsrv.crt dirsrv/server.crt
cp dirsrv.key dirsrv/server.key
cp ca.crt dirsrv/ca/ca.crt
# Los servidores corren con su propio usuario: tienen que poder leer las llaves.
chmod 644 ./*.key dirsrv/server.key
