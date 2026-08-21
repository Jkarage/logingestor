package providers

// API hosts for providers whose endpoint is not supplied by the customer.
//
// These are variables rather than constants only so tests can point them at a
// local server. Nothing in the product reassigns them; a provider whose host is
// legitimately per-customer (Mattermost, GitHub Enterprise) takes it as a
// credential instead.
var (
	linearEndpoint = "https://api.linear.app/graphql"
	whatsappBase   = "https://graph.facebook.com/v21.0"
	githubBase     = "https://api.github.com"
	atLiveBase     = "https://api.africastalking.com"
	atSandboxBase  = "https://api.sandbox.africastalking.com"
)

// alertText is the one-line rendering shared by the providers that deliver
// plain text. Keeping it in one place means a Teams card, a Google Chat message
// and an SMS all describe an alert the same way.
func alertText(level, project, message, source string, when string) string {
	return "[" + level + "] " + project + ": " + message + " (source: " + source + ", " + when + ")"
}
