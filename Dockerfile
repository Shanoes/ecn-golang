FROM golang:1.14-alpine3.11 as builder

# Static build flags
# needed if we build on glibc system (most Linux) and run on musl libc (Alpine)
# not needed if we both build and run on Alpine
# ENV CGO_ENABLED 0
# ENV GOOS linux

RUN apk update
RUN apk add --no-cache git
# OPC UA
RUN go get -u github.com/gopcua/opcua
# MQTT
RUN go get github.com/eclipse/paho.mqtt.golang
RUN go get github.com/gorilla/websocket
RUN go get golang.org/x/net/proxy

RUN mkdir /build 
ADD ./src/monitor.go /build/
WORKDIR /build 
RUN go build -o monitor monitor.go


FROM alpine:3.11

RUN adduser -S -D -H -h /app appuser
USER appuser
WORKDIR /app
COPY --from=builder /build/monitor .

ENTRYPOINT ["./monitor"]
