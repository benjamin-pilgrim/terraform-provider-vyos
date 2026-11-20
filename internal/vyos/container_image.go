package vyos

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

var containerImageReference = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?::[0-9]+)?/[a-z0-9]+(?:[._/-]+[a-z0-9]+)*@sha256:[a-f0-9]{64}$`)

// ValidateContainerImage limits references to immutable, fully qualified images.
// VyOS passes the reference to its operational command, so shell metacharacters
// and command options must never be accepted here.
func ValidateContainerImage(image string) error {
	registry, _, _ := strings.Cut(image, "/")
	if !containerImageReference.MatchString(image) || (!strings.ContainsAny(registry, ".:") && registry != "localhost") {
		return errors.New("use a fully qualified repository@sha256:<64 lowercase hexadecimal digits> reference without a tag")
	}
	return nil
}

func (vc *VyosConfig) containerImageExists(ctx context.Context, image string) (bool, error) {
	data, err := vc.apiClient.request(ctx, "show", map[string]any{"op": "show", "path": []string{"container", "image", "json"}})
	if err != nil {
		return false, err
	}
	if text, ok := data.(string); ok {
		if err := json.Unmarshal([]byte(text), &data); err != nil {
			return false, errors.New("invalid container image JSON listing")
		}
	}
	rows, ok := data.([]any)
	if !ok {
		return false, errors.New("unexpected container image listing (expected JSON array)")
	}
	for _, row := range rows {
		fields, ok := row.(map[string]any)
		if !ok {
			return false, errors.New("unexpected container image listing entry")
		}
		var digests any
		found := false
		for key, value := range fields {
			if strings.EqualFold(strings.ReplaceAll(key, "_", ""), "repodigests") {
				digests, found = value, true
				break
			}
		}
		if !found {
			return false, errors.New("container image listing does not include repository digests")
		}
		if digests == nil {
			continue
		}
		refs, ok := digests.([]any)
		if !ok {
			return false, errors.New("invalid repository digest list")
		}
		for _, ref := range refs {
			name, ok := ref.(string)
			if !ok {
				return false, errors.New("invalid repository digest reference")
			}
			if name == image {
				return true, nil
			}
		}
	}
	return false, nil
}

func (vc *VyosConfig) ContainerImageExists(ctx context.Context, image string) (bool, error) {
	if err := ValidateContainerImage(image); err != nil {
		return false, err
	}
	vc.mutex.Lock()
	defer vc.mutex.Unlock()
	return vc.containerImageExists(ctx, image)
}

// EnsureContainerImage adopts an already cached digest without pulling again.
// Pulls do not change router configuration or use commit-confirm/save operations.
func (vc *VyosConfig) EnsureContainerImage(ctx context.Context, image string) error {
	if err := ValidateContainerImage(image); err != nil {
		return err
	}
	vc.mutex.Lock()
	defer vc.mutex.Unlock()
	exists, err := vc.containerImageExists(ctx, image)
	if err != nil || exists {
		return err
	}
	if _, err := vc.apiClient.request(ctx, "container-image", map[string]any{"op": "add", "name": image}); err != nil {
		return err
	}
	exists, err = vc.containerImageExists(ctx, image)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("container image pull completed without the requested repository digest; inspect router logs")
	}
	return nil
}
