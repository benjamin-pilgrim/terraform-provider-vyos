package vyos

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPSMutationsRejectedBeforeConfigure(t *testing.T) {
	for _, path := range []string{"service", "service https", "service https api keys id TF-SECOND key"} {
		t.Run(path, func(t *testing.T) {
			writes := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/retrieve" {
					writes++
				}
				fmt.Fprint(w, `{"success":true,"data":{}}`)
			}))
			defer s.Close()
			vc := New(&Client{HTTP: s.Client(), Endpoint: s.URL, Key: "test"}, false, "")
			if err := vc.Set(context.Background(), path, "second-key"); err == nil || !strings.Contains(err.Error(), "self-configuration") {
				t.Fatalf("unsupported operation: %v", err)
			}
			if writes != 0 {
				t.Fatal("unsafe self-change or save sent")
			}
		})
	}
}

func TestAsyncAcknowledgmentNeverSaves(t *testing.T) {
	for _, message := range []string{asyncCommitMessage, "prefix: " + asyncCommitMessage} {
		t.Run(message, func(t *testing.T) {
			saves := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/retrieve":
					fmt.Fprint(w, `{"success":true,"data":{}}`)
				case "/configure":
					fmt.Fprintf(w, `{"success":true,"data":%q}`, message)
				case "/config-file":
					saves++
					fmt.Fprint(w, `{"success":true,"data":null}`)
				}
			}))
			defer s.Close()
			vc := New(&Client{HTTP: s.Client(), Endpoint: s.URL, Key: "test"}, false, "")
			if err := vc.Set(context.Background(), "node x", "new"); err == nil {
				t.Fatal("async change reported successful")
			}
			if saves != 0 {
				t.Fatal("saved before asynchronous commit completion")
			}
		})
	}
}

func TestTimedHTTPSRejectionLeavesNoPendingCommit(t *testing.T) {
	vc, fixture := newBatchFixture(t, false, "")
	vc.SetCommitConfirmMinutes(1)
	attempted, err := vc.MutateBatch(context.Background(), Batch{"https": {Path: []string{"service", "https", "api", "keys", "id", "SECOND", "key"}, Value: `"fixture"`}}, "create")
	if err == nil || attempted || vc.pendingConfirm {
		t.Fatalf("unsafe preflight result: attempted=%v pending=%v err=%v", attempted, vc.pendingConfirm, err)
	}
	for _, call := range fixture.calls {
		if call != "/retrieve" {
			t.Fatal("write sent for rejected HTTPS change")
		}
	}
	vc.SetCommitConfirmMinutes(0) // The basic fixture implements untimed configure bodies.
	if _, err := vc.MutateBatch(context.Background(), sampleBatch(), "create"); err != nil {
		t.Fatalf("rejection poisoned later safe operation: %v", err)
	}
}

func TestTimedAsyncAcknowledgmentNeverConfirmsOrSaves(t *testing.T) {
	vc, fixture := newBatchFixture(t, false, "")
	vc.SetCommitConfirmMinutes(1)
	base := vc.apiClient.HTTP.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	vc.apiClient.HTTP.Transport = mutationTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/configure" {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"success":true,"data":"` + asyncCommitMessage + `"}`)), Request: req}, nil
		}
		return base.RoundTrip(req)
	})
	if _, err := vc.MutateBatch(context.Background(), sampleBatch(), "create"); err == nil {
		t.Fatal("async commit reported successful")
	}
	for _, call := range fixture.calls {
		if call == "/config-file" {
			t.Fatal("confirmed or saved asynchronous commit")
		}
	}
}

type mutationTransport func(*http.Request) (*http.Response, error)

func (f mutationTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
