package storetest_test

import (
	"testing"

	"github.com/ti/common-go/background"
	"github.com/ti/common-go/background/storetest"
)

func TestMemoryStore(t *testing.T) {
	storetest.Run(t, func(*testing.T) background.Store { return background.NewMemoryStore() })
}
