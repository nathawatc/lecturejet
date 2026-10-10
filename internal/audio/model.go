package audio

import _ "embed"

//go:embed assets/std.rnnn
var stdModelBytes []byte

// ModelBytes returns the embedded std.rnnn bytes.
func ModelBytes() []byte {
	return stdModelBytes
}
