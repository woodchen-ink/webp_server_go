FROM golang:1.23 AS builder

ARG EXHAUST_PATH=/opt/exhaust
RUN apt update && apt install --no-install-recommends libvips-dev -y && mkdir /build
COPY go.mod /build
RUN cd /build && go mod download

COPY . /build
RUN cd /build && sed -i "s|\"\"|\"${EXHAUST_PATH}\"|g" config.json  \
    && sed -i 's/127.0.0.1/0.0.0.0/g' config.json  \
    && go build -ldflags="-s -w" -o webp-server .

FROM alpine:latest

RUN apk update && \
    apk add --no-cache libvips ca-certificates && \
    rm -rf /var/cache/apk/*
    
COPY --from=builder /build/webp-server  /usr/bin/webp-server
COPY --from=builder /build/config.json /etc/config.json
    
WORKDIR /opt
VOLUME /opt/exhaust
CMD ["/usr/bin/webp-server", "--config", "/etc/config.json"]
    
