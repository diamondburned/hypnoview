package httputil

import "net/http"

// RoundTripFunc is a function that implements http.RoundTripper.
type RoundTripFunc func(*http.Request) (*http.Response, error)

var _ http.RoundTripper = RoundTripFunc(nil)

// RoundTrip implements http.RoundTripper.
func (f RoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// ClientMiddleware is a middleware for http.RoundTripper.
type ClientMiddleware func(http.RoundTripper) http.RoundTripper

// UseClientMiddlewares applies the given middlewares to a new http.Client.
// If client is nil, then the default http.Client is copied and used.
func UseClientMiddlewares(client *http.Client, middlewares ...ClientMiddleware) *http.Client {
	if client == nil {
		c2 := *http.DefaultClient
		client = &c2
	}
	if client.Transport == nil {
		client.Transport = http.DefaultTransport
	}
	for _, middleware := range middlewares {
		client.Transport = middleware(client.Transport)
	}
	return client
}
