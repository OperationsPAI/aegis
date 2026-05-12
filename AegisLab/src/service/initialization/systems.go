package initialization

import (
	"aegis/platform/config"
	"aegis/service/common"

	chaos "github.com/OperationsPAI/chaos-experiment/handler"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// InitializeSystems primes the chaos-experiment runtime registry from etcd
// (via Viper, which has already been populated by the config listener).
// etcd is the single source of truth for injection.system.* — there is no
// systems table to read here anymore, and the config manager reads Viper
// on demand so no explicit reload is needed.
func InitializeSystems(db *gorm.DB) error {
	systems := config.GetChaosSystemConfigManager().GetAll()

	enabled := make(map[string]struct{}, len(systems))
	for name, cfg := range systems {
		if !cfg.IsEnabled() {
			continue
		}
		enabled[name] = struct{}{}

		sysCfg := chaos.SystemConfig{
			Name:        name,
			NsPattern:   cfg.NsPattern,
			DisplayName: cfg.DisplayName,
			AppLabelKey: normalizeAppLabelKey(cfg.AppLabelKey),
		}
		// Update in place when already registered so the compile-time
		// static metadata providers (service endpoints, DB ops, JVM methods)
		// survive the etcd refresh. Unregister+Register would wipe them,
		// which breaks guided resolve when system_metadata is empty
		// (see issue #129).
		if chaos.IsSystemRegistered(name) {
			if err := chaos.UpdateSystem(sysCfg); err != nil {
				logrus.WithError(err).Warnf("Failed to update registered system %s", name)
				continue
			}
			logrus.Infof("Updated system registration: %s (%s)", name, cfg.DisplayName)
		} else {
			if err := chaos.RegisterSystem(sysCfg); err != nil {
				logrus.WithError(err).Warnf("Failed to register system %s", name)
				continue
			}
			logrus.Infof("Registered system: %s (%s)", name, cfg.DisplayName)
		}
	}

	// Drop any runtime-only registrations that are no longer enabled in etcd.
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

	logrus.Infof("Chaos system config manager loaded %d systems (%d enabled)",
		len(systems), len(enabled))

	common.InvalidateGlobalMetadataStoreCache()
	return nil
}

// normalizeAppLabelKey mirrors the helper in module/chaossystem — blank values
// fall back to "app" to stay compatible with existing chaos-experiment behavior.
func normalizeAppLabelKey(key string) string {
	if key == "" {
		return "app"
	}
	return key
}
