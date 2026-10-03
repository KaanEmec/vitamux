# Release image: SPA build → static Go binary → distroless (non-root), plus pg_dump/pg_restore.
# Build stages run on the builder's native platform (no emulation); Go cross-compiles for the
# target, apk installs the target's packages into a separate root, and the final stage only
# copies files, so multi-arch builds need no QEMU.
FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web/*.go ./web/
COPY --from=web /src/web/build ./web/build
ARG VERSION=0.0.0-dev
ARG COMMIT=unknown
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -tags webui -trimpath \
    -ldflags "-s -w -X github.com/KaanEmec/vitamux/internal/version.Version=${VERSION} -X github.com/KaanEmec/vitamux/internal/version.Commit=${COMMIT}" \
    -o /out/vitamux ./cmd/vitamux
# Empty mount points; the final image has no shell, so they are created here and chowned on copy.
RUN mkdir -p /out/data /out/secrets /out/backups

# PostgreSQL 18 client for `vitamux backup|restore` (docs/operations/backup.md): Alpine's package
# for the target architecture, installed into /pg without running its scripts. Only pg_dump,
# pg_restore and the musl libraries they link (about 12 MB) reach the final image; no shell.
FROM --platform=$BUILDPLATFORM alpine:3.24 AS pg
ARG TARGETARCH
RUN set -eu; \
    case "$TARGETARCH" in amd64) arch=x86_64 ;; arm64) arch=aarch64 ;; *) echo "unsupported arch $TARGETARCH" >&2; exit 1 ;; esac; \
    apk add --quiet --no-cache --root /pg --initdb --arch "$arch" --keys-dir "/usr/share/apk/keys/$arch" \
        --repositories-file /etc/apk/repositories --no-scripts postgresql18-client; \
    mkdir -p /out/lib /out/usr/lib /out/usr/bin; \
    cp /pg/usr/libexec/postgresql18/pg_dump /pg/usr/libexec/postgresql18/pg_restore /out/usr/bin/; \
    cp -P /pg/lib/ld-musl-$arch.so.1 /pg/lib/libc.musl-$arch.so.1 /out/lib/; \
    for l in libpq.so.5 libssl.so.3 libcrypto.so.3 libz.so.1 libzstd.so.1 liblz4.so.1; do cp -L /pg/usr/lib/$l /out/usr/lib/; done

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go /out/vitamux /vitamux
COPY --from=pg /out/ /
COPY LICENSE NOTICE THIRD_PARTY_NOTICES.md /licenses/
# Named volumes inherit this ownership on first use, so the non-root user can write them:
# /data holds blobs and documents, /secrets holds the master key (`vitamux admin init-secrets`),
# /backups the scheduled backups (VITAMUX_BACKUP_DIR).
COPY --from=go --chown=65532:65532 /out/data /data
COPY --from=go --chown=65532:65532 /out/secrets /secrets
COPY --from=go --chown=65532:65532 /out/backups /backups
ENV VITAMUX_HTTP_ADDR=:8080 \
    VITAMUX_DATA_DIR=/data
EXPOSE 8080
VOLUME ["/data"]
USER nonroot:nonroot
ENTRYPOINT ["/vitamux"]
CMD ["serve"]
