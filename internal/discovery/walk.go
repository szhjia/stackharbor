package discovery

import (
	"context"
	"github.com/szhjia/stackharbor/internal/model"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var skipped = map[string]bool{".git": true, "node_modules": true, ".venv": true, "venv": true, "dist": true, "build": true, "coverage": true, ".pdforge": true, "data": true, ".stackharbor": true, ".superpowers": true, ".cache": true}

func scan(ctx context.Context, root string, paths, exclude []string) ([]string, []string, []model.Diagnostic) {
	dirs := []string{}
	ds := []model.Diagnostic{}
	count := 0
	rootIsTool := toolDistribution(root)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if p != root && (toolDistribution(p) || (rootIsTool && p == filepath.Join(root, "examples")) || skipped[d.Name()]) {
				return fs.SkipDir
			}
			rel, _ := filepath.Rel(root, p)
			for _, x := range exclude {
				if rel == x || strings.HasPrefix(rel, x+string(filepath.Separator)) {
					return fs.SkipDir
				}
			}
			count++
			if count > 10000 {
				return fs.ErrInvalid
			}
			dirs = append(dirs, p)
		} else if d.Name() == "stackharbor.yaml" {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		ds = append(ds, model.Error(root, "discovery", err.Error()))
	}
	return paths, dirs, ds
}

func toolDistribution(dir string) bool {
	info, err := os.Lstat(filepath.Join(dir, ".stackharbor-tool"))
	return err == nil && info.Mode().IsRegular()
}
