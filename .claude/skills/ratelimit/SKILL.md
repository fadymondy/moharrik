---
name: ratelimit
description: Add request rate limiting / throttling to a togo app with the ratelimit plugin — define token-bucket policies, apply middleware keyed by IP/user/route, and emit 429 + standard rate-limit headers.
---

# togo ratelimit

Use this skill to rate-limit endpoints in a togo app with `togo-framework/ratelimit`.

## Define a policy
`ratelimit.Rate("<name>", <limit>, <window>)` — e.g. `Rate("api", 60, time.Minute)`.

## Apply as middleware
```go
s, _ := ratelimit.FromKernel(k)
r.With(s.Middleware(ratelimit.Rate("api", 60, time.Minute), nil)).Get("/things", handler)
```
- Default key is the client IP (`ratelimit.ClientIP`, honors `X-Forwarded-For`).
- Custom key: pass a `func(*http.Request) string` (user id, API key, email, route, tenant).
- Sets `X-RateLimit-Limit/Remaining/Reset`; denies with `429` + `Retry-After`.

## Direct check
```go
allowed, retryAfter := s.Allow(ctx, key, ratelimit.Rate("export", 10, time.Hour))
```

## Tips
- Pick a sensible burst (`Limit`) — it equals the bucket size.
- Key auth/login/OTP endpoints by identity, not just IP, to stop credential stuffing.
- For multiple app instances, back the limiter with Redis via the `Store` interface + `s.WithStore`.
