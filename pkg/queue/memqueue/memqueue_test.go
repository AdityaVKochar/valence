package memqueue

import (
	"testing"

	"github.com/AdityaVKochar/valence/pkg/queue"
	"github.com/AdityaVKochar/valence/pkg/queue/queuetest"
)

func TestConformance(t *testing.T) {
	queuetest.Run(t, func(*testing.T) queue.Queue { return New() })
}
