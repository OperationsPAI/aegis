package sso

import (
	"encoding/json"

	"aegis/platform/jwtkeys"

	"github.com/golang-jwt/jwt/v5"
)

type jwksDoc struct {
	cached jwtkeys.JWKS
	json   []byte
}

func newJWKSDoc(pub *jwtkeys.Signer) (*jwksDoc, error) {
	jwks := jwtkeys.JWKSFromPublicKey(pub.PublicKey(), pub.Kid)
	body, err := json.Marshal(jwks)
	if err != nil {
		return nil, err
	}
	return &jwksDoc{cached: jwks, json: body}, nil
}

func signWithKid(claims jwt.Claims, signer *jwtkeys.Signer) (string, error) {
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	if signer.Kid != "" {
		tok.Header["kid"] = signer.Kid
	}
	return tok.SignedString(signer.PrivateKey)
}
