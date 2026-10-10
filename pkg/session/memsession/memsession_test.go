package memsession

import (
	"testing"

	"github.com/AdityaVKochar/valence/pkg/session"
	"github.com/AdityaVKochar/valence/pkg/session/sessiontest"
)

func TestConformance(t *testing.T) {
	sessiontest.Run(t, func(*testing.T) (session.Store, int64, int64) { return New(), 1, 2 })
}
