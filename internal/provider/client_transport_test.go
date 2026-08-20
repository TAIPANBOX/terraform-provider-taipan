package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The failures a live httptest server cannot produce, and therefore the ones no
// test in this repository had ever reached: the backend being unreachable, the
// connection dropping mid-response, and a 200 carrying something that is not
// the JSON the client expects.
//
// These are not exotic. They are what an operator meets when a VPN drops, when
// a backend restarts under `terraform apply`, or when a proxy answers 200 with
// an HTML error page. The diagnostic they produce is the whole of what that
// operator sees, so what it says matters as much as that it fails.
//
// The same limit applies here as everywhere else in this package: these are
// unit tests over the client. They say nothing about how a resource behaves
// when the client returns one of these.

// A base URL nothing is listening on. Port 1 is reserved and unroutable in
// practice, which fails fast rather than waiting out a DNS or connect timeout.
const unreachableBase = "http://127.0.0.1:1"

func unreachableCloud() *CloudClient {
	return &CloudClient{
		BaseURL:    unreachableBase,
		APIKey:     "k:org:admin",
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
	}
}

func unreachableWardryx() *WardryxClient {
	return &WardryxClient{
		BaseURL:    unreachableBase,
		APIKey:     "k",
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
	}
}

func TestAnUnreachableBackendSaysSoAndNamesNoCredential(t *testing.T) {
	t.Parallel()

	// Invariant 3: no credential reaches state, a log line, or an error
	// message. This is the error most likely to be pasted into a chat or an
	// issue, because it is the one that fires when nothing is wrong with the
	// configuration, so it is the one worth checking for a leak.
	const secret = "k:org:admin"

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"SetBudget", func() error {
			_, err := unreachableCloud().SetBudget(context.Background(), "run-1", 1)
			return err
		}},
		{"ListBudgets", func() error {
			_, err := unreachableCloud().ListBudgets(context.Background())
			return err
		}},
		{"SetUnitBudget", func() error {
			_, err := unreachableCloud().SetUnitBudget(context.Background(), "unit-1", 1)
			return err
		}},
		{"ListUnitBudgets", func() error {
			_, err := unreachableCloud().ListUnitBudgets(context.Background())
			return err
		}},
		{"PutPolicy", func() error {
			_, err := unreachableWardryx().PutPolicy(context.Background(), "p", WardryxPolicyDocument{})
			return err
		}},
		{"GetPolicy", func() error {
			_, err := unreachableWardryx().GetPolicy(context.Background(), "p")
			return err
		}},
		{"DeletePolicy", func() error {
			return unreachableWardryx().DeletePolicy(context.Background(), "p")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.call()
			if err == nil {
				t.Fatal("an unreachable backend must be an error, not a silent success")
			}
			msg := err.Error()
			if !strings.Contains(msg, "127.0.0.1:1") && !strings.Contains(msg, "connect") &&
				!strings.Contains(msg, "refused") && !strings.Contains(msg, "dial") {
				t.Errorf("the error should say the call could not be made, got: %s", msg)
			}
			if strings.Contains(msg, secret) {
				t.Errorf("the bearer key reached an error message: %s", msg)
			}
		})
	}
}

func TestAConnectionDroppedMidResponseIsAnErrorNotAnEmptyResult(t *testing.T) {
	t.Parallel()

	// A proxy or a backend restarting can promise a body and then close. The
	// dangerous outcome is not the error, it is reading a short body as a
	// complete one: an empty budget list is indistinguishable from "this run
	// has no budget", and Terraform would plan on it.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"budgets":[`))
		// Hijack and close without writing the promised bytes, which is what
		// io.ReadAll sees as an unexpected EOF.
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, _, err := hj.Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	defer server.Close()

	c := &CloudClient{
		BaseURL:    server.URL,
		APIKey:     "k:org:admin",
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	}

	_, err := c.ListBudgets(context.Background())
	if err == nil {
		t.Fatal("a truncated response must be an error, never an empty list")
	}

	// Naming the layer, not just asserting that something failed. This
	// behaviour is defended twice: `do` refuses a body it could not finish
	// reading, and the JSON decode below it refuses what a truncated body
	// leaves. A test happy with either cannot tell which one is still working,
	// and a mutation pass on 2026-08-20 showed exactly that: removing `do`'s
	// check left the test green because the decode caught it instead.
	if !strings.Contains(err.Error(), "read taipan cloud API response") {
		t.Errorf(
			"the read must fail where the body is read, not further down where a "+
				"truncated body merely fails to parse; got: %v", err,
		)
	}
}

func TestA200CarryingSomethingOtherThanJSONIsAnError(t *testing.T) {
	t.Parallel()

	// A captive portal, a misconfigured ingress or an auth proxy answers 200
	// with HTML. The status code says success and the body is not what the
	// client asked for, which is the one combination a status check alone
	// cannot catch.
	for _, tc := range []struct {
		name string
		call func(base string) error
	}{
		{"ListBudgets", func(base string) error {
			c := &CloudClient{BaseURL: base, APIKey: "k", HTTPClient: server5s()}
			_, err := c.ListBudgets(context.Background())
			return err
		}},
		{"SetBudget", func(base string) error {
			c := &CloudClient{BaseURL: base, APIKey: "k", HTTPClient: server5s()}
			_, err := c.SetBudget(context.Background(), "run-1", 1)
			return err
		}},
		{"GetPolicy", func(base string) error {
			c := &WardryxClient{BaseURL: base, APIKey: "k", HTTPClient: server5s()}
			_, err := c.GetPolicy(context.Background(), "p")
			return err
		}},
		{"PutPolicy", func(base string) error {
			c := &WardryxClient{BaseURL: base, APIKey: "k", HTTPClient: server5s()}
			_, err := c.PutPolicy(context.Background(), "p", WardryxPolicyDocument{})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("<html><body>Sign in to continue</body></html>"))
			}))
			defer server.Close()

			if err := tc.call(server.URL); err == nil {
				t.Fatal("a 200 that is not the expected JSON must be an error, not a zero value")
			}
		})
	}
}

func server5s() *http.Client {
	return &http.Client{Timeout: 5 * time.Second}
}
