// Copyright 2014 The oauth2 Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ooauth2

import (
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const (
	defaultTokenType            = "Bearer"
	expirationHysteresisSeconds = 60
)

// Token represents the crendentials used to authorize
// the requests to access protected resources on the OAuth 2.0
// provider's backend.
type Token struct {
	// A token that authorizes and authenticates the requests.
	AccessToken string `json:"access_token"`

	// Identifies the type of token returned.
	TokenType string `json:"token_type,omitempty"`

	// A token that may be used to obtain a new access token.
	RefreshToken string `json:"refresh_token,omitempty"`

	// The remaining lifetime of the access token.
	Expiry time.Time `json:"expiry,omitempty"`

	// raw optionally contains extra metadata from the server
	// when updating a token.
	raw interface{}
}

// Extra returns an extra field returned from the server during token retrieval.
// E.g.
//     idToken := token.Extra("id_token")
//
func (t *Token) Extra(key string) string {
	if vals, ok := t.raw.(url.Values); ok {
		return vals.Get(key)
	}
	if raw, ok := t.raw.(map[string]interface{}); ok {
		if val, ok := raw[key].(string); ok {
			return val
		}
	}
	return ""
}

// ExpiringSoon returns true if the token is expired or
// will be expiring soon within a hysteresis, currently 60 seconds
func (t *Token) ExpiringSoon() bool {
	return t.ExpiringWithin(expirationHysteresisSeconds * time.Second)
}

// ExpiringWithin returns true if the token is expired or
// will be expiring soon within the given time frame
func (t *Token) ExpiringWithin(hysteresis time.Duration) bool {
	if t.Expiry.IsZero() {
		return false
	} else {
		futureExpiry := t.Expiry.Add(hysteresis)
		return t.Expired() || futureExpiry.Before(time.Now())
	}
}

// Expired returns true if there is no access token or the
// access token is expired.
func (t *Token) Expired() bool {
	if t.AccessToken == "" {
		return true
	}
	if t.Expiry.IsZero() {
		return false
	}
	return t.Expiry.Before(time.Now())
}

// Transport is an http.RoundTripper that makes OAuth 2.0 HTTP requests.
type Transport struct {
	opts *Options
	base http.RoundTripper

	mu    sync.RWMutex
	token *Token
	modReq map[*http.Request]*http.Request // original -> modified
}

// NewTransport creates a new Transport that uses the provided
// token fetcher as token retrieving strategy. It authenticates
// the requests and delegates origTransport to make the actual requests.
func newTransport(base http.RoundTripper, opts *Options, token *Token) *Transport {
	return &Transport{
		base:  base,
		opts:  opts,
		token: token,
	}
}

// RoundTrip authorizes and authenticates the request with an
// access token. If no token exists or token is expired,
// tries to refresh/fetch a new token.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {

	token, err := t.CheckAndRefreshToken()
	if err != nil {
		return nil, err
	}

	// To set the Authorization header, we must make a copy of the Request
	// so that we don't modify the Request we were given.
	// This is required by the specification of http.RoundTripper.
	req2 := cloneRequest(req)
	typ := token.TokenType
	if typ == "" {
		typ = defaultTokenType
	}
	req2.Header.Set("Authorization", typ+" "+token.AccessToken)
	t.setModReq(req, req2)

	res, err := t.base.RoundTrip(req2)
	if err != nil {
		t.setModReq(req, nil)
		return nil, err
	}
	res.Body = &onEOFReader{
		rc: res.Body,
		fn: func() { t.setModReq(req, nil) },
	}
	return res, nil
}

// Token returns the token that authorizes and
// authenticates the transport.
func (t *Transport) Token() *Token {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.token
}

// CheckAndRefresh check token expiration using hysteresis,
// will fetch a new token if possible using refreshToken,
// and then write it to the TokenStore if defined.
func (t *Transport) CheckAndRefreshToken() (*Token, error) {
	token := t.token

	// Do an initial check to see if the token is expired
	// before acquiring a lock.
	if token == nil || token.ExpiringSoon() {
		// Acquire a lock, and then re-check expiration
		// to not refresh and store the token multiple
		// times
		t.mu.Lock()
		defer t.mu.Unlock()
		// Now locked, refresh the token as the token is often not
		// mutated.
		token = t.token

		if token == nil || token.ExpiringSoon() {
			// Check if the token is refreshable.
			// If token is refreshable, don't return an error,
			// rather refresh.
			if err := t.refreshToken(); err != nil {
				return nil, err
			}
			token = t.token
			// Place the token back into the store under the lock
			if t.opts.TokenStore != nil {
				t.opts.TokenStore.WriteToken(token)
			}
		}
	}
	return token, nil
}

// refreshToken retrieves a new token, if a refreshing/fetching
// method is known and required credentials are presented
// (such as a refresh token).
func (t *Transport) refreshToken() error {
	token, err := t.opts.TokenFetcherFunc(t.token)
	if err != nil {
		return err
	}
	t.token = token
	return nil
}


func (t *Transport) setModReq(orig, mod *http.Request) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.modReq == nil {
		t.modReq = make(map[*http.Request]*http.Request)
	}
	if mod == nil {
		delete(t.modReq, orig)
	} else {
		t.modReq[orig] = mod
	}
}

// cloneRequest returns a clone of the provided *http.Request.
// The clone is a shallow copy of the struct and its Header map.
func cloneRequest(r *http.Request) *http.Request {
	// shallow copy of the struct
	r2 := new(http.Request)
	*r2 = *r
	// deep copy of the Header
	r2.Header = make(http.Header)
	for k, s := range r.Header {
		r2.Header[k] = s
	}
	return r2
}

type onEOFReader struct {
	rc io.ReadCloser
	fn func()
}

func (r *onEOFReader) Read(p []byte) (n int, err error) {
	n, err = r.rc.Read(p)
	if err == io.EOF {
		r.runFunc()
	}
	return
}

func (r *onEOFReader) Close() error {
	err := r.rc.Close()
	r.runFunc()
	return err
}

func (r *onEOFReader) runFunc() {
	if fn := r.fn; fn != nil {
		fn()
		r.fn = nil
	}
}
