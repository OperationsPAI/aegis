package pedestal

import (
	"testing"

	"go.uber.org/fx"
	"gorm.io/gorm"
)

func TestModuleValidatesWithSuppliedDB(t *testing.T) {
	err := fx.ValidateApp(
		Module,
		fx.Supply(&gorm.DB{}),
	)
	if err != nil {
		t.Fatalf("fx.ValidateApp failed: %v", err)
	}
}
