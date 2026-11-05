package provider

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/TGNThump/terraform-provider-vyos/internal/vyos"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var errUnknownValue = errors.New("configuration contains unknown values")

// configurationJSON preserves legacy JSON strings and encodes native HCL values.
// Nested strings are always literal VyOS values, never interpreted as JSON.
func configurationJSON(value types.Dynamic) (string, error) {
	if value.IsNull() {
		return "{}", nil
	}
	if value.IsUnknown() {
		return "", errUnknownValue
	}
	underlying := value.UnderlyingValue()
	if text, ok := underlying.(types.String); ok {
		if text.IsUnknown() {
			return "", errUnknownValue
		}
		if text.IsNull() {
			return "", errors.New("configuration cannot be null")
		}
		raw := text.ValueString()
		if _, err := vyos.DecodeValue(raw); err == nil {
			return raw, nil
		}
		// Preserve valid legacy JSON while also accepting literal scalar strings.
		encoded, _ := json.Marshal(raw)
		return string(encoded), nil
	}
	native, err := nativeConfiguration(underlying)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(native)
	if err != nil {
		return "", errors.New("cannot encode configuration")
	}
	_, err = vyos.DecodeValue(string(raw))
	return string(raw), err
}

func nativeConfiguration(value attr.Value) (any, error) {
	if value == nil || value.IsNull() {
		return nil, nil
	}
	if value.IsUnknown() {
		return nil, errUnknownValue
	}
	switch v := value.(type) {
	case types.Dynamic:
		return nativeConfiguration(v.UnderlyingValue())
	case types.String:
		return v.ValueString(), nil
	case types.Object:
		return nativeObject(v.Attributes())
	case types.Map:
		return nativeObject(v.Elements())
	case types.Tuple:
		return nativeList(v.Elements())
	case types.List:
		return nativeList(v.Elements())
	default:
		return nil, errors.New("configuration values must be objects, strings or nonempty lists of strings; use strings for numbers and booleans")
	}
}
func nativeObject(values map[string]attr.Value) (any, error) {
	out := map[string]any{}
	for k, v := range values {
		value, err := nativeConfiguration(v)
		if err != nil {
			return nil, err
		}
		out[k] = value
	}
	return out, nil
}
func nativeList(values []attr.Value) (any, error) {
	out := make([]any, 0, len(values))
	for _, v := range values {
		value, err := nativeConfiguration(v)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}

func valueFromRouter(value any) attr.Value {
	switch v := value.(type) {
	case string:
		return types.StringValue(v)
	case map[string]any:
		attrs := map[string]attr.Value{}
		typs := map[string]attr.Type{}
		for k, child := range v {
			attrs[k] = valueFromRouter(child)
			typs[k] = attrs[k].Type(context.Background())
		}
		return types.ObjectValueMust(typs, attrs)
	case []any:
		attrs := make([]attr.Value, len(v))
		typs := make([]attr.Type, len(v))
		for i, child := range v {
			attrs[i] = valueFromRouter(child)
			typs[i] = attrs[i].Type(context.Background())
		}
		return types.TupleValueMust(typs, attrs)
	default:
		return types.DynamicNull()
	}
}
func refreshConfiguration(prior types.Dynamic, remote any) types.Dynamic {
	raw, err := configurationJSON(prior)
	if err == nil && !prior.IsNull() && !prior.IsUnknown() && vyos.Equivalent(raw, remote) {
		return prior
	}
	// Imports have no prior representation. Keep the legacy JSON representation
	// until configuration establishes whether the user wants native HCL values.
	if _, legacy := prior.UnderlyingValue().(types.String); legacy || prior.IsNull() || prior.IsUnknown() {
		encoded, _ := json.Marshal(remote)
		return types.DynamicValue(types.StringValue(string(encoded)))
	}
	return types.DynamicValue(valueFromRouter(remote))
}
