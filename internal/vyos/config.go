package vyos

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// VyosConfig serializes reads and complete configure/save sequences for one
// provider instance. Reads always fetch active config, including external drift.
// Other provider instances and external writers are not coordinated.
type VyosConfig struct {
	apiClient      *Client
	skipSaving     bool
	pendingConfirm bool
	confirmMinutes int64
	saveFile       string
	mutex          sync.Mutex
	managedPaths   map[string]pathClaim
}

func New(c *Client, skipSaving bool, saveFile string) *VyosConfig {
	return &VyosConfig{apiClient: c, skipSaving: skipSaving, saveFile: saveFile}
}

func ParsePath(path string) ([]string, error) {
	if path == "" || strings.Join(strings.Fields(path), " ") != path {
		return nil, errors.New("path must contain nonempty components separated by single spaces")
	}
	for _, r := range path {
		if unicode.IsControl(r) {
			return nil, errors.New("path contains control characters")
		}
	}
	return strings.Split(path, " "), nil
}

func (vc *VyosConfig) snapshot(ctx context.Context) (map[string]any, error) {
	resp, err := vc.apiClient.request(ctx, "retrieve", map[string]any{"op": "showConfig", "path": []string{}})
	if err != nil {
		return nil, err
	}
	tree, ok := resp.(map[string]any)
	if !ok {
		return nil, errors.New("unexpected configuration response (expected object)")
	}
	return tree, nil
}

func (vc *VyosConfig) show(ctx context.Context, path []string) (any, error) {
	tree, err := vc.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return configNode(tree, path)
}

func configNode(tree map[string]any, path []string) (any, error) {
	var node any = tree
	for _, component := range path {
		m, ok := node.(map[string]any)
		if !ok {
			return nil, errors.New("configuration path traverses a scalar or list")
		}
		node, ok = m[component]
		if !ok {
			return nil, nil
		}
		if node == nil {
			return nil, errors.New("unexpected null configuration node")
		}
	}
	return node, nil
}

func (vc *VyosConfig) claim(path string) error {
	components, err := ParsePath(path)
	if err != nil {
		return err
	}
	return vc.claimPaths([][]string{components}, "")
}

func (vc *VyosConfig) Show(ctx context.Context, path string) (any, error) {
	components, err := ParsePath(path)
	if err != nil {
		return nil, err
	}
	vc.mutex.Lock()
	defer vc.mutex.Unlock()
	if err := vc.claim(path); err != nil {
		return nil, err
	}
	value, err := vc.show(ctx, components)
	if err == nil && value == nil {
		vc.release(components)
	}
	return value, err
}

// AppliedError means the active commit succeeded but persistence failed or could
// not be confirmed. Callers
// must retain the resource identity so a failed create can be cleaned up.
type AppliedError struct{}

func (*AppliedError) Error() string {
	return "configuration committed, but saving failed or could not be confirmed. Inspect active and saved configuration, then restore saving and save explicitly on the router; a later Terraform apply may have no changes"
}

func (vc *VyosConfig) save(ctx context.Context) error {
	if vc.skipSaving {
		return nil
	}
	payload := map[string]any{"op": "save"}
	if vc.saveFile != "" {
		payload["file"] = vc.saveFile
	}
	if _, err := vc.apiClient.request(ctx, "config-file", payload); err != nil {
		return &AppliedError{}
	}
	return nil
}

// Operations uses path arrays, preserving tag names as individual components.
// null object members are omitted; {} represents a flag. Values must be strings
// (or nonempty lists of strings), matching the values returned by VyOS.
func Operations(path []string, value any) ([]map[string]any, error) {
	var ops []map[string]any
	var walk func([]string, any) error
	walk = func(p []string, v any) error {
		switch x := v.(type) {
		case map[string]any:
			if len(x) == 0 {
				ops = append(ops, map[string]any{"op": "set", "path": p})
				return nil
			}
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if k == "" || strings.IndexFunc(k, unicode.IsControl) >= 0 {
					return errors.New("invalid configuration object key")
				}
				if x[k] == nil {
					continue
				}
				child := append(append([]string{}, p...), k)
				if err := walk(child, x[k]); err != nil {
					return err
				}
			}
		case string:
			ops = append(ops, map[string]any{"op": "set", "path": p, "value": x})
		case []any:
			if len(x) == 0 {
				return errors.New("empty lists cannot be configured; omit the member instead")
			}
			for _, v := range x {
				s, ok := v.(string)
				if !ok {
					return errors.New("list members must be strings")
				}
				ops = append(ops, map[string]any{"op": "set", "path": p, "value": s})
			}
		default:
			return errors.New("configuration values must be objects, strings or nonempty lists of strings; null is only allowed as an omitted object member")
		}
		return nil
	}
	if err := walk(path, value); err != nil {
		return nil, err
	}
	if len(ops) == 0 {
		return nil, errors.New("configuration contains no values or flags")
	}
	return ops, nil
}

func DecodeValue(raw string) (any, error) {
	var v any
	if json.Unmarshal([]byte(raw), &v) != nil {
		return nil, errors.New("value must be valid JSON")
	}
	if _, err := Operations([]string{"validation"}, v); err != nil {
		return nil, err
	}
	return v, nil
}

func (vc *VyosConfig) mutate(ctx context.Context, path string, value any, mode string) error {
	components, err := ParsePath(path)
	if err != nil {
		return err
	}
	var ops []map[string]any
	if mode != "delete" {
		ops, err = Operations(components, value)
		if err != nil {
			return err
		}
	}
	vc.mutex.Lock()
	defer vc.mutex.Unlock()
	if err := vc.claim(path); err != nil {
		return err
	}
	// Existence check and mutation share the lock, preventing competing creates
	// from adopting/overwriting each other's configuration.
	current, err := vc.show(ctx, components)
	if err != nil {
		return err
	}
	if mode == "create" && current != nil {
		return errors.New("configuration path already exists; import it or select a narrower unowned path")
	}
	if mode == "delete" && current == nil {
		vc.release(components)
		return nil
	}
	if mode != "create" && current != nil {
		ops = append([]map[string]any{{"op": "delete", "path": components}}, ops...)
	}
	raw := ""
	if mode != "delete" {
		encoded, _ := json.Marshal(value)
		raw = string(encoded)
	}
	if _, err := vc.configure(ctx, ops, Batch{"resource": {Path: components, Value: raw}}); err != nil {
		return err
	}
	if mode == "delete" {
		vc.release(components)
	}
	return nil
}

func (vc *VyosConfig) Set(ctx context.Context, path string, value any) error {
	return vc.mutate(ctx, path, value, "create")
}

func (vc *VyosConfig) Update(ctx context.Context, path string, value any) error {
	return vc.mutate(ctx, path, value, "update")
}

func (vc *VyosConfig) Delete(ctx context.Context, path string) error {
	return vc.mutate(ctx, path, nil, "delete")
}

// Equivalent preserves a user's formatting and omitted null object members on
// refresh without suppressing meaningful drift. Lists remain order sensitive.
func Equivalent(raw string, remote any) bool {
	v, err := DecodeValue(raw)
	if err != nil {
		return false
	}
	var clean func(any) (any, bool)
	clean = func(x any) (any, bool) {
		if list, ok := x.([]any); ok && len(list) == 1 {
			return clean(list[0])
		}
		if m, ok := x.(map[string]any); ok {
			out := map[string]any{}
			for k, v := range m {
				if v != nil {
					if value, keep := clean(v); keep {
						out[k] = value
					}
				}
			}
			return out, len(m) == 0 || len(out) > 0
		}
		return x, true
	}
	desired, _ := clean(v)
	actual, _ := clean(remote)

	a, _ := json.Marshal(desired)
	b, _ := json.Marshal(actual)
	return string(a) == string(b)
}
