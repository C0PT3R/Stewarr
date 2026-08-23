FROM golang:1.23-alpine AS build
RUN apk add --no-cache build-base sqlite-dev
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/togetharr ./cmd/togetharr

FROM alpine:3.22
RUN apk add --no-cache sqlite-libs \
    && addgroup -g 1000 -S togetharr \
    && adduser -u 1000 -S -G togetharr togetharr \
    && mkdir -p /config/state \
    && chown -R togetharr:togetharr /config
WORKDIR /app
COPY --from=build /out/togetharr /usr/local/bin/togetharr
USER 1000:1000
EXPOSE 8088
ENTRYPOINT ["togetharr"]
CMD ["-config", "/config/config.json"]
