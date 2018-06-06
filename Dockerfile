FROM golang:1.10 as builder

ARG COMPONENT

WORKDIR /go/src/github.com/projectriff/${COMPONENT}
COPY vendor/ vendor/

COPY cmd/ cmd/
COPY pkg/ pkg/

RUN go build -o /riff-entrypoint cmd/${COMPONENT}.go

###########

FROM debian:wheezy-slim

# The following line forces the creation of a /tmp directory
WORKDIR /tmp

WORKDIR /

COPY --from=builder /riff-entrypoint /riff-entrypoint

ENTRYPOINT ["/riff-entrypoint"]
