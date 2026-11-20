# syntax=docker/dockerfile:1

# The build stage always runs on the builder's own platform and cross-compiles, so a
# multi-arch build (linux/amd64 + linux/arm64) never runs the Go toolchain under QEMU.
FROM --platform=$BUILDPLATFORM golang:1.27.4@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /planka-mcp ./cmd/planka-mcp
# The licenses of the code compiled in travel with the binary.
RUN ./scripts/notices.sh /THIRD_PARTY_NOTICES

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
# The MCP registry checks this label against the server name before it accepts the image.
LABEL io.modelcontextprotocol.server.name="io.github.sierikov/planka-mcp" \
      org.opencontainers.image.source="https://github.com/sierikov/planka-mcp" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /planka-mcp /planka-mcp
COPY --from=build /src/LICENSE /THIRD_PARTY_NOTICES /
ENV PLANKA_MCP_TRANSPORT=http PLANKA_MCP_ADDR=:8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/planka-mcp"]
