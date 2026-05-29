package version

// Version and Date are edited by scripts/bump.sh on each release.
// Run `./scripts/bump.sh` (patch), `./scripts/bump.sh minor`, or
// `./scripts/bump.sh major` to increment and commit automatically.
var (
	Version = "1.0.1"
	Date    = "2026-05-29"
)

// LatestReleaseURL is the endpoint `mos version` fetches to check for updates.
// deploy.sh writes the current version to the server so this always reflects
// the latest deployed release. Update if DefaultServerIP in shared/paths.go changes.
const LatestReleaseURL = "http://178.128.151.84:8080/"
