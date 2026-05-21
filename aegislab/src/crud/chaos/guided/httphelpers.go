package guided

var allHTTPMethods = []string{"GET", "POST", "PUT", "DELETE", "HEAD", "OPTIONS", "PATCH"}

func filteredHTTPMethods(exclude string) []string {
	out := make([]string, 0, len(allHTTPMethods))
	for _, m := range allHTTPMethods {
		if m != exclude {
			out = append(out, m)
		}
	}
	return out
}
