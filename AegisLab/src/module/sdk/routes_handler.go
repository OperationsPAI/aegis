package sdk

import "aegis/framework"

func AsRoutesHandler(handler *Handler) framework.SDKRoutesHandler {
	return handler
}
