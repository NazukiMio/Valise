//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly

package service

// tryFileLock has no cross-process lock on this system yet: copies are
// not guarded against a second Valise process.
func tryFileLock(string) (func(), bool, error) { return func() {}, false, nil }
