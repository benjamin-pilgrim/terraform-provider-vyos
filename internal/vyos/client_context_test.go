package vyos

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRequestRetainsSafeContextFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	client := &Client{HTTP: server.Client(), Endpoint: server.URL, Key: "SECRET"}
	_, err := client.request(ctx, "retrieve", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline cause lost: %v", err)
	}
	if strings.Contains(err.Error(), "SECRET") {
		t.Fatal("credential leaked")
	}
}
