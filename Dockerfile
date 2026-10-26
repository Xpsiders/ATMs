# syntax=docker/dockerfile:1
FROM golang:alpine AS build
WORKDIR /src
ENV GOTOOLCHAIN=auto CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /out/api ./cmd/api
RUN go build -o /out/seed ./scripts/seed
RUN GOBIN=/out go install -tags 'no_sqlite3 no_mysql no_vertica no_clickhouse no_mssql no_ydb' \
    github.com/pressly/goose/v3/cmd/goose@latest

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/api /out/seed /out/goose /usr/local/bin/
COPY db/migrations /app/db/migrations
EXPOSE 8080
# Default: apply migrations, load the (idempotent) seed, then run the API. This
# makes a plain `docker run` / Render Docker service work with no extra command.
# goose must succeed (a bad DATABASE_URL fails the boot loudly); seed is allowed
# to fail without blocking startup. Compose overrides this per service.
CMD ["sh", "-c", "goose -dir /app/db/migrations postgres \"$DATABASE_URL\" up && (seed || echo 'seed skipped') && exec api"]