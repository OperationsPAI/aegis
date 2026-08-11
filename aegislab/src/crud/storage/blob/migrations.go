package blob

import "aegis/platform/framework"

// Migrations registers blob tables: blob_objects (metadata) and
// blob_bucket_configs (runtime-created buckets).
func Migrations() framework.MigrationRegistrar {
	return framework.MigrationRegistrar{
		Module:   "blob",
		Entities: []any{&ObjectRecord{}, &BucketConfigRecord{}},
	}
}
