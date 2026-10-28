# syntax=docker/dockerfile:1
# Un solo contenedor (SPEC §13.11): binario estático con SQLite y la UI embebidos.

# Etapa 1: frontend. Node 26 no trae corepack: pnpm es el binario standalone del
# release, con versión fijada y hash verificado (SPEC §15). Nunca npm.
FROM --platform=$BUILDPLATFORM node:26-bookworm-slim@sha256:28147155d47dc31abe0a46e0d12b59b2fb3d0a2dcc5dc65fce76a074180b16fe AS ui
ARG PNPM_VERSION=12.16.1
ADD --checksum=sha256:565ff81174a14f6f7bd5d84c1b17e51031a80230d370a1fe24be5d16bd02a825 \
    https://github.com/pnpm/pnpm/releases/download/v${PNPM_VERSION}/pnpm-linux-x64.tar.gz /tmp/pnpm.tar.gz
RUN mkdir /opt/pnpm && tar -xzf /tmp/pnpm.tar.gz -C /opt/pnpm && rm /tmp/pnpm.tar.gz
ENV PATH=/opt/pnpm:$PATH
WORKDIR /src/ui
COPY ui/package.json ui/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY ui/ ./
RUN pnpm build

# Etapa 2: binario estático, sin CGO.
FROM --platform=$BUILDPLATFORM golang:1.27-bookworm@sha256:972b4110ba0f47539a975c31e52c64765e1ff19f2d74469ae4a595c6676e80a5 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/ui/dist ./ui/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/kudawiki ./cmd/kudawiki \
 && mkdir /out/data /out/keys

# Etapa final: sin shell ni gestor de paquetes, usuario sin privilegios.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/kudawiki /kudawiki
# Los volúmenes nuevos heredan este dueño: así el usuario sin privilegios puede escribir.
COPY --from=build --chown=nonroot:nonroot /out/data /data
COPY --from=build --chown=nonroot:nonroot /out/keys /keys
ENV KUDAWIKI_DATA_DIR=/data \
    KUDAWIKI_MASTER_KEY_FILE=/keys/master.key
USER nonroot:nonroot
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s CMD ["/kudawiki", "healthcheck"]
ENTRYPOINT ["/kudawiki"]
CMD ["serve"]
