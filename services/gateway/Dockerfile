# syntax=docker/dockerfile:1

# --- Build stage: compile a static binary. ---
FROM golang:1.21 AS build
WORKDIR /src

# Cache module downloads across builds.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO disabled -> a static binary that runs on a distroless base.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/gateway ./services/gateway

# --- Runtime stage: distroless, non-root, no shell. ---
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/gateway /gateway
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/gateway"]
