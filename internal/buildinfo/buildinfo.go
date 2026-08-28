package buildinfo

// These values are replaced by release builds through -ldflags.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)
