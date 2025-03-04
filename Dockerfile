FROM golang:1.24.0 AS build

ARG TARGETOS
ARG TARGETARCH

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/cache go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /go/bin/app

FROM scratch as runtime
COPY --from=build /go/bin/app /app
USER app

ENTRYPOINT ["/app"]
