//go:build !unix

package sandbox

import (
	"context"
	"errors"
)

type Unsafe struct{}

func NewUnsafe(string) (*Unsafe, error) {
	return nil, errors.New("local-unsafe needs a Unix system; on Windows run the worker inside WSL2")
}

func (s *Unsafe) Name() string                           { return "local-unsafe" }
func (s *Unsafe) Health(context.Context) error           { return errors.New("unsupported") }
func (s *Unsafe) Open(context.Context, int) (Box, error) { return nil, errors.New("unsupported") }

func RunHelperIfRequested() {}
