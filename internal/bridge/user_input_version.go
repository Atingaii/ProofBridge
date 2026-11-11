package bridge

import (
	"regexp"
	"strconv"
)

// See docs/features/native-structured-user-input.md for the upstream protocol
// boundaries. These are minimum versions, not an allowlist of tested releases.
const codexUserInputMinimumVersion = "0.147.0"
const claudeUserInputMinimumVersion = "2.0.71"

var userInputVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

func userInputVersionAtLeast(version, minimum string) bool {
	actual := userInputVersionPattern.FindStringSubmatch(version)
	floor := userInputVersionPattern.FindStringSubmatch(minimum)
	if actual == nil || floor == nil {
		return false
	}
	var a, b [3]uint64
	for i := range a {
		var err error
		a[i], err = strconv.ParseUint(actual[i+1], 10, 64)
		if err != nil {
			return false
		}
		b[i], err = strconv.ParseUint(floor[i+1], 10, 64)
		if err != nil {
			return false
		}
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	// A prerelease of the minimum release precedes that stable release.
	return actual[4] == ""
}
