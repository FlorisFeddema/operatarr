FROM golang:1.25 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download

COPY jobrunner/ jobrunner/

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o jobrunner jobrunner/main.go

FROM gcr.io/distroless/static:nonroot

LABEL org.opencontainers.image.source="https://github.com/FlorisFeddema/operatarr"

WORKDIR /
COPY --from=builder /workspace/jobrunner .
USER 65532:65532

ENTRYPOINT ["/jobrunner"]
