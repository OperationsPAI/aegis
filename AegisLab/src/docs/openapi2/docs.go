package openapi2

import "github.com/swaggo/swag"

const docTemplate = `{
    "swagger": "2.0",
    "info": {
        "title": "AegisLab API",
        "version": "dev"
    },
    "paths": {
        "/api/v2/auth/login": {
            "post": {
                "summary": "User login"
            }
        }
    }
}`

type swaggerInfo struct{}

func (swaggerInfo) ReadDoc() string {
	return docTemplate
}

func (swaggerInfo) InstanceName() string {
	return swag.Name
}

func init() {
	swag.Register(swag.Name, swaggerInfo{})
}
