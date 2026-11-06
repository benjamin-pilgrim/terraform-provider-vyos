package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The entire entries object is dynamic because named entries can have different
// configuration types. Terraform collection elements must share a single type.

func batchModels(ctx context.Context, values types.Dynamic) (map[string]batchEntryModel, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	models := map[string]batchEntryModel{}
	if values.IsNull() || values.IsUnknown() || values.IsUnderlyingValueUnknown() || values.IsUnderlyingValueNull() {
		diagnostics.AddError("Unknown batch entries", "Entry names and paths must be known when planning changes to an existing batch.")
		return models, diagnostics
	}
	var entries map[string]attr.Value
	switch v := values.UnderlyingValue().(type) {
	case types.Object:
		entries = v.Attributes()
	case types.Map:
		entries = v.Elements()
	default:
		diagnostics.AddError("Invalid batch entries", "Use an object of named entries, each containing path and exactly one of value or include.")
		return models, diagnostics
	}
	for name, entry := range entries {
		if entry.IsNull() {
			diagnostics.AddError("Invalid batch entry", fmt.Sprintf("Entry %q must contain path and exactly one of value or include.", name))
			continue
		}
		if entry.IsUnknown() {
			models[name] = batchEntryModel{Path: types.ListUnknown(types.StringType), Value: types.DynamicUnknown()}
			continue
		}
		object, ok := entry.(types.Object)
		if !ok {
			diagnostics.AddError("Invalid batch entry", fmt.Sprintf("Entry %q must be an object containing path and exactly one of value or include.", name))
			continue
		}
		fields := object.Attributes()
		if len(fields) != 2 || fields["path"] == nil || (fields["value"] == nil) == (fields["include"] == nil) {
			diagnostics.AddError("Invalid batch entry", fmt.Sprintf("Entry %q must contain exactly path and exactly one of value or include.", name))
			continue
		}
		field := "value"
		partial := fields["include"] != nil
		if partial {
			field = "include"
		}
		value, ok := fields[field].(types.Dynamic)
		if !ok {
			value = types.DynamicValue(fields[field])
		}
		model := batchEntryModel{Value: value, Partial: partial, originalPath: fields["path"]}
		var elements []attr.Value
		if fields["path"].IsUnknown() {
			model.Path = types.ListUnknown(types.StringType)
		} else if fields["path"].IsNull() {
			model.Path = types.ListNull(types.StringType)
		} else {
			switch v := fields["path"].(type) {
			case types.Tuple:
				elements = v.Elements()
			case types.List:
				elements = v.Elements()
			default:
				diagnostics.AddError("Invalid batch path", fmt.Sprintf("Entry %q path must be an array of strings.", name))
				continue
			}
			for _, component := range elements {
				if _, ok := component.(types.String); !ok {
					diagnostics.AddError("Invalid path component", "Path components must be strings.")
				}
			}
			if diagnostics.HasError() {
				continue
			}
			var d diag.Diagnostics
			model.Path, d = types.ListValue(types.StringType, elements)
			diagnostics.Append(d...)
		}
		models[name] = model
	}
	return models, diagnostics
}
func packBatchModels(ctx context.Context, models map[string]batchEntryModel) (types.Dynamic, diag.Diagnostics) {
	entries := map[string]attr.Value{}
	typs := map[string]attr.Type{}
	for name, model := range models {
		var p attr.Value = model.Path
		if model.originalPath != nil {
			p = model.originalPath
		}
		v := model.Value.UnderlyingValue()
		if model.Value.IsUnknown() {
			v = types.DynamicUnknown()
		}
		if v == nil {
			v = types.StringNull()
		}
		field := "value"
		if model.Partial {
			field = "include"
		}
		entries[name] = types.ObjectValueMust(map[string]attr.Type{"path": p.Type(ctx), field: v.Type(ctx)}, map[string]attr.Value{"path": p, field: v})
		typs[name] = entries[name].Type(ctx)
	}
	object, d := types.ObjectValue(typs, entries)
	return types.DynamicValue(object), d
}
