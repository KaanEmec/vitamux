# Release image: SPA build → static Go binary → distroless (non-root).
FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27-alpine AS go
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web/*.go ./web/
COPY --from=web /src/web/build ./web/build
ARG VERSION=0.0.0-dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 go build -tags webui -trimpath \
    -ldflags "-s -w -X github.com/KaanEmec/vitamux/internal/version.Version=${VERSION} -X github.com/KaanEmec/vitamux/internal/version.Commit=${COMMIT}" \
    -o /out/vitamux ./cmd/vitamux

# Note: `vitamux backup` will need pg_dump (J13.5); the base image is revisited then.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go /out/vitamux /vitamux
ENV VITAMUX_HTTP_ADDR=:8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/vitamux"]
CMD ["serve"]
