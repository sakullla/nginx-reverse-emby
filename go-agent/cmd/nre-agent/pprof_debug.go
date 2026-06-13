//go:build debug

package main

import (
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
)

func startPprofServer() {
	addr := os.Getenv("NRE_PPROF_ADDR")
	if addr == "" {
		return
	}

	go func() {
		log.Printf("[agent] pprof listening on %s", addr)
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Printf("[agent] pprof server stopped: %v", err)
		}
	}()
}
