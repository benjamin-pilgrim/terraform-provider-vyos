package vyos

import (
	"context"
	"errors"
	"time"
)

// SetCommitConfirmMinutes configures the optional per-operation rollback timer.
// Call before sharing this client with resources.
func (vc *VyosConfig) SetCommitConfirmMinutes(minutes int64) { vc.confirmMinutes = minutes }

// MutationError means a configure request was attempted. The router can have
// partially applied it even if the response was lost; retain identity for cleanup.
type MutationError struct {
	Err          error
	PendingUntil time.Time
}

func (e *MutationError) Error() string { return e.Err.Error() }
func (e *MutationError) Unwrap() error { return e.Err }

// configure holds the caller's provider mutex through commit, verification,
// confirmation and saving. No automatic retries can extend an uncertain commit.
func (vc *VyosConfig) configure(ctx context.Context, ops []map[string]any, desired Batch) (bool, error) {
	if vc.pendingConfirm {
		return false, errors.New("a previous commit-confirm is unresolved; further writes are blocked for this provider instance. Inspect the router and wait for recovery before retrying")
	}
	if len(ops) == 0 {
		return false, nil
	}
	var payload any = ops
	if vc.confirmMinutes > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(vc.confirmMinutes)*time.Minute-10*time.Second)
		defer cancel()
		payload = map[string]any{"commands": ops, "confirm_time": vc.confirmMinutes}
		vc.pendingConfirm = true
	}
	if _, err := vc.apiClient.request(ctx, "configure", payload); err != nil {
		return true, vc.mutationError(err)
	}
	if vc.confirmMinutes > 0 {
		if err := vc.verifyCommit(ctx, desired); err != nil {
			return true, vc.mutationError(errors.New("cannot reach the router or verify the change; configuration was not confirmed or saved. VyOS will recover on timeout if the timer is running. Inspect the router before retrying"))
		}
		if _, err := vc.apiClient.request(ctx, "config-file", map[string]any{"op": "confirm"}); err != nil {
			return true, vc.mutationError(errors.New("confirmation failed or its response was lost; configuration was not saved. Inspect the router before retrying"))
		}
		// Confirmation is idempotent when no timer remains. Recheck so expiry
		// between the first read and confirmation cannot report a false success.
		if err := vc.verifyCommit(ctx, desired); err != nil {
			return true, vc.mutationError(errors.New("the change could not be verified after confirmation, possibly because the timer expired; configuration was not saved. Inspect the router before retrying"))
		}
		vc.pendingConfirm = false
	}
	return true, vc.save(ctx)
}

func (vc *VyosConfig) verifyCommit(ctx context.Context, desired Batch) error {
	// Prove that a new management connection works, rather than reusing an
	// established connection that may bypass newly applied firewall rules.
	vc.apiClient.HTTP.CloseIdleConnections()
	current, err := vc.batchSnapshot(ctx, desired)
	if err != nil {
		return err
	}
	for name, entry := range desired {
		value, present := current[name]
		if entry.Value == "" {
			if present {
				return errors.New("deleted configuration is still present")
			}
		} else if !present || !Equivalent(entry.Value, value) {
			return errors.New("active configuration differs from the desired change")
		}
	}
	return nil
}

func (vc *VyosConfig) mutationError(err error) *MutationError {
	uncertain := &MutationError{Err: err}
	if vc.confirmMinutes > 0 {
		uncertain.PendingUntil = time.Now().Add(time.Duration(vc.confirmMinutes)*time.Minute + 10*time.Second)
	}
	return uncertain
}
