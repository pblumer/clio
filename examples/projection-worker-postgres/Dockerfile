# Multi-Stage-Build des Projection Workers (statisches Single-Binary).
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /worker .

FROM gcr.io/distroless/static-debian12
COPY --from=build /worker /worker
ENTRYPOINT ["/worker"]
