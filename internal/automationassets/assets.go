// Package automationassets serves the credential-free ProofBridge end-user skill.
package automationassets

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

const Version = "1"

//go:embed install.sh skill/SKILL.md skill/references/tools.md skill/checksums.txt
var embedded embed.FS

// Installer downloads from the official GitHub release and never writes CLI configuration.
var Installer = readAsset("install.sh")

// SkillFiles are the files managed by the installer. They never contain a PAT.
var SkillFiles = map[string]string{
	"SKILL.md":            readAsset("skill/SKILL.md"),
	"references/tools.md": readAsset("skill/references/tools.md"),
}

func readAsset(name string) string {
	data, err := embedded.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func manifest() string {
	names := make([]string, 0, len(SkillFiles))
	for name := range SkillFiles {
		names = append(names, name)
	}
	sort.Strings(names)
	var out strings.Builder
	for _, name := range names {
		sum := sha256.Sum256([]byte(SkillFiles[name]))
		fmt.Fprintf(&out, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	return out.String()
}

// Handler serves /skills/install.sh and /skills/proofbridge/*.
// hubURL is canonical configuration, never an untrusted request Host.
func Handler(hubURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-cache")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body string
		switch r.URL.Path {
		case "/skills/", "/skills":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"version": Version, "hubUrl": hubURL,
					"files": []string{"SKILL.md", "references/tools.md"},
				})
			}
			return
		case "/skills/install.sh":
			body = Installer
			w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
		case "/skills/proofbridge/checksums.txt":
			body = manifest()
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		default:
			name := strings.TrimPrefix(r.URL.Path, "/skills/proofbridge/")
			value, found := SkillFiles[name]
			if !found || !strings.HasPrefix(r.URL.Path, "/skills/proofbridge/") {
				http.NotFound(w, r)
				return
			}
			body = value
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}
		hash := sha256.Sum256([]byte(body))
		etag := `"` + hex.EncodeToString(hash[:]) + `"`
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(body))
		}
	})
}
