# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /snaplink ./cmd/snaplink

# Distroless static image: no shell, no package manager, runs as non-root
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /snaplink /snaplink
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/snaplink"]
