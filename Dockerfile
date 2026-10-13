# --- frontend ---
FROM node:22-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN if [ -f package.json ]; then npm ci; fi
COPY web/ ./
RUN if [ -f package.json ]; then npm run build; fi

# --- backend ---
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.Version=${VERSION}" -o /out/bshop ./cmd/bshop

# --- runtime ---
FROM alpine:3.21
# postgresql17-client: pg_restore/psql for importing an RWP database from the admin
RUN apk add --no-cache ca-certificates tzdata postgresql17-client && adduser -D -u 1000 app
COPY --from=build /out/bshop /app/bshop
USER app
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s CMD wget -qO- http://127.0.0.1:8080/api/health || exit 1
ENTRYPOINT ["/app/bshop"]
