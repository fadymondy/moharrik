package mcp

/*
Client ID Metadata Documents (MCP authorization, client-registration section).

A client may use an https URL as its client_id; the document at that URL names
the client and lists its redirect URIs. claude.ai offers this as its
recommended option ("Use Claude's published identity").

Fetching a URL a stranger chose is a server-side request forgery risk, so the
fetch is narrow: https only, no redirects, a short timeout, a small body, and a
dialer that refuses loopback, private, link-local and other non-public
addresses AFTER resolution — checking the hostname alone is defeated by DNS
that answers with 127.0.0.1.

It only ever runs on the consent step, which needs a signed-in member, so an
anonymous caller cannot make this server fetch anything.
*/

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

func isCIMDClientID(id string) bool {
	u, err := url.Parse(id)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.Path != "" && u.Path != "/"
}

func (s *oauthServer) resolveCIMD(ctx context.Context, clientID string) (*oauthClient, error) {
	u, err := url.Parse(clientID)
	if err != nil || u.User != nil || u.Fragment != "" || strings.Contains(clientID, "#") {
		return nil, errors.New("client_id is not a valid metadata document URL")
	}
	body, err := s.fetchCIMD(ctx, clientID)
	if err != nil {
		return nil, err
	}
	var doc struct {
		ClientID                string          `json:"client_id"`
		ClientName              string          `json:"client_name"`
		ClientURI               string          `json:"client_uri"`
		RedirectURIs            []string        `json:"redirect_uris"`
		TokenEndpointAuthMethod string          `json:"token_endpoint_auth_method"`
		SigningAlg              string          `json:"token_endpoint_auth_signing_alg"`
		JWKSURI                 string          `json:"jwks_uri"`
		JWKS                    json.RawMessage `json:"jwks"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, errors.New("metadata document is not valid JSON")
	}
	if doc.ClientID != clientID {
		return nil, errors.New("metadata document client_id does not match its URL")
	}
	if strings.TrimSpace(doc.ClientName) == "" || len(doc.RedirectURIs) == 0 || len(doc.RedirectURIs) > 20 {
		return nil, errors.New("metadata document must name the client and list its redirect_uris")
	}
	for _, r := range doc.RedirectURIs {
		if !validRedirectURI(r) {
			return nil, errors.New("metadata document lists a redirect URI that is not https or localhost")
		}
	}
	// Public clients, or private_key_jwt with published keys (ChatGPT). A
	// document cannot carry a shared secret, so the secret methods are out.
	method := doc.TokenEndpointAuthMethod
	if method == "" {
		method = "none"
	}
	jwksURI, jwks := "", ""
	switch method {
	case "none":
	case "private_key_jwt":
		if doc.SigningAlg != "" && !allowedAlg(doc.SigningAlg) {
			return nil, fmt.Errorf("token_endpoint_auth_signing_alg %q is not supported", doc.SigningAlg)
		}
		switch {
		case len(doc.JWKS) > 0 && string(doc.JWKS) != "null":
			if _, err := parseJWKS(doc.JWKS); err != nil {
				return nil, errors.New("metadata document jwks is not a key set")
			}
			jwks = string(doc.JWKS)
		case doc.JWKSURI != "":
			if !validJWKSURI(doc.JWKSURI) {
				return nil, errors.New("metadata document jwks_uri must be an https URL")
			}
			jwksURI = doc.JWKSURI
		default:
			return nil, errors.New("private_key_jwt needs jwks or jwks_uri in the metadata document")
		}
	default:
		return nil, fmt.Errorf("token_endpoint_auth_method %q is not supported for metadata document clients", method)
	}
	clientURI := ""
	if cu, err := url.Parse(doc.ClientURI); err == nil && cu.Scheme == "https" {
		clientURI = doc.ClientURI
	}
	c := &oauthClient{
		ID:           clientID,
		Name:         oauthClip(strings.TrimSpace(doc.ClientName), 120),
		URI:          clientURI,
		RedirectURIs: doc.RedirectURIs,
		AuthMethod:   method,
		CIMD:         true,
		JWKSURI:      jwksURI,
		JWKS:         jwks,
	}
	if method == "private_key_jwt" {
		// Fetch the keys now: a jwks_uri that cannot be reached (or points at a
		// private address) fails the consent, not the token exchange later.
		if _, err := s.keysFor(ctx, c, true); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// assertionSubject reads the (unverified) sub of a client assertion, only to
// know which client's keys to verify it with.
func assertionSubject(assertion string) string {
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var c struct {
		Sub string `json:"sub"`
	}
	_ = json.Unmarshal(raw, &c)
	return c.Sub
}

func fetchClientMetadata(ctx context.Context, clientID string) ([]byte, error) {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || !isPublicIP(ip) {
				return fmt.Errorf("refusing to fetch client metadata from a non-public address")
			}
			return nil
		},
	}
	client := &http.Client{
		Timeout: 8 * time.Second,
		Transport: &http.Transport{
			DialContext:         dialer.DialContext,
			TLSHandshakeTimeout: 5 * time.Second,
			Proxy:               nil,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("client metadata must not redirect")
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, clientID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not fetch client metadata: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("client metadata answered %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 64<<10+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 64<<10 {
		return nil, errors.New("client metadata document is too large")
	}
	return body, nil
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func isPublicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || cgnat.Contains(ip))
}
