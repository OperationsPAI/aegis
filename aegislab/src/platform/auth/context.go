package auth

import "github.com/gin-gonic/gin"

const principalContextKey = "auth.principal"

func SetPrincipal(c *gin.Context, p Principal) {
	c.Set(principalContextKey, p)
}

func GetPrincipal(c *gin.Context) (Principal, bool) {
	v, exists := c.Get(principalContextKey)
	if !exists {
		return Principal{}, false
	}
	p, ok := v.(Principal)
	return p, ok
}
