// Command remote-agent runs the self-hosted agent board.
//
//	remote-agent serve --config <file>
//	remote-agent healthcheck --url <url> [--timeout 5s]
//	remote-agent version
package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/s1n7ax/remote-agent/internal/app"
	"github.com/s1n7ax/remote-agent/internal/config"
	"github.com/s1n7ax/remote-agent/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: remote-agent serve --config <file> | healthcheck --url <url> | version")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "healthcheck":
		err = healthcheck(os.Args[2:])
	case "version", "--version":
		fmt.Println(version)
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "remote-agent:", err)
		os.Exit(1)
	}
}

func serve(args []string) error {
	fl := flag.NewFlagSet("serve", flag.ContinueOnError)
	path := fl.String("config", "/var/lib/remote-agent/config.json", "config file")
	if err := fl.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	spa, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	return app.Serve(ctx, cfg, spa, version, log)
}

func healthcheck(args []string) error {
	fl := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	url := fl.String("url", "http://127.0.0.1:7700", "base URL of the app")
	timeout := fl.Duration("timeout", 5*time.Second, "request timeout")
	if err := fl.Parse(args); err != nil {
		return err
	}
	client := http.Client{Timeout: *timeout}
	resp, err := client.Get(strings.TrimRight(*url, "/") + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: %s", resp.Status)
	}
	return nil
}
