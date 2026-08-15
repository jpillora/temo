# syntax=docker/dockerfile:1

ARG GO_VERSION=1.26
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build

ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
ARG VERSION=dev
ARG COMMIT=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./

RUN export GOARM="${TARGETVARIANT#v}"; \
    if [ "$TARGETARCH" != "arm" ]; then unset GOARM; fi; \
    CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/temo .

FROM scratch

ARG VERSION=dev
ARG COMMIT=unknown

LABEL org.opencontainers.image.title="temo" \
      org.opencontainers.image.description="A terminal demoscene in pure Go" \
      org.opencontainers.image.source="https://github.com/jpillora/temo" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$COMMIT"

ENV TERM=xterm-256color \
    COLORTERM=truecolor

COPY --from=build /out/temo /temo
USER 65532:65532
ENTRYPOINT ["/temo"]
