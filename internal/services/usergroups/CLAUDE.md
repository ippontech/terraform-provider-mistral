# internal/services/usergroups

`mistral_user_group`, `mistral_user_group_member`, `mistral_user_group_workspace` and the `mistral_user_group` data source: the user groups of the organization of the Admin API key. Every endpoint is tagged `beta.admin.user-groups`; every type is `Admin`, `beta`, `fakeOnly`.

## API

- Reference: <https://docs.mistral.ai/api/endpoint/beta/admin/user-groups>. Guides: <https://docs.mistral.ai/admin/admin-api/manage-groups-roles>, <https://docs.mistral.ai/admin/identity-access/groups>, <https://docs.mistral.ai/admin/admin-api/user-provisioning>.
- Groups: `POST /v1/admin/user-groups` (`name` up to 255, `description`, `parent_group_ids` as a non-nullable array), `GET`, `PATCH`, `DELETE /{group_uuid}`. On `PATCH`, null or absent means "leave unchanged": `description: ""` clears it, `parent_group_ids: []` removes every parent.
- Organization role: `PATCH /{group_uuid}/organization-role` with `organization_role`, required: a name (`member`, `billing_manager`, `organization_admin`), a legacy letter (`A`, `M`, `B`) or a role UUID. It answers 204. There is no way to clear the role.
- Members: `POST` and `DELETE /{group_uuid}/members`, both with `{"user_uuids": [...]}` and a 204. The list `GET /{group_uuid}/members` pages from 1 (`page`, `page_size` up to 100, `total`) under the key `members`; there is no read of one member.
- Workspaces: `POST /{group_uuid}/workspaces` (`workspace_uuid`, `role_names`; 201 with no body), `PATCH` and `DELETE /{group_uuid}/workspaces/{workspace_uuid}` (204). The list pages like the members one under `items`, and gives `roles: [{name, uuid}]` plus the deprecated `role_name`, `role_uuid`. `role_names` and `roles` are mutually exclusive; the singular `role` is deprecated.
- Nesting can be set from both ends: `parent_group_ids` on the child (create, `PATCH`, `GET`), or `GET`/`PATCH /{group_uuid}/nested` (`child_group_uuids`, full replacement) on the parent.
- `POST /v1/admin/user-groups/provision-workspace` is deprecated in the specification for `POST /{group_uuid}/workspaces` with `role_names`. The user provisioning guide still shows it.
- SCIM: `POST /v1/admin/scim/sync` (tag `beta.admin.scim`) syncs users, groups and memberships, and can `delete_missing_groups` ("Organization groups that are absent from the SCIM provider"). The groups guide: externally managed groups "cannot be edited directly in Mistral for fields owned by the identity provider". The admin overview says SCIM "will automate onboarding and offboarding when it becomes generally available".

## Decisions

- **Nesting from the child only.** `mistral_user_group.parent_group_ids` is the one way to nest groups; `/nested` is not used. The field lives on the group: the create nests the group in the same call, the read comes from the same `GET`, and each parent-child edge belongs to the child's resource alone. A `child_group_ids` on the parent as well would let two resources claim the same edge and undo each other. The cost: Terraform cannot nest a group it does not manage (such as a SCIM group) under one it does.
- **`provision-workspace` out of scope.** It is deprecated, answers 204 with nothing to read back, and `mistral_user_group_workspace` covers it.
- **`organization_role` is Optional and Computed**, with `UseStateForUnknown`: the API cannot clear it, so a removed argument keeps the last value rather than planning a change the API cannot make. Only the three names are accepted: a UUID in the configuration could not be compared with the name the API may answer. A legacy letter in an answer is mapped to its name (`B` as `billing_manager` is our reading); any other value is kept as written.
- **Two routes, two calls.** The group `PATCH` is sent only when `name`, `description` or `parent_group_ids` change, and the role `PATCH` only when the role changes, so that a role change never rewrites fields an identity provider may own. A role call is followed by a `GET`, since it answers 204.
- **Create saves the group before the role.** If the role call fails, the group is in the state and tainted, and the next apply replaces it rather than creating a second group.
- **No silent adoption.** `mistral_user_group_member` and `mistral_user_group_workspace` look for the membership or the assignment before the `POST`, and fail with the `import` block when it exists: the member `POST` succeeds silently for a user already there, and what the assignment `POST` does on an existing one is not documented.
- **One `Find` per type.** `client.FindUserGroupMember` and `client.FindUserGroupWorkspace` walk every page (`page_size=100`, the maximum) and compare the ID. Each is the one function to replace when Mistral adds a read route. A 404 means the group is gone, and `Read` removes the resource.
- **Identity before removal.** Every `Read` that removes the resource (a 404, or a member or assignment missing from the list) first sets the identity from the state: terraform-plugin-framework v1.19.0 otherwise fails an import of a missing object with "Missing Resource Identity After Read" instead of Terraform's "Cannot import non-existent remote object".
- **`mistral_user_group_member` has no update.** Both arguments require a replacement, and `Update` leaves the prior state, so an argument added later without `RequiresReplace` fails loudly instead of doing nothing. The user's name and email in the member list belong to the user, not the membership: not mapped.
- **`role_names` is a set**, sent sorted. It is read back from `roles[].name`: that those names are the `role_names` sent is our reading (the guide lists the same names for `GET /v1/admin/roles`).
- **The data source looks up by `name`**, its natural key: a group synced by SCIM or made in the Admin Panel is known by its name, and `mistral_user_group_workspace`, `mistral_user_group_member` and `parent_group_ids` consume its ID. `search` narrows the list, but its matching is undocumented, so names are compared exactly; zero or several matches fail the read. There is no lookup by ID: a user who has the ID needs no data source.
- **Empty values.** `description` and `parent_group_ids` refuse empty values and read `""`, null and `[]` as null. An absent or null `externally_managed` reads as `false`.
- **SCIM coexistence**, as far as the documentation goes: a synced group is read with the data source and given workspace access with `mistral_user_group_workspace`; its members belong to the identity provider. The pages say this, and that a sync with `delete_missing_groups` may delete groups Terraform created, which is not documented either way.

## To check on the first live run

- The format of `organization_role` in a `GET`: a name, a letter, or a UUID. A UUID would plan a change on every run.
- The `organization_role` of a new group (null in the fake), and whether `externally_managed` is sent for groups created through the API.
- Whether `description: ""` comes back as `""` or null (both read as null), and whether `parent_group_ids` comes back on `POST` and `PATCH`.
- That `roles[].name` echoes the `role_names` sent, and what the assignment `POST` does for a workspace the group already has.
- How `search` matches (the fake: a case-insensitive substring).
- The 404 body of a missing group, and whether the member and workspace lists of a deleted group answer 404 (the fake does).
- Whether a member `DELETE` for a user not in the group answers 204 or 404 (both are accepted).
- Whether a SCIM sync with `delete_missing_groups` deletes groups created through the API.

## Tests

- `fake_test.go` serves the thirteen routes over three collections (groups, memberships, assignments), with the `PATCH` semantics, the cascade of a group delete, the 404 of the lists of a missing group, pages from 1, and `search`. `seedGroup`, `seedMembers` and `seedAssignments` store objects created outside Terraform; `assertWalkedPage2` checks that a find reached the second page.
- One `<name>_fake_test.go` per type: lifecycle with exact bodies and both imports, an import of an ID the fake does not hold, refusal to take over, deletion and drift outside Terraform, a 5xx sent once, errors, validation.
- `user_group_resource_test.go` holds the acceptance tests. The member and workspace ones need `MISTRAL_ACC_MEMBER_USER_ID` and `MISTRAL_ACC_WORKSPACE_ID` (`acctest.Fixture`), and skip without them.
