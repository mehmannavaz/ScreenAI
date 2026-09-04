// Command screenai runs Google's real closed-source Screen AI component
// (OCR + Screen2x main content extraction) on any input format, from
// the command line or as an HTTP service.
package main

import (
	"os"

	"github.com/mehmannavaz/ScreenAI/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:]))
}
