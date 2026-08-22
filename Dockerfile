FROM golang:1.23-alpine AS build
RUN apk add --no-cache build-base sqlite-dev
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/spartarr ./cmd/spartarr

FROM alpine:3.22
RUN apk add --no-cache sqlite-libs \
    && addgroup -g 1000 -S spartarr \
    && adduser -u 1000 -S -G spartarr spartarr \
    && mkdir -p /config/state \
    && chown -R spartarr:spartarr /config
WORKDIR /app
COPY --from=build /out/spartarr /usr/local/bin/spartarr
USER 1000:1000
EXPOSE 8088
ENTRYPOINT ["spartarr"]
CMD ["-config", "/config/config.json"]
