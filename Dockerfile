# Runtime image: snap-server plus cross-compiled sp binaries served at /dl/.
FROM golang:1.27.4-alpine3.24 AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-buildvcs=false
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
RUN --mount=type=cache,target=/root/.cache/go-build --mount=type=cache,target=/go/pkg/mod <<'SH'
set -eu
ld="-s -w -X github.com/Battleroid/snap-share/internal/version.Version=${VERSION} -X github.com/Battleroid/snap-share/internal/version.Commit=${COMMIT}"
go build -trimpath -ldflags "$ld" -o /out/snap-server ./cmd/snap-server
for t in linux/amd64 linux/arm64 darwin/arm64 windows/amd64; do
  os=${t%/*} arch=${t#*/} ext=""
  [ "$os" = windows ] && ext=.exe
  GOOS=$os GOARCH=$arch go build -trimpath -ldflags "$ld" -o "/out/cli/snap-cli-$os-$arch$ext" ./cmd/sp
done
SH

FROM alpine:3.24.4
RUN apk add --no-cache ffmpeg ca-certificates tzdata \
 && addgroup -S -g 10001 snap && adduser -S -D -H -u 10001 -G snap snap
COPY --from=build /out/snap-server /usr/local/bin/snap-server
COPY --from=build /out/cli /usr/share/snap/cli
ENV SNAP_DATA_DIR=/data SNAP_LISTEN=:8080 SNAP_METRICS_LISTEN=:9090
USER 10001:10001
EXPOSE 8080 9090
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["snap-server", "healthcheck"]
ENTRYPOINT ["snap-server"]
CMD ["serve"]
