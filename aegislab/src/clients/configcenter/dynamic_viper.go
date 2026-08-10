package configcenterclient

import (
	"context"
	"time"

	"aegis/crud/admin/configcenter"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// DynamicViperNamespace is the configcenter namespace whose entries are
// mirrored into viper so legacy `config.GetString("aegis.<key>")` callers
// (e.g. orchestrator dispatcher, catalog preflight) observe live etcd values
// without being rewritten to call configcenterclient directly.
const DynamicViperNamespace = "aegis"

// BootstrapDynamicViper seeds viper with every current entry under
// `namespace` and then watches for changes, mirroring each event back into
// viper under the dotted key "<namespace>.<entry.Key>".
//
// Best-effort: a missing / unreachable configcenter logs WARN and leaves
// viper untouched (callers fall back to TOML / env). Returns the cancel
// func for the background watcher.
func BootstrapDynamicViper(ctx context.Context, c Client, namespace string) (stop func(), err error) {
	listCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	entries, listErr := c.List(listCtx, namespace)
	if listErr != nil {
		logrus.WithError(listErr).WithField("namespace", namespace).
			Warn("configcenterclient: initial List failed; viper not seeded")
	} else {
		for _, e := range entries {
			applyEntryToViper(namespace, e)
		}
		logrus.WithFields(logrus.Fields{
			"namespace": namespace,
			"count":     len(entries),
		}).Info("configcenterclient: viper seeded from configcenter")
	}

	wctx, wcancel := context.WithCancel(context.Background())
	events, watchCancel, werr := c.Watch(wctx, namespace)
	if werr != nil {
		wcancel()
		return func() {}, werr
	}
	go func() {
		defer watchCancel()
		for e := range events {
			applyEntryToViper(namespace, e)
		}
	}()
	return func() {
		wcancel()
	}, nil
}

func applyEntryToViper(namespace string, e configcenter.Entry) {
	fullKey := namespace + "." + e.Key
	viper.Set(fullKey, e.Value)
	logrus.WithFields(logrus.Fields{
		"viper_key": fullKey,
		"layer":     e.Layer,
	}).Info("configcenterclient: viper.Set from configcenter")
}
