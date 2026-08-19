package enginecli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/deagy/cadre/cli/internal/engine/service"
)

// cmdServe runs the HTTP surface.
//
// Bound to loopback unless an address is given explicitly. This service
// dispatches agents and accepts approval decisions, and nothing in it
// authenticates a caller -- so the default must not be reachable from off the
// host. Choosing otherwise is the operator's to do, deliberately.
func cmdServe(argv []string, deps Deps) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	address := fs.String("address", "127.0.0.1:8099", "Address to listen on")
	shutdownGrace := fs.Duration("shutdown-grace", 10*time.Second,
		"How long to let in-flight requests finish on shutdown")
	if !parse(fs, argv, deps) {
		return 2
	}

	server := &service.Server{KernelRoot: deps.KernelRoot}
	if deps.Prepare != nil {
		server.Build = deps.Prepare
	}

	listener, err := net.Listen("tcp", *address)
	if err != nil {
		return deps.fail("cadre serve: %v", err)
	}

	host, _, splitErr := net.SplitHostPort(*address)
	if splitErr == nil && host != "127.0.0.1" && host != "localhost" && host != "::1" {
		_, _ = fmt.Fprintf(deps.Stderr,
			"cadre serve: listening on %s, which is not loopback. Nothing here authenticates a "+
				"caller, and it dispatches agents and accepts approval decisions.\n", *address)
	}
	_, _ = fmt.Fprintf(deps.Stdout, "listening on %s\n", listener.Addr())

	httpServer := &http.Server{
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// A run mid-dispatch should finish rather than be cut off: its agents have
	// already been called, and abandoning the response loses what they said.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.Serve(listener) }()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return deps.fail("cadre serve: %v", err)
		}
	case <-stop:
		_, _ = fmt.Fprintln(deps.Stdout, "shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), *shutdownGrace)
		defer cancel()
		if err := httpServer.Shutdown(ctx); err != nil {
			return deps.fail("cadre serve: %v", err)
		}
	}
	return 0
}
