FROM golang:1.23-alpine AS build
WORKDIR /src
ENV GOPROXY=https://goproxy.cn,https://proxy.golang.org,direct \
    GOSUMDB=off CGO_ENABLED=0 GOFLAGS=-mod=mod
COPY go.mod ./
RUN go mod download || true
COPY . .
ARG CMD
RUN go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/${CMD}

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D appuser
USER appuser
COPY --from=build /out/app /app
EXPOSE 8080
ENTRYPOINT ["/app"]
