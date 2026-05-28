package ratelimiter2

type RateLimit interface {
	Allow(key string) bool
}
