// Command trueproxies-mcp is an MCP server for the TrueProxies customer API.
// It runs over stdio for local use, or as a stateless Streamable HTTP server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version is set at build time with -ldflags "-X main.version=1.0.0".
var version = "dev"

const usage = `usage:
  trueproxies-mcp stdio                serve over stdin and stdout; API key from TRUEPROXIES_API_KEY
  trueproxies-mcp http [-addr :8080]   serve Streamable HTTP at /mcp; API key from each request's Authorization header`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	base := os.Getenv("TRUEPROXIES_API_URL")
	if base == "" {
		base = "https://api.trueproxies.com"
	}
	api := newAPIClient(base, version)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch os.Args[1] {
	case "stdio":
		auth := ""
		if key := strings.TrimSpace(os.Getenv("TRUEPROXIES_API_KEY")); key != "" {
			auth = "Bearer " + strings.TrimPrefix(key, "Bearer ")
		}
		srv := newServer(api, func(*mcp.CallToolRequest) string { return auth })
		if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
			log.Fatal(err)
		}
	case "http":
		fs := flag.NewFlagSet("http", flag.ExitOnError)
		addr := fs.String("addr", ":8080", "listen address")
		_ = fs.Parse(os.Args[2:])
		srv := newServer(api, func(req *mcp.CallToolRequest) string {
			if req.Extra == nil {
				return ""
			}
			return req.Extra.Header.Get("Authorization")
		})
		mux := http.NewServeMux()
		mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv },
			&mcp.StreamableHTTPOptions{Stateless: true}))
		mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok\n")) })
		hs := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = hs.Shutdown(shutdown)
		}()
		log.Printf("trueproxies-mcp %s listening on %s", version, *addr)
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
}
