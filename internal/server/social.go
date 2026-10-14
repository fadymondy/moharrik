package server

import (
	"context"
	"sync"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/moharrik/internal/account"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/social"
	"github.com/fadymondy/moharrik/internal/workspaces"
)

// newSocial builds Sign in with Google/Apple for a booted app (nil during the OpenAPI export).
func newSocial(a *app.App) *social.Service {
	if a == nil {
		return nil
	}
	svc, ok := auth.FromKernel(a.Kernel)
	if !ok {
		panic("social: auth service not registered")
	}
	return &social.Service{
		DB:   a.SysDB,
		Auth: svc,
		Log:  a.Log,
		Env:  social.FromEnv,
		DeleteWorkspace: func(ctx context.Context, id string) error {
			return workspaces.DeleteAsOwner(ctx, a, id, social.ErrWorkspaceRelease)
		},
	}
}

// registerSocial mounts the native sign-in and account routes (huma) and, on a live app, the
// browser routes, and advertises the configured providers on GET /api/auth/methods.
func registerSocial(api huma.API, a *app.App) {
	s := newSocial(a)
	get := func() *social.Service { return s }
	social.RegisterNative(api, get)
	social.RegisterAccount(api, get)
	if s == nil {
		return
	}
	s.Mount(a.Kernel.Router)
	c := s.Env()
	if err := c.AppleKeyErr; err != nil {
		a.Log.Error("social: Sign in with Apple is off — APPLE_PRIVATE_KEY cannot be read", "err", err)
	}
	advertise(c)
}

var advertised sync.Map

// advertise lists the providers on the auth plugin's GET /api/auth/methods, once per process
// (the plugin's list only grows). The web reads type "oauth" (a browser redirect); the apps also
// read type "native" (Apple on iOS, a Google id_token).
func advertise(c social.Config) {
	add := func(name, label, typ, url string) {
		if _, dup := advertised.LoadOrStore(name+"/"+typ, true); !dup {
			auth.RegisterLoginMethod(auth.LoginMethod{Name: name, Label: label, Type: typ, URL: url})
		}
	}
	if c.AppleWeb() {
		add("apple", "Sign in with Apple", "oauth", "/api/auth/oauth/apple")
	}
	if c.AppleNative() {
		add("apple", "Sign in with Apple", "native", "/api/auth/native/apple")
	}
	if c.GoogleWeb() {
		add("google", "Continue with Google", "oauth", "/api/auth/oauth/google")
	}
	if c.GoogleNative() {
		add("google", "Continue with Google", "native", "/api/auth/native/google")
	}
}

// registerAccount mounts email sign-in for the apps and the mailed-link endpoints (forgot / reset
// password, confirm an address).
func registerAccount(api huma.API, a *app.App) {
	if a != nil {
		svc, ok := auth.FromKernel(a.Kernel)
		if !ok {
			panic("account: auth service not registered")
		}
		account.Configure(a, svc)
	}
	account.Register(api, a)
}
