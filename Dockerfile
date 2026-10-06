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
# The version the program reports: 1.2.3 for a release, see the Docker image workflow.
ARG VERSION=dev
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=frontend /src/backend/internal/web/files/build ./internal/web/files/build
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath \
      -ldflags="-s -w -X github.com/Chriszly/usage-control/backend/internal/version.Version=$VERSION" \
      -o /out/usage-control ./cmd/usage-control
# The power add-on, run as a service of its own when compose.yaml's power
# profile is picked.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/usage-control-power ./cmd/usage-control-power
# The folder the history database is kept in, mounted as a volume at run time.
RUN mkdir /out/data

# A minimal image without a shell or package manager, running as a non-root user.
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=backend /out/usage-control /out/usage-control-power /
# Owned by the nonroot user (65532), so a new volume mounted here is writable.
COPY --from=backend --chown=65532:65532 /out/data /data
ENV DATABASE_PATH=/data/usage-control.db
VOLUME /data
EXPOSE 9393
ENTRYPOINT ["/usage-control"]
