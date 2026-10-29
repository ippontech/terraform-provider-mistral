# internal/services/workspaces

`mistral_workspace`, `mistral_workspace_member`, and the `mistral_workspace` and `mistral_workspaces` data sources: the workspaces of the organization and their members, through the Admin API (`providerdata.Admin`). Every endpoint is tagged `beta.admin.workspaces`, except `GET /v1/admin/users/{user_id}` and `GET /v1/admin/roles` (`beta.admin.users`).

## API

- Guides: <https://docs.mistral.ai/admin/admin-api/manage-workspaces>, <https://docs.mistral.ai/admin/workspaces/your-first-workspace>, <https://docs.mistral.ai/admin/identity-access/roles-permissions>, <https://docs.mistral.ai/admin/admin-api/manage-groups-roles>. Reference: <https://docs.mistral.ai/api/endpoint/beta/admin/workspaces> and <https://docs.mistral.ai/api/endpoint/beta/admin/users>.
- `POST /v1/admin/workspaces` takes `name` and `admin_user_id` (required), `description` and `icon` (default `""`, at most 255 and 2 characters) and `add_all_org_members` (default `false`). It answers `WorkspaceEnrichedOUT`.
- `PATCH /v1/admin/workspaces/{workspace_uuid}` takes `name`, `description`, `icon`, all nullable. `DELETE` on the same path is "Archive a Workspace" (204). The guide: archiving "cannot be undone", "The default Workspace cannot be archived", names "must be unique within an Organization", at most 500 active workspaces.
- No read by ID. `GET /v1/admin/workspaces` pages by number from 1 (`page_size` at most 100) with a `total`, filters with `search` ("Search term to filter Workspaces by name") and `is_archived` ("Whether to include archived Workspaces", default false). `WorkspaceOUT` has no archived flag.
- Members: `PATCH .../{workspace_uuid}/users` ("Add or update", which the guide calls idempotent), `POST .../add-users` ("Add"), `DELETE .../remove-users` with a body, answering `not_deleted_members`. Both writes take `members[]` of `user_uuid` and `role_names` (or `roles`, UUIDs; the singular `role` and `role_name` are deprecated).
- No route reads a membership or lists the members of a workspace. `GET /v1/admin/users/{user_id}` answers the user's `workspaces[]` with `raw_roles`: role UUIDs (`StaticWorkspaceRoles`) or the legacy codes `A` and `M` (`WorkspaceRole`). `GET /v1/admin/roles` names the UUIDs ("RBAC must be enabled for the Organization").
- New members get `workspace_contributor` "regardless of their Organization role"; roles add up.

## Decisions

- **`findWorkspace`** lists the active workspaces with `search=<name>`, then all of them (a rename outside Terraform, or an import, escapes the search), then with `is_archived=true`: a workspace found only there is archived, since the answer has no flag. `Read` removes an archived workspace with a warning (root `CLAUDE.md`, *State after status changes*). Replace this function when Mistral adds a read by ID.
- **`admin_user_id` and `add_all_org_members` are create-only, not `RequiresReplace`.** A replacement archives the workspace and everything in it, for fields that only seed its first members; and the plan must follow the configuration, so the change cannot be hidden. A change plans an in-place update that sends nothing and records the value; the `createOnly` plan modifier warns at plan time. Neither is read back: after an import, `admin_user_id` is null (the first apply records it) and `add_all_org_members` reads as its default, `false`. No argument replaces a workspace.
- **`description` and `icon` default to `""`,** the create defaults; an answer's null reads as `""`. The update sends all three fields, `""` included, since our reading of a nullable `PATCH` field is "left as it is".
- **Members through `PATCH .../users`.** It is the documented idempotent upsert, retried on 5xx, and the update needs it anyway. `POST .../add-users` is not used: its behaviour on an existing member is undocumented, and it is not idempotent. The create checks the membership first and fails with the import block (no silent adoption); the admin of a new workspace is already a member.
- **`role_names` is a set**, default `["workspace_contributor"]`, with no `OneOf`: `GET /v1/admin/roles` is "the authoritative list", and custom roles exist. The request sorts it. `findWorkspaceMember` reads the user, takes the workspace's `raw_roles` and names them with `GET /v1/admin/roles`; a raw role the list does not name stays as it is, so the plan shows it.
- **Delete of a member.** A 404 (workspace gone) is done. When `not_deleted_members` lists the user, the membership decides: our reading is that the API lists users that were not members.
- **Data sources.** `mistral_workspace` looks up by `name`, unique in an organization and the name users know (the Admin Panel shows no ID); it searches, then keeps the exact match. `mistral_workspaces` lists the active workspaces, every page. Not mapped: `members_count`, `groups_count` (they change with every member), `spend_limit` (set in the Admin Panel only).

## To check on the first live run

- `is_archived=true`: archived workspaces only, or active ones too (`findWorkspace` works with both); whether an archived workspace answers 404 to `PATCH` and `DELETE`, and keeps its name.
- `search`: substring or prefix, case-sensitive or not (the data source keeps the exact match either way).
- `PATCH` with a null field: kept or cleared (the provider never sends null).
- Whether `RoleOut.name` is the role name (`workspace_admin`) or the label (`Admin`), and whether `raw_roles` comes back as UUIDs or `A`/`M` after a `PATCH` with `role_names`. Either one wrong means a perpetual diff on `role_names`.
- Whether `PATCH .../users` replaces a member's roles or adds to them, and what it answers for a user outside the organization.
- What `remove-users` answers for a user that is not a member.
- What becomes of the API keys and resources of an archived workspace.

## Tests

- `fake_test.go` serves the eight routes over workspaces, members and users. A create makes `admin_user_id` a workspace admin, `add_all_org_members` adds every user; archived workspaces hide from the default list; role names map to the fake's own UUIDs. `keep(user)` makes the next removal leave the user in place.
- `workspace_resource_fake_test.go`, `workspace_member_resource_fake_test.go` and `workspace_data_source_fake_test.go` hold the fake tests; `workspace_resource_test.go` the acceptance test, which needs `MISTRAL_ACC_ADMIN_USER_ID` and `MISTRAL_ACC_MEMBER_USER_ID` and archives a workspace on every run.
- An import by identity of a workspace plans an update (`admin_user_id`), so that step sets `ExpectNonEmptyPlan` and checks the update.
- `create_only_internal_test.go` checks the plan warning of `createOnly`, which the resource tests cannot see.
- Every `Read` that removes the resource sets its identity first: during an import, terraform-plugin-framework v1.19.0 fails such a Read with "Missing Resource Identity After Read" instead of Terraform's "Cannot import non-existent remote object". The `ImportOfAMissing` tests pin it.
