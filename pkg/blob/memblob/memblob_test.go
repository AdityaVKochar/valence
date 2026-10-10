package memblob

import (
	"testing"

	"github.com/AdityaVKochar/valence/pkg/blob"
	"github.com/AdityaVKochar/valence/pkg/blob/blobtest"
)

func TestConformance(t *testing.T) {
	blobtest.Run(t, func(*testing.T) blob.Store { return New() })
}
