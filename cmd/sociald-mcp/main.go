package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"

	"github.com/qilinbro/msg-manager/internal/mcp"
)

func main() {
	logger := log.New(os.Stderr, "sociald-mcp: ", log.LstdFlags)
	server, err := mcp.NewFromEnv()
	if err != nil {
		logger.Print(err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := server.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		// Transport errors may contain client-supplied data. Keep diagnostics
		// generic; neither credentials nor message text belong in process logs.
		logger.Print("MCP connection closed with a protocol or transport error")
		os.Exit(1)
	}
}
