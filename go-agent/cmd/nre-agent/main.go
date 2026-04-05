package main

import (
	"log"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/app"
)

func main() {
	if _, err := app.New(app.Config{AgentID: "bootstrap"}); err != nil {
		log.Fatal(err)
	}
}
