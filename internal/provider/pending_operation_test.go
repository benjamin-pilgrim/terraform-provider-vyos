package provider

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TGNThump/terraform-provider-vyos/internal/vyos"
	"github.com/hashicorp/terraform-plugin-framework/diag"
)

type memoryPrivate map[string][]byte

func (p memoryPrivate) GetKey(_ context.Context, key string) ([]byte, diag.Diagnostics) {
	return p[key], nil
}
func (p memoryPrivate) SetKey(_ context.Context, key string, value []byte) diag.Diagnostics {
	if value == nil {
		delete(p, key)
	} else {
		p[key] = value
	}
	return nil
}

func TestPendingOperationSurvivesEarlyRetry(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	until := now.Add(time.Minute)
	stored := memoryPrivate{}
	err := &vyos.MutationError{Err: errors.New("lost connection"), PendingUntil: until}
	if d := recordPendingOperation(ctx, stored, err); d.HasError() {
		t.Fatal(d)
	}
	// Simulate a fresh provider process reading persisted private bytes.
	next := memoryPrivate{pendingOperationKey: append([]byte(nil), stored[pendingOperationKey]...)}
	if blocked, d := pendingOperation(ctx, next, now); !blocked || !d.HasError() {
		t.Fatal("early refresh accepted temporary state")
	}
	if len(next) == 0 {
		t.Fatal("early retry lost pending metadata")
	}
	if blocked, d := pendingOperation(ctx, next, until.Add(time.Second)); blocked || d.HasError() || len(next) != 0 {
		t.Fatal("expired recovery marker did not clear")
	}
}

func TestUntimedAndSaveFailuresDoNotMarkRecovery(t *testing.T) {
	for _, err := range []error{&vyos.MutationError{Err: errors.New("untimed")}, &vyos.AppliedError{}} {
		stored := memoryPrivate{}
		if d := recordPendingOperation(context.Background(), stored, err); d.HasError() || len(stored) != 0 {
			t.Fatal("nonpending failure blocked refresh")
		}
	}
}
