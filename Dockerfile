# Server image (the API, with the browser app built into it). amd64 only
# (SPEC.md section 7). One static, cgo-free binary on distroless static
# (ADR 0001, A38).
FROM node:24-slim@sha256:dc26ae92c9fb15cf6b1d63e20f0944b2efe0f115b20e00be48fb23fceb061c54 AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-fund --no-audit
COPY web/ ./
RUN npm run build

FROM golang:1.27.4@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The app, embedded by the web package (go:embed).
COPY --from=web /web/build ./web/build
# A release stamps its version (ADR 0043); any other build is a -dev one.
ARG VERSION=0.1.0-dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
    -ldflags "-s -w -X github.com/RIKIKU/RadioArchive/internal/version.Version=${VERSION}" -o /out/radioarchive ./cmd/radioarchive
# The recordings volume's mount point (R20), owned by distroless's nonroot
# user so a new named volume starts out writable by the API.
RUN mkdir -p /out/recordings /out/setup-export /out/backup-status

FROM gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
COPY --from=build /out/radioarchive /usr/local/bin/radioarchive
COPY --from=build --chown=65532:65532 /out/recordings /var/lib/radioarchive/recordings
# The setup export's folder, written by the API for the backup service, and
# the backup's status, which the API only reads (ADR 0041).
COPY --from=build --chown=65532:65532 /out/setup-export /var/lib/radioarchive/setup-export
COPY --from=build /out/backup-status /var/lib/radioarchive/backup-status
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/radioarchive"]
CMD ["serve"]
