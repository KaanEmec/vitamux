# Release image: SPA build → static Go binary → distroless (non-root).
# Build stages run on the builder's native platform (no emulation); Go cross-compiles for the
# target, and the final stage only copies files, so multi-arch builds need no QEMU.
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
RUN mkdir -p /out/data /out/secrets

# Note: `vitamux backup` will need pg_dump (J13.5); the base image is revisited then.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go /out/vitamux /vitamux
COPY LICENSE NOTICE THIRD_PARTY_NOTICES.md /licenses/
# Named volumes inherit this ownership on first use, so the non-root user can write them:
# /data holds blobs and documents, /secrets holds the master key (`vitamux admin init-secrets`).
COPY --from=go --chown=65532:65532 /out/data /data
COPY --from=go --chown=65532:65532 /out/secrets /secrets
ENV VITAMUX_HTTP_ADDR=:8080 \
    VITAMUX_DATA_DIR=/data
EXPOSE 8080
VOLUME ["/data"]
USER nonroot:nonroot
ENTRYPOINT ["/vitamux"]
CMD ["serve"]
