package main

import (
	"fmt"

	"github.com/sakullla/nginx-reverse-emby/plugin-sdk/go/compatfixture"
)

func main() {
	fmt.Print(compatfixture.PolicyV1GuestHex())
}
