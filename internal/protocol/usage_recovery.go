package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// RecoveryMarkerID records an attempted discovery without inventing a native
// session or adding any usage events. Versioning permits retry after upgrades.
func (r OrchestrationUsageRecovery) RecoveryMarkerID() string {
	raw, _ := json.Marshal(r)
	sum := sha256.Sum256(raw)
	return "model-recovery-v2-" + hex.EncodeToString(sum[:16])
}
