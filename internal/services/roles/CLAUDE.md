# internal/services/roles

The `mistral_roles` data source: the organization and workspace roles of the Admin API, with the Admin API key. The endpoint is tagged `beta.admin.users`. Nobody on the team has called the Admin API: the fake is the only test.

## API

- `GET /v1/admin/roles` answers `RolesOut`: `organization_roles` and `workspace_roles`, each a list of `RoleOut` (`uuid`, `name`, `description`, `is_custom_role`), not paginated. Reference: <https://docs.mistral.ai/api/endpoint/beta/admin/users>; guide: <https://docs.mistral.ai/admin/admin-api/manage-groups-roles#roles>.
- The guide calls it "the authoritative list of roles and their UUIDs", and says RBAC must be enabled for the organization; the answer without RBAC is undocumented.
- The requests of the Admin API take roles by name (`role_names`: `member`, `billing_manager`, `organization_admin` for the organization; `user`, `dev`, `workspace_admin` and others for a workspace) or by UUID (`roles`). Responses return `raw_roles`: UUIDs, or legacy letters.

## Decisions

- **Both scopes in one data source**, as the API answers them; `is_custom_role` is `is_custom`.
- **Not the service account roles.** `GET /v1/service-accounts/assignable-roles` lists, per workspace and with the standard key, the workspace roles a service account can hold. It belongs to the service accounts area; the page of `mistral_roles` says so.
- `mistral_invite` (package `users`) calls the same endpoint through `client.ListRoles` to name the roles of an imported invite.

## To check on the first live run

- That `name` holds the `role_names` value (`member`), not the label of the Admin Panel (`Member`). `TestAccRolesDataSource` checks it.
- The answer for an organization without RBAC.

## Tests

- `fake_test.go` answers the roles a test passes; `roles_data_source_fake_test.go` covers exact values, empty lists and an error; `roles_data_source_test.go` is the acceptance test.
