package guided

import (
	"os"
	"testing"

	"aegis/platform/systemconfig"
	"aegis/platform/systemconfig/systemconfigtest"
)

func TestMain(m *testing.M) {
	systemconfig.SetProvider(systemconfigtest.NewInMemoryProvider(systemconfigtest.BuiltinFixtures()...))
	os.Exit(m.Run())
}
