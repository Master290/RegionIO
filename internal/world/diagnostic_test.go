package world

import (
	"os"
	"testing"
)

// requireDiagnostic gates the expensive one-shot worldgen probes behind their
// own environment variable, which is the convention every diagnostic in this
// package follows. Run unconditionally they dominate `go test ./...`, and that
// command has to stay a fast signal.
func requireDiagnostic(t *testing.T, variable string) {
	t.Helper()
	if os.Getenv(variable) != "1" {
		t.Skipf("set %s=1 to run this diagnostic", variable)
	}
}
