// Command fdcimport builds nutrition.db from the pinned USDA FoodData Central
// downloads and the curated aliases (§7.1).
//
//	fdcimport -cache ~/.cache/health-assistant/fdc -out nutrition.db
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wanggang316/health-assistant/server/internal/nutrition"
)

func main() {
	dir := "internal/nutrition"
	lock := flag.String("lock", filepath.Join(dir, "datasets.lock"), "datasets.lock path")
	aliases := flag.String("aliases", filepath.Join(dir, "aliases.csv"), "aliases.csv path")
	cache := flag.String("cache", filepath.Join(os.TempDir(), "fdc"), "download cache directory")
	out := flag.String("out", "nutrition.db", "output path")
	flag.Parse()
	if err := run(context.Background(), *lock, *aliases, *cache, *out); err != nil {
		fmt.Fprintln(os.Stderr, "fdcimport:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, lockPath, aliasPath, cacheDir, out string) error {
	lf, err := os.Open(lockPath)
	if err != nil {
		return err
	}
	defer lf.Close()
	ds, err := nutrition.ParseLock(lf)
	if err != nil {
		return err
	}
	var sources []nutrition.Source
	for _, d := range ds {
		p, err := nutrition.Fetch(ctx, d, cacheDir)
		if err != nil {
			return err
		}
		sources = append(sources, nutrition.Source{Dataset: d, ZipPath: p})
	}
	af, err := os.Open(aliasPath)
	if err != nil {
		return err
	}
	defer af.Close()
	version, err := nutrition.Build(ctx, out, sources, af)
	if err != nil {
		return err
	}
	fmt.Printf("wrote %s (nutrition_version %s)\n", out, version)
	return nil
}
