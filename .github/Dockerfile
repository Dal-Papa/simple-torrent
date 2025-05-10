# build stage
FROM golang:alpine AS build
RUN apk update && apk add git
ADD . /src
WORKDIR /src
ENV CGO_ENABLED 0
RUN go build \
  -trimpath \
  -ldflags "-s -w -X main.version=$(git describe --abbrev=0 --tags)" \
  -o /tmp/bin
# run stage
FROM alpine
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /tmp/bin /usr/local/bin/simple-torrent
ENTRYPOINT ["simple-torrent"]