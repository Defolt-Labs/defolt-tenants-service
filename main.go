package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"defolt-tenants-service/logger"
	"defolt-tenants-service/server"
)

func main() {
	app, err := server.Initialize()
	if err != nil {
		logger.LogError("", "boot", "server init failed: "+err.Error())
		os.Exit(1)
	}
	defer app.Cleanup()

	srv := &http.Server{
		Addr:              ":" + app.Config.ServerPort,
		Handler:           app.Engine,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.LogInfo("boot", "listening on :"+app.Config.ServerPort)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.LogError("", "boot", "listen: "+err.Error())
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.LogInfo("boot", "shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
