FROM golang:1.23 AS builder

WORKDIR /workdir

COPY go.mod go.mod
COPY server/ server/
COPY internal/ internal/

RUN go mod tidy && \
    cd server && \
    CGO_ENABLED=0 go build -a -o ./server main.go

FROM alpine:latest

WORKDIR /workdir

COPY --from=builder /workdir/server/server server

ENTRYPOINT [ "/workdir/server" ]