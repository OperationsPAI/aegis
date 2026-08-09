package notification

import "time"

// Notification is one row in a user's inbox. Per-recipient: a single
// Event fan-out to N users produces N Notification rows, each tracked
// independently for read/archive state. This avoids the read-receipts
// join on every list query.
type Notification struct {
	ID          int64     `gorm:"primaryKey;autoIncrement"`
	UserID      int       `gorm:"not null;index:idx_user_unread,priority:1;index:idx_user_category,priority:1"`
	Category    string    `gorm:"not null;size:64;index:idx_user_category,priority:2"`
	Severity    string    `gorm:"not null;size:16;default:'info'"`
	Title       string    `gorm:"not null;size:256"`
	Body        string    `gorm:"type:text"`
	LinkTo      string    `gorm:"size:512"`
	ActorUserID *int      `gorm:"index"`
	ActorName   string    `gorm:"size:128"` // denormalised at publish time
	EntityKind  string    `gorm:"size:64;index:idx_entity,priority:1"`
	EntityID    string    `gorm:"size:128;index:idx_entity,priority:2"`
	Payload     []byte    `gorm:"type:json;serializer:json"`
	DedupeKey   string    `gorm:"size:128;index"`
	CreatedAt   time.Time `gorm:"autoCreateTime;index:idx_user_unread,priority:3,sort:desc;index:idx_user_category,priority:3,sort:desc"`
	ReadAt      *time.Time
	ArchivedAt  *time.Time
}

func (Notification) TableName() string { return "notifications" }

// NotificationSubscription stores per-user per-category per-channel
// preferences. v1 ships with everything default-on; the UI to manage
// these is in a follow-up.
type NotificationSubscription struct {
	UserID    int       `gorm:"primaryKey"`
	Category  string    `gorm:"primaryKey;size:64"`
	Channel   string    `gorm:"primaryKey;size:32"`
	Enabled   bool      `gorm:"not null;default:true"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"`
}

func (NotificationSubscription) TableName() string { return "notification_subscriptions" }

// NotificationDelivery records every Recipient×Channel attempt. v1
// writes one row per attempt with status; v2 will use this to drive a
// retry queue and the operator-facing delivery dashboard.
type NotificationDelivery struct {
	ID             int64  `gorm:"primaryKey;autoIncrement"`
	NotificationID int64  `gorm:"index"`
	UserID         int    `gorm:"index"`
	Channel        string `gorm:"not null;size:32;index"`
	Status         string `gorm:"not null;size:16;index"`
	Attempt        int    `gorm:"not null;default:1"`
	Error          string `gorm:"type:text"`
	CreatedAt      time.Time
}

func (NotificationDelivery) TableName() string { return "notification_deliveries" }
