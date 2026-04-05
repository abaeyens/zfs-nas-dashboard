package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/abaeyens/zfs-nas-dashboard/internal/broker"
	"github.com/abaeyens/zfs-nas-dashboard/internal/config"
	"github.com/abaeyens/zfs-nas-dashboard/internal/handler"
	"github.com/abaeyens/zfs-nas-dashboard/internal/poller"
	"github.com/abaeyens/zfs-nas-dashboard/internal/store"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("config")
	}

	st, err := store.Open(cfg.DataDir)
	if err != nil {
		log.Fatal().Err(err).Msg("store")
	}
	defer st.Close()

	br := broker.New()
	p := poller.New(cfg, st, br)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	p.Start(ctx)

	router := handler.NewRouter(cfg, p, br, st)
	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
	}

	go func() {
		<-ctx.Done()
		log.Info().Msg("shutting down")
		if err := srv.Shutdown(context.Background()); err != nil {
			log.Error().Err(err).Msg("shutdown")
		}
	}()

	log.Info().Str("addr", srv.Addr).Msg("listening")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal().Err(err).Msg("server")
	}
}

