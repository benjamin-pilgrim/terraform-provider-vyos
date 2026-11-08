package vyos

import "errors"

// MutationError means a configure request was attempted. VyOS can partially
// apply it even when the response fails; retain identity for cleanup.
type MutationError struct{ Err error }

func (e *MutationError) Error() string { return e.Err.Error() }
func (e *MutationError) Unwrap() error { return e.Err }

// VyOS commits HTTPS self-configuration after sending its response and exposes
// no portable completion/timer proof. Reject before sending any operation in
// the batch; an accepted background change cannot safely be canceled by us.
func validateMutation(ops []map[string]any) error {
	for _, op := range ops {
		path, _ := op["path"].([]string)
		if len(path) > 0 && path[0] == "service" && (len(path) == 1 || path[1] == "https") {
			return errors.New("HTTPS API self-configuration is unsupported: VyOS commits service https asynchronously without reliable completion or rollback-timer verification; use another management channel and select paths outside service https")
		}
	}
	return nil
}

const asyncCommitMessage = "Requested HTTP API server configuration change; commit will be called in the background"
