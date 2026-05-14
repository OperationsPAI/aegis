package sso

import (
	"net/http"
	"strings"
	"time"

	"aegis/platform/consts"
	"aegis/platform/crypto"
	"aegis/platform/model"
	"aegis/platform/utils"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// grantAuthCode handles the `authorization_code` grant on /token.
//
//	@Summary		OIDC token: authorization_code grant
//	@Description	Internal dispatch path of /token for `grant_type=authorization_code`. Consumes the one-time code persisted by /authorize, verifies PKCE for public clients, and returns access + refresh tokens.
//	@Tags			OIDC
//	@ID				oidc_token_authorization_code
//	@Accept			x-www-form-urlencoded
//	@Produce		json
//	@Param			code			formData	string	true	"Authorization code from /authorize"
//	@Param			redirect_uri	formData	string	true	"Redirect URI used at /authorize"
//	@Param			code_verifier	formData	string	false	"PKCE code verifier (required when /authorize used PKCE)"
//	@Success		200	{object}	tokenResp			"Token response"
//	@Failure		400	{object}	map[string]string	"OIDC error"
func (s *OIDCService) grantAuthCode(c *gin.Context, cli *model.OIDCClient) {
	code := c.PostForm("code")
	redirectURI := c.PostForm("redirect_uri")
	ar, err := s.consumeAuthRequest(c.Request.Context(), code)
	if err != nil {
		tokenError(c, http.StatusBadRequest, consts.OIDCErrorInvalidGrant, err.Error())
		return
	}
	if ar.ClientID != cli.ClientID || ar.RedirectURI != redirectURI {
		tokenError(c, http.StatusBadRequest, consts.OIDCErrorInvalidGrant, "code does not match client/redirect")
		return
	}
	codeVerifier := c.PostForm("code_verifier")
	if ar.CodeChallenge != "" {
		if codeVerifier == "" {
			tokenError(c, http.StatusBadRequest, consts.OIDCErrorInvalidGrant, "code_verifier required")
			return
		}
		if !verifyPKCE(ar.CodeChallenge, ar.CodeChallengeMethod, codeVerifier) {
			tokenError(c, http.StatusBadRequest, consts.OIDCErrorInvalidGrant, "code_verifier mismatch")
			return
		}
	} else if !cli.IsConfidential {
		tokenError(c, http.StatusBadRequest, consts.OIDCErrorInvalidGrant, "public client requires PKCE")
		return
	}
	u, err := s.users.GetByID(c.Request.Context(), ar.UserID)
	if err != nil {
		tokenError(c, http.StatusBadRequest, consts.OIDCErrorInvalidGrant, "user gone")
		return
	}
	s.respondUserToken(c, cli, u, true)
}

// grantRefresh handles the `refresh_token` grant on /token.
//
//	@Summary		OIDC token: refresh_token grant
//	@Description	Internal dispatch path of /token for `grant_type=refresh_token`. Validates the refresh token's owning client, rotates the refresh token, and issues a fresh access token.
//	@Tags			OIDC
//	@ID				oidc_token_refresh_token
//	@Accept			x-www-form-urlencoded
//	@Produce		json
//	@Param			refresh_token	formData	string	true	"Refresh token issued by a previous token call"
//	@Success		200	{object}	tokenResp			"Token response"
//	@Failure		400	{object}	map[string]string	"OIDC error"
func (s *OIDCService) grantRefresh(c *gin.Context, cli *model.OIDCClient) {
	rt := c.PostForm("refresh_token")
	rec, err := s.loadRefresh(c.Request.Context(), rt)
	if err != nil {
		tokenError(c, http.StatusBadRequest, consts.OIDCErrorInvalidGrant, err.Error())
		return
	}
	if rec.ClientID != cli.ClientID {
		tokenError(c, http.StatusBadRequest, consts.OIDCErrorInvalidGrant, "refresh token bound to different client")
		return
	}
	u, err := s.users.GetByID(c.Request.Context(), rec.UserID)
	if err != nil {
		tokenError(c, http.StatusBadRequest, consts.OIDCErrorInvalidGrant, "user gone")
		return
	}
	// Rotate the refresh token to invalidate the replayed one.
	_, _ = s.redis.DeleteKey(c.Request.Context(), refreshRedisPrefix+rt)
	s.respondUserToken(c, cli, u, true)
}

// grantClientCredentials handles the `client_credentials` grant on /token.
//
//	@Summary		OIDC token: client_credentials grant
//	@Description	Internal dispatch path of /token for `grant_type=client_credentials`. Issues a service token bound to the client's `service` and configured scopes — used for service-to-service authentication.
//	@Tags			OIDC
//	@ID				oidc_token_client_credentials
//	@Accept			x-www-form-urlencoded
//	@Produce		json
//	@Success		200	{object}	tokenResp			"Service token response"
//	@Failure		500	{object}	map[string]string	"Token signing failed"
func (s *OIDCService) grantClientCredentials(c *gin.Context, cli *model.OIDCClient) {
	exp := time.Now().Add(utils.ServiceTokenExpiration)
	claims := jwt.MapClaims{
		"iss":                     s.issuer,
		consts.OIDCClaimSubject:   consts.ClaimSubjectServicePrefix + cli.Service,
		consts.OIDCClaimAudience:  []string{consts.AudienceSSOInternal},
		"exp":                     exp.Unix(),
		"iat":                     time.Now().Unix(),
		"service":                 cli.Service,
		"scopes":                  cli.Scopes,
		consts.OIDCClaimTokenType: consts.TokenTypeService,
	}
	signed, err := signWithKid(claims, s.signer)
	if err != nil {
		tokenError(c, http.StatusInternalServerError, consts.OIDCErrorServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, tokenResp{
		AccessToken: signed,
		TokenType:   consts.TokenTypeBearer,
		ExpiresIn:   int64(time.Until(exp).Seconds()),
	})
}

// grantPassword handles the `password` grant on /token.
//
//	@Summary		OIDC token: password grant
//	@Description	Internal dispatch path of /token for `grant_type=password`. Verifies the resource owner's username/password and issues an access token. Intended for trusted first-party CLI/test clients only.
//	@Tags			OIDC
//	@ID				oidc_token_password
//	@Accept			x-www-form-urlencoded
//	@Produce		json
//	@Param			username	formData	string	true	"Username or email"
//	@Param			password	formData	string	true	"Password"
//	@Success		200	{object}	tokenResp			"Token response"
//	@Failure		401	{object}	map[string]string	"Invalid credentials"
func (s *OIDCService) grantPassword(c *gin.Context, cli *model.OIDCClient) {
	username := c.PostForm("username")
	password := c.PostForm("password")
	u, err := s.users.GetByUsername(c.Request.Context(), username)
	if err != nil && strings.Contains(username, "@") {
		u, err = s.users.GetByEmail(c.Request.Context(), username)
	}
	if err != nil || !crypto.VerifyPassword(password, u.Password) || !u.IsActive {
		tokenError(c, http.StatusUnauthorized, consts.OIDCErrorInvalidGrant, "invalid credentials")
		return
	}
	s.respondUserToken(c, cli, u, false)
}

func (s *OIDCService) respondUserToken(c *gin.Context, cli *model.OIDCClient, u *model.User, withRefresh bool) {
	roles, _ := s.users.ListRoleNames(c.Request.Context(), u.ID)
	isAdmin := false
	for _, r := range roles {
		if r == string(consts.RoleSuperAdmin) || r == string(consts.RoleAdmin) {
			isAdmin = true
			break
		}
	}
	access, expiresAt, err := utils.GenerateToken(u.ID, u.Username, u.Email, u.IsActive, isAdmin, roles, s.signer.PrivateKey, s.signer.Kid)
	if err != nil {
		tokenError(c, http.StatusInternalServerError, consts.OIDCErrorServerError, err.Error())
		return
	}
	resp := tokenResp{
		AccessToken: access,
		TokenType:   consts.TokenTypeBearer,
		ExpiresIn:   int64(time.Until(expiresAt).Seconds()),
	}
	if withRefresh && grantAllowed(cli, consts.OIDCGrantRefreshToken) {
		rt, err := randomToken(32)
		if err != nil {
			tokenError(c, http.StatusInternalServerError, consts.OIDCErrorServerError, err.Error())
			return
		}
		if err := s.storeRefresh(c.Request.Context(), rt, refreshRecord{UserID: u.ID, ClientID: cli.ClientID}); err != nil {
			tokenError(c, http.StatusInternalServerError, consts.OIDCErrorServerError, err.Error())
			return
		}
		resp.RefreshToken = rt
	}
	c.JSON(http.StatusOK, resp)
}

func grantAllowed(cli *model.OIDCClient, grant string) bool {
	for _, g := range cli.Grants {
		if g == grant {
			return true
		}
	}
	return false
}

func redirectAllowed(cli *model.OIDCClient, uri string) bool {
	for _, r := range cli.RedirectURIs {
		if r == uri {
			return true
		}
	}
	return false
}
