// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package users

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
)

var (
	_ resource.ResourceWithConfigure   = &InviteResource{}
	_ resource.ResourceWithImportState = &InviteResource{}
	_ resource.ResourceWithIdentity    = &InviteResource{}
)

// NewInviteResource returns the mistral_invite resource.
func NewInviteResource() resource.Resource {
	return &InviteResource{}
}

// InviteResource manages an invitation to the organization:
// POST /v1/admin/users-invite and DELETE /v1/admin/users-invite/{invite_uuid}
// (https://docs.mistral.ai/admin/admin-api/manage-users#invitations). The API
// has no update and no read by ID: every argument replaces the invite, and
// Read goes through findInvite.
type InviteResource struct {
	client *client.Client
}

// The status of an invite. The API has no status field: findInvite derives
// it from the expired flag of the listed invite, and from the member the
// invite made once Mistral no longer lists it.
const (
	statusPending  = "pending"
	statusExpired  = "expired"
	statusAccepted = "accepted"
)

// organizationRoleNames are the names role_names takes (OrganizationInviteIN
// in the specification).
var organizationRoleNames = []string{"member", "billing_manager", "organization_admin"}

// oneAddress is a single email address. The API reads commas and new lines
// in email as separators between several invites, which one resource cannot
// track.
var oneAddress = regexp.MustCompile(`^[^\s,@]+@[^\s,@]+$`)

// inviteModel is the state of mistral_invite.
type inviteModel struct {
	ID           types.String `tfsdk:"id"`
	Email        types.String `tfsdk:"email"`
	RoleNames    []string     `tfsdk:"role_names"`
	WorkspaceIDs []string     `tfsdk:"workspace_ids"`
	Status       types.String `tfsdk:"status"`
	CreatedAt    types.String `tfsdk:"created_at"`
}

// inviteIdentity is the resource identity of mistral_invite.
type inviteIdentity struct {
	ID types.String `tfsdk:"id"`
}

// Metadata names the resource mistral_invite.
func (r *InviteResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_invite"
}

// IdentitySchema identifies an invite by its UUID.
func (r *InviteResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"id": identityschema.StringAttribute{RequiredForImport: true, Description: "The invite UUID."},
		},
	}
}

// Schema declares every argument RequiresReplace, since the API cannot
// update an invite.
func (r *InviteResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replaceSet := []planmodifier.Set{setplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Invites a person to the organization by email. The invite stays in the state once accepted or expired; " +
			"destroying an accepted invite leaves the member in the organization.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The invite UUID.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"email": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The email address to invite: one address. Changing it replaces the invite.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{stringvalidator.RegexMatches(oneAddress,
					"must be one email address: a list separated by commas or new lines would send several invites, which one resource cannot track")},
			},
			"role_names": schema.SetAttribute{
				Required:    true,
				ElementType: types.StringType,
				MarkdownDescription: "The organization roles the person gets on joining: `member`, `billing_manager` or `organization_admin`. " +
					"Changing them replaces the invite.",
				PlanModifiers: replaceSet,
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(stringvalidator.OneOf(organizationRoleNames...)),
				},
			},
			"workspace_ids": schema.SetAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "The IDs of the workspaces the person joins on accepting. Changing them replaces the invite.",
				PlanModifiers:       replaceSet,
				Validators:          []validator.Set{setvalidator.SizeAtLeast(1)},
			},
			"status": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "`pending` until the person accepts, `expired` when the invite expired unaccepted, " +
					"`accepted` once the address belongs to a member of the organization.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "When the invite was created, in RFC 3339 format.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// Configure takes the client, with the Admin API key.
func (r *InviteResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerdata.Configure(req.ProviderData, providerdata.Admin, &resp.Diagnostics)
}

// Create checks that nothing exists for the address, sends the invite, then
// finds it: the answer of the create holds counts, not the invite.
func (r *InviteResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan inviteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	email := plan.Email.ValueString()

	// No silent adoption: a pending invite made outside Terraform could not be
	// told apart from the one this create makes, and a member has nothing to
	// accept.
	before, err := r.client.ListUsers(ctx, email)
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the invite", err.Error())
		return
	}
	if resp.Diagnostics.Append(refuseExisting(email, before)...); resp.Diagnostics.HasError() {
		return
	}

	result, err := r.client.InviteUsers(ctx, client.InviteRequest{Email: email, RoleNames: sorted(plan.RoleNames), WorkspaceIDs: sorted(plan.WorkspaceIDs)})
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the invite", err.Error())
		return
	}
	if resp.Diagnostics.Append(refusedByAPI(email, *result)...); resp.Diagnostics.HasError() {
		return
	}

	invite, err := r.findCreatedInvite(ctx, email)
	if err != nil {
		resp.Diagnostics.AddError("Cannot find the invite", "Mistral accepted the invite, but listing it failed: "+err.Error())
		return
	}
	if invite == nil {
		resp.Diagnostics.AddError("Cannot find the invite",
			fmt.Sprintf("Mistral accepted the invite for %q, but does not list it. Look for it in the Invited Users of the Admin Panel, "+
				"and import it with an import block once it shows there.", email))
		return
	}
	plan.ID = types.StringValue(invite.ID)
	plan.Status = types.StringValue(inviteStatus(*invite))
	plan.CreatedAt = timestamp(invite.CreatedAt)
	resp.Diagnostics.Append(setInviteState(ctx, plan, &resp.State, resp.Identity)...)
}

// Read refreshes the status, and removes the invite once Mistral neither
// lists it nor has a member with its address: revoked, or a member who left.
// After an import, only id is in the state: Read fills the arguments from
// the API.
func (r *InviteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state inviteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	found, err := r.findInvite(ctx, state.ID.ValueString(), state.Email.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the invite", err.Error())
		return
	}
	if found == nil {
		// The framework (v1.19.0) wants an identity after every Read that
		// ends without error, even one that removes the resource. An import
		// by string ID starts without one, and would fail with "Missing
		// Resource Identity After Read" instead of Terraform's "Cannot import
		// non-existent remote object".
		resp.Diagnostics.Append(resp.Identity.Set(ctx, inviteIdentity{ID: state.ID})...)
		resp.State.RemoveResource(ctx)
		return
	}
	state.Status = types.StringValue(found.status)
	if found.invite != nil && state.Email.IsNull() {
		if err := r.importArguments(ctx, &state, *found.invite); err != nil {
			resp.Diagnostics.AddError("Cannot read the invite", err.Error())
			return
		}
	}
	resp.Diagnostics.Append(setInviteState(ctx, state, &resp.State, resp.Identity)...)
}

// Update is never called: every argument requires a replacement, and the
// computed attributes change only on Read.
func (r *InviteResource) Update(context.Context, resource.UpdateRequest, *resource.UpdateResponse) {}

// Delete revokes a pending or expired invite. An accepted invite is gone at
// Mistral: revoking it would mean removing the member, which this resource
// never does, so Delete only forgets it.
func (r *InviteResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state inviteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || state.Status.ValueString() == statusAccepted {
		return
	}
	err := r.client.DeleteInvite(ctx, state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot delete the invite", err.Error())
	}
}

// ImportState takes the invite UUID, as an ID or an identity.
func (r *InviteResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughWithIdentity(ctx, path.Root("id"), path.Root("id"), req, resp)
}

// importArguments fills the arguments of an imported invite from the API.
// Otherwise Read keeps them from the state: Mistral cannot change an invite,
// and it returns the roles as UUIDs or letters, not as the names the
// configuration holds.
func (r *InviteResource) importArguments(ctx context.Context, state *inviteModel, inv client.Invite) error {
	names, err := r.roleNames(ctx, inv.RawRoles)
	if err != nil {
		return err
	}
	state.Email = types.StringValue(inv.Email)
	state.RoleNames = names
	state.CreatedAt = timestamp(inv.CreatedAt)
	// The API answers [] for no workspace; the schema refuses an empty set
	// and the state holds null instead, so that [] and null never alternate.
	if len(inv.WorkspaceIDs) > 0 {
		state.WorkspaceIDs = inv.WorkspaceIDs
	}
	return nil
}

// refuseExisting fails a create when the address already has a member, or a
// pending invite, which the create must not take over. An expired invite
// does not count: a new invite is what its owner needs.
func refuseExisting(email string, dir *client.UserDirectory) (diags diag.Diagnostics) {
	if u := findUser(dir, email); u != nil {
		diags.AddAttributeError(path.Root("email"), "Already a member",
			fmt.Sprintf("The organization already has a member with the email %q (user ID %q): there is no one to invite. "+
				"Read the member with the mistral_user data source.", email, u.ID))
		return diags
	}
	for _, inv := range dir.Invites {
		if !inv.Expired && sameEmail(inv.Email, email) {
			diags.AddAttributeError(path.Root("email"), "Invite already exists",
				fmt.Sprintf("A pending invite for %q exists (ID %q). Import it with an import block (id = %q) to manage it, "+
					"or revoke it in the Admin Panel.", email, inv.ID, inv.ID))
			return diags
		}
	}
	return diags
}

// refusedByAPI reads the refusals the API answers with a 200: an address it
// finds invalid, or one that became a member since refuseExisting ran.
func refusedByAPI(email string, result client.InviteResult) (diags diag.Diagnostics) {
	switch {
	case len(result.InvalidEmails) > 0:
		diags.AddAttributeError(path.Root("email"), "Invalid email",
			fmt.Sprintf("Mistral refused %s as invalid.", strings.Join(result.InvalidEmails, ", ")))
	case len(result.AlreadyMembers) > 0:
		diags.AddAttributeError(path.Root("email"), "Already a member",
			fmt.Sprintf("Mistral did not invite %q: the address belongs to a member of the organization.", email))
	case result.InvitedCount != nil && *result.InvitedCount == 0:
		diags.AddError("Cannot create the invite", fmt.Sprintf("Mistral invited no one for %q, and gave no reason.", email))
	}
	return diags
}

// sorted returns the elements of a set in order, so that a request does not
// depend on the order Terraform hands them in; nil stays nil, which the
// request sends as null.
func sorted(set []string) []string {
	if set == nil {
		return nil
	}
	return slices.Sorted(slices.Values(set))
}

// setInviteState writes the invite and its identity.
func setInviteState(ctx context.Context, m inviteModel, state *tfsdk.State, identity *tfsdk.ResourceIdentity) diag.Diagnostics {
	diags := state.Set(ctx, &m)
	if identity != nil {
		diags.Append(identity.Set(ctx, inviteIdentity{ID: m.ID})...)
	}
	return diags
}
