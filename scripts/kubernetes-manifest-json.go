package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"go.yaml.in/yaml/v3"
)

func main() {
	if len(os.Args) != 2 {
		fatal(errors.New("usage: kubernetes-manifest-json <manifest.yaml>"))
	}

	manifest, err := os.Open(os.Args[1])
	if err != nil {
		fatal(err)
	}
	defer manifest.Close()

	decoder := yaml.NewDecoder(manifest)
	documents := make([]map[string]any, 0)
	for {
		var document map[string]any
		err := decoder.Decode(&document)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			fatal(err)
		}
		if len(document) != 0 {
			documents = append(documents, document)
		}
	}

	if err := json.NewEncoder(os.Stdout).Encode(documents); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "convert Kubernetes manifest: %v\n", err)
	os.Exit(1)
}
