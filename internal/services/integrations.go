package services

// IntegrationTools defines the shared tool metadata for the integrations
// app. Both agent and MCP surfaces loop over this list so input schemas
// and descriptions stay identical across callers.
//
// None of these tools is AdminOnly: user-scoped types must be callable by
// regular users. Per-call scope enforcement lives in the handler
// (configure/delete check the caller role against the TypeSpec's Scope).
var IntegrationTools = []ToolMeta{
	{
		Name: "configure_integration",
		Description: "Start configuring an external integration (e.g. an email account, GitHub PAT), or edit an existing one. " +
			"Returns a short-lived URL the user opens in their browser to fill in a form. " +
			"If the integration already exists, the form prefills current non-secret values and omits credential fields — a user can edit settings (signature, ports, display name, etc.) without re-entering a one-time password. " +
			"To change a credential, the user must first delete_integration and then configure_integration fresh. " +
			"The URL is single-use and expires in 15 minutes. Use list_integration_types to see what can be configured.",
		Schema: PropsReq(map[string]any{
			"provider":  Field("string", "Integration provider key (e.g. \"github\", \"email\")."),
			"auth_type": Field("string", "Authentication mechanism (e.g. \"api_key\", \"imap_smtp\")."),
		}, "provider", "auth_type"),
	},
	{
		Name: "check_integration_status",
		Description: "Check whether a pending integration setup has been completed by the user. " +
			"Call this after configure_integration once the user says they've filled in the form.",
		Schema: PropsReq(map[string]any{
			"pending_id": Field("string", "The pending_id returned by configure_integration."),
		}, "pending_id"),
	},
	{
		Name: "list_integrations",
		Description: "List the caller's configured integrations plus any tenant-scoped (workspace-wide) ones. " +
			"Secrets are never included in the output. Admins can pass all=true to see every user's integrations.",
		Schema: Props(map[string]any{
			"all": map[string]any{"type": "boolean", "description": "Admins only: include integrations belonging to other users in the tenant."},
		}),
	},
	{
		Name:        "delete_integration",
		Description: "Delete a configured integration by id. Regular users can only delete their own user-scoped integrations; admins can delete any.",
		Schema: PropsReq(map[string]any{
			"integration_id": Field("string", "The integration UUID (from list_integrations)."),
		}, "integration_id"),
	},
	{
		Name: "list_integration_types",
		Description: "List the integration types available to configure (e.g. \"github:api_key\", \"email:imap_smtp\") " +
			"with their display names, scope (user vs tenant), and required fields.",
		Schema: Props(map[string]any{}),
	},
}
