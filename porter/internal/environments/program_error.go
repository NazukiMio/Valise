package environments

import "strconv"

// ProgramError contains only a fixed reason and archive entry count. No path,
// file bytes, remote output or decoder error is exposed through diagnostics.
type ProgramError struct {
	Code  string
	Entry int
}

func (e *ProgramError) Error() string {
	return ErrProgram.Error() + ": " + e.Code + " (entry " + strconv.Itoa(e.Entry) + ")"
}
func (e *ProgramError) Unwrap() error { return ErrProgram }
