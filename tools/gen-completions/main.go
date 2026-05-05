// Command gen-completions writes bash/zsh/fish completion scripts for bosun.
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
	outDir := os.Getenv("COMPLETIONS_OUT_DIR")
	if outDir == "" {
		outDir = filepath.Join("contrib", "completions")
	}

	cmd := cli.NewRootCmd()

	shells := []struct {
		dir   string
		file  string
		write func(*os.File) error
	}{
		{"bash", "bosun.bash", func(f *os.File) error { return generate.WriteBashCompletion(f, cmd) }},
		{"zsh", "bosun.zsh", func(f *os.File) error { return generate.WriteZshCompletion(f, cmd) }},
		{"fish", "bosun.fish", func(f *os.File) error { return generate.WriteFishCompletion(f, cmd) }},
	}

	for _, s := range shells {
		dir := filepath.Join(outDir, s.dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("unable to create %s completions dir: %v", s.dir, err)
		}
		path := filepath.Join(dir, s.file)
		f, err := os.Create(path)
		if err != nil {
			log.Fatalf("unable to create %s: %v", path, err)
		}
		if err := s.write(f); err != nil {
			_ = f.Close()
			log.Fatalf("unable to generate %s completion: %v", s.dir, err)
		}
		if err := f.Close(); err != nil {
			log.Fatalf("unable to close %s: %v", path, err)
		}
	}

	fmt.Printf("wrote completions to %s\n", outDir)
}
