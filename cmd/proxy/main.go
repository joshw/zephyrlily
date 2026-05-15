package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joshw/zephyrlily/internal/proxy/api"
)

func main() {
	listen := flag.String("listen", ":7888", "proxy listen address")
	lily := flag.String("lily", "rpi.lily.org:7777", "Lily server address")
	flag.Parse()

	cfg := api.Config{
		ListenAddr: *listen,
		LilyAddr:   *lily,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := api.New(cfg)
	if err := srv.Run(ctx); err != nil {
		log.Fatalf("proxy: %v", err)
	}
}
