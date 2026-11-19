package mock_test

import (
	"testing"

	"github.com/ti/common-go/dependencies/database"
	"github.com/ti/common-go/dependencies/database/dbtest"
	_ "github.com/ti/common-go/dependencies/database/mock"
)

// The mock must behave like the real engines: it passes the same suite.
func TestConformance(t *testing.T) {
	db, err := database.New(t.Context(), "mock://local/dbtest")
	if err != nil {
		t.Fatal(err)
	}
	dbtest.Run(t, db, dbtest.Options{})
}
