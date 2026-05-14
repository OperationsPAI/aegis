package pedestal

import "testing"

// Compile-time assertion: *Repository must satisfy Reader.
var _ Reader = (*Repository)(nil)

func TestAsReaderReturnsSameRepo(t *testing.T) {
	repo := &Repository{}
	reader := AsReader(repo)
	if reader != repo {
		t.Error("AsReader should return the same *Repository as a Reader")
	}
}
