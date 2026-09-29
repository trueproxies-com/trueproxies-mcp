FROM --platform=$BUILDPLATFORM golang:1.26.8-alpine AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /trueproxies-mcp .

FROM gcr.io/distroless/static-debian12:nonroot
LABEL io.modelcontextprotocol.server.name="com.trueproxies/mcp" \
      org.opencontainers.image.source="https://github.com/trueproxies-com/trueproxies-mcp" \
      org.opencontainers.image.description="MCP server for TrueProxies proxy accounts" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /trueproxies-mcp /trueproxies-mcp
ENTRYPOINT ["/trueproxies-mcp"]
CMD ["stdio"]
