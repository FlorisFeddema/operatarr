# syntax=docker/dockerfile:1.7
FROM golang:1.27@sha256:ec47ea3725fea0cc88f6de94919aef19f40f12fc1ec0e2d1b3426dbf15ecca04 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
COPY test/e2e/sonarr-mock/main.go main.go
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -trimpath -o /sonarr-mock main.go

FROM gcr.io/distroless/static:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931

WORKDIR /
COPY --from=builder /sonarr-mock /sonarr-mock
USER 65532:65532

EXPOSE 8989
ENTRYPOINT ["/sonarr-mock"]
