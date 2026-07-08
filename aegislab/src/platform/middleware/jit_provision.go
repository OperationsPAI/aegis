package middleware

import (
	"errors"

	"aegis/platform/auth"
	"aegis/platform/consts"
	"aegis/platform/model"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// JITProvision returns a Gin middleware that ensures every externally
// authenticated human principal has a corresponding MySQL user row.
//
// It runs AFTER the auth middleware (UnifiedAuth / TrustedHeaderAuth) so a
// Principal is already present in the context. For principals whose Idp is
// "aegis" (local auth) or whose type is non-human (service / task /
// service_account) the middleware is a no-op.
//
// On first encounter (no user row for the token's email) a lightweight
// record is created. On subsequent requests the existing row is reused and
// the principal's UserID is back-filled so downstream handlers see a valid
// FK-compatible identifier.
func JITProvision(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if db == nil {
			c.Next()
			return
		}

		p, ok := auth.GetPrincipal(c)
		if !ok || p.Typ != auth.PrincipalHuman || isLocalIdp(p.Idp) || p.Email == "" {
			c.Next()
			return
		}

		userID, created, err := ensureUser(db, p)
		if err != nil {
			logrus.WithError(err).WithField("email", p.Email).
				Warn("jit_provision: failed to ensure user record")
			c.Next()
			return
		}

		if userID != p.UserID {
			p.UserID = userID
			auth.SetPrincipal(c, p)
			c.Set(consts.CtxKeyUserID, userID)
		}

		if created {
			syncRoles(db, userID, p.Roles)
		}

		c.Next()
	}
}

func isLocalIdp(idp string) bool {
	switch idp {
	case "", "aegis", "local", "internal", "gateway":
		return true
	}
	return false
}

func ensureUser(db *gorm.DB, p auth.Principal) (userID int, created bool, err error) {
	var user model.User
	err = db.Where("email = ? AND status != ?", p.Email, consts.CommonDeleted).
		First(&user).Error
	if err == nil {
		return user.ID, false, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, err
	}

	username := p.Username
	if username == "" {
		username = p.Email
	}

	user = model.User{
		Username: username,
		Email:    p.Email,
		FullName: username,
		IsActive: true,
		Password: "",
		Status:   consts.CommonEnabled,
	}
	if err := db.Omit("active_username").Create(&user).Error; err != nil {
		var existing model.User
		if findErr := db.Where("email = ? AND status != ?", p.Email, consts.CommonDeleted).
			First(&existing).Error; findErr == nil {
			return existing.ID, false, nil
		}
		return 0, false, err
	}

	logrus.WithField("email", p.Email).WithField("user_id", user.ID).
		Info("jit_provision: created user record for external IdP principal")

	return user.ID, true, nil
}

func syncRoles(db *gorm.DB, userID int, tokenRoles []string) {
	if len(tokenRoles) == 0 {
		return
	}

	for _, roleName := range tokenRoles {
		var role model.Role
		if err := db.Where("name = ? AND status = ?", roleName, consts.CommonEnabled).
			First(&role).Error; err != nil {
			continue
		}

		var count int64
		db.Model(&model.UserRole{}).
			Where("user_id = ? AND role_id = ?", userID, role.ID).
			Count(&count)
		if count > 0 {
			continue
		}

		if err := db.Create(&model.UserRole{
			UserID: userID,
			RoleID: role.ID,
		}).Error; err != nil {
			logrus.WithError(err).WithField("role", roleName).
				Warn("jit_provision: failed to assign role")
		}
	}
}
