# Samba AD DC de prueba (F6, flavor ad). No hay una imagen oficial: se arma desde Debian, fijado
# por digest.
FROM debian:trixie-slim@sha256:f6b08b09a05725f9431502d3b5714f5c4576e82c1a84fa1debe0c24a7e0c3ec4
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends samba samba-ad-dc samba-ad-provision winbind ldb-tools \
 && rm -rf /var/lib/apt/lists/* /etc/samba/smb.conf /var/lib/samba/*
COPY entrypoint.sh /etc/kudawiki/entrypoint.sh
ENTRYPOINT ["/bin/sh", "/etc/kudawiki/entrypoint.sh"]
