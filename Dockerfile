# Builds one image for the machine it is pulled on (arm64 for a Raspberry Pi,
# amd64 for a PC). The frontend and the Go binary are built on the build
# machine's own architecture and the binary is cross-compiled for the target,
# so building for a Pi never runs the Angular build on the Pi.

FROM --platform=$BUILDPLATFORM node:26-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
# Writes the website into /src/backend/internal/web/files/build.
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS backend
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=frontend /src/backend/internal/web/files/build ./internal/web/files/build
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/usage-control ./cmd/usage-control
# The folder the history database is kept in, mounted as a volume at run time.
RUN mkdir /out/data

# A minimal image without a shell or package manager, running as a non-root user.
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=backend /out/usage-control /usage-control
# Owned by the nonroot user (65532), so a new volume mounted here is writable.
COPY --from=backend --chown=65532:65532 /out/data /data
ENV DATABASE_PATH=/data/usage-control.db
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/usage-control"]
