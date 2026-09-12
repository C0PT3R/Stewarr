FROM golang:1.23-alpine AS build
# nodejs/npm are build-time only, used solely to type-check the TypeScript
# assets (tsc); esbuild does the actual bundling via its Go API and never
# touches Node. Neither ships in the final runtime image below.
RUN apk add --no-cache build-base sqlite-dev nodejs npm
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN npm --prefix internal/httpui/static/src ci
RUN npm --prefix internal/httpui/static/src run typecheck
RUN go run ./tools/buildassets
RUN go test ./...
RUN CGO_ENABLED=1 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/stewarr ./cmd/stewarr

FROM alpine:3.22
RUN apk add --no-cache sqlite-libs tzdata \
    && addgroup -g 1000 -S stewarr \
    && adduser -u 1000 -S -G stewarr stewarr \
    && mkdir -p /config/state \
    && chown -R stewarr:stewarr /config
WORKDIR /app
COPY --from=build /out/stewarr /usr/local/bin/stewarr
USER 1000:1000
EXPOSE 8088
ENTRYPOINT ["stewarr"]
CMD ["-config", "/config/config.json"]
