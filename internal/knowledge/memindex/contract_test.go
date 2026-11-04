package memindex_test

import (
	"testing"

	"github.com/Mesteriis/veda-wiki/internal/knowledge"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/indextest"
	"github.com/Mesteriis/veda-wiki/internal/knowledge/memindex"
)

// The shared knowledge.Index contract; memindex is the reference.
func TestIndexContract(t *testing.T) {
	indextest.Run(t, func(*testing.T) knowledge.Index { return memindex.New() })
}
