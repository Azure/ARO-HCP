package middleware

// contextKey is an unexported type used to embed content in the request context, so users must acquire the value with our getters.
type contextKey string
