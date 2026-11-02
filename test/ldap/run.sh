#!/bin/sh
# Criterio de salida de F6: levanta los directorios de prueba, siembra 389 DS y FreeIPA, y corre
# los tests de integración (-tags ldap_integration). Idempotente: se puede correr de nuevo sobre
# los contenedores que ya están. `down -v` borra todo, certificados aparte (test/ldap/.certs).
set -eu
cd "$(dirname "$0")"
# En Git Bash (Windows), que no convierta las rutas del contenedor en rutas de Windows.
export MSYS_NO_PATHCONV=1
compose="docker compose -f docker-compose.test.yml"

echo "== levantando los directorios (FreeIPA tarda varios minutos la primera vez)"
$compose up -d --build --wait --wait-timeout 1500 certs samba openldap openldap-plain dirsrv freeipa

echo "== sembrando 389 DS y FreeIPA"
$compose exec -T dirsrv sh /seed.sh
$compose exec -T freeipa sh /seed.sh

echo "== tests de integración"
$compose run --rm go
