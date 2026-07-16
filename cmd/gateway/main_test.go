package main

import "testing"

func TestVersionOutput(t *testing.T) {
	previous := version
	version = "v1.1.0"
	t.Cleanup(func() {
		version = previous
	})

	if got := versionOutput(); got != "llm-gateway v1.1.0" {
		t.Fatalf("versionOutput() = %q", got)
	}
}
