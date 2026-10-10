//go:build cgo && linux

package media

/*
#include <stdint.h>
*/
import "C"

import "sync"

var (
	renderProgressMu sync.RWMutex
	renderProgress   func(int)
	renderCallMu     sync.Mutex
)

func setRenderProgress(progress func(int)) {
	renderProgressMu.Lock()
	renderProgress = progress
	renderProgressMu.Unlock()
}

//export lecturejetProgress
func lecturejetProgress(permille C.int) {
	renderProgressMu.RLock()
	progress := renderProgress
	renderProgressMu.RUnlock()
	if progress != nil {
		progress(int(permille))
	}
}

func withRenderCall(call func()) {
	renderCallMu.Lock()
	defer renderCallMu.Unlock()
	call()
}
