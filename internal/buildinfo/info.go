package buildinfo

var (
	// Version will be set via ldflags during build.
	Version = "dev"
	// Commit will be set via ldflags during build.
	Commit = "none"
	// Date will be set via ldflags during build.
	Date = "unknown"
)

// Token identifies THIS BUILD to a browser that is already showing a page, so
// a screen can notice it is running code the server has since replaced and
// reload itself. A fix shipped mid-quiz is worth nothing if the wall and
// twenty phones keep running the bundle they booted with an hour ago.
//
// The commit is the right input because it is identical across every process
// of one deploy. A timestamp or a process id would differ between two web
// containers, and screens would then reload forever, flip-flopping between
// whichever one answered.
func Token() string {
	if Commit != "" && Commit != "none" {
		return Commit
	}
	// A local `go run` has no ldflags, so this is the stable "dev" and a
	// developer's screen never self-reloads -- which is what you want while
	// you are the one pressing reload.
	return Version
}
