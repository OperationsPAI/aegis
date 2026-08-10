package middleware

import (
	"aegis/platform/auth"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// UserProvisioner abstracts the user lookup/create operations needed by JIT
// provisioning. Implemented by the crud-level user service to avoid an
// import from platform → crud.
type UserProvisioner interface {
	EnsureExternalUser(email, username string, roles []string) (userID int, err error)
}

// JITProvision returns a Gin middleware that ensures every authenticated
// human principal with UserID=0 gets a corresponding MySQL user row.
func JITProvision(prov UserProvisioner) gin.HandlerFunc {
	return func(c *gin.Context) {
		if prov == nil {
			c.Next()
			return
		}

		p, ok := auth.GetPrincipal(c)
		if !ok || p.Typ != auth.PrincipalHuman || p.Email == "" {
			c.Next()
			return
		}
		if p.UserID != 0 {
			c.Next()
			return
		}

		username := p.Username
		if username == "" {
			username = p.Email
		}

		userID, err := prov.EnsureExternalUser(p.Email, username, p.Roles)
		if err != nil {
			logrus.WithError(err).WithField("email", p.Email).
				Warn("jit_provision: failed to ensure user record")
			c.Next()
			return
		}

		if userID != 0 && userID != p.UserID {
			p.UserID = userID
			auth.SetPrincipal(c, p)
		}

		c.Next()
	}
}

