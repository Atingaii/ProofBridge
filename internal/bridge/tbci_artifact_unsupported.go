//go:build !linux || !amd64

package bridge

func embeddedTBCIArtifact() tbciArtifact {
	return tbciArtifact{platform: "unsupported"}
}
