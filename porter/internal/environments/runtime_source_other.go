//go:build !linux

package environments

import "context"

func observeRuntimeSource(context.Context, InstalledSource) (*runtimeSource, error) {
	return nil, ErrSource
}
