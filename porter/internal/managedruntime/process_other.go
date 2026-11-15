//go:build !linux

package managedruntime

func emptyGroup(group, unit string) (bool, error)                       { return false, ErrState }
func ownedMainProcess(pid int, group, unit string) bool                 { return false }
func processStart(pid int) (string, error)                              { return "", ErrState }
func ownedListener(group, unit, host string, port int) bool             { return false }
func nativeProcess(pid int, group, unit string) (string, string, error) { return "", "", ErrState }
