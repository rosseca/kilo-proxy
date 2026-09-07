// Regenerate the selected K artwork: go run ./cmd/icon-assets.
package main

import (
	"fmt"
	"kilo-local/internal/brand"
	"os"
	"path/filepath"
)

func main() {
	for name, data := range brand.Assets() {
		path := filepath.Join("ui", name)
		if err := os.WriteFile(path, data, 0644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
