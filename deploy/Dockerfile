# Стадия 1: сборка. Здесь есть Go, исходники и кеш модулей,
# в итоговый образ из этой стадии попадёт только готовый бинарь
FROM golang:1.27-alpine AS build

WORKDIR /src

# Сначала только зависимости: этот слой берётся из кеша, пока не меняются go.mod и go.sum
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 — статический бинарь без зависимости от libc (её нет в distroless)
# -trimpath убирает из бинаря локальные пути, -s -w — отладочную информацию
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/trip-service ./cmd/trip-service

# Стадия 2: итоговый образ. Ни shell, ни Go, ни исходников — только бинарь
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/trip-service /trip-service

# Пользователь nonroot (uid 65532) уже заведён в базовом образе
USER nonroot:nonroot

EXPOSE 8080

ENTRYPOINT ["/trip-service"]
