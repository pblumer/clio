# Mehrstufiger Build: statisches Binary, dann minimales distroless-Image.

FROM golang:1.24 AS build
WORKDIR /src

# Abhängigkeiten zuerst (besseres Layer-Caching).
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath \
	-ldflags "-s -w -X main.version=${VERSION}" \
	-o /cliostore ./cmd/cliostore

# Datenverzeichnis dem nonroot-User (uid 65532) zuweisen, damit das per Volume
# gemountete /data beschreibbar ist (bbolt legt dort die DB an).
RUN mkdir -p /data && chown 65532:65532 /data

# distroless/static: keine Shell, nonroot-User, CA-Zertifikate enthalten.
FROM gcr.io/distroless/static-debian12:nonroot

# Standard-Listen-Adresse (siehe CLIO_ADDR).
EXPOSE 3000

# Datenbank-Verzeichnis (per Volume mounten, um Daten zu persistieren).
ENV CLIO_DB_PATH=/data/clio.db
COPY --from=build --chown=65532:65532 /data /data
VOLUME ["/data"]

COPY --from=build /cliostore /cliostore
ENTRYPOINT ["/cliostore"]
