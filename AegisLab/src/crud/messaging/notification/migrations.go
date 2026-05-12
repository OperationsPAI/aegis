package notification

import "aegis/platform/framework"

// Migrations registers the inbox tables.
func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module: "notification",
		Entities: []any{
			&Notification{},
			&NotificationSubscription{},
			&NotificationDelivery{},
		},
	}
}
