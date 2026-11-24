# Server image (the API). amd64 only (SPEC.md section 7). One static,
# cgo-free binary on distroless static (ADR 0001, A38).
FROM golang:1.27.4@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o /out/radioarchive ./cmd/radioarchive
# The recordings volume's mount point (R20), owned by distroless's nonroot
# user so a new named volume starts out writable by the API.
RUN mkdir -p /out/recordings

FROM gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
COPY --from=build /out/radioarchive /usr/local/bin/radioarchive
COPY --from=build --chown=65532:65532 /out/recordings /var/lib/radioarchive/recordings
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/radioarchive"]
CMD ["serve"]
