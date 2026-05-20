# syntax=docker/dockerfile:1.7
FROM golang:1.26-alpine AS builder

ENV GOTOOLCHAIN=auto \
    GO111MODULE=on \
    CGO_ENABLED=0

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
RUN GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/buddy .

FROM scratch

COPY --from=builder /etc/ssl/certs /etc/ssl/certs
COPY --from=builder /out/buddy /buddy

USER 65534:65534

EXPOSE 53/udp
EXPOSE 53/tcp

ENTRYPOINT ["/buddy"]
