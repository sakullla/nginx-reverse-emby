package main

import (
	"log"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/app"
	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/config"
)

func main() {
	if _, err := app.New(config.Default()); err != nil {
		log.Fatal(err)
	}
}
