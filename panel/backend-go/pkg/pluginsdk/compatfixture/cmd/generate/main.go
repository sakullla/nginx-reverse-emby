package main

import (
	"fmt"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/pkg/pluginsdk/compatfixture"
)

func main() {
	fmt.Print(compatfixture.PolicyV1GuestHex())
}
