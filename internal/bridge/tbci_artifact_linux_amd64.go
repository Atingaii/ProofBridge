//go:build linux && amd64

package bridge

import _ "embed"

//go:embed tbci_assets/tbci-linux-amd64
var embeddedTBCILinuxAMD64 []byte

func embeddedTBCIArtifact() tbciArtifact {
	return tbciArtifact{
		data:     embeddedTBCILinuxAMD64,
		digest:   "82f2fa855505a7cb3f5189f1e48db6a9b02433e5a623d3df3547af70c60f027d",
		platform: "linux/amd64",
		version:  "39136b7f06e0+proofbridge-rocq-v3",
	}
}
