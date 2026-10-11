package ui

import "os"

// IsTerminal reports whether v is an open character device terminal.
func IsTerminal(v any) bool {
	if file, ok := v.(*os.File); ok && file != nil {
		info, err := file.Stat()
		return err == nil && info.Mode()&os.ModeCharDevice != 0
	}
	return false
}
