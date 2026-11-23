package exitcode

const (
	OK           = 0
	GeneralError = 1
	StillProvis  = 2
	AuthFailure  = 3
	RateLimited  = 4
	NotFound     = 5
	// TestsFailed: `al test` ran the tests and at least one failed.
	TestsFailed = 6
	// TestsNotRun: `al test` could not run the tests (BC below 28, no .NET, altool or env failure).
	TestsNotRun     = 7
	ProvisionFailed = 10
	// Timeout matches the convention used by GNU coreutils `timeout(1)` so wait scripts
	// can distinguish "never reached the wanted state" from a generic failure.
	Timeout = 124
)
