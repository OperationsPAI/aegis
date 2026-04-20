package initialization

import (
	"aegis/config"
	"aegis/service/common"
	"fmt"

	chaos "github.com/OperationsPAI/chaos-experiment/handler"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// InitializeSystems wires the systems table into chaos-experiment, making the
// database the source of truth for which benchmark systems exist at runtime.
func InitializeSystems(db *gorm.DB) error {
	config.SetChaosConfigDB(db)

	systems, err := newBootstrapStore(db).listEnabledSystems()
	if err != nil {
		return fmt.Errorf("failed to load enabled systems: %w", err)
	}

	enabled := make(map[string]struct{}, len(systems))
	for _, sys := range systems {
		enabled[sys.Name] = struct{}{}
		if chaos.IsSystemRegistered(sys.Name) {
			if err := chaos.UnregisterSystem(sys.Name); err != nil {
				logrus.WithError(err).Warnf("Failed to replace registered system %s", sys.Name)
			}
		}
		if err := chaos.RegisterSystem(chaos.SystemConfig{
			Name:        sys.Name,
			NsPattern:   sys.NsPattern,
			DisplayName: sys.DisplayName,
			AppLabelKey: sys.AppLabelKey,
		}); err != nil {
			logrus.WithError(err).Warnf("Failed to register system %s", sys.Name)
		} else {
			logrus.Infof("Registered system: %s (%s)", sys.Name, sys.DisplayName)
		}
	}

	for _, registered := range chaos.GetAllSystemTypes() {
		if _, ok := enabled[registered.String()]; ok {
			continue
		}
		if err := chaos.UnregisterSystem(registered.String()); err == nil {
			logrus.Infof("Removed runtime-only system registration: %s", registered.String())
		}
	}

	store := common.NewDBMetadataStore(db)
	chaos.SetMetadataStore(store)
	logrus.Info("Set global DBMetadataStore for chaos-experiment")

	if err := config.GetChaosSystemConfigManager().Reload(func() error { return nil }); err != nil {
		logrus.Warnf("Failed to reload chaos system config: %v", err)
	} else {
		logrus.Infof("Chaos system config manager loaded %d systems", len(config.GetChaosSystemConfigManager().GetAll()))
	}

	common.InvalidateGlobalMetadataStoreCache()
	return nil
}
