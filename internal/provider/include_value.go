package provider

import (
	"context"
	"errors"

	"github.com/TGNThump/terraform-provider-vyos/internal/vyos"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func includeStrings(value types.Dynamic) ([]string, error) {
	if value.IsUnknown() || value.IsUnderlyingValueUnknown() {
		return nil, errUnknownValue
	}
	if value.IsNull() || value.IsUnderlyingValueNull() {
		return nil, errors.New("include must be a collection of strings, not null")
	}
	var elements []attr.Value
	switch v := value.UnderlyingValue().(type) {
	case types.Tuple:
		elements = v.Elements()
	case types.List:
		elements = v.Elements()
	case types.Set:
		elements = v.Elements()
	default:
		return nil, errors.New("include must be a collection of strings")
	}
	result := make([]string, 0, len(elements))
	for _, element := range elements {
		if element.IsUnknown() {
			return nil, errUnknownValue
		}
		text, ok := element.(types.String)
		if !ok || text.IsNull() {
			return nil, errors.New("include values must be non-null strings")
		}
		result = append(result, text.ValueString())
	}
	if err := vyos.ValidateInclude(result); err != nil {
		return nil, err
	}
	return result, nil
}
func refreshInclude(prior types.Dynamic, present []string) types.Dynamic {
	original, err := includeStrings(prior)
	if err == nil && len(original) == len(present) {
		found := map[string]bool{}
		for _, value := range present {
			found[value] = true
		}
		same := true
		for _, value := range original {
			if !found[value] {
				same = false
			}
		}
		if same {
			return prior
		}
	}
	values := make([]attr.Value, len(present))
	typs := make([]attr.Type, len(present))
	for i, value := range present {
		values[i] = types.StringValue(value)
		typs[i] = types.StringType
	}
	switch prior.UnderlyingValue().(type) {
	case types.Set:
		return types.DynamicValue(types.SetValueMust(types.StringType, values))
	case types.List:
		return types.DynamicValue(types.ListValueMust(types.StringType, values))
	default:
		return types.DynamicValue(types.TupleValueMust(typs, values))
	}
}

// Keep all possibly applied includes after an uncertain update. Assign values
// moving between entries to their planned entry so ownership remains disjoint.
func retainIncludeOwnership(ctx context.Context, prior, planned types.Dynamic) (types.Dynamic, diag.Diagnostics) {
	before, diagnostics := batchModels(ctx, prior)
	after, d := batchModels(ctx, planned)
	diagnostics.Append(d...)
	if diagnostics.HasError() {
		return prior, diagnostics
	}
	owned := map[string]map[string]bool{}
	values := map[string][]string{}
	for name, entry := range after {
		if !entry.Partial {
			continue
		}
		members, err := includeStrings(entry.Value)
		if err != nil {
			diagnostics.AddError("Invalid include ownership", err.Error())
			continue
		}
		path, _ := entry.Path.ToTerraformValue(ctx)
		key := path.String()
		if owned[key] == nil {
			owned[key] = map[string]bool{}
		}
		values[name] = append([]string{}, members...)
		for _, member := range members {
			owned[key][member] = true
		}
	}
	for name, entry := range before {
		if !entry.Partial {
			continue
		}
		members, err := includeStrings(entry.Value)
		if err != nil {
			diagnostics.AddError("Invalid prior include ownership", err.Error())
			continue
		}
		path, _ := entry.Path.ToTerraformValue(ctx)
		key := path.String()
		if owned[key] == nil {
			owned[key] = map[string]bool{}
		}
		for _, member := range members {
			if !owned[key][member] {
				values[name] = append(values[name], member)
				owned[key][member] = true
			}
		}
	}
	for name, members := range values {
		entry := after[name]
		entry.Value = refreshInclude(entry.Value, members)
		before[name] = entry
	}
	if diagnostics.HasError() {
		return prior, diagnostics
	}
	result, d := packBatchModels(ctx, before)
	diagnostics.Append(d...)
	return result, diagnostics
}
