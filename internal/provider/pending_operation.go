package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/TGNThump/terraform-provider-vyos/internal/vyos"
	"github.com/hashicorp/terraform-plugin-framework/diag"
)

// Framework private data survives an errored operation without adding user-facing
// schema fields. Preserve state while temporary active changes can still roll back.
type privateData interface {
	GetKey(context.Context, string) ([]byte, diag.Diagnostics)
	SetKey(context.Context, string, []byte) diag.Diagnostics
}

const pendingOperationKey = "commit_confirm_pending_until"

func recordPendingOperation(ctx context.Context, p privateData, err error) diag.Diagnostics {
	var uncertain *vyos.MutationError
	if !errors.As(err, &uncertain) || uncertain.PendingUntil.IsZero() {
		return nil
	}
	raw, _ := json.Marshal(uncertain.PendingUntil)
	return p.SetKey(ctx, pendingOperationKey, raw)
}
func pendingOperation(ctx context.Context, p privateData, now time.Time) (bool, diag.Diagnostics) {
	raw, diagnostics := p.GetKey(ctx, pendingOperationKey)
	if diagnostics.HasError() || len(raw) == 0 {
		return diagnostics.HasError(), diagnostics
	}
	var until time.Time
	if json.Unmarshal(raw, &until) != nil {
		diagnostics.AddError("Invalid pending-operation state", "Cannot read the saved recovery deadline; inspect the router before repairing resource state.")
		return true, diagnostics
	}
	if now.Before(until) {
		diagnostics.AddError("VyOS recovery pending", fmt.Sprintf("A previous timed change could not be confirmed. Its active configuration is temporary. Wait until %s and inspect the router before retrying; resource state has been retained.", until.UTC().Format(time.RFC3339)))
		return true, diagnostics
	}
	diagnostics.Append(p.SetKey(ctx, pendingOperationKey, nil)...)
	return diagnostics.HasError(), diagnostics
}
