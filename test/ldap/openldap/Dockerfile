# OpenLDAP de prueba (F6). No hay una imagen oficial: se arma desde Debian, fijado por digest.
# Trae también openssl y ldap-utils, así sirve para generar los certificados de prueba.
FROM debian:trixie-slim@sha256:f6b08b09a05725f9431502d3b5714f5c4576e82c1a84fa1debe0c24a7e0c3ec4
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends slapd ldap-utils openssl \
 && rm -rf /var/lib/apt/lists/* /etc/ldap/slapd.d /var/lib/ldap/*
COPY slapd.conf seed.ldif entrypoint.sh /etc/kudawiki/
ENTRYPOINT ["/bin/sh", "/etc/kudawiki/entrypoint.sh"]
