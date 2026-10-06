package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
)

type assetManifest struct {
	Version         string            `json:"version"`
	ProtocolVersion int               `json:"protocol_version"`
	SourceSHA256    string            `json:"source_sha256"`
	Files           map[string]string `json:"files"`
}

func validateAssets(root fs.FS, version string, protocol int) error {
	data, err := fs.ReadFile(root, "asset-manifest.json")
	if err != nil {
		return fmt.Errorf("frontend manifest unavailable: %w; run make web-build", err)
	}
	var manifest assetManifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("invalid frontend manifest: %w", err)
	}
	if manifest.Version != version || manifest.ProtocolVersion != protocol {
		return fmt.Errorf("frontend version/protocol mismatch; run make web-build")
	}
	if manifest.Files["index.html"] == "" {
		return fmt.Errorf("frontend index missing from manifest")
	}
	javascript := false
	for path, expected := range manifest.Files {
		data, err := fs.ReadFile(root, path)
		if err != nil {
			return fmt.Errorf("frontend asset %s unavailable: %w", path, err)
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != expected {
			return fmt.Errorf("frontend asset %s digest mismatch; run make web-build", path)
		}
		if strings.HasPrefix(path, "assets/") && strings.HasSuffix(path, ".js") {
			javascript = true
		}
	}
	if !javascript {
		return fmt.Errorf("frontend JavaScript bundle missing")
	}
	return nil
}
