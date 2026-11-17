// Command labelgate manages Cloudflare DNS, Tunnel ingress and Access from
// Docker container labels.
//
// Required behavior (WP6), see dev/docs/v0.3-design.md §9:
//
//	labelgate [--config PATH] [--version]      run (mode from config)
//	labelgate [--config PATH] healthcheck      container HEALTHCHECK
//
// Exit codes: 0 clean shutdown (SIGINT/SIGTERM) or healthy, 1 fatal error / unhealthy.
// Shutdown order: API + agent hub stop accepting -> reconciler stops -> store closes.
package main

import (
	"fmt"
	"io"
	"os"

	flag "github.com/spf13/pflag"

	"github.com/channinghe/labelgate/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run parses args, loads config, wires components and blocks until shutdown.
// It returns the process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("labelgate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.StringP("config", "c", "", "path to configuration file")
	showVersion := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *showVersion {
		fmt.Fprintf(stdout, "labelgate %s\n", version.Version)
		return 0
	}
	_ = configPath
	fmt.Fprintln(stderr, "labelgate: v0.3 backend not implemented yet")
	return 1
}
