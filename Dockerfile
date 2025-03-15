FROM golang:1.23 AS builder

WORKDIR /workdir

COPY go.mod go.mod
COPY main.go main.go
COPY internal/ internal/

RUN go mod tidy && \
    CGO_ENABLED=0 go build -a -o ./leader-elector main.go

FROM alpine:latest

WORKDIR /workdir

COPY --from=builder /workdir/leader-elector leader-elector

ENTRYPOINT [ "/workdir/leader-elector" ]