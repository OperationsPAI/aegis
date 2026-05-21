package guidedcli

// HTTP Method enum
type HTTPMethod int

const (
	GET HTTPMethod = iota
	POST
	PUT
	DELETE
	HEAD
	OPTIONS
	PATCH
)

var httpMethodMap = map[HTTPMethod]string{
	GET:     "GET",
	POST:    "POST",
	PUT:     "PUT",
	DELETE:  "DELETE",
	HEAD:    "HEAD",
	OPTIONS: "OPTIONS",
	PATCH:   "PATCH",
}

// GetHTTPMethodName returns the string representation of an HTTP method
func GetHTTPMethodName(method HTTPMethod) string {
	if name, exists := httpMethodMap[method]; exists {
		return name
	}
	return "GET" // Default to GET
}

// GetFilteredHTTPMethods returns HTTP methods excluding the specified original method
func GetFilteredHTTPMethods(originalMethod string) []HTTPMethod {
	var filtered []HTTPMethod

	for method, name := range httpMethodMap {
		if name != originalMethod {
			filtered = append(filtered, method)
		}
	}

	// Sort to ensure consistent ordering
	// Manual sort since we have a small, fixed set
	var sorted []HTTPMethod
	methodOrder := []HTTPMethod{GET, POST, PUT, DELETE, HEAD, OPTIONS, PATCH}

	for _, orderedMethod := range methodOrder {
		for _, filteredMethod := range filtered {
			if orderedMethod == filteredMethod {
				sorted = append(sorted, filteredMethod)
				break
			}
		}
	}

	return sorted
}

// GetFilteredHTTPMethodByIndex returns the HTTP method at the given index from filtered list
func GetFilteredHTTPMethodByIndex(originalMethod string, index int) HTTPMethod {
	filtered := GetFilteredHTTPMethods(originalMethod)
	if index >= 0 && index < len(filtered) {
		return filtered[index]
	}
	// Return first available method as fallback
	if len(filtered) > 0 {
		return filtered[0]
	}
	return GET // Ultimate fallback
}

// HTTP Status Codes for replace
type HTTPStatusCode int

const (
	BadRequest HTTPStatusCode = iota
	Unauthorized
	Forbidden
	NotFound
	MethodNotAllowed
	RequestTimeout
	InternalServerError
	BadGateway
	ServiceUnavailable
	GatewayTimeout
)

var httpStatusCodeMap = map[HTTPStatusCode]int32{
	BadRequest:          400,
	Unauthorized:        401,
	Forbidden:           403,
	NotFound:            404,
	MethodNotAllowed:    405,
	RequestTimeout:      408,
	InternalServerError: 500,
	BadGateway:          502,
	ServiceUnavailable:  503,
	GatewayTimeout:      504,
}

// GetHTTPStatusCode returns the numeric HTTP status code
func GetHTTPStatusCode(statusCode HTTPStatusCode) int32 {
	if code, exists := httpStatusCodeMap[statusCode]; exists {
		return code
	}
	return 500 // Default to Internal Server Error
}

