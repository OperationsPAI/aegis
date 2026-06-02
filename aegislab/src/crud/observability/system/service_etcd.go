package system

import (
	"encoding/json"
	"fmt"
	"strings"

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

// configCenterKey maps a dotted dynamic_config key (e.g.
// "rate_limiting.max_concurrent_restarts_pedestal") to the configcenter etcd
// key /aegis/<env>/<namespace>/<key>, splitting at the first dot. ok is false
// for keys without a namespace.key split (these have no /aegis representation).
func configCenterKey(dottedKey string) (string, bool) {
	dot := strings.IndexByte(dottedKey, '.')
	if dot <= 0 || dot == len(dottedKey)-1 {
		return "", false
	}
	env := config.GetString("env")
	if env == "" {
		env = "dev"
	}
	return fmt.Sprintf("/aegis/%s/%s/%s", env, dottedKey[:dot], dottedKey[dot+1:]), true
}

// configCenterValue JSON-encodes a plain dynamic_config string value into the
// JSON-scalar form the configcenter (/aegis) tree stores.
func configCenterValue(plain string) (string, error) {
	b, err := json.Marshal(plain)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func setViperIfNeeded(cfg *model.DynamicConfig, newValue string) error {
	if cfg.Scope == consts.ConfigScopeConsumer {
		return nil
	}
	return config.SetViperValue(cfg.Key, newValue, cfg.ValueType)
}
