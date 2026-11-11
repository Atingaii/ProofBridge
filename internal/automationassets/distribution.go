package automationassets

import "strings"

// DistributionRef is a dedicated release tag, published together with the code
// that embeds it. Never move a published tag; release a new tag for changed assets.
const DistributionRef = "proofbridge-skill-v1.1.0"

const GitHubAssetRoot = "https://raw.githubusercontent.com/Atingaii/ProofBridge/" + DistributionRef + "/internal/automationassets"

// InstallCommand downloads and runs the public installer without sending Hub
// credentials or relying on the Hub's certificate. A failed download never runs
// a partial script or becomes a successful pipeline exit.
func InstallCommand(client string) string {
	if client != "codex" && client != "claude" {
		return ""
	}
	script := `set -eu; umask 077; installer=$(mktemp "${TMPDIR:-/tmp}/proofbridge-install.XXXXXXXX"); trap 'rm -f "$installer"' EXIT HUP INT TERM; ` +
		`curl --fail --silent --show-error --location --connect-timeout 15 --max-time 90 --retry 2 --proto '=https' --proto-redir '=https' ` +
		shellQuote(GitHubAssetRoot+"/install.sh") + ` --output "$installer"; sh "$installer" --client ` + shellQuote(client) + ` --scope user`
	return "sh -c " + shellQuote(script)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
