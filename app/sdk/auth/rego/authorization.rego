package ingestor.rego

import rego.v1

role_viewer := "VIEWER"

role_project_manager := "PROJECT MANAGER"

role_org_admin := "ORG ADMIN"

role_super_admin := "SUPER ADMIN"

role_all := {role_viewer, role_project_manager, role_org_admin, role_super_admin}

default rule_any := false

rule_any if {
	claim_roles := {role | some role in input.Roles}
	input_roles := role_all & claim_roles
	count(input_roles) > 0
}

default rule_viewer_only := false

rule_viewer_only if {
	claim_roles := {role | some role in input.Roles}
	input_user := {role_viewer} & claim_roles
	count(input_user) > 0
}

default rule_project_manager_only := false

rule_project_manager_only if {
	claim_roles := {role | some role in input.Roles}
	input_admin := {role_project_manager} & claim_roles
	count(input_admin) > 0
}

default rule_org_admin_only := false

rule_org_admin_only if {
	claim_roles := {role | some role in input.Roles}
	input_admin := {role_org_admin} & claim_roles
	count(input_admin) > 0
}

default rule_super_admin_only := false

rule_super_admin_only if {
	claim_roles := {role | some role in input.Roles}
	input_admin := {role_super_admin} & claim_roles
	count(input_admin) > 0
}

default rule_super_admin_or_subject := false

rule_super_admin_or_subject if {
	claim_roles := {role | some role in input.Roles}
	input_admin := {role_super_admin} & claim_roles
	count(input_admin) > 0
} else if {
	claim_roles := {role | some role in input.Roles}
	input_user := {role_super_admin} & claim_roles
	count(input_user) > 0
	input.UserID == input.Subject
}
