package system

import (
	"aegis/platform/config"
	"aegis/platform/consts"
	"aegis/platform/model"
)

func etcdPrefixForScope(scope consts.ConfigScope) string {
	switch scope {
	case consts.ConfigScopeProducer:
		return consts.ConfigEtcdProducerPrefix
	case consts.ConfigScopeConsumer:
		return consts.ConfigEtcdConsumerPrefix
	case consts.ConfigScopeGlobal:
		return consts.ConfigEtcdGlobalPrefix
	}
	return ""
}

func setViperIfNeeded(cfg *model.DynamicConfig, newValue string) error {
	if cfg.Scope == consts.ConfigScopeConsumer {
		return nil
	}
	return config.SetViperValue(cfg.Key, newValue, cfg.ValueType)
}
