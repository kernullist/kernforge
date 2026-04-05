package main

func shouldCancelOnEscape(hasForegroundTarget bool, shouldCancel func() bool) bool {
	if !hasForegroundTarget {
		return false
	}
	if shouldCancel != nil && !shouldCancel() {
		return false
	}
	return true
}

func confirmAndCancel(confirmCancel func() bool, cancel func()) bool {
	if confirmCancel != nil && !confirmCancel() {
		return false
	}
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func shouldCancelOnRepeatedEscape(hasForegroundTarget bool, repeatedPress bool, shouldCancel func() bool) bool {
	if !hasForegroundTarget {
		return false
	}
	if !repeatedPress {
		return false
	}
	return shouldCancelOnEscape(true, shouldCancel)
}

func isAsyncKeyPressed(state uintptr) bool {
	keyState := uint16(state)
	return (keyState&0x8000) != 0 || (keyState&0x0001) != 0
}

func isPIDInParentChain(targetPID uint32, currentPID uint32, parentLookup func(uint32) (uint32, bool)) bool {
	if targetPID == 0 || currentPID == 0 || parentLookup == nil {
		return false
	}

	visited := make(map[uint32]struct{})
	pid := currentPID
	for pid != 0 {
		if pid == targetPID {
			return true
		}
		if _, exists := visited[pid]; exists {
			return false
		}
		visited[pid] = struct{}{}

		parentPID, ok := parentLookup(pid)
		if !ok {
			return false
		}
		pid = parentPID
	}

	return false
}
