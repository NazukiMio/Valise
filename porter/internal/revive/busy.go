package revive

// RootsBusy reports whether a current user process still uses these roots.
// It never signals a process or exposes its command line.
func RootsBusy(roots []string) bool { return len(busyProcesses(roots, "")) != 0 }
