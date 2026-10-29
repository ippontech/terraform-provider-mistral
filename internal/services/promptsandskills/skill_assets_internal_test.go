// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package promptsandskills

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// TestValidateAssetsChecksEveryAssetPastAnUnknownOne checks that an asset
// unknown at plan time does not end the validation of the others: the invalid
// asset still gets its error. Go iterates a map in random order, so with seven
// unknown assets a loop that stopped at the first one would miss the invalid
// asset in seven runs out of eight.
func TestValidateAssetsChecksEveryAssetPastAnUnknownOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var resp resource.SchemaResponse
	NewSkillResource().Schema(ctx, resource.SchemaRequest{}, &resp)
	configType := resp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	assetsType := configType.AttributeTypes["assets"].(tftypes.Map)
	assetType := assetsType.ElementType.(tftypes.Object)

	assets := map[string]tftypes.Value{}
	for i := range 7 {
		assets[fmt.Sprintf("unknown-%d", i)] = tftypes.NewValue(assetType, tftypes.UnknownValue)
	}
	both := map[string]tftypes.Value{}
	for name, typ := range assetType.AttributeTypes {
		both[name] = tftypes.NewValue(typ, nil)
	}
	both["content"] = tftypes.NewValue(tftypes.String, "text")
	both["content_base64"] = tftypes.NewValue(tftypes.String, "dGV4dA==")
	assets["both"] = tftypes.NewValue(assetType, both)

	attrs := map[string]tftypes.Value{}
	for name, typ := range configType.AttributeTypes {
		attrs[name] = tftypes.NewValue(typ, nil)
	}
	attrs["assets"] = tftypes.NewValue(assetsType, assets)
	config := tfsdk.Config{Schema: resp.Schema, Raw: tftypes.NewValue(configType, attrs)}

	var diags diag.Diagnostics
	validateAssets(ctx, config, &diags)
	if diags.ErrorsCount() != 1 {
		t.Fatalf("errors = %d, want 1 for the asset that sets both contents: %v", diags.ErrorsCount(), diags)
	}
	withPath, ok := diags.Errors()[0].(diag.DiagnosticWithPath)
	if !ok || !withPath.Path().Equal(path.Root("assets").AtMapKey("both")) {
		t.Errorf("error = %v, want it on assets[\"both\"]", diags.Errors()[0])
	}
}
