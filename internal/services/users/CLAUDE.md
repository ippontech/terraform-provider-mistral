# internal/services/users

`mistral_invite` and the `mistral_user` and `mistral_users` data sources: the invitations and the members of the organization, with the Admin API key. Every endpoint is tagged `beta.admin.users`. Nobody on the team has called the Admin API: the fake is the only test.

## API

- Reference: <https://docs.mistral.ai/api/endpoint/beta/admin/users>; guides: <https://docs.mistral.ai/admin/admin-api/manage-users>, <https://docs.mistral.ai/admin/admin-api/user-provisioning>; Admin Panel: <https://docs.mistral.ai/admin/identity-access/user-management>.
- `GET /v1/admin/users` lists members (`AdminOrganizationMemberOUT`, no first and last name) and pending invites (`OrganizationInviteOUT`, with `expired`, `created_at`, `workspace_uuids`) together: `page` from 1, `page_size` up to 100, `total` of both, filter `email`. `GET /v1/admin/users/{user_id}` (`AdminUserOUT`) adds `first_name` and `last_name`.
- `POST /v1/admin/users-invite` takes `email` (one address, or several separated by commas or new lines), `role_names` (`member`, `billing_manager`, `organization_admin`) or `roles` (UUIDs), `subscription_types`, `workspace_uuids`, `email_language`. It answers `OrganizationInvitesCreateOUT`: `invalid_emails`, `already_members`, `invited_members_count`, with a 200 even when it refuses an address, and **no invite ID**.
- `GET /v1/admin/users-invite` lists invites without a filter, pagination, expiry or creation date (`OrganizationUserInviteOUT`). `DELETE /v1/admin/users-invite/{invite_uuid}` revokes one. No read by ID, no update.
- Roles come back as `raw_roles`: role UUIDs (`StaticOrganizationRoles`, which the specification does not name) or the legacy letters of `UserRole` (`A`, `M`, `B`). The provisioning guide names `A` (Admin) and `M` (Member); `B` for billing manager is our reading.
- `POST /v1/admin/users/{user_id}/deactivate` "remove[s] them from every organization they belong to, which revokes their API keys". Never wired.

## Decisions

- **One find function per seam.** `findInvite` stands in for a read by ID, `findCreatedInvite` for an ID in the create's answer: each is the one function to replace when Mistral adds what it stands for. Both list `GET /v1/admin/users?email=`, the narrowest filter, and compare the address themselves (case-insensitive), since the filter may be a substring match.
- **Brief versus specification.** The brief named `GET /v1/admin/users-invite` filtered by email; that route has no filter and no expiry, so the provider uses `GET /v1/admin/users`.
- **Status.** The API has none. `pending` or `expired` comes from the listed invite. Once accepted, Mistral no longer lists the invite; an address that belongs to a member then reads `accepted`, and the invite stays in the state (CLAUDE.md, "State after status changes"). With neither, `Read` removes it. A member who leaves therefore plans a new invite, in plain sight.
- **Destroy.** A pending or expired invite is revoked. An accepted one is only forgotten: the call would at best answer 404, and removing the member is out of scope.
- **No silent adoption.** Create refuses a pending invite for the address (with the import block) and a member with it. An expired invite does not block: the new invite is another one, and `findCreatedInvite` takes the newest pending invite of the address.
- **Arguments are kept on Read.** Mistral cannot change an invite, and returns its roles as UUIDs or letters, not as the names the configuration holds. `Read` refreshes `status` only, and fills the arguments from the API only after an import, naming the roles through `GET /v1/admin/roles` (called only when a UUID is there). A role it cannot name is kept as returned, so the plan shows it.
- **Sorted bodies.** `role_names` and `workspace_ids` are sets; the create sends them sorted, so a body never depends on Terraform's order.
- **Not mapped:** `subscription_types` and `subscription_seat_automatic_granting` (paid seats, not returned by the list, so neither drift nor import could see them), `email_language` (not returned; changing it would resend the invite), the deprecated `role`, `role_name` and `subscription_type`.
- **Data sources.** `mistral_user` looks up by `email` (root `CLAUDE.md`), then reads by ID for the names. `mistral_users` holds members only. Both expose the roles as returned (`organization_roles`, `workspaces[].roles`), with `mistral_roles` to name them, and read a list the API leaves out as null, not empty.
- **Identity on a removed invite.** terraform-plugin-framework v1.19.0 wants an identity after any `Read` without error, even one that removes the resource; an import by string ID of a missing invite would fail with "Missing Resource Identity After Read". `Read` sets the identity before `RemoveResource`.

## A `mistral_user` resource, later

Not in this package yet, because:

- `POST /v1/admin/users` creates in bulk and answers 200 on partial failure (`invalid_emails`), with `email_to_user_id` as the only link to the created users;
- `subscription_types` assigns paid seats (`CHAT`, `MISTRAL_CODE`);
- a profile can be managed by SSO or SCIM: name and email then come from the identity provider (user management page);
- `deactivate` removes the person from every organization and revokes their API keys.

A later resource would manage roles and seats only (`PATCH /v1/admin/users/{user_id}`), take a member that exists (import, or a create that refuses an existing address), destroy through `DELETE /v1/admin/users/{user_id}` and never through `deactivate`, say that it cannot remove the last organization admin, and refuse to run on an organization whose users SCIM provisions, since both would write the same members.

## To check on the first live run

- That `GET /v1/admin/users?email=` returns the invite of the address in `invites`, with `expired`; whether the filter is exact or a substring match; whether `page_size` counts members and invites together.
- What `raw_roles` holds for an invite made with `role_names`: UUIDs or letters, and whether `GET /v1/admin/roles` names them with the `role_names` values.
- That an accepted invite leaves both lists, and what `DELETE` answers for it.
- Whether a second invite to an address with an expired one creates a new invite or refreshes the old one (`findCreatedInvite` handles both).
- The answer of the create for an address that already has a pending invite.
- Whether the create accepts an empty `workspace_uuids`, and whether the list answers `[]` for an invite to no workspace, as we read it: the provider refuses an empty `workspace_ids` and reads `[]` as null (root `CLAUDE.md`, *Empty collections*).

## Tests

- `fake_test.go` serves the five routes: the list pages members and invites together and filters by substring; a create stores the roles as the fake's role UUIDs; `accept` and `expire` stage the status changes; `invalid`, `unlisted` and `afterInvite` stage the refusals and failures. Its clock moves one minute per write: `at(n)` is the n-th write.
- `invite_resource_fake_test.go` holds the lifecycle, the exact bodies and both imports; `_status_` the accepted and expired invites; `_errors_` the refusals, faults and validators; `invite_internal_test.go` the refusals of the create's answer.
- `user_data_source_fake_test.go` and `users_data_source_fake_test.go` cover two pages, exact values, not found and errors. `invite_resource_test.go` holds the acceptance tests.
