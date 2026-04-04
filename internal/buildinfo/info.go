package buildinfo

var (
	// Version will be set via ldflags during build.
	Version = "dev"
	// Commit will be set via ldflags during build.
	Commit = "none"
	// Date will be set via ldflags during build.
	Date = "unknown"
)
