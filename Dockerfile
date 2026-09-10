# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS base
WORKDIR /app
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download

FROM base AS dev
COPY . .
EXPOSE 8081 50000/tcp 50000-50100/udp
CMD ["go", "run", "./cmd/server"]

FROM base AS build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/voice ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot AS prod
COPY --from=build /out/voice /voice
USER nonroot:nonroot
EXPOSE 8081 50000/tcp 50000-50100/udp
ENTRYPOINT ["/voice"]
