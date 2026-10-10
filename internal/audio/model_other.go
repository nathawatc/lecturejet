//go:build !linux

package audio

import "fmt"

type ModelHandle struct{}

func OpenEmbeddedModel() (*ModelHandle, error) {
	return nil, fmt.Errorf("embedded memfd model requires Linux")
}

func (m *ModelHandle) Path() string { return "" }
func (m *ModelHandle) Close() error { return nil }
