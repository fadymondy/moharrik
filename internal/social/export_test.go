package social

// Test hooks for the external test package (social_test), which needs the fake in socialtest.
var (
	VerifyIDToken     = verifyIDToken
	AppleClientSecret = appleClientSecret
	Truthy            = truthy
	PersonFrom        = personFrom
)
