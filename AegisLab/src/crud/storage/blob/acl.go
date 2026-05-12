package blob

// Authorizer enforces the per-bucket ACL — role 2 of the six-role
// breakdown. v1 is intentionally narrow: role lists in config, plus a
// public_read flag. Real RBAC integration (via module/auth roles)
// lands once a producer needs it.
type Authorizer struct{}

func NewAuthorizer() *Authorizer { return &Authorizer{} }

// Subject is what the handler passes in after JWT verification.
type Subject struct {
	UserID    int
	Roles     []string
	IsService bool
}

// CanWrite returns true when the caller is allowed to issue a PUT /
// presign-put against this bucket. Empty write_roles means "any
// authenticated caller can write" — fine for the scratch bucket; real
// production buckets always declare roles.
func (a *Authorizer) CanWrite(b *BucketConfig, s Subject) bool {
	if len(b.WriteRoles) == 0 {
		return true
	}
	if s.IsService {
		for _, r := range b.WriteRoles {
			if r == "service" {
				return true
			}
		}
	}
	return hasAnyRole(s.Roles, b.WriteRoles)
}

// CanRead returns true for public_read buckets, or when the caller's
// role intersects read_roles, or when the caller is the uploader.
func (a *Authorizer) CanRead(b *BucketConfig, s Subject, uploadedBy *int) bool {
	if b.PublicRead {
		return true
	}
	if uploadedBy != nil && *uploadedBy == s.UserID && s.UserID != 0 {
		return true
	}
	if len(b.ReadRoles) == 0 {
		return s.UserID != 0 || s.IsService
	}
	if s.IsService {
		for _, r := range b.ReadRoles {
			if r == "service" {
				return true
			}
		}
	}
	return hasAnyRole(s.Roles, b.ReadRoles)
}

func hasAnyRole(have, want []string) bool {
	for _, h := range have {
		for _, w := range want {
			if h == w {
				return true
			}
		}
	}
	return false
}
