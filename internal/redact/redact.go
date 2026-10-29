// Package redact removes values that must not reach logs.
package redact

import (
	"errors"
	"net/url"
	"strings"
)

// URLQuery removes the query and fragment from the URL of the *url.Error in
// err's chain and returns err. Call it on an HTTP client error before the
// error is wrapped or logged, because wrapping copies the message.
func URLQuery(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if i := strings.IndexAny(urlErr.URL, "?#"); i >= 0 {
			urlErr.URL = urlErr.URL[:i]
		}
	}
	return err
}
