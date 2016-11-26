// Copyright 2014 The oauth2 Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ooauth2_test

import (
	"fmt"
	"log"
	"net/http"
	"testing"

	"github.com/theatrus/ooauth2"
)

// TODO(jbd): Remove after Go 1.4.
// Related to https://codereview.appspot.com/107320046
func TestA(t *testing.T) {}

func Example_regular() {
	opts, err := ooauth2.New(
		ooauth2.Client("YOUR_CLIENT_ID", "YOUR_CLIENT_SECRET"),
		ooauth2.RedirectURL("YOUR_REDIRECT_URL"),
		ooauth2.Scope("SCOPE1", "SCOPE2"),
		ooauth2.Endpoint(
			"https://provider.com/o/ooauth2/auth",
			"https://provider.com/o/ooauth2/token",
		),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Redirect user to consent page to ask for permission
	// for the scopes specified above.
	url := opts.AuthCodeURL("state", "online", "auto")
	fmt.Printf("Visit the URL for the auth dialog: %v", url)

	// Use the authorization code that is pushed to the redirect URL.
	// NewTransportWithCode will do the handshake to retrieve
	// an access token and initiate a Transport that is
	// authorized and authenticated by the retrieved token.
	var code string
	if _, err = fmt.Scan(&code); err != nil {
		log.Fatal(err)
	}
	t, err := opts.NewTransportFromCode(code)
	if err != nil {
		log.Fatal(err)
	}

	// You can use t to initiate a new http.Client and
	// start making authenticated requests.
	client := http.Client{Transport: t}
	client.Get("...")
}
