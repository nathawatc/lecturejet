package ui

import (
	"bytes"
	"testing"
)

func TestIsTerminalNonFile(t *testing.T) {
	if IsTerminal(nil) {
		t.Fatal("expected nil not to be terminal")
	}
	var buf bytes.Buffer
	if IsTerminal(&buf) {
		t.Fatal("expected bytes.Buffer not to be terminal")
	}
}
