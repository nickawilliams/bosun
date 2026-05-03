// Command gen-man writes a troff man page for bosun.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/nickawilliams/bosun/internal/cli"
	"github.com/nickawilliams/bosun/internal/generate"
)

func main() {
	outDir := os.Getenv("MAN_OUT_DIR")
	if outDir == "" {
		outDir = filepath.Join("contrib", "man")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		log.Fatalf("unable to create man dir: %v", err)
	}

	root := cli.NewRootCmd()
	outPath := filepath.Join(outDir, "bosun.1")

	f, err := os.Create(outPath)
	if err != nil {
		log.Fatalf("unable to create man page file: %v", err)
	}
	defer f.Close()

	if err := generate.WriteManPage(f, root, generate.ManPageOptions{}); err != nil {
		log.Fatalf("unable to render man page: %v", err)
	}

	fmt.Printf("wrote man page to %s\n", outPath)
}
