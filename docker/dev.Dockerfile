# Toolchain image for make test/lint/build. Not the runtime image (see /Dockerfile).
FROM golang:1.27.4-alpine3.24
RUN apk add --no-cache ffmpeg build-base git
ENV CGO_ENABLED=1 GOTOOLCHAIN=local
RUN GOBIN=/usr/local/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 \
 && GOBIN=/usr/local/bin go install golang.org/x/vuln/cmd/govulncheck@v1.10.0 \
 && rm -rf /root/.cache /go/pkg
