package evidence

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
)

func RunCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		_, _ = fmt.Fprintln(stdout, "usage: gateway evidence collect --gateway-url URL --output DIR [--revision REV] [--image DIGEST]")
		return 0
	}
	if args[0] != "collect" {
		_, _ = fmt.Fprintf(stderr, "unknown evidence command %q\n", args[0])
		return 2
	}
	flags := flag.NewFlagSet("evidence collect", flag.ContinueOnError)
	flags.SetOutput(stderr)
	options := Options{Token: os.Getenv("GATEWAY_EVIDENCE_ADMIN_TOKEN")}
	flags.StringVar(&options.GatewayURL, "gateway-url", "", "Gateway base URL")
	flags.StringVar(&options.OutputDir, "output", "", "empty evidence output directory")
	flags.StringVar(&options.Revision, "revision", "", "candidate Git revision")
	flags.StringVar(&options.Image, "image", "", "candidate image digest or tag")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if _, err := (Collector{}).Collect(ctx, options); err != nil {
		_, _ = fmt.Fprintln(stderr, "evidence collection failed")
		return 1
	}
	return 0
}
