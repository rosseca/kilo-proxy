package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
)

const headlessEmbeddingsUsage = "Use embeddings models [--json]."

func headlessEmbeddingsHelp(args []string) bool {
	return len(args) == 1 && (args[0] == "--help" || args[0] == "-h") || len(args) == 2 && args[0] == "models" && (args[1] == "--help" || args[1] == "-h")
}

func headlessEmbeddings(ctx context.Context, client headlessSetupCaller, args []string, stdout io.Writer) error {
	if headlessEmbeddingsHelp(args) {
		_, err := fmt.Fprintln(stdout, headlessEmbeddingsUsage)
		return err
	}
	if len(args) == 0 || args[0] != "models" {
		return errors.New(headlessEmbeddingsUsage)
	}
	f := headlessSetupFlags("embeddings models")
	asJSON := f.Bool("json", false, "Print the organization embedding catalog as JSON")
	if f.Parse(args[1:]) != nil || f.NArg() != 0 {
		return errors.New(headlessEmbeddingsUsage)
	}
	var catalog embeddingCatalog
	if err := client.call(ctx, "GET", "/api/embedding-models", nil, &catalog); err != nil {
		return err
	}
	if *asJSON {
		return headlessWriteJSON(stdout, catalog)
	}
	if len(catalog.Models) == 0 {
		_, err := fmt.Fprintln(stdout, "No embedding models are listed for this organization.")
		return err
	}
	for _, m := range catalog.Models {
		dimension := "unknown"
		if m.Dimension != nil {
			dimension = strconv.Itoa(*m.Dimension)
		}
		if _, err := fmt.Fprintf(stdout, "%s\t%s\tdimensions=%s\n", headlessTerminalText(m.ID), headlessTerminalText(m.Name), dimension); err != nil {
			return err
		}
	}
	return nil
}
