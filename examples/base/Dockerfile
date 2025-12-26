# syntax=docker/dockerfile:1

ARG GO_VERSION=1.24
FROM golang:${GO_VERSION}-bullseye AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

WORKDIR /src/examples/base
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /usr/local/bin/pocketbase

FROM alpine:3.20

RUN addgroup -S pocketbase && adduser -S pocketbase -G pocketbase
RUN apk add --no-cache ca-certificates curl

WORKDIR /pb

COPY --from=builder /usr/local/bin/pocketbase /usr/local/bin/pocketbase
COPY --from=builder /src/examples/base/pb_migrations ./pb_migrations
RUN mkdir -p pb_data pb_hooks pb_public && chown -R pocketbase:pocketbase /pb

USER pocketbase

EXPOSE 8090

ENTRYPOINT ["/usr/local/bin/pocketbase"]
CMD ["serve", "--http=0.0.0.0:8090", "--dir", "/pb/pb_data"]
