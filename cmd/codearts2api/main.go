package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/handsomedogx/codearts2api/internal/config"
	"github.com/handsomedogx/codearts2api/internal/server"
)

func main() {
	settings := config.Load()
	addr := strings.TrimSpace(os.Getenv("CODEARTS2API_ADDR"))
	if addr == "" {
		addr = fmt.Sprintf("%s:%d", settings.Host, settings.Port)
	}

	handler, stopRuntime, err := server.NewRuntime()
	if err != nil {
		log.Fatal(err)
	}
	defer stopRuntime()

	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	log.Printf("codearts2api (Go) listening on http://%s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
