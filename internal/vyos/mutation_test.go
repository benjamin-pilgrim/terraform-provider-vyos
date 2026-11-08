package vyos

import (
	"context"
	"fmt"
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
