package httputil

import (
	"log/slog"
	"net/http"
)

// ClientLogOpts are options for logging requests.
type ClientLogOpts struct {
	LogRequest   bool
	RequestLevel slog.Level

	LogResponse   bool
	ResponseLevel slog.Level

	LogResponseError   bool
	ResponseErrorLevel slog.Level
}

// DefaultClientLogOpts are the default ClientLogOpts.
var DefaultClientLogOpts = ClientLogOpts{
	LogResponse:        true,
	ResponseLevel:      slog.LevelInfo,
	LogResponseError:   true,
	ResponseErrorLevel: slog.LevelError,
}

// WithClientLogger returns a ClientMiddleware that logs requests.
func WithClientLogger(logger *slog.Logger, opts ClientLogOpts) ClientMiddleware {
	return func(next http.RoundTripper) http.RoundTripper {
		return RoundTripFunc(func(req *http.Request) (*http.Response, error) {
			requestAttrs := slog.Group(
				"request",
				"method", req.Method,
				"url", req.URL.String(),
				"headers", req.Header)

			if opts.LogRequest {
				logger.Log(req.Context(), opts.RequestLevel,
					"outgoing request",
					requestAttrs)
			}

			resp, err := next.RoundTrip(req)
			if err != nil {
				if opts.LogResponseError {
					logger.Log(req.Context(), opts.ResponseErrorLevel,
						"outgoing response error",
						requestAttrs,
						"error", err)
				}
				return nil, err
			}

			responseAttrs := slog.Group(
				"response",
				"status", resp.Status,
				"headers", resp.Header)

			if resp.StatusCode >= 400 {
				if opts.LogResponseError {
					logger.Log(req.Context(), opts.ResponseErrorLevel,
						"outgoing response error",
						requestAttrs,
						responseAttrs)
				}
			} else if opts.LogResponse {
				logger.Log(req.Context(), opts.ResponseLevel,
					"outgoing response",
					requestAttrs,
					responseAttrs)
			}

			return resp, nil
		})
	}
}
