package main

import (
	"context"
	"forge/internal/cli"
	"os"
	"os/signal"
)

var version = "dev"
var commit = "unknown"
var date = "unknown"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Execute(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, cli.Dependencies{Version: version, Commit: commit, Date: date})
	cancel()
	os.Exit(code)
}
