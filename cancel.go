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
