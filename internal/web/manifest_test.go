package web

import (
	"encoding/json"
	"github.com/szhjia/stackharbor/internal/buildinfo"
	"github.com/szhjia/stackharbor/internal/sessionapi"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestEmbeddedAssetManifestMatchesBackend(t *testing.T) {
	root, _ := fs.Sub(assetFiles, "dist")
	if err := validateAssets(root, buildinfo.Version, sessionapi.ProtocolVersion); err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{}
	fs.WalkDir(root, ".", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			data, _ := fs.ReadFile(root, path)
			files[path] = &fstest.MapFile{Data: data}
		}
		return err
	})
	var m assetManifest
	json.Unmarshal(files["asset-manifest.json"].Data, &m)
	m.Version = "999.0.0"
	files["asset-manifest.json"].Data, _ = json.Marshal(m)
	if validateAssets(files, buildinfo.Version, sessionapi.ProtocolVersion) == nil {
		t.Fatal("version mismatch accepted")
	}
	m.Version = buildinfo.Version
	m.ProtocolVersion = 999
	files["asset-manifest.json"].Data, _ = json.Marshal(m)
	if validateAssets(files, buildinfo.Version, sessionapi.ProtocolVersion) == nil {
		t.Fatal("protocol mismatch accepted")
	}
	m.ProtocolVersion = sessionapi.ProtocolVersion
	files["asset-manifest.json"].Data, _ = json.Marshal(m)
	files["index.html"].Data = []byte("empty fallback")
	if validateAssets(files, buildinfo.Version, sessionapi.ProtocolVersion) == nil {
		t.Fatal("modified index accepted")
	}
}
