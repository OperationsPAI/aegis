package share

import (
	"aegis/platform/config"

	"github.com/spf13/viper"
	"go.uber.org/fx"
)

var Module = fx.Module("share",
	fx.Provide(provideConfig),
	fx.Provide(NewBlobBackend),
	fx.Provide(NewRepository),
	fx.Provide(NewService),
	fx.Provide(NewHandler),
	fx.Provide(
		fx.Annotate(RoutesPortal, fx.ResultTags(`group:"routes"`)),
		fx.Annotate(Migrations, fx.ResultTags(`group:"migrations"`)),
	),
	fx.Invoke(MountPublic),
)

func provideConfig() Config {
	cfg := Config{
		Bucket:            config.GetString("share.bucket"),
		PublicBaseURL:     config.GetString("share.public_base_url"),
		DefaultTTLSeconds: viper.GetInt64("share.default_ttl_seconds"),
		MaxTTLSeconds:     viper.GetInt64("share.max_ttl_seconds"),
		MaxViews:          config.GetInt("share.max_views"),
		MaxUploadBytes:    viper.GetInt64("share.max_upload_bytes"),
		UserQuotaBytes:    viper.GetInt64("share.user_quota_bytes"),
	}
	if cfg.Bucket == "" {
		cfg.Bucket = "shared"
	}
	if cfg.DefaultTTLSeconds == 0 {
		cfg.DefaultTTLSeconds = 604800
	}
	if cfg.MaxTTLSeconds == 0 {
		cfg.MaxTTLSeconds = 2592000
	}
	if cfg.MaxViews == 0 {
		cfg.MaxViews = 10000
	}
	if cfg.MaxUploadBytes == 0 {
		cfg.MaxUploadBytes = 1 << 30
	}
	if cfg.UserQuotaBytes == 0 {
		cfg.UserQuotaBytes = 10 << 30
	}
	return cfg
}
