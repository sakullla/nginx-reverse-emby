package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/sakullla/nginx-reverse-emby/panel/backend-go/pkg/pluginsdk/internal/protogen"
)

func main() {
	sdkRoot := flag.String("sdk-root", "../../plugin-sdk", "path to the canonical plugin-sdk directory")
	output := flag.String("output", "./pkg/pluginsdk/protoschema/descriptors_gen.go", "generated Go descriptor output")
	flag.Parse()

	descriptorSet, err := protogen.CompileDescriptorSet(context.Background(), *sdkRoot)
	if err != nil {
		fatal(err)
	}
	generated, err := protogen.RenderGo(descriptorSet)
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*output, generated, 0o644); err != nil {
		fatal(fmt.Errorf("write %s: %w", *output, err))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
