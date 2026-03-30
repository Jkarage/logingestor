package auth

import (
	_ "embed"
)

// These are the current set of rules we have for auth.
const (
	RuleAuthenticate       = "auth"
	RuleAny                = "rule_any"
	RuleViewerOnly         = "rule_viewer_only"
	RuleProjectManagerOnly = "rule_project_manager_only"
	RuleOrgAdminOnly       = "rule_org_admin_only"
	RuleAdminOnly          = "rule_super_admin_only"
	RuleAdminOrSubject     = "rule_super_admin_or_subject"
)

// Package name of our rego code.
const (
	opaPackage string = "ingestor.rego"
)

// Core OPA policies.
var (
	//go:embed rego/authentication.rego
	regoAuthentication string

	//go:embed rego/authorization.rego
	regoAuthorization string
)
